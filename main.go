package main

import (
	"log"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	windowLength       = time.Second / 1
	startTPS           = 100_000
	bucketBurstPercent = 10
	dataPath           = "./data/MBD-mini/trx/**/*.parquet"
)

var blackHole uint64

type generatorState struct {
	actualTPS         int64
	totalTransactions int64
}

func main() {
	gs := generatorState{}
	var consumedSinceTick atomic.Int64
	throttler := NewTransactionsThrottler(startTPS, bucketBurstPercent)

	commands := make(chan command, 10)
	batches, err := produceBatches(dataPath)
	if err != nil {
		log.Fatalf("cannot start load generator: %v", err)
	}

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C

	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(startTPS))
	server := startHttpServer(commands, promMetrics)

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		consumeBatches(batches, &consumedSinceTick)
	}()

	for {
		select {
		case <-consumerDone:
			return
		case cmd := <-commands:
			switch cmd.kind {
			case setTPS:
				throttler.setTPS(cmd.targetTPS)
				promMetrics.targetTPS.Set(float64(cmd.targetTPS))
			case quit:
				err := server.Close()
				if err != nil {
					return
				}
				return
			case getStatus:
				snapshot := statusSnapshot{
					TargetTPS:         strconv.Itoa(throttler.GetTPS()),
					ActualTPS:         strconv.Itoa(int(gs.actualTPS)),
					TotalTransactions: strconv.Itoa(int(gs.totalTransactions)),
				}
				cmd.reply <- snapshot
			}
		case <-metrics:
			delta := consumedSinceTick.Swap(0)
			gs.actualTPS = delta * int64(time.Second/windowLength)
			gs.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(gs.actualTPS))
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
