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

const maxCommandBodyBytes = 4096

type commandRequest struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

func commandsHandler(requests runtimeRequests, cfg config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommandBodyBytes))
		if err != nil {
			var limitErr *http.MaxBytesError
			if errors.As(err, &limitErr) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
			}
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var request commandRequest
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		command, err := request.runtimeCommand(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := executeRuntimeCommand(r.Context(), requests, command)
		if err != nil {
			if errors.Is(err, errRuntimeUnavailable) {
				http.Error(w, "Runtime unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		if result.rejected {
			w.WriteHeader(http.StatusConflict)
		}
	})
}

func (r commandRequest) runtimeCommand(cfg config) (runtimeCommand, error) {
	var command runtimeCommand
	var limits rangeConfig
	var invalid string
	switch r.Action {
	case "set-reader-workers":
		command.kind, limits, invalid = cmdSetReaderWorkers, cfg.Reader.Workers, "Invalid Reader workers"
	case "set-sender-workers":
		command.kind, limits, invalid = cmdSetSenderWorkers, cfg.Sender.Workers, "Invalid Sender setting"
	case "set-requested-tps":
		command.kind, limits, invalid = cmdSetRequestedTPS, cfg.Throttler.RequestedTPS, "Invalid requested TPS"
	case "set-throttler-installed":
		var value *bool
		if err := json.Unmarshal(r.Value, &value); err != nil || value == nil || !cfg.Throttler.Installed.contains(*value) {
			return runtimeCommand{}, errors.New("Invalid throttler installed value")
		}
		return runtimeCommand{kind: cmdSetThrottlerInstalled, installed: *value}, nil
	default:
		return runtimeCommand{}, errors.New("Unknown command")
	}
	var value *int
	if err := json.Unmarshal(r.Value, &value); err != nil || value == nil || !limits.contains(*value) {
		return runtimeCommand{}, errors.New(invalid)
	}
	command.value = *value
	return command, nil
}

func snapshotHandler(requests runtimeRequests, logger *zap.Logger) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status, err := requestRuntimeStatus(r.Context(), requests)
		if err != nil {
			if errors.Is(err, errRuntimeUnavailable) {
				http.Error(w, "Runtime unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
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
