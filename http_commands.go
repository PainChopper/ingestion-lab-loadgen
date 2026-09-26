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

func commandsHandler(plane controlPlane, policy policy, loggers ...*zap.Logger) http.Handler {
	logger := loggerOrNop(loggers)
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
			cr.Action == "set-throttler-installation-mode" ||
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
			if result := plane.dispatch(cmdRun, 0, ""); result.status == commandConflict {
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
			plane.dispatchAsync(cmdPause)
		case "reset":
			if result := plane.dispatch(cmdReset, 0, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-read-batch-size":
			var value int
			if err := json.Unmarshal(cr.Value, &value); err != nil || !validReadBatchSize(policy, value) {
				http.Error(w, "Invalid read batch size", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetReadBatchSize, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-reader-workers":
			var value int
			if len(cr.Value) == 0 || string(cr.Value) == "null" || json.Unmarshal(cr.Value, &value) != nil || !policy.Reader.Workers.contains(value) {
				http.Error(w, "Invalid Reader workers", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetReaderWorkers, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-reader-channel-capacity":
			var value int
			if string(cr.Value) == "null" {
				http.Error(w, "Invalid reader channel capacity", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !validReaderChannelCapacity(policy, value) {
				http.Error(w, "Invalid reader channel capacity", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetReaderChannelCapacity, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-sender-channel-capacity":
			var value int
			if string(cr.Value) == "null" {
				http.Error(w, "Invalid sender channel capacity", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !validSenderChannelCapacity(policy, value) {
				http.Error(w, "Invalid sender channel capacity", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetSenderChannelCapacity, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-requested-tps":
			var value int
			if len(cr.Value) == 0 || string(cr.Value) == "null" {
				http.Error(w, "Invalid requested TPS", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(cr.Value, &value); err != nil || !policy.Throttler.RequestedTPS.contains(value) {
				http.Error(w, "Invalid requested TPS", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetRequestedTPS, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-throttler-installation-mode":
			var value string
			if err := json.Unmarshal(cr.Value, &value); err != nil || !policy.Throttler.InstallationMode.contains(value) {
				http.Error(w, "Invalid throttler installation mode", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(cmdSetThrottlerInstallationMode, 0, value); result.status == commandConflict {
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
			var setting rangePolicy
			var kind requestKind
			setting, kind = policy.Sender.Workers, cmdSetSenderWorkers
			if !setting.contains(value) {
				http.Error(w, "Invalid Sender setting", http.StatusBadRequest)
				return
			}
			if result := plane.dispatch(kind, value, ""); result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		default:
			http.Error(w, "Unknown command", http.StatusBadRequest)
			return
		}
	}

	return http.HandlerFunc(handler)
}
