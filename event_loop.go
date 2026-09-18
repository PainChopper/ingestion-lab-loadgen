package main

import (
	"log"
	"sync/atomic"
	"time"
)

func (state *controlState) eventLoop(
	requests <-chan request,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	produce func() (<-chan []Transaction, error),
) {
	var consumedSinceTick atomic.Int64
	var consumerDone chan struct{}

	for {
		select {
		case <-consumerDone:
			return
		case cmd, ok := <-requests:
			if !ok {
				return
			}
			switch cmd.kind {
			case getSnapshot:
				snapshot := statusSnapshot{
					RunState:          state.lifecycle.currentState(),
					TotalTransactions: state.totalTransactions,
					ReaderWorkers:     1,
					SenderWorkers:     0,
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				if state.lifecycle.currentState() != runStateIdle {
					continue
				}
				batches, err := produce()
				if err != nil {
					log.Printf("cannot start load generator: %v", err)
					continue
				}
				state.lifecycle.run()
				consumerDone = make(chan struct{})
				go func() {
					defer close(consumerDone)
					consumeBatches(batches, &consumedSinceTick)
				}()
			case cmdPause:
			case cmdReset:
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
