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

type channelTelemetryMeasurements struct {
	blockedAt     map[uint64]time.Time
	nextBlockedID uint64
	blockedMs     time.Duration

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

type channelTelemetry struct {
	mu sync.Mutex

	batches      <-chan []Transaction
	batchSize    int
	measurements channelTelemetryMeasurements
}

func (q *channelTelemetry) start(batches <-chan []Transaction, batchSize int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = batches
	q.batchSize = batchSize
}

func (q *channelTelemetry) send(ctx context.Context, batches chan<- []Transaction, batch []Transaction) bool {
	return q.sendWithBlocked(ctx, batches, batch, nil, nil)
}

func (q *channelTelemetry) sendWithBlocked(
	ctx context.Context, batches chan<- []Transaction, batch []Transaction,
	onBlocked, onSent func(),
) bool {
	select {
	case batches <- batch:
		q.recordSend(len(batch))
		return true
	case <-ctx.Done():
		return false
	default:
	}

	blockedID := q.startBlocked(time.Now())
	if onBlocked != nil {
		onBlocked()
	}
	select {
	case batches <- batch:
		q.finishBlockedWriter(blockedID, time.Now())
		if onSent != nil {
			onSent()
		}
		q.recordSend(len(batch))
		return true
	case <-ctx.Done():
		q.finishBlockedWriter(blockedID, time.Now())
		return false
	}
}

func (q *channelTelemetry) snapshot(now time.Time) channelMeasurements {
	q.mu.Lock()
	defer q.mu.Unlock()

	measurements := channelMeasurements{}
	totalBlocked := q.measurements.blockedMs
	if q.batches != nil {
		measurements.capacity = cap(q.batches)
		measurements.depthBatches = len(q.batches)
		measurements.bufferedTransactions = measurements.depthBatches * q.batchSize
	}
	measurements.blockedSenders = len(q.measurements.blockedAt)
	for _, since := range q.measurements.blockedAt {
		blocked := now.Sub(since)
		if blocked > 0 {
			if blocked.Milliseconds() > measurements.oldestBlockedSenderMs {
				measurements.oldestBlockedSenderMs = blocked.Milliseconds()
			}
			totalBlocked += blocked
		}
	}
	measurements.blockedMs = totalBlocked.Milliseconds()
	measurements.sentBatchesTotal = q.measurements.sentBatchesTotal
	measurements.sentTransactionsTotal = q.measurements.sentTransactionsTotal
	measurements.receivedBatchesTotal = q.measurements.receivedBatchesTotal
	measurements.receivedTransactionsTotal = q.measurements.receivedTransactionsTotal
	measurements.sentBatchesPerSecond = q.measurements.sentBatchesPerSecond
	measurements.sentTransactionsPerSecond = q.measurements.sentTransactionsPerSecond
	measurements.receivedBatchesPerSecond = q.measurements.receivedBatchesPerSecond
	measurements.receivedTransactionsPerSecond = q.measurements.receivedTransactionsPerSecond
	return measurements
}

func (q *channelTelemetry) recordSend(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.measurements.sentBatchesTotal++
	q.measurements.sentTransactionsTotal += int64(transactions)
	q.measurements.sentBatchesSinceTick++
	q.measurements.sentTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) recordReceive(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.measurements.receivedBatchesTotal++
	q.measurements.receivedTransactionsTotal += int64(transactions)
	q.measurements.receivedBatchesSinceTick++
	q.measurements.receivedTransactionsSinceTick += int64(transactions)
}

func (q *channelTelemetry) sample(window time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	seconds := window.Seconds()
	q.measurements.sentBatchesPerSecond = float64(q.measurements.sentBatchesSinceTick) / seconds
	q.measurements.sentTransactionsPerSecond = float64(q.measurements.sentTransactionsSinceTick) / seconds
	q.measurements.receivedBatchesPerSecond = float64(q.measurements.receivedBatchesSinceTick) / seconds
	q.measurements.receivedTransactionsPerSecond = float64(q.measurements.receivedTransactionsSinceTick) / seconds
	q.measurements.sentBatchesSinceTick = 0
	q.measurements.sentTransactionsSinceTick = 0
	q.measurements.receivedBatchesSinceTick = 0
	q.measurements.receivedTransactionsSinceTick = 0
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
	q.measurements = channelTelemetryMeasurements{}
}

func (q *channelTelemetry) startBlocked(now time.Time) uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.measurements.blockedAt == nil {
		q.measurements.blockedAt = make(map[uint64]time.Time)
	}
	q.measurements.nextBlockedID++
	id := q.measurements.nextBlockedID
	q.measurements.blockedAt[id] = now
	return id
}

// finishBlocked keeps the single-writer Sender path unchanged.
func (q *channelTelemetry) finishBlocked(now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id := range q.measurements.blockedAt {
		q.finishBlockedLocked(id, now)
		return
	}
}

func (q *channelTelemetry) finishBlockedWriter(id uint64, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.finishBlockedLocked(id, now)
}

func (q *channelTelemetry) finishBlockedLocked(id uint64, now time.Time) {
	since, ok := q.measurements.blockedAt[id]
	if !ok {
		return
	}
	if blocked := now.Sub(since); blocked > 0 {
		q.measurements.blockedMs += blocked
	}
	delete(q.measurements.blockedAt, id)
}
