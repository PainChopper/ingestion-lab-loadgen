package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const gracefulShutdownTimeout = 10 * time.Second

const snapshotPath = "/api/loadgen/snapshot"
const commandsPath = "/api/loadgen/commands"

func newServeMux(requests chan<- runtimeCommand, metrics *PrometheusMetrics, config config, logger *zap.Logger) (*http.ServeMux, error) {
	parsed, err := url.Parse(config.Sender.API.URL)
	if err != nil {
		return nil, fmt.Errorf("sender.api.url: %w", err)
	}
	ingestPath := parsed.Path
	if ingestPath == "" {
		ingestPath = "/"
	}
	if !strings.HasPrefix(ingestPath, "/") || strings.ContainsAny(ingestPath, "{} \t\r\n") {
		return nil, fmt.Errorf("sender.api.url: path %q is not a literal HTTP route", ingestPath)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(requests, logger))
	mux.Handle(commandsPath, commandsHandler(requests, config, logger))
	registerDeveloperRoutes(mux)

	if metrics != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	}

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	request := &http.Request{Method: http.MethodPost, URL: &url.URL{Path: ingestPath}}
	if _, pattern := mux.Handler(request); pattern != "" || ingestPath == "/metrics" {
		return nil, fmt.Errorf("sender.api.url: path %q conflicts with a service route", ingestPath)
	}
	mux.Handle(ingestPath, internalTestIngestHandler())
	return mux, nil
}

func startHTTPServer(requests chan<- runtimeCommand, metrics *PrometheusMetrics, config config, logger *zap.Logger) (*http.Server, <-chan error, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	mux, err := newServeMux(requests, metrics, config, logger)
	if err != nil {
		return nil, nil, err
	}

	server := &http.Server{
		Addr:    config.HTTP.Addr,
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

func shutdownHTTPServer(ctx context.Context, server *http.Server, done <-chan error, logger *zap.Logger) {
	if logger == nil {
		logger = zap.NewNop()
	}
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
