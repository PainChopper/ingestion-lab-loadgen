// Package httpapi serves the LoadGen control API and developer routes.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/pprof"

	"go.uber.org/zap"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/control"
)

// RuntimeControl provides runtime snapshots and accepts validated live commands.
type RuntimeControl interface {
	// Snapshot returns the current runtime state.
	Snapshot(context.Context) (api.Snapshot, error)
	// ValidCommand reports whether a command is supported by the runtime.
	ValidCommand(control.Command) bool
	// Execute applies a runtime command and reports whether it was accepted.
	Execute(context.Context, control.Command) (bool, error)
}

// Options holds the fixed inputs for the HTTP handler.
type Options struct {
	// Startup exposes the immutable startup configuration.
	Startup api.ConfigResponse
	// Metrics serves the Prometheus endpoint.
	Metrics http.Handler
	// OpenAPISpec contains the bytes of the OpenAPI document.
	OpenAPISpec []byte
	// Logger records HTTP response encoding failures.
	Logger *zap.Logger
}

// NewHandler registers the API, developer routes and metrics on an HTTP mux.
func NewHandler(requests RuntimeControl, options Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+api.SnapshotPath, snapshotHandler(requests, options.Logger))
	mux.Handle("GET "+api.ConfigPath, configHandler(options.Startup, options.Logger))
	mux.Handle("POST "+api.CommandsPath, commandsHandler(requests))
	registerDeveloperRoutes(mux, options.OpenAPISpec)

	mux.Handle("/metrics", options.Metrics)

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

const maxCommandBodyBytes = 4096

func commandsHandler(requests RuntimeControl) http.Handler {
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
		command, err := parseRuntimeCommand(request, requests.ValidCommand)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		applied, err := requests.Execute(r.Context(), command)
		if err != nil {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		if !applied {
			w.WriteHeader(http.StatusConflict)
		}
	})
}

func parseRuntimeCommand(r api.CommandRequest, valid func(control.Command) bool) (control.Command, error) {
	var invalid string
	switch r.Action {
	case api.SetReaderWorkers:
		invalid = "invalid reader workers"
	case api.SetSenderWorkers:
		invalid = "invalid sender workers"
	case api.SetRequestedTPS:
		invalid = "invalid requested tps"
	case api.SetThrottlerInstalled:
		var value *bool
		if err := json.Unmarshal(r.Value, &value); err != nil || value == nil {
			return control.Command{}, errors.New("invalid throttler installed value")
		}
		return control.Command{Action: api.SetThrottlerInstalled, Installed: *value}, nil
	default:
		return control.Command{}, errors.New("unknown command")
	}
	var value *int
	if err := json.Unmarshal(r.Value, &value); err != nil || value == nil {
		return control.Command{}, errors.New(invalid)
	}
	command := control.Command{Action: r.Action, Value: *value}
	if !valid(command) {
		return control.Command{}, errors.New(invalid)
	}
	return command, nil
}

func configHandler(config api.ConfigResponse, logger *zap.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(config); err != nil {
			logger.Warn("config response encoding failed", zap.String("event", "config_response_failed"), zap.Error(err))
		}
	})
}

func snapshotHandler(requests RuntimeControl, logger *zap.Logger) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := requests.Snapshot(r.Context())
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
