package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

type commandRequest struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

func commandsHandler(commands chan<- request, policy policy) http.Handler {
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
			cr.Action == "set-sender-channel-capacity"
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
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdRun, commandReply: reply}
			if result := <-reply; result.err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				if err := json.NewEncoder(w).Encode(struct {
					Error string `json:"error"`
				}{Error: result.err.Error()}); err != nil {
					log.Printf("encode command error: %v", err)
					return
				}
			}
		case "pause":
			commands <- request{kind: cmdPause}
		case "reset":
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdReset, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-read-batch-size":
			var value int
			if err := json.Unmarshal(cr.Value, &value); err != nil || !validReadBatchSize(policy, value) {
				http.Error(w, "Invalid read batch size", http.StatusBadRequest)
				return
			}
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetReadBatchSize, value: value, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
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
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetReaderChannelCapacity, value: value, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
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
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetSenderChannelCapacity, value: value, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
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
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetRequestedTPS, value: value, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		case "set-throttler-installation-mode":
			var value string
			if err := json.Unmarshal(cr.Value, &value); err != nil || !policy.Throttler.InstallationMode.contains(value) {
				http.Error(w, "Invalid throttler installation mode", http.StatusBadRequest)
				return
			}
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetThrottlerInstallationMode, textValue: value, commandReply: reply}
			if result := <-reply; result.status == commandConflict {
				w.WriteHeader(http.StatusConflict)
			}
		default:
			http.Error(w, "Unknown command", http.StatusBadRequest)
			return
		}
	}

	return http.HandlerFunc(handler)
}
