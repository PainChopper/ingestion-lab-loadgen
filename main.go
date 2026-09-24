package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configArgument := ""
	if len(os.Args) > 2 {
		log.Fatal("usage: loadgen [config-path]")
	}
	if len(os.Args) == 2 {
		configArgument = os.Args[1]
	}
	loadedPolicy, _, err := loadPolicy(configArgument)
	if err != nil {
		log.Fatal(err)
	}
	state := controlState{
		run:      controlRunState{lifecycle: newLifecycle()},
		controls: configuredControls{policy: loadedPolicy},
	}
	requests := make(chan request, 10)
	runtime := newRuntimeMetrics(loadedPolicy)
	defer runtime.stop()
	state.metricsWindow = runtime.window

	appCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	server, serverDone := startHTTPServer(requests, runtime.promMetrics, loadedPolicy)
	eventLoopDone := make(chan struct{})
	go func() {
		defer close(eventLoopDone)
		state.runEventLoop(appCtx, requests, runtime.metrics, runtime.promMetrics)
	}()

	select {
	case <-appCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		shutdownHTTPServer(shutdownCtx, server, serverDone)
		select {
		case <-eventLoopDone:
		case <-shutdownCtx.Done():
			log.Printf("load generator shutdown timed out: %v", shutdownCtx.Err())
		}
	case <-eventLoopDone:
	}
}
