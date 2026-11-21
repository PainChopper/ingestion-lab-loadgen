package pipeline

import "testing"

func TestReaderTelemetryCountsRows(t *testing.T) {
	var telemetry readerTelemetry
	telemetry.recordRead(0)
	telemetry.recordRead(-1)
	if got := telemetry.snapshot(); got != 0 {
		t.Fatalf("nonpositive reads counted: %d", got)
	}
	telemetry.recordRead(3)
	telemetry.recordRead(6)
	for range 2 {
		if got := telemetry.snapshot(); got != 9 {
			t.Fatalf("rows read = %d, want 9", got)
		}
	}
}
