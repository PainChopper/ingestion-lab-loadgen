package main

import (
	"context"
	"sync"
	"time"
)

type channelMeasurements struct {
	capacity                      int
	depthBatches                  int
	bufferedTransactions          int
	blockedSenders                int
	oldestBlockedSenderMs         int64
	blockedMs                     int64
	sentBatchesTotal              int64
	sentTransactionsTotal         int64
	receivedBatchesTotal          int64
	receivedTransactionsTotal     int64
	sentBatchesPerSecond          float64
	sentTransactionsPerSecond     float64
	receivedBatchesPerSecond      float64
	receivedTransactionsPerSecond float64
}

type channelTelemetry struct {
	mu sync.Mutex

	batches   <-chan []Transaction
	batchSize int

	blockedAt time.Time
	blockedMs time.Duration

	sentBatchesTotal              int64
	sentTransactionsTotal         int64
	receivedBatchesTotal          int64
	receivedTransactionsTotal     int64
	sentBatchesPerSecond          float64
	sentTransactionsPerSecond     float64
	receivedBatchesPerSecond      float64
	receivedTransactionsPerSecond float64

	sentBatchesSinceTick          int64
	sentTransactionsSinceTick     int64
	receivedBatchesSinceTick      int64
	receivedTransactionsSinceTick int64
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
	measurements.sentBatchesPerSecond = q.sentBatchesPerSecond
	measurements.sentTransactionsPerSecond = q.sentTransactionsPerSecond
	measurements.receivedBatchesPerSecond = q.receivedBatchesPerSecond
	measurements.receivedTransactionsPerSecond = q.receivedTransactionsPerSecond
	return measurements
}

func (q *channelTelemetry) recordSend(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sentBatchesTotal++
	q.sentTransactionsTotal += int64(transactions)
	q.sentBatchesSinceTick++
	q.sentTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) recordReceive(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.receivedBatchesTotal++
	q.receivedTransactionsTotal += int64(transactions)
	q.receivedBatchesSinceTick++
	q.receivedTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) sample(window time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	seconds := window.Seconds()
	q.sentBatchesPerSecond = float64(q.sentBatchesSinceTick) / seconds
	q.sentTransactionsPerSecond = float64(q.sentTransactionsSinceTick) / seconds
	q.receivedBatchesPerSecond = float64(q.receivedBatchesSinceTick) / seconds
	q.receivedTransactionsPerSecond = float64(q.receivedTransactionsSinceTick) / seconds
	q.sentBatchesSinceTick = 0
	q.sentTransactionsSinceTick = 0
	q.receivedBatchesSinceTick = 0
	q.receivedTransactionsSinceTick = 0
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
	q.sentBatchesPerSecond = 0
	q.sentTransactionsPerSecond = 0
	q.receivedBatchesPerSecond = 0
	q.receivedTransactionsPerSecond = 0
	q.sentBatchesSinceTick = 0
	q.sentTransactionsSinceTick = 0
	q.receivedBatchesSinceTick = 0
	q.receivedTransactionsSinceTick = 0
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
