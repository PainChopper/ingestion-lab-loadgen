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

func newServeMux(requests runtimeRequests, metrics *PrometheusMetrics, config config, logger *zap.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(requests, logger))
	mux.Handle(commandsPath, commandsHandler(requests, config))
	registerDeveloperRoutes(mux)

	mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

func startHTTPServer(requests runtimeRequests, metrics *PrometheusMetrics, config config, logger *zap.Logger) (*http.Server, <-chan error, error) {
	server := &http.Server{
		Addr:              config.HTTP.Addr,
		Handler:           newServeMux(requests, metrics, config, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       time.Minute,
		// A fixed write deadline could interrupt pprof requests.
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen HTTP server: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		defer close(done)
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	logger.Info("HTTP server starting", zap.String("event", "service_started"), zap.String("http_addr", server.Addr))
	return server, done, nil
}

func shutdownHTTPServer(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return closeErr
		}
		return errors.Join(err, closeErr)
	}
	return nil
}
