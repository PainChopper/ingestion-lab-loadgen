package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"go.uber.org/zap"
)

type commandRequest struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

func commandsHandler(requests chan<- runtimeCommand, config config, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		cr := commandRequest{}
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&cr); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		strictAction := cr.Action == "set-requested-tps" ||
			cr.Action == "set-throttler-installed" ||
			cr.Action == "set-sender-channel-capacity" ||
			cr.Action == "set-reader-workers" ||
			cr.Action == "set-sender-workers"
		if strictAction {
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&cr); err != nil {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
				return
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
				return
			}
		}

		switch cr.Action {
		case "run":
			if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			} else if result.err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				if err := json.NewEncoder(w).Encode(struct {
					Error string `json:"error"`
				}{Error: result.err.Error()}); err != nil {
					logger.Error("command response encoding failed", zap.String("event", "run_failed"))
					return
				}
			}
		case "pause":
			requests <- runtimeCommand{kind: cmdPause}
		case "reset":
			if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-read-batch-size":
			var value int
			if err := json.Unmarshal(cr.Value, &value); err != nil || !config.Reader.ReadBatchSize.contains(value) {
				http.Error(w, "Invalid read batch size", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:  cmdSetReadBatchSize,
				value: value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-reader-workers":
			var value int
			if len(cr.Value) == 0 || string(cr.Value) == "null" || json.Unmarshal(cr.Value, &value) != nil || !config.Reader.Workers.contains(value) {
				http.Error(w, "Invalid Reader workers", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:  cmdSetReaderWorkers,
				value: value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-reader-channel-capacity":
			var value int
			if string(cr.Value) == "null" {
				http.Error(w, "Invalid reader channel capacity", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !config.ReaderChannel.Capacity.contains(value) {
				http.Error(w, "Invalid reader channel capacity", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:  cmdSetReaderChannelCapacity,
				value: value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-sender-channel-capacity":
			var value int
			if string(cr.Value) == "null" {
				http.Error(w, "Invalid sender channel capacity", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !config.SenderChannel.Capacity.contains(value) {
				http.Error(w, "Invalid sender channel capacity", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:  cmdSetSenderChannelCapacity,
				value: value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-requested-tps":
			var value int
			if len(cr.Value) == 0 || string(cr.Value) == "null" {
				http.Error(w, "Invalid requested TPS", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !config.Throttler.RequestedTPS.contains(value) {
				http.Error(w, "Invalid requested TPS", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:  cmdSetRequestedTPS,
				value: value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-throttler-installed":
			var value *bool
			if err := json.Unmarshal(cr.Value, &value); err != nil || value == nil || !config.Throttler.Installed.contains(*value) {
				http.Error(w, "Invalid throttler installed value", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{
				kind:      cmdSetThrottlerInstalled,
				installed: *value,
			}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-sender-workers":
			var value int
			if len(cr.Value) == 0 || string(cr.Value) == "null" {
				http.Error(w, "Invalid Sender setting", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil {
				http.Error(w, "Invalid Sender setting", http.StatusBadRequest)
				return
			}
			var setting rangeConfig
			var kind runtimeCommandKind
			setting, kind = config.Sender.Workers, cmdSetSenderWorkers
			if !setting.contains(value) {
				http.Error(w, "Invalid Sender setting", http.StatusBadRequest)
				return
			}
			if result := executeRuntimeCommand(requests, runtimeCommand{kind: kind, value: value}); result.rejected {
				w.WriteHeader(http.StatusConflict)
			}
		default:
			http.Error(w, "Unknown command", http.StatusBadRequest)
			return
		}
	}

	return http.HandlerFunc(handler)
}
