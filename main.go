package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
	configArgument := ""
	if len(os.Args) > 2 {
		return
	}
	if len(os.Args) == 2 {
		configArgument = os.Args[1]
	}
	loadedPolicy, _, err := loadPolicy(configArgument)
	if err != nil {
		return
	}
	logger, err := newStdoutApplicationLogger(loadedPolicy.Logging.Level)
	if err != nil {
		return
	}
	defer func() { _ = logger.Sync() }()
	logger.Info("service started", zap.String("event", "service_started"))
	state := controlState{
		run:      controlRunState{lifecycle: newLifecycle()},
		controls: configuredControls{policy: loadedPolicy},
		logger:   logger,
	}
	plane := newControlPlane(10)
	runtime := newRuntimeMetrics(loadedPolicy)
	defer runtime.stop()
	state.metricsWindow = runtime.window

	appCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	server, serverDone := startHTTPServer(plane, runtime.promMetrics, loadedPolicy, logger)
	eventLoopDone := make(chan struct{})
	go func() {
		defer close(eventLoopDone)
		state.runEventLoop(appCtx, plane, runtime.metrics, runtime.promMetrics)
	}()

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
}
