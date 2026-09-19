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
	produce func(context.Context, int, int) (<-chan []Transaction, error),
) {
	var consumedSinceTick atomic.Int64
	var batches <-chan []Transaction
	var cancelConsumer context.CancelFunc
	var consumerDone <-chan struct{}
	var cancelProducer context.CancelFunc
	defer func() {
		if cancelConsumer != nil {
			cancelConsumer()
			<-consumerDone
		}
		if cancelProducer != nil {
			cancelProducer()
			for range batches {
			}
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
				reader := state.reader.snapshot()
				queue1 := state.queue1.snapshot(time.Now())
				if state.lifecycle.currentState() == runStateIdle {
					queue1.capacity = state.queue1Capacity()
				}
				snapshot := statusSnapshot{
					RunState:                          state.lifecycle.currentState(),
					TotalTransactions:                 state.totalTransactions,
					ReaderWorkers:                     1,
					ReaderReadBatchSize:               state.readBatchSize(),
					SenderWorkers:                     0,
					ElapsedMs:                         state.elapsedMs(time.Now()),
					StartError:                        state.startError,
					ReaderReadTPS:                     reader.readTPS,
					ReaderRowsRead:                    reader.rowsRead,
					ReaderSource:                      reader.source,
					Queue1Capacity:                    queue1.capacity,
					Queue1DepthBatches:                queue1.depthBatches,
					Queue1QueuedTransactions:          queue1.queuedTransactions,
					Queue1BlockedSenders:              queue1.blockedSenders,
					Queue1OldestBlockedSenderMs:       queue1.oldestBlockedSenderMs,
					Queue1BlockedMs:                   queue1.blockedMs,
					Queue1EnqueuedBatchesTotal:        queue1.enqueuedBatchesTotal,
					Queue1EnqueuedTransactionsTotal:   queue1.enqueuedTransactionsTotal,
					Queue1DequeuedBatchesTotal:        queue1.dequeuedBatchesTotal,
					Queue1DequeuedTransactionsTotal:   queue1.dequeuedTransactionsTotal,
					Queue1InputBatchesPerSecond:       queue1.inputBatchesPerSecond,
					Queue1InputTransactionsPerSecond:  queue1.inputTransactionsPerSecond,
					Queue1OutputBatchesPerSecond:      queue1.outputBatchesPerSecond,
					Queue1OutputTransactionsPerSecond: queue1.outputTransactionsPerSecond,
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				if state.lifecycle.currentState() == runStateIdle {
					state.reader.startInterval(time.Now())
					producerContext, cancel := context.WithCancel(context.Background())
					var err error
					batches, err = produce(producerContext, state.readBatchSize(), state.queue1Capacity())
					if err != nil {
						cancel()
						state.reader.reset()
						log.Printf("cannot start load generator: %v", err)
						message := err.Error()
						state.startError = &message
						if cmd.commandReply != nil {
							cmd.commandReply <- commandResult{err: err}
						}
						continue
					}
					cancelProducer = cancel
				}
				if state.lifecycle.run() {
					state.runStartedAt = time.Now()
					state.startError = nil
					cancelConsumer, consumerDone = startConsumer(batches, &consumedSinceTick, &state.queue1)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			case cmdPause:
				if state.lifecycle.currentState() != runStateRunning {
					continue
				}
				cancelConsumer()
				<-consumerDone
				state.pauseElapsed(time.Now())
				cancelConsumer = nil
				consumerDone = nil
				delta := consumedSinceTick.Swap(0)
				state.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				state.actualTPS = 0
				promMetrics.actualTPS.Set(0)
				state.lifecycle.pause()
			case cmdReset:
				result := commandResult{status: commandAccepted}
				switch state.lifecycle.currentState() {
				case runStatePaused:
					state.lifecycle.reset()
					cancelProducer()
					for range batches {
					}
					batches = nil
					cancelProducer = nil
					state.resetProgress(&consumedSinceTick, promMetrics)
					state.lifecycle.completeReset()
				case runStateIdle:
					state.resetProgress(&consumedSinceTick, promMetrics)
				default:
					result.status = commandConflict
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReadBatchSize:
				result := commandResult{status: commandAccepted}
				if state.lifecycle.currentState() != runStateIdle {
					result.status = commandConflict
				} else if !validReadBatchSize(cmd.value) {
					result.status = commandConflict
				} else {
					state.configuredReadBatchSize = cmd.value
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetQueueCapacity:
				result := commandResult{status: commandAccepted}
				if state.lifecycle.currentState() != runStateIdle || !validQueue1Capacity(cmd.value) {
					result.status = commandConflict
				} else {
					state.configuredQueue1Capacity = cmd.value
					state.queue1CapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			}

		case <-metrics:
			state.reader.sample(time.Now())
			state.queue1.sample(windowLength)
			delta := consumedSinceTick.Swap(0)
			state.actualTPS = delta * int64(time.Second/windowLength)
			state.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(state.actualTPS))
			promMetrics.transactionsTotal.Add(float64(delta))
		}
	}
}

func (state *controlState) resetProgress(consumedSinceTick *atomic.Int64, promMetrics *Metrics) {
	state.reader.reset()
	state.queue1.reset()
	consumedSinceTick.Store(0)
	state.totalTransactions = 0
	state.actualTPS = 0
	state.elapsedBeforeRun = 0
	state.runStartedAt = time.Time{}
	state.startError = nil
	promMetrics.actualTPS.Set(0)
}

func (state *controlState) elapsedMs(now time.Time) int64 {
	elapsed := state.elapsedBeforeRun
	if state.lifecycle.currentState() == runStateRunning {
		elapsed += now.Sub(state.runStartedAt)
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}

func (state *controlState) pauseElapsed(now time.Time) {
	state.elapsedBeforeRun += now.Sub(state.runStartedAt)
	state.runStartedAt = time.Time{}
}

func startConsumer(
	batches <-chan []Transaction,
	consumedSinceTick *atomic.Int64,
	queueTelemetry *queue1Telemetry,
) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(ctx, batches, consumedSinceTick, queueTelemetry)
	}()
	return cancel, done
}
