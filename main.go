package main

import (
	"fmt"
	"strconv"

	"time"
)

const (
	windowLength     = time.Second
	startTPS     int = 100
)

type generatorState struct {
	targetTPS         int
	actualTPS         int
	totalTransactions int
}

func main() {
	gs := generatorState{}
	gs.targetTPS = startTPS

	transactions := produceTransactions("./data/MBD-mini/trx/**/*.parquet")

	rateLimiter := time.NewTicker(time.Second / time.Duration(gs.targetTPS))
	defer rateLimiter.Stop()
	rateLimit := rateLimiter.C

	commands := make(chan command, 10)

	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C

	server := startHttpServer(commands)

	for {
		select {
		case <-rateLimit:
			select {
			case tran, ok := <-transactions:
				if !ok {
					return
				}
				fmt.Printf("%s: %s\n", tran.ClientID, tran.Amount)
				gs.actualTPS++
			default:
			}
		case cmd := <-commands:
			switch cmd.kind {
			case setTPS:
				gs.targetTPS = cmd.targetTPS
				rateLimiter.Stop()
				if gs.targetTPS == 0 {
					rateLimit = nil
				} else {
					rateLimiter = time.NewTicker(time.Second / time.Duration(gs.targetTPS))
					rateLimit = rateLimiter.C
				}
			case quit:
				server.Close()
				return
			case getStatus:
				snapshot := statusSnapshot{
					TargetTPS: strconv.Itoa(gs.targetTPS),
				}
				cmd.reply <- snapshot
			}
		case <-metrics:
		}
	}
}
