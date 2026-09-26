package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

const cliUsage = "usage: ingestion-lab-loadgen serve [--config <path>]"

type cliCommand struct {
	configPath string
	showUsage  bool
}

func parseCLI(args []string) (cliCommand, error) {
	if len(args) == 0 {
		return cliCommand{}, nil
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return cliCommand{showUsage: true}, nil
	}

	if args[0] != "serve" {
		if len(args) == 1 {
			return cliCommand{configPath: args[0]}, nil
		}
		return cliCommand{}, fmt.Errorf("unexpected command %q", args[0])
	}

	switch len(args) {
	case 1:
		return cliCommand{}, nil
	case 2:
		if args[1] == "--help" {
			return cliCommand{showUsage: true}, nil
		}
		return cliCommand{}, fmt.Errorf("invalid serve arguments")
	case 3:
		if args[1] != "--config" {
			return cliCommand{}, fmt.Errorf("unexpected argument %q", args[1])
		}
		return cliCommand{configPath: args[2]}, nil
	default:
		return cliCommand{}, fmt.Errorf("invalid serve arguments")
	}
}

func newServeState(loadedPolicy policy, logger *zap.Logger) controlState {
	return controlState{
		run:      controlRunState{lifecycle: newLifecycle()},
		controls: configuredControls{policy: loadedPolicy},
		logger:   logger,
	}
}

func runServe(appCtx context.Context, configPath string) error {
	loadedPolicy, _, err := loadPolicy(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, err := newStdoutApplicationLogger(loadedPolicy.Logging.Level)
	if err != nil {
		return fmt.Errorf("create application logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("service started", zap.String("event", "service_started"))
	state := newServeState(loadedPolicy, logger)
	plane := newControlPlane(10)
	runtime := newRuntimeMetrics(loadedPolicy)
	defer runtime.stop()
	state.metricsWindow = runtime.window

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
	return nil
}
