package main

import (
	"context"
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
	var batches <-chan []Transaction
	var cancelConsumer context.CancelFunc
	var consumerDone <-chan struct{}
	defer func() {
		if cancelConsumer != nil {
			cancelConsumer()
			<-consumerDone
		}
	}()

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
				if state.lifecycle.currentState() == runStateIdle {
					var err error
					batches, err = produce()
					if err != nil {
						log.Printf("cannot start load generator: %v", err)
						continue
					}
				}
				if state.lifecycle.run() {
					cancelConsumer, consumerDone = startConsumer(batches, &consumedSinceTick)
				}
			case cmdPause:
				if state.lifecycle.currentState() != runStateRunning {
					continue
				}
				cancelConsumer()
				<-consumerDone
				cancelConsumer = nil
				consumerDone = nil
				delta := consumedSinceTick.Swap(0)
				state.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				state.actualTPS = 0
				promMetrics.actualTPS.Set(0)
				state.lifecycle.pause()
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

func startConsumer(
	batches <-chan []Transaction,
	consumedSinceTick *atomic.Int64,
) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(ctx, batches, consumedSinceTick)
	}()
	return cancel, done
}
