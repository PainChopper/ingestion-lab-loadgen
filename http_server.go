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
	cmdSetReadBatchSize
	cmdSetReaderWorkers
	cmdSetReaderChannelCapacity
	cmdSetSenderChannelCapacity
	cmdSetRequestedTPS
	cmdSetThrottlerInstallationMode
	cmdSetSenderWorkers
)

type request struct {
	kind          requestKind
	snapshotReply chan statusSnapshot
	commandReply  chan commandResult
	value         int
	textValue     string
}

type commandStatus int

const (
	commandAccepted commandStatus = iota
	commandConflict
)

type commandResult struct {
	status commandStatus
	err    error
}

const snapshotPath = "/api/loadgen/snapshot"
const commandsPath = "/api/loadgen/commands"
const internalTestIngestPath = "/internal/test/ingest"

func newServeMux(requests chan<- request, metrics *Metrics, policy policy) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(snapshotPath, snapshotHandler(requests))
	mux.Handle(commandsPath, commandsHandler(requests, policy))
	mux.Handle(internalTestIngestPath, internalTestIngestHandler())

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

func startHttpServer(requests chan request, metrics *Metrics, policy policy) {
	mux := newServeMux(requests, metrics, policy)

	server := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: mux,
	}

	go func() {
		_ = server.ListenAndServe()
	}()
}
