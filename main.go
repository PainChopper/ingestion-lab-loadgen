package main

import (
	"context"
	"log"
	"os"
	"runtime"
	"time"
)

const (
	windowLength = time.Second / 1
	startTPS     = 100_000
)

var blackHole uint64

type controlState struct {
	actualTPS                       int64
	totalTransactions               int64
	elapsedBeforeRun                time.Duration
	runStartedAt                    time.Time
	startError                      *string
	reader                          readerTelemetry
	readerChannel                   readerChannelTelemetry
	configuredReadBatchSize         int
	configuredReaderChannelCapacity int
	readerChannelCapacityConfigured bool
	policy                          policy

	lifecycle *lifecycle
}

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
	state := controlState{lifecycle: newLifecycle(), policy: loadedPolicy}

	requests := make(chan request, 10)

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C
	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(startTPS))

	startHttpServer(requests, promMetrics, loadedPolicy)

	state.eventLoop(
		requests,
		metrics,
		promMetrics,
		func(ctx context.Context, batchSize, readerChannelCapacity int) (<-chan []Transaction, error) {
			return produceBatches(ctx, loadedPolicy.Source.Path, batchSize, readerChannelCapacity, &state.reader, &state.readerChannel)
		},
	)
}

func consumeTransaction(tran *Transaction) {
	blackHole = uint64(tran.Fold)
	blackHole ^= uint64(tran.EventType) * 1099511628211
	blackHole ^= uint64(tran.EventSubtype) * 1469598103934665603
	blackHole ^= uint64(tran.Currency) * 7809847782465536322
	blackHole ^= uint64(len(tran.ClientID)) << 32
	blackHole ^= uint64(tran.Amount)
	runtime.KeepAlive(blackHole)
}
