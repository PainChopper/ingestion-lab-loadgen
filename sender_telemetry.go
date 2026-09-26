package main

import "sync"

type senderMeasurements struct {
	completedBatches int64
}

type senderTelemetry struct {
	mu               sync.Mutex
	completedBatches int64
}

func (t *senderTelemetry) finishBatch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedBatches++
}

func (t *senderTelemetry) snapshot() senderMeasurements {
	t.mu.Lock()
	defer t.mu.Unlock()
	return senderMeasurements{completedBatches: t.completedBatches}
}

func (t *senderTelemetry) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedBatches = 0
}
