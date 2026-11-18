package pipeline

import (
	"context"
	"sync"
	"time"
)

type QueueSnapshot struct {
	Capacity                      int
	DepthBatches                  int
	BufferedTransactions          int
	BlockedSenders                int
	OldestBlockedSenderMS         int64
	BlockedMS                     int64
	SentBatchesTotal              int64
	SentTransactionsTotal         int64
	ReceivedBatchesTotal          int64
	ReceivedTransactionsTotal     int64
	SentBatchesPerSecond          float64
	SentTransactionsPerSecond     float64
	ReceivedBatchesPerSecond      float64
	ReceivedTransactionsPerSecond float64
}

type queueMeasurements struct {
	blockedAt     map[uint64]time.Time
	nextBlockedID uint64
	blocked       time.Duration

	SentBatchesTotal              int64
	SentTransactionsTotal         int64
	ReceivedBatchesTotal          int64
	ReceivedTransactionsTotal     int64
	SentBatchesPerSecond          float64
	SentTransactionsPerSecond     float64
	ReceivedBatchesPerSecond      float64
	ReceivedTransactionsPerSecond float64

	sentBatchesSinceTick          int64
	sentTransactionsSinceTick     int64
	receivedBatchesSinceTick      int64
	receivedTransactionsSinceTick int64
}

type batchQueue struct {
	mu sync.Mutex

	ch           chan []Transaction
	batchSize    int
	measurements queueMeasurements
}

type blockedChannelSend struct {
	telemetry *batchQueue
	batch     []Transaction
	blockedID uint64
}

func newBatchQueue(capacity, batchSize int) *batchQueue {
	return &batchQueue{ch: make(chan []Transaction, capacity), batchSize: batchSize}
}

func (q *batchQueue) trySend(batch []Transaction) bool {
	select {
	case q.ch <- batch:
		q.recordSend(len(batch))
		return true
	default:
		return false
	}
}

func (q *batchQueue) beginBlockedSend(batch []Transaction) blockedChannelSend {
	return blockedChannelSend{
		telemetry: q,
		batch:     batch,
		blockedID: q.startBlocked(time.Now()),
	}
}

func (send blockedChannelSend) wait(ctx context.Context) bool {
	select {
	case send.telemetry.ch <- send.batch:
		send.telemetry.finishBlockedWriter(send.blockedID, time.Now())
		send.telemetry.recordSend(len(send.batch))
		return true
	case <-ctx.Done():
		send.telemetry.finishBlockedWriter(send.blockedID, time.Now())
		return false
	}
}

func (q *batchQueue) snapshot(now time.Time) QueueSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	measurements := QueueSnapshot{}
	totalBlocked := q.measurements.blocked
	if q.ch != nil {
		measurements.Capacity = cap(q.ch)
		measurements.DepthBatches = len(q.ch)
		measurements.BufferedTransactions = measurements.DepthBatches * q.batchSize
	}
	measurements.BlockedSenders = len(q.measurements.blockedAt)
	for _, since := range q.measurements.blockedAt {
		blocked := now.Sub(since)
		if blocked > 0 {
			if blocked.Milliseconds() > measurements.OldestBlockedSenderMS {
				measurements.OldestBlockedSenderMS = blocked.Milliseconds()
			}
			totalBlocked += blocked
		}
	}
	measurements.BlockedMS = totalBlocked.Milliseconds()
	measurements.SentBatchesTotal = q.measurements.SentBatchesTotal
	measurements.SentTransactionsTotal = q.measurements.SentTransactionsTotal
	measurements.ReceivedBatchesTotal = q.measurements.ReceivedBatchesTotal
	measurements.ReceivedTransactionsTotal = q.measurements.ReceivedTransactionsTotal
	measurements.SentBatchesPerSecond = q.measurements.SentBatchesPerSecond
	measurements.SentTransactionsPerSecond = q.measurements.SentTransactionsPerSecond
	measurements.ReceivedBatchesPerSecond = q.measurements.ReceivedBatchesPerSecond
	measurements.ReceivedTransactionsPerSecond = q.measurements.ReceivedTransactionsPerSecond
	return measurements
}

func (q *batchQueue) recordSend(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.measurements.SentBatchesTotal++
	q.measurements.SentTransactionsTotal += int64(transactions)
	q.measurements.sentBatchesSinceTick++
	q.measurements.sentTransactionsSinceTick += int64(transactions)
}

func (q *batchQueue) recordReceive(transactions int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.measurements.ReceivedBatchesTotal++
	q.measurements.ReceivedTransactionsTotal += int64(transactions)
	q.measurements.receivedBatchesSinceTick++
	q.measurements.receivedTransactionsSinceTick += int64(transactions)
}

func (q *batchQueue) sample(window time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	seconds := window.Seconds()
	q.measurements.SentBatchesPerSecond = float64(q.measurements.sentBatchesSinceTick) / seconds
	q.measurements.SentTransactionsPerSecond = float64(q.measurements.sentTransactionsSinceTick) / seconds
	q.measurements.ReceivedBatchesPerSecond = float64(q.measurements.receivedBatchesSinceTick) / seconds
	q.measurements.ReceivedTransactionsPerSecond = float64(q.measurements.receivedTransactionsSinceTick) / seconds
	q.measurements.sentBatchesSinceTick = 0
	q.measurements.sentTransactionsSinceTick = 0
	q.measurements.receivedBatchesSinceTick = 0
	q.measurements.receivedTransactionsSinceTick = 0
}

func (q *batchQueue) startBlocked(now time.Time) uint64 {
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

func (q *batchQueue) finishBlockedWriter(id uint64, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.finishBlockedLocked(id, now)
}

func (q *batchQueue) finishBlockedLocked(id uint64, now time.Time) {
	since, ok := q.measurements.blockedAt[id]
	if !ok {
		return
	}
	if blocked := now.Sub(since); blocked > 0 {
		q.measurements.blocked += blocked
	}
	delete(q.measurements.blockedAt, id)
}
