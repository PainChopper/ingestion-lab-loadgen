package main

import (
	"context"
	"runtime"
	"time"
)

const (
	windowLength = time.Second / 1
	startTPS     = 100_000
	dataPath     = "./data/MBD-mini/trx/**/*.parquet"
)

var blackHole uint64

type controlState struct {
	actualTPS               int64
	totalTransactions       int64
	elapsedBeforeRun        time.Duration
	runStartedAt            time.Time
	startError              *string
	reader                  readerTelemetry
	queue1                  queue1Telemetry
	configuredReadBatchSize int

	lifecycle *lifecycle
}

func main() {
	state := controlState{lifecycle: newLifecycle()}

	requests := make(chan request, 10)

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C
	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(startTPS))

	startHttpServer(requests, promMetrics)

	state.eventLoop(
		requests,
		metrics,
		promMetrics,
		func(ctx context.Context, batchSize int) (<-chan []Transaction, error) {
			return produceBatches(ctx, dataPath, batchSize, &state.reader, &state.queue1)
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
