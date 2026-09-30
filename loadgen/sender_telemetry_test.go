package main

import "testing"

func TestSenderTelemetryCountsCompletedBatches(t *testing.T) {
	var telemetry senderTelemetry
	telemetry.finishBatch()
	snapshot := telemetry.snapshot()
	if snapshot.completedBatches != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
