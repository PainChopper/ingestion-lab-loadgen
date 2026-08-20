package main

import (
	"encoding/json"
	"net/http"
)

type commandRequest struct {
	Action string `json:"action"` // "targetTPS", "quit"
	Value  string `json:"value"`  // "100" - transactions per second (TPS)
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
			commands <- request{kind: cmdRun}
		case "pause":
			commands <- request{kind: cmdPause}
		case "reset":
			commands <- request{kind: cmdReset}
		default:
			http.Error(w, "Unknown command", http.StatusBadRequest)
			return
		}

	}
	return http.HandlerFunc(handler)
}
