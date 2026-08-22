package main

import (
	"log"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	windowLength = time.Second / 1
	startTPS     = 100_000
	dataPath     = "./data/MBD-mini/trx/**/*.parquet"
)

var blackHole uint64

type controlState struct {
	actualTPS         int64
	totalTransactions int64

	lifecycle *lifecycle
}

func main() {
	state := controlState{lifecycle: newLifecycle()}
	var consumedSinceTick atomic.Int64

	commands := make(chan request, 10)
	batches, err := produceBatches(dataPath)
	if err != nil {
		log.Fatalf("cannot start load generator: %v", err)
	}

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C
	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(startTPS))

	startHttpServer(commands, promMetrics)

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		consumeBatches(batches, &consumedSinceTick)
	}()

	if !state.lifecycle.run() {
		log.Fatal("lifecycle did not enter running state")
	}

	for {
		select {
		case <-consumerDone:
			return
		case cmd := <-commands:
			switch cmd.kind {
			case getSnapshot:
				snapshot := statusSnapshot{
					RunState:          state.lifecycle.currentState(),
					TotalTransactions: state.totalTransactions,
					ReaderWorkers:     1,
					SenderWorkers:     0,
				}
				cmd.snapshotReply <- snapshot
			}

		case <-metrics:
			delta := consumedSinceTick.Swap(0)
			state.actualTPS = delta * int64(time.Second/windowLength)
			state.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(state.actualTPS))
			promMetrics.transactionsTotal.Add(float64(delta))
		}
	}
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
