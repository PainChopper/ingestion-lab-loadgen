package main

import (
	"context"
	"sync"
	"time"
)

type queue1Measurements struct {
	capacity                    int
	depthBatches                int
	queuedTransactions          int
	blockedSenders              int
	oldestBlockedSenderMs       int64
	blockedMs                   int64
	enqueuedBatchesTotal        int64
	enqueuedTransactionsTotal   int64
	dequeuedBatchesTotal        int64
	dequeuedTransactionsTotal   int64
	inputBatchesPerSecond       float64
	inputTransactionsPerSecond  float64
	outputBatchesPerSecond      float64
	outputTransactionsPerSecond float64
}

type queue1Telemetry struct {
	mu sync.Mutex

	batches   <-chan []Transaction
	batchSize int

	blockedAt time.Time
	blockedMs time.Duration

	enqueuedBatchesTotal        int64
	enqueuedTransactionsTotal   int64
	dequeuedBatchesTotal        int64
	dequeuedTransactionsTotal   int64
	inputBatchesSinceTick       int64
	inputTransactionsSinceTick  int64
	outputBatchesSinceTick      int64
	outputTransactionsSinceTick int64
	inputBatchesPerSecond       float64
	inputTransactionsPerSecond  float64
	outputBatchesPerSecond      float64
	outputTransactionsPerSecond float64
}

func (q *queue1Telemetry) start(batches <-chan []Transaction, batchSize int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = batches
	q.batchSize = batchSize
}

func (q *queue1Telemetry) send(ctx context.Context, batches chan<- []Transaction, batch []Transaction) bool {
	if len(batches) != cap(batches) {
		select {
		case batches <- batch:
			q.recordEnqueue(len(batch))
			return true
		case <-ctx.Done():
			return false
		}
	}

	q.startBlocked(time.Now())
	select {
	case batches <- batch:
		q.finishBlocked(time.Now())
		q.recordEnqueue(len(batch))
		return true
	case <-ctx.Done():
		q.finishBlocked(time.Now())
		return false
	}
}

func (q *queue1Telemetry) snapshot(now time.Time) queue1Measurements {
	q.mu.Lock()
	defer q.mu.Unlock()

	measurements := queue1Measurements{}
	totalBlocked := q.blockedMs
	if q.batches != nil {
		measurements.capacity = cap(q.batches)
		measurements.depthBatches = len(q.batches)
		measurements.queuedTransactions = measurements.depthBatches * q.batchSize
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
	measurements.enqueuedBatchesTotal = q.enqueuedBatchesTotal
	measurements.enqueuedTransactionsTotal = q.enqueuedTransactionsTotal
	measurements.dequeuedBatchesTotal = q.dequeuedBatchesTotal
	measurements.dequeuedTransactionsTotal = q.dequeuedTransactionsTotal
	measurements.inputBatchesPerSecond = q.inputBatchesPerSecond
	measurements.inputTransactionsPerSecond = q.inputTransactionsPerSecond
	measurements.outputBatchesPerSecond = q.outputBatchesPerSecond
	measurements.outputTransactionsPerSecond = q.outputTransactionsPerSecond
	return measurements
}

func (q *queue1Telemetry) recordEnqueue(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.enqueuedBatchesTotal++
	q.enqueuedTransactionsTotal += int64(transactions)
	q.inputBatchesSinceTick++
	q.inputTransactionsSinceTick += int64(transactions)
}

func (q *queue1Telemetry) recordDequeue(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dequeuedBatchesTotal++
	q.dequeuedTransactionsTotal += int64(transactions)
	q.outputBatchesSinceTick++
	q.outputTransactionsSinceTick += int64(transactions)
}

func (q *queue1Telemetry) sample(window time.Duration) {
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

func (q *queue1Telemetry) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = nil
	q.batchSize = 0
	q.blockedAt = time.Time{}
	q.blockedMs = 0
	q.enqueuedBatchesTotal = 0
	q.enqueuedTransactionsTotal = 0
	q.dequeuedBatchesTotal = 0
	q.dequeuedTransactionsTotal = 0
	q.inputBatchesSinceTick = 0
	q.inputTransactionsSinceTick = 0
	q.outputBatchesSinceTick = 0
	q.outputTransactionsSinceTick = 0
	q.inputBatchesPerSecond = 0
	q.inputTransactionsPerSecond = 0
	q.outputBatchesPerSecond = 0
	q.outputTransactionsPerSecond = 0
}

func (q *queue1Telemetry) startBlocked(now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.blockedAt = now
}

func (q *queue1Telemetry) finishBlocked(now time.Time) {
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
