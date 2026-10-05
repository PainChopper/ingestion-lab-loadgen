package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
)

func runServe(appCtx context.Context, configPath string, runAfterStart bool) error {
	serviceCtx, stopService := context.WithCancel(appCtx)
	defer stopService()

	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, err := newApplicationLogger(cfg.Logging.Level, os.Stdout)
	if err != nil {
		return fmt.Errorf("create application logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	state := newControlState(cfg, logger)
	requests := make(chan runtimeCommand, 10)
	metrics := newServiceMetrics(cfg)
	defer metrics.stop()

	server, serverDone, err := startHTTPServer(requests, metrics.prometheusMetrics, cfg, logger)
	if err != nil {
		return err
	}
	logger.Info("service started", zap.String("event", "service_started"))
	eventLoopDone := make(chan struct{})
	go func() {
		defer close(eventLoopDone)
		state.runEventLoop(
			serviceCtx,
			requests,
			metrics.ticks,
			metrics.prometheusMetrics,
		)
	}()
	if runAfterStart {
		if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
			defer cancel()
			stopService()
			shutdownHTTPServer(shutdownCtx, server, serverDone, logger)
			<-eventLoopDone
			return fmt.Errorf("start run: %w", result.err)
		} else if result.rejected {
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
