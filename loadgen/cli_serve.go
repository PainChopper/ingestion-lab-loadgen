package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

const cliUsage = `usage:
  ingestion-lab-loadgen serve [--config <path>] [--run]
  ingestion-lab-loadgen snapshot [--url <url>]
  ingestion-lab-loadgen status [--url <url>]
  ingestion-lab-loadgen run [--url <url>]
  ingestion-lab-loadgen pause [--url <url>]
  ingestion-lab-loadgen resume [--url <url>]
  ingestion-lab-loadgen reset [--url <url>]
  ingestion-lab-loadgen set reader-workers <N> [--url <url>]
  ingestion-lab-loadgen set sender-workers <N> [--url <url>]
  ingestion-lab-loadgen set requested-tps <N> [--url <url>]
  ingestion-lab-loadgen set throttler-mode <installed|bypass> [--url <url>]`

type cliCommand struct {
	configPath    string
	showUsage     bool
	runAfterStart bool
	remoteAction  string
	remoteURL     string
	remoteSet     remoteSetCommand
}

func parseCLI(args []string) (cliCommand, error) {
	if len(args) == 0 {
		return cliCommand{}, nil
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return cliCommand{showUsage: true}, nil
	}

	if isRemoteCLIAction(args[0]) {
		return parseRemoteCLI(args)
	}
	if args[0] == "set" {
		return parseSetCLI(args)
	}
	if args[0] != "serve" {
		if len(args) == 1 {
			return cliCommand{configPath: args[0]}, nil
		}
		return cliCommand{}, fmt.Errorf("unexpected command %q", args[0])
	}

	command := cliCommand{}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--help":
			if len(args) == 2 {
				command.showUsage = true
				return command, nil
			}
			return cliCommand{}, fmt.Errorf("invalid serve arguments")
		case "--config":
			if command.configPath != "" || index+1 >= len(args) || args[index+1] == "" {
				return cliCommand{}, fmt.Errorf("invalid serve arguments: --config requires one non-empty path")
			}
			index++
			command.configPath = args[index]
		case "--run":
			if command.runAfterStart {
				return cliCommand{}, fmt.Errorf("--run may be specified once")
			}
			command.runAfterStart = true
		default:
			return cliCommand{}, fmt.Errorf("unexpected argument %q", args[index])
		}
	}
	return command, nil
}

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
	logger, err := newStdoutApplicationLogger(config.Logging.Level)
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
