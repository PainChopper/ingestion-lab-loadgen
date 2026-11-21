package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type QueueSnapshot struct {
	DepthBatches              int
	BlockedSenders            int
	OldestBlockedSenderMS     int64
	BlockedMS                 int64
	SentBatchesTotal          int64
	SentTransactionsTotal     int64
	ReceivedBatchesTotal      int64
	ReceivedTransactionsTotal int64
}

type queueMeasurements struct {
	blockedAt     map[uint64]time.Time
	nextBlockedID uint64
	blocked       time.Duration

	sentBatchesTotal          atomic.Int64
	sentTransactionsTotal     atomic.Int64
	receivedBatchesTotal      atomic.Int64
	receivedTransactionsTotal atomic.Int64
}

type batchQueue struct {
	mu sync.Mutex

	ch           chan []Transaction
	measurements queueMeasurements
}

type blockedChannelSend struct {
	queue     *batchQueue
	batch     []Transaction
	blockedID uint64
}

func newBatchQueue(capacity int) *batchQueue {
	return &batchQueue{ch: make(chan []Transaction, capacity)}
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
		queue:     q,
		batch:     batch,
		blockedID: q.startBlocked(time.Now()),
	}
}

func (send blockedChannelSend) wait(ctx context.Context) bool {
	select {
	case send.queue.ch <- send.batch:
		send.queue.finishBlockedWriter(send.blockedID, time.Now())
		send.queue.recordSend(len(send.batch))
		return true
	case <-ctx.Done():
		send.queue.finishBlockedWriter(send.blockedID, time.Now())
		return false
	}
}

func (q *batchQueue) snapshot(now time.Time) QueueSnapshot {
	measurements := QueueSnapshot{
		DepthBatches:              len(q.ch),
		SentBatchesTotal:          q.measurements.sentBatchesTotal.Load(),
		SentTransactionsTotal:     q.measurements.sentTransactionsTotal.Load(),
		ReceivedBatchesTotal:      q.measurements.receivedBatchesTotal.Load(),
		ReceivedTransactionsTotal: q.measurements.receivedTransactionsTotal.Load(),
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	totalBlocked := q.measurements.blocked
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
	return measurements
}

func (q *batchQueue) recordSend(transactions int) {
	q.measurements.sentBatchesTotal.Add(1)
	q.measurements.sentTransactionsTotal.Add(int64(transactions))
}

func (q *batchQueue) recordReceive(transactions int) {
	q.measurements.receivedBatchesTotal.Add(1)
	q.measurements.receivedTransactionsTotal.Add(int64(transactions))
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
