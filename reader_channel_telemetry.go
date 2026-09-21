package main

import (
	"context"
	"sync"
	"time"
)

type channelMeasurements struct {
	capacity                    int
	depthBatches                int
	bufferedTransactions        int
	blockedSenders              int
	oldestBlockedSenderMs       int64
	blockedMs                   int64
	sentBatchesTotal            int64
	sentTransactionsTotal       int64
	receivedBatchesTotal        int64
	receivedTransactionsTotal   int64
	inputBatchesPerSecond       float64
	inputTransactionsPerSecond  float64
	outputBatchesPerSecond      float64
	outputTransactionsPerSecond float64
}

type channelTelemetry struct {
	mu sync.Mutex

	batches   <-chan []Transaction
	batchSize int

	blockedAt time.Time
	blockedMs time.Duration

	sentBatchesTotal            int64
	sentTransactionsTotal       int64
	receivedBatchesTotal        int64
	receivedTransactionsTotal   int64
	inputBatchesSinceTick       int64
	inputTransactionsSinceTick  int64
	outputBatchesSinceTick      int64
	outputTransactionsSinceTick int64
	inputBatchesPerSecond       float64
	inputTransactionsPerSecond  float64
	outputBatchesPerSecond      float64
	outputTransactionsPerSecond float64
}

func (q *channelTelemetry) start(batches <-chan []Transaction, batchSize int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = batches
	q.batchSize = batchSize
}

func (q *channelTelemetry) send(ctx context.Context, batches chan<- []Transaction, batch []Transaction) bool {
	if len(batches) != cap(batches) {
		select {
		case batches <- batch:
			q.recordSend(len(batch))
			return true
		case <-ctx.Done():
			return false
		}
	}

	q.startBlocked(time.Now())
	select {
	case batches <- batch:
		q.finishBlocked(time.Now())
		q.recordSend(len(batch))
		return true
	case <-ctx.Done():
		q.finishBlocked(time.Now())
		return false
	}
}

func (q *channelTelemetry) snapshot(now time.Time) channelMeasurements {
	q.mu.Lock()
	defer q.mu.Unlock()

	measurements := channelMeasurements{}
	totalBlocked := q.blockedMs
	if q.batches != nil {
		measurements.capacity = cap(q.batches)
		measurements.depthBatches = len(q.batches)
		measurements.bufferedTransactions = measurements.depthBatches * q.batchSize
	}
	if !q.blockedAt.IsZero() {
		measurements.blockedSenders = 1
		blocked := now.Sub(q.blockedAt)
		if blocked > 0 {
			measurements.oldestBlockedSenderMs = blocked.Milliseconds()
			totalBlocked += blocked
		}
	}
	measurements.blockedMs = totalBlocked.Milliseconds()
	measurements.sentBatchesTotal = q.sentBatchesTotal
	measurements.sentTransactionsTotal = q.sentTransactionsTotal
	measurements.receivedBatchesTotal = q.receivedBatchesTotal
	measurements.receivedTransactionsTotal = q.receivedTransactionsTotal
	measurements.inputBatchesPerSecond = q.inputBatchesPerSecond
	measurements.inputTransactionsPerSecond = q.inputTransactionsPerSecond
	measurements.outputBatchesPerSecond = q.outputBatchesPerSecond
	measurements.outputTransactionsPerSecond = q.outputTransactionsPerSecond
	return measurements
}

func (q *channelTelemetry) recordSend(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sentBatchesTotal++
	q.sentTransactionsTotal += int64(transactions)
	q.inputBatchesSinceTick++
	q.inputTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) recordReceive(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.receivedBatchesTotal++
	q.receivedTransactionsTotal += int64(transactions)
	q.outputBatchesSinceTick++
	q.outputTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) sample(window time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	seconds := window.Seconds()
	q.inputBatchesPerSecond = float64(q.inputBatchesSinceTick) / seconds
	q.inputTransactionsPerSecond = float64(q.inputTransactionsSinceTick) / seconds
	q.outputBatchesPerSecond = float64(q.outputBatchesSinceTick) / seconds
	q.outputTransactionsPerSecond = float64(q.outputTransactionsSinceTick) / seconds
	q.inputBatchesSinceTick = 0
	q.inputTransactionsSinceTick = 0
	q.outputBatchesSinceTick = 0
	q.outputTransactionsSinceTick = 0
}

func (q *channelTelemetry) clearMeasurements() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.clearMeasurementsLocked()
}

func (q *channelTelemetry) detach() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = nil
	q.batchSize = 0
	q.clearMeasurementsLocked()
}

func (q *channelTelemetry) clearMeasurementsLocked() {
	q.blockedAt = time.Time{}
	q.blockedMs = 0
	q.sentBatchesTotal = 0
	q.sentTransactionsTotal = 0
	q.receivedBatchesTotal = 0
	q.receivedTransactionsTotal = 0
	q.inputBatchesSinceTick = 0
	q.inputTransactionsSinceTick = 0
	q.outputBatchesSinceTick = 0
	q.outputTransactionsSinceTick = 0
	q.inputBatchesPerSecond = 0
	q.inputTransactionsPerSecond = 0
	q.outputBatchesPerSecond = 0
	q.outputTransactionsPerSecond = 0
}

func (q *channelTelemetry) startBlocked(now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.blockedAt = now
}

func (q *channelTelemetry) finishBlocked(now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.blockedAt.IsZero() {
		return
	}
	if blocked := now.Sub(q.blockedAt); blocked > 0 {
		q.blockedMs += blocked
	}
	q.blockedAt = time.Time{}
}
