package main

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strconv"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type cmdType int

const (
	setTPS cmdType = iota
	getStatus
	quit
	getSnapshot
)

type command struct {
	kind          cmdType
	targetTPS     int
	reply_old     chan statusSnapshot_old
	snapshotReply chan statusSnapshot
}

type controlRequest struct {
	Action string `json:"action"` // "targetTPS", "quit"
	Value  string `json:"value"`  // "100" - transactions per second (TPS)
}

type statusSnapshot_old struct {
	TargetTPS         string `json:"targetTPS"`
	ActualTPS         string `json:"actualTPS"`
	TotalTransactions string `json:"totalTransactions"`
}

type statusSnapshot struct {
	RunState          runState `json:"runState"`
	TotalTransactions int64    `json:"totalTransactions"`
	ReaderWorkers     int      `json:"readerWorkers"`
	SenderWorkers     int      `json:"senderWorkers"`
}

const snapshotPath = "/api/loadgen/snapshot"

func newServeMux(commands chan<- command, metrics *Metrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(commands))

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
			commands <- command{kind: setTPS, targetTPS: tps}
		case "quit":
			commands <- command{kind: quit}
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
		cmd := command{kind: getStatus, reply_old: replyChan}
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

func startHttpServer(out chan command, metrics *Metrics) *http.Server {
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
