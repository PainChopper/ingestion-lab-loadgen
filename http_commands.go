package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type commandRequest struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

func commandsHandler(commands chan<- request) http.Handler {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cr := commandRequest{}
		if err := json.NewDecoder(r.Body).Decode(&cr); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
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
			if err := json.Unmarshal(cr.Value, &value); err != nil || !validReadBatchSize(value) {
				http.Error(w, "Invalid read batch size", http.StatusBadRequest)
				return
			}
			reply := make(chan commandResult, 1)
			commands <- request{kind: cmdSetReadBatchSize, value: value, commandReply: reply}
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
