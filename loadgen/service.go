package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
)

func newServeState(loadedConfig config, logger *zap.Logger) controlState {
	return controlState{
		run:      controlRunState{lifecycle: newLifecycle()},
		controls: configuredControls{config: loadedConfig},
		logger:   logger,
	}
}

func runServe(appCtx context.Context, configPath string, runAfterStart bool) error {
	serviceCtx, stopService := context.WithCancel(appCtx)
	defer stopService()

	config, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, err := newApplicationLogger(config.Logging.Level, os.Stdout)
	if err != nil {
		return fmt.Errorf("create application logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	state := newServeState(config, logger)
	plane := newRuntimeControl(10)
	runtime := newRuntimeMetrics(config)
	defer runtime.stop()
	state.metricsWindow = runtime.window

	server, serverDone, err := startHTTPServer(plane, runtime.promMetrics, config, logger)
	if err != nil {
		return err
	}
	logger.Info("service started", zap.String("event", "service_started"))
	eventLoopDone := make(chan struct{})
	go func() {
		defer close(eventLoopDone)
		state.runEventLoop(serviceCtx, plane, runtime.metrics, runtime.promMetrics)
	}()
	if runAfterStart {
		if result := plane.execute(runtimeCommand{kind: cmdRun}); result.err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			stopService()
			shutdownHTTPServer(shutdownCtx, server, serverDone, logger)
			<-eventLoopDone
			return fmt.Errorf("start run: %w", result.err)
		} else if result.status == commandConflict {
			stopService()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			shutdownHTTPServer(shutdownCtx, server, serverDone, logger)
			<-eventLoopDone
			return fmt.Errorf("start run: lifecycle conflict")
		}
	}

	select {
	case <-appCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		logger.Info("service stopping", zap.String("event", "service_stopping"))
		shutdownHTTPServer(shutdownCtx, server, serverDone, logger)
		select {
		case <-eventLoopDone:
		case <-shutdownCtx.Done():
			logger.Warn("service shutdown timed out", zap.String("event", "run_stopped"))
		}
	case <-eventLoopDone:
	}
	logger.Info("service stopped", zap.String("event", "service_stopped"))
	return nil
}
