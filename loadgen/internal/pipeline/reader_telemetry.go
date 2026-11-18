package pipeline

import (
	"sync"
	"time"
)

type readerTelemetry struct {
	mu           sync.Mutex
	RowsRead     int64
	ReadTPS      float64
	intervalAt   time.Time
	intervalRows int64
}

type readerMeasurements struct {
	ReadTPS  float64
	RowsRead int64
}

func (r *readerTelemetry) startInterval(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intervalAt = now
	r.intervalRows = r.RowsRead
	r.ReadTPS = 0
}

func (r *readerTelemetry) recordRead(n int) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.RowsRead += int64(n)
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
	r.ReadTPS = float64(r.RowsRead-r.intervalRows) / elapsed.Seconds()
	r.intervalAt = now
	r.intervalRows = r.RowsRead
}

func (r *readerTelemetry) snapshot() readerMeasurements {
	r.mu.Lock()
	defer r.mu.Unlock()
	measurements := readerMeasurements{
		ReadTPS:  r.ReadTPS,
		RowsRead: r.RowsRead,
	}
	return measurements
}
