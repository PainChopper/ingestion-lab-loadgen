package main

import "testing"

func TestSenderTelemetryReturnsSortedIndependentSlotCopies(t *testing.T) {
	var telemetry senderTelemetry
	telemetry.startWorker(2)
	telemetry.startWorker(0)
	telemetry.setLifecycle(2, "draining")
	telemetry.setActivity(2, "backoff")
	telemetry.finishBatch(0, false)
	snapshot := telemetry.snapshot()
	if snapshot.liveWorkers != 2 || snapshot.drainingWorkers != 1 || snapshot.terminalBatches != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.workerSlots[0].Ordinal != 0 || snapshot.workerSlots[1].Ordinal != 2 {
		t.Fatalf("slots not sorted: %+v", snapshot.workerSlots)
	}
	if !snapshot.workerSlots[0].TerminalError || snapshot.workerSlots[1].Lifecycle != "draining" {
		t.Fatalf("slot dimensions = %+v", snapshot.workerSlots)
	}
	snapshot.workerSlots[0].TerminalError = false
	if !telemetry.snapshot().workerSlots[0].TerminalError {
		t.Fatal("snapshot mutated internal telemetry")
	}
}
