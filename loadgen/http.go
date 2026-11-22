package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	swaggerui "github.com/alexliesenfeld/go-swagger-ui"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

const gracefulShutdownTimeout = 10 * time.Second

func newServeMux(requests runtimeRequests, metrics *prometheusMetrics, cfg config.Config, logger *zap.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET "+api.SnapshotPath, snapshotHandler(requests, logger))
	mux.Handle("GET "+api.ConfigPath, configHandler(newConfigResponse(cfg), logger))
	mux.Handle("POST "+api.CommandsPath, commandsHandler(requests, cfg))
	registerDeveloperRoutes(mux)

	mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

func startHTTPServer(requests runtimeRequests, metrics *prometheusMetrics, cfg config.Config, logger *zap.Logger) (*http.Server, <-chan error, error) {
	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           newServeMux(requests, metrics, cfg, logger),
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
	logger.Info("service started", zap.String("event", "service_started"), zap.String("http_addr", listener.Addr().String()))
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

const maxCommandBodyBytes = 4096

func commandsHandler(requests runtimeRequests, cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommandBodyBytes))
		if err != nil {
			var limitErr *http.MaxBytesError
			if errors.As(err, &limitErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid request body", http.StatusBadRequest)
			}
			return
		}
		request, err := api.DecodeCommand(bytes.NewReader(body))
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		command, err := parseRuntimeCommand(request, cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		applied, err := executeRuntimeCommand(r.Context(), requests, command)
		if err != nil {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		if !applied {
			w.WriteHeader(http.StatusConflict)
		}
	})
}

func parseRuntimeCommand(r api.CommandRequest, cfg config.Config) (runtimeCommand, error) {
	var command runtimeCommand
	var contains func(int) bool
	var invalid string
	switch r.Action {
	case api.SetReaderWorkers:
		command.kind, contains, invalid = setReaderWorkers, cfg.Reader.Workers.Contains, "invalid reader workers"
	case api.SetSenderWorkers:
		command.kind, contains, invalid = setSenderWorkers, cfg.Sender.Workers.Contains, "invalid sender workers"
	case api.SetRequestedTPS:
		command.kind, contains, invalid = setRequestedTPS, cfg.Throttler.RequestedTPS.Contains, "invalid requested tps"
	case api.SetThrottlerInstalled:
		var value *bool
		if err := json.Unmarshal(r.Value, &value); err != nil || value == nil {
			return runtimeCommand{}, errors.New("invalid throttler installed value")
		}
		return runtimeCommand{kind: setThrottlerInstalled, installed: *value}, nil
	default:
		return runtimeCommand{}, errors.New("unknown command")
	}
	var value *int
	if err := json.Unmarshal(r.Value, &value); err != nil || value == nil || !contains(*value) {
		return runtimeCommand{}, errors.New(invalid)
	}
	command.value = *value
	return command, nil
}

func newConfigResponse(cfg config.Config) api.ConfigResponse {
	return api.ConfigResponse{
		ReaderWorkersMax:      cfg.Reader.Workers.Max,
		SenderWorkersMax:      cfg.Sender.Workers.Max,
		RequestedTpsMax:       cfg.Throttler.RequestedTPS.Max,
		ReadBatchSize:         cfg.Reader.ReadBatchSize,
		ReaderChannelCapacity: cfg.ReaderChannel.Capacity,
		SenderChannelCapacity: cfg.SenderChannel.Capacity,
		SourcePath:            cfg.Source.Path,
	}
}

func configHandler(config api.ConfigResponse, logger *zap.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(config); err != nil {
			logger.Warn("config response encoding failed", zap.String("event", "config_response_failed"), zap.Error(err))
		}
	})
}

func snapshotHandler(requests runtimeRequests, logger *zap.Logger) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := requestSnapshot(r.Context(), requests)
		if err != nil {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			logger.Warn("snapshot response encoding failed", zap.String("event", "snapshot_response_failed"), zap.Error(err))
			return
		}
	}
	return http.HandlerFunc(handler)
}

const developerOpenAPIPath = "/developer/openapi.json"

const developerSwaggerUIPath = "/developer/swagger/ui/"

//go:embed openapi/loadgen.openapi.json
var openAPISpec []byte

func registerDeveloperRoutes(mux *http.ServeMux) {
	mux.Handle(developerOpenAPIPath, staticDeveloperAsset("application/json", openAPISpec))
	mux.Handle(developerSwaggerUIPath, swaggerui.NewHandler(
		swaggerui.WithBasePath(developerSwaggerUIPath),
		swaggerui.WithSpecURL(developerOpenAPIPath),
		swaggerui.WithHTMLTitle("Loadgen OpenAPI"),
		swaggerui.WithTryItOutEnabled(true),
		swaggerui.WithValidatorURL(true, "none"),
	))
}

func staticDeveloperAsset(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	})
}
