package main

import (
	"log"
	"runtime"
	"strconv"
	"time"
)

const (
	windowLength       = time.Second / 10
	startTPS           = 100_000
	bucketBurstPercent = 10
	dataPath           = "./data/MBD-mini/trx/**/*.parquet"
)

var blackHole uint64

type generatorState struct {
	currentSecondTPS            int
	actualTPS                   int
	totalTransactions           int
	prometheusTransactionsCount int
}

func main() {
	gs := generatorState{}

	throttler := NewTransactionsThrottler(startTPS, bucketBurstPercent)
	rawTransactions, err := produceTransactions(dataPath)
	if err != nil {
		log.Fatalf("Cannot start load generator: %v", err)
	}

	//transactions := throttler.Throttle(rawTransactions)
	transactions := rawTransactions

	commands := make(chan command, 10)

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C

	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(startTPS))
	server := startHttpServer(commands, promMetrics)

	for {
		select {
		case tran, ok := <-transactions:
			if !ok {
				return
			}
			consumeTransaction(tran)
			gs.currentSecondTPS++
			gs.totalTransactions++
			gs.prometheusTransactionsCount++
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
					ActualTPS:         strconv.Itoa(gs.actualTPS),
					TotalTransactions: strconv.Itoa(gs.totalTransactions),
				}
				cmd.reply <- snapshot
			}
		case <-metrics:
			gs.actualTPS = gs.currentSecondTPS * int(time.Second/windowLength)
			gs.currentSecondTPS = 0
			promMetrics.actualTPS.Set(float64(gs.actualTPS))
			promMetrics.transactionsTotal.Add(float64(gs.prometheusTransactionsCount))
			gs.prometheusTransactionsCount = 0
		}
	}
}

func consumeTransaction(tran *Transaction) {
	blackHole = uint64(tran.Fold)
	blackHole ^= uint64(tran.EventType) * 1099511628211
	blackHole ^= uint64(tran.EventSubtype) * 1469598103934665603
	blackHole ^= uint64(tran.Currency) * 7809847782465536322
	blackHole ^= uint64(len(tran.ClientID)) << 32
	blackHole ^= uint64(len(tran.Amount))
	runtime.KeepAlive(blackHole)
}
