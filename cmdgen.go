package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type cmdType int

const (
	setTPS cmdType = iota
	getStatus
	quit
)

type command struct {
	kind      cmdType
	targetTPS int
	reply     chan statusSnapshot
}

type controlRequest struct {
	Action string `json:"action"` // "targetTPS", "quit"
	Value  string `json:"value"`  // "100" - transactions per second (TPS)
}

type statusSnapshot struct {
	TargetTPS         string `json:"targetTPS"`
	ActualTPS         string `json:"actualTPS"`
	TotalTransactions string `json:"totalTransactions"`
}

func startHttpServer(out chan command) *http.Server {
	mux := http.NewServeMux()

	controlHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		req := controlRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		switch req.Action {
		case "targetTPS":
			tps, err := strconv.Atoi(req.Value)
			if err != nil {
				http.Error(w, "Invalid TPS format", http.StatusBadRequest)
				return
			}
			out <- command{kind: setTPS, targetTPS: tps}
		case "quit":
			out <- command{kind: quit}
		default:
			http.Error(w, "Unknown command", http.StatusBadRequest)
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

	fileServer := http.FileServer(http.Dir("./ui"))
	mux.Handle("/", fileServer)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}

	go server.ListenAndServe()
	return server
}
