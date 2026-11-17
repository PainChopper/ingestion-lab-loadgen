package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

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
