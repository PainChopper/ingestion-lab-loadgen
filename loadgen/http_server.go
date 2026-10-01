package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const gracefulShutdownTimeout = 10 * time.Second

const snapshotPath = "/api/loadgen/snapshot"
const commandsPath = "/api/loadgen/commands"
const internalTestIngestPath = "/internal/test/ingest"

func newServeMux(plane runtimeControl, metrics *Metrics, config config, loggers ...*zap.Logger) *http.ServeMux {
	logger := loggerOrNop(loggers)
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(plane, logger))
	mux.Handle(commandsPath, commandsHandler(plane, config, logger))
	mux.Handle(internalTestIngestPath, internalTestIngestHandler())
	registerDeveloperRoutes(mux)

	if metrics != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	}

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

func startHTTPServer(plane runtimeControl, metrics *Metrics, config config, logger *zap.Logger) (*http.Server, <-chan error, error) {
	mux := newServeMux(plane, metrics, config, logger)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen HTTP server: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	logger.Info("HTTP server starting", zap.String("event", "service_started"), zap.String("http_addr", server.Addr))
	return server, done, nil
}

func shutdownHTTPServer(ctx context.Context, server *http.Server, done <-chan error, loggers ...*zap.Logger) {
	logger := loggerOrNop(loggers)
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful HTTP shutdown failed", zap.String("event", "run_failed"))
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("forced HTTP close failed", zap.String("event", "run_failed"))
		}
	}
	if err := <-done; err != nil {
		logger.Error("HTTP server stopped unexpectedly", zap.String("event", "run_failed"))
	}
}
