package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, serveWithSignals))
}

func serveWithSignals(configPath string) error {
	appCtx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	signalsDone := make(chan struct{})
	go func() {
		defer close(signalsDone)
		select {
		case <-signals:
		case <-appCtx.Done():
		}
		signal.Stop(signals)
		cancel()
	}()
	defer func() {
		cancel()
		<-signalsDone
	}()
	return runServe(appCtx, configPath)
}
