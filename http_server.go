package main

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type requestKind int

const (
	getSnapshot requestKind = iota
	cmdRun
	cmdPause
	cmdReset
)

type request struct {
	kind          requestKind
	snapshotReply chan statusSnapshot
}

const snapshotPath = "/api/loadgen/snapshot"
const commandsPath = "/api/loadgen/commands"

func newServeMux(commands chan<- request, metrics *Metrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(commands))
	mux.Handle(commandsPath, commandsHandler(commands))

	if metrics != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	}

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

func startHttpServer(out chan request, metrics *Metrics) {
	mux := newServeMux(out, metrics)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}

	go func() {
		_ = server.ListenAndServe()
	}()
}
