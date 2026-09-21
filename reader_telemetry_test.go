package main

import (
	"testing"
	"time"
)

func TestReaderTelemetryMeasuresElapsedIntervals(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	var telemetry readerTelemetry
	if got := telemetry.snapshot(); got.readTPS != 0 || got.rowsRead != 0 || got.source != nil {
		t.Fatalf("initial measurements = %+v", got)
	}

	telemetry.startInterval(start)
	telemetry.recordRead(3, "data/part-1.parquet")
	telemetry.sample(start.Add(1500 * time.Millisecond))
	got := telemetry.snapshot()
	if got.readTPS != 2 || got.rowsRead != 3 || got.source == nil || *got.source != "data/part-1.parquet" {
		t.Fatalf("first interval = %+v, want 2 rows/s, 3 rows, first source", got)
	}

	telemetry.recordRead(6, "data/part-2.parquet")
	telemetry.sample(start.Add(3500 * time.Millisecond))
	got = telemetry.snapshot()
	if got.readTPS != 3 || got.rowsRead != 9 || got.source == nil || *got.source != "data/part-2.parquet" {
		t.Fatalf("second interval = %+v, want 3 rows/s, 9 rows, second source", got)
	}

	telemetry.sample(start.Add(4500 * time.Millisecond))
	got = telemetry.snapshot()
	if got.readTPS != 0 || got.rowsRead != 9 {
		t.Fatalf("idle interval = %+v, want zero rate and 9 rows", got)
	}

	telemetry.reset()
	got = telemetry.snapshot()
	if got.readTPS != 0 || got.rowsRead != 0 || got.source != nil {
		t.Fatalf("after Reset = %+v, want zero measurements and null source", got)
	}
}
