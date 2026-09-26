package main

import (
	"sync"
	"time"
)

type readerTelemetry struct {
	mu           sync.Mutex
	rowsRead     int64
	readTPS      float64
	intervalAt   time.Time
	intervalRows int64
}

type readerMeasurements struct {
	readTPS  float64
	rowsRead int64
}

func (r *readerTelemetry) startInterval(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intervalAt = now
	r.intervalRows = r.rowsRead
}

func (r *readerTelemetry) recordRead(n int) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rowsRead += int64(n)
}

func (r *readerTelemetry) sample(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.intervalAt.IsZero() {
		return
	}
	elapsed := now.Sub(r.intervalAt)
	if elapsed <= 0 {
		return
	}
	r.readTPS = float64(r.rowsRead-r.intervalRows) / elapsed.Seconds()
	r.intervalAt = now
	r.intervalRows = r.rowsRead
}

func (r *readerTelemetry) snapshot() readerMeasurements {
	r.mu.Lock()
	defer r.mu.Unlock()
	measurements := readerMeasurements{
		readTPS:  r.readTPS,
		rowsRead: r.rowsRead,
	}
	return measurements
}

func (r *readerTelemetry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rowsRead = 0
	r.readTPS = 0
	r.intervalAt = time.Time{}
	r.intervalRows = 0
}
