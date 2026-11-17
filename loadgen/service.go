package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
)

func runServe(appCtx context.Context, configPath string) error {
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
	commands := make(chan runtimeCommand, 10)
	loopDone := make(chan struct{})
	requests := runtimeRequests{commands: commands, done: loopDone}
	metrics := newServiceMetrics(cfg)
	defer metrics.stop()

	server, serverDone, err := startHTTPServer(requests, metrics.prometheusMetrics, cfg, logger)
	if err != nil {
		return err
	}
	logger.Info("service started", zap.String("event", "service_started"))
	events := runtimeEvents{
		requests:   commands,
		source:     prepareSourceAsync(cfg.Source.Path),
		serverDone: serverDone,
		metrics:    metrics,
	}
	deadline, runErr := state.runEventLoop(appCtx, events, gracefulShutdownTimeout)
	close(loopDone)
	if deadline.IsZero() {
		deadline = time.Now().Add(gracefulShutdownTimeout)
	}
	shutdownCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := shutdownHTTPServer(shutdownCtx, server); err != nil {
		logger.Error("graceful HTTP shutdown failed", zap.String("event", "run_failed"), zap.Error(err))
		runErr = errors.Join(runErr, fmt.Errorf("shutdown http server: %w", err))
	}
	select {
	case err := <-serverDone:
		runErr = errors.Join(runErr, err)
	case <-shutdownCtx.Done():
	}
	logger.Info("service stopped", zap.String("event", "service_stopped"))
	return runErr
}

type sourceDiscovery struct {
	source readerSource
	err    error
}

func prepareSourceAsync(path string) <-chan sourceDiscovery {
	ready := make(chan sourceDiscovery, 1)
	go func() {
		source, err := discoverReaderSource(path)
		ready <- sourceDiscovery{source: source, err: err}
	}()
	return ready
}
