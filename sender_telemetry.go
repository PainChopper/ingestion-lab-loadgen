package main

import (
	"fmt"
	"sort"
	"sync"
)

type senderWorkerSlot struct {
	ID            string `json:"id"`
	Ordinal       int    `json:"ordinal"`
	Activity      string `json:"activity"`
	Lifecycle     string `json:"lifecycle"`
	TerminalError bool   `json:"terminalError"`
}

type senderMeasurements struct {
	liveWorkers     int
	drainingWorkers int
	workerSlots     []senderWorkerSlot
	terminalBatches int64
}

type senderTelemetry struct {
	mu              sync.Mutex
	slots           map[int]senderWorkerSlot
	terminalBatches int64
}

func (t *senderTelemetry) startWorker(ordinal int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.slots == nil {
		t.slots = make(map[int]senderWorkerSlot)
	}
	t.slots[ordinal] = senderWorkerSlot{
		ID:        fmt.Sprintf("sender-worker-%d", ordinal),
		Ordinal:   ordinal,
		Activity:  "idle",
		Lifecycle: "active",
	}
}

func (t *senderTelemetry) finishWorker(ordinal int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.slots, ordinal)
}

func (t *senderTelemetry) setLifecycle(ordinal int, lifecycle string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[ordinal]
	if !ok {
		return
	}
	slot.Lifecycle = lifecycle
	t.slots[ordinal] = slot
}

func (t *senderTelemetry) setActivity(ordinal int, activity string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[ordinal]
	if !ok {
		return
	}
	slot.Activity = activity
	t.slots[ordinal] = slot
}

func (t *senderTelemetry) finishBatch(ordinal int, success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[ordinal]
	if !ok {
		return
	}
	slot.Activity = "idle"
	slot.TerminalError = !success
	t.slots[ordinal] = slot
	t.terminalBatches++
}

func (t *senderTelemetry) snapshot() senderMeasurements {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := senderMeasurements{
		workerSlots:     make([]senderWorkerSlot, 0, len(t.slots)),
		terminalBatches: t.terminalBatches,
	}
	for _, slot := range t.slots {
		result.workerSlots = append(result.workerSlots, slot)
		if slot.Lifecycle == "draining" {
			result.drainingWorkers++
		}
	}
	sort.Slice(result.workerSlots, func(i, j int) bool {
		return result.workerSlots[i].Ordinal < result.workerSlots[j].Ordinal
	})
	result.liveWorkers = len(result.workerSlots)
	return result
}

func (t *senderTelemetry) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.terminalBatches = 0
}
