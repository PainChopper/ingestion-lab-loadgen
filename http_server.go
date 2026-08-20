package main

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strconv"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type requestKind int

const (
	getSnapshot requestKind = iota
	cmdRun
	cmdPause
	cmdReset
	setTPS

	getStatus
	quit
)

type request struct {
	kind          requestKind
	targetTPS     int
	reply_old     chan statusSnapshot_old
	snapshotReply chan statusSnapshot
}

type statusSnapshot_old struct {
	TargetTPS         string `json:"targetTPS"`
	ActualTPS         string `json:"actualTPS"`
	TotalTransactions string `json:"totalTransactions"`
}

const snapshotPath = "/api/loadgen/snapshot"
const commandsPath = "/api/loadgen/commands"

func newServeMux(commands chan<- request, metrics *Metrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(commands))
	mux.Handle(commandsPath, commandsHandler(commands))

	controlHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		req := commandRequest{}
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
			commands <- request{kind: setTPS, targetTPS: tps}
		case "quit":
			commands <- request{kind: quit}
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

		replyChan := make(chan statusSnapshot_old, 1)
		cmd := request{kind: getStatus, reply_old: replyChan}
		commands <- cmd
		snapshot := <-replyChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	}

	mux.HandleFunc("/control", controlHandler)
	mux.HandleFunc("/status", statusHandler)

	if metrics != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	}

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	fileServer := http.FileServer(http.Dir("./ui"))
	mux.Handle("/", fileServer)

	return mux
}

func startHttpServer(out chan request, metrics *Metrics) *http.Server {
	mux := newServeMux(out, metrics)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}

	go func() {
		_ = server.ListenAndServe()
	}()

	return server
}
