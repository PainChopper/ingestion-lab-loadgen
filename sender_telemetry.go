package main

import (
	"sort"
	"sync"
)

type senderWorkerSlot struct {
	WorkerID      int    `json:"workerId"`
	Activity      string `json:"activity"`
	Lifecycle     string `json:"lifecycle"`
	TerminalError bool   `json:"terminalError"`
}

type senderMeasurements struct {
	liveWorkers      int
	drainingWorkers  int
	workerSlots      []senderWorkerSlot
	completedBatches int64
}

type senderTelemetry struct {
	mu               sync.Mutex
	slots            map[int]senderWorkerSlot
	completedBatches int64
}

func (t *senderTelemetry) startWorker(workerID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.slots == nil {
		t.slots = make(map[int]senderWorkerSlot)
	}
	t.slots[workerID] = senderWorkerSlot{
		WorkerID:  workerID,
		Activity:  "idle",
		Lifecycle: "active",
	}
}

func (t *senderTelemetry) finishWorker(workerID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.slots, workerID)
}

func (t *senderTelemetry) setLifecycle(workerID int, lifecycle string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[workerID]
	if !ok {
		return
	}
	slot.Lifecycle = lifecycle
	t.slots[workerID] = slot
}

func (t *senderTelemetry) setActivity(workerID int, activity string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[workerID]
	if !ok {
		return
	}
	slot.Activity = activity
	t.slots[workerID] = slot
}

func (t *senderTelemetry) finishBatch(workerID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot, ok := t.slots[workerID]
	if !ok {
		return
	}
	slot.Activity = "idle"
	slot.TerminalError = false
	t.slots[workerID] = slot
	t.completedBatches++
}

func (t *senderTelemetry) snapshot() senderMeasurements {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := senderMeasurements{
		workerSlots:      make([]senderWorkerSlot, 0, len(t.slots)),
		completedBatches: t.completedBatches,
	}
	for _, slot := range t.slots {
		result.workerSlots = append(result.workerSlots, slot)
		if slot.Lifecycle == "draining" {
			result.drainingWorkers++
		}
	}
	sort.Slice(result.workerSlots, func(i, j int) bool {
		return result.workerSlots[i].WorkerID < result.workerSlots[j].WorkerID
	})
	result.liveWorkers = len(result.workerSlots)
	return result
}

func (t *senderTelemetry) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedBatches = 0
}
