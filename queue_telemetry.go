package main

import (
	"context"
	"sync"
	"time"
)

const batchReadAheadCapacity = 2

type queue1Measurements struct {
	capacity              int
	depthBatches          int
	queuedTransactions    int
	blockedSenders        int
	oldestBlockedSenderMs int64
	blockedMs             int64
}

type queue1Telemetry struct {
	mu sync.Mutex

	batches   <-chan []Transaction
	batchSize int

	blockedAt time.Time
	blockedMs time.Duration
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
			return true
		case <-ctx.Done():
			return false
		}
	}

	q.startBlocked(time.Now())
	select {
	case batches <- batch:
		q.finishBlocked(time.Now())
		return true
	case <-ctx.Done():
		q.finishBlocked(time.Now())
		return false
	}
}

func (q *queue1Telemetry) snapshot(now time.Time) queue1Measurements {
	q.mu.Lock()
	defer q.mu.Unlock()

	measurements := queue1Measurements{
		capacity: batchReadAheadCapacity,
	}
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
	return measurements
}

func (q *queue1Telemetry) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = nil
	q.batchSize = 0
	q.blockedAt = time.Time{}
	q.blockedMs = 0
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
