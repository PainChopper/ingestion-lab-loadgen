package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type readerTelemetry struct {
	mu           sync.Mutex
	rowsRead     int64
	source       string
	readTPS      float64
	intervalAt   time.Time
	intervalRows int64
	slots        map[int]readerWorkerSlot
}

type readerMeasurements struct {
	readTPS         float64
	rowsRead        int64
	source          *string
	liveWorkers     int
	drainingWorkers int
	workerSlots     []readerWorkerSlot
}

type readerWorkerSlot struct {
	ID        string  `json:"id"`
	Ordinal   int     `json:"ordinal"`
	Activity  string  `json:"activity"`
	Lifecycle string  `json:"lifecycle"`
	Source    *string `json:"source"`
}

func (r *readerTelemetry) startInterval(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.intervalAt = now
	r.intervalRows = r.rowsRead
}

func (r *readerTelemetry) recordRead(n int, source string) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rowsRead += int64(n)
	r.source = source
}

func (r *readerTelemetry) registerWorker(ordinal int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slots == nil {
		r.slots = make(map[int]readerWorkerSlot)
	}
	r.slots[ordinal] = readerWorkerSlot{ID: fmt.Sprintf("reader-worker-%d", ordinal), Ordinal: ordinal, Activity: "idle", Lifecycle: "active"}
}

func (r *readerTelemetry) unregisterWorker(ordinal int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slots, ordinal)
}

func (r *readerTelemetry) setWorkerLifecycle(ordinal int, lifecycle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.slots[ordinal]
	if !ok {
		return
	}
	slot.Lifecycle = lifecycle
	r.slots[ordinal] = slot
}

func (r *readerTelemetry) setWorkerReading(ordinal int, source string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.slots[ordinal]
	if !ok {
		return
	}
	slot.Activity = "reading"
	slot.Source = &source
	r.slots[ordinal] = slot
}

func (r *readerTelemetry) setWorkerBlocked(ordinal int) {
	r.setWorkerActivity(ordinal, "blocked")
}

func (r *readerTelemetry) setWorkerCompleted(ordinal int) {
	r.setWorkerActivity(ordinal, "completed")
}

func (r *readerTelemetry) setWorkerActivity(ordinal int, activity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.slots[ordinal]
	if !ok {
		return
	}
	slot.Activity = activity
	r.slots[ordinal] = slot
}

func (r *readerTelemetry) setWorkerIdle(ordinal int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.slots[ordinal]
	if !ok {
		return
	}
	slot.Activity = "idle"
	slot.Source = nil
	r.slots[ordinal] = slot
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
		readTPS:     r.readTPS,
		rowsRead:    r.rowsRead,
		workerSlots: make([]readerWorkerSlot, 0, len(r.slots)),
	}
	for _, slot := range r.slots {
		measurements.workerSlots = append(measurements.workerSlots, slot)
		if slot.Lifecycle == "draining" {
			measurements.drainingWorkers++
		}
	}
	sort.Slice(measurements.workerSlots, func(i, j int) bool { return measurements.workerSlots[i].Ordinal < measurements.workerSlots[j].Ordinal })
	measurements.liveWorkers = len(measurements.workerSlots)
	if r.source != "" {
		source := r.source
		measurements.source = &source
	}
	return measurements
}

func (r *readerTelemetry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rowsRead = 0
	r.source = ""
	r.readTPS = 0
	r.intervalAt = time.Time{}
	r.intervalRows = 0
}
