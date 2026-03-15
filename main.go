package main

import (
	"log"
	"strconv"
	"time"
)

const (
	windowLength       = time.Second / 10
	startTPS           = 100_000
	bucketBurstPercent = 10
	dataPath           = "./data/MBD-mini/trx/**/*.parquet"
)

type generatorState struct {
	currentSecondTPS  int
	actualTPS         int
	totalTransactions int
}

func main() {
	gs := generatorState{}

	throttler := NewTransactionsThrottler(startTPS, bucketBurstPercent)
	rawTransactions, err := produceTransactions(dataPath)
	if err != nil {
		log.Fatalf("Cannot start load generator: %v", err)
	}
	transactions := throttler.Throttle(rawTransactions)
	// transactions := rawTransactions

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
			promMetrics.transactionsTotal.Inc()
		case cmd := <-commands:
			switch cmd.kind {
			case setTPS:
				throttler.setTPS(cmd.targetTPS)
				promMetrics.targetTPS.Set(float64(cmd.targetTPS))
			case quit:
				server.Close()
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
		}
	}
}

func consumeTransaction(tran *Transaction) {
	// TODO: implement transaction consumption
}
