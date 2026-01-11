package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type cmdType int

const (
	setMode cmdType = iota
	setPulse
	getStatus
	quit
)

type command struct {
	kind  cmdType
	mode  mode
	pulse time.Duration
	reply chan statusSnapshot
}

type controlRequest struct {
	Action string `json:"action"` // "mode", "pulse", "quit"
	Value  string `json:"value"`  // "normal"/"burst" или "100ms"
}

type statusSnapshot struct {
	Mode  string `json:"mode"`
	Pulse string `json:"pulse"`
	Limit string `json:"limit"`
}

func startHttpServer(out chan command) {
	mux := http.NewServeMux()

	controlHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		req := controlRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch req.Action {
		case "mode":
			if req.Value != "normal" && req.Value != "burst" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			newMode := mode(normal)
			if req.Value == "burst" {
				newMode = mode(burst)
			}
			out <- command{kind: setMode, mode: newMode}
		case "pulse":
			if req.Value == "100ms" {
				out <- command{kind: setPulse, pulse: 100 * time.Millisecond}
			}
		case "quit":
			out <- command{kind: quit}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		replyChan := make(chan statusSnapshot, 1)
		cmd := command{kind: getStatus, reply: replyChan}
		out <- cmd
		snapshot := <-replyChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	}

	mux.HandleFunc("/control", controlHandler)
	mux.HandleFunc("/status", statusHandler)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	server.ListenAndServe()
}
