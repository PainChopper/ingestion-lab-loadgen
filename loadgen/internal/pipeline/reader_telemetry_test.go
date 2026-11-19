package pipeline

import (
	"testing"
	"time"
)

func TestReaderTelemetryMeasuresElapsedIntervals(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	var telemetry readerTelemetry
	if got := telemetry.snapshot(); got.ReadTPS != 0 || got.RowsRead != 0 {
		t.Fatalf("initial measurements = %+v", got)
	}
	telemetry.sample(start)
	telemetry.recordRead(0)
	telemetry.recordRead(-1)
	if got := telemetry.snapshot(); got != (readerMeasurements{}) {
		t.Fatalf("unstarted sample or nonpositive read changed measurements: %+v", got)
	}

	telemetry.startInterval(start)
	telemetry.recordRead(3)
	for _, test := range []struct {
		name string
		now  time.Time
	}{
		{name: "same time", now: start},
		{name: "earlier time", now: start.Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			telemetry.sample(test.now)
			if got := telemetry.snapshot(); got.RowsRead != 3 || got.ReadTPS != 0 {
				t.Fatalf("nonpositive interval changed measurements: %+v", got)
			}
		})
	}
	telemetry.sample(start.Add(1500 * time.Millisecond))
	got := telemetry.snapshot()
	if got.ReadTPS != 2 || got.RowsRead != 3 {
		t.Fatalf("first interval = %+v, want 2 rows/s and 3 rows", got)
	}

	telemetry.recordRead(6)
	telemetry.sample(start.Add(3500 * time.Millisecond))
	got = telemetry.snapshot()
	if got.ReadTPS != 3 || got.RowsRead != 9 {
		t.Fatalf("second interval = %+v, want 3 rows/s and 9 rows", got)
	}

	telemetry.sample(start.Add(4500 * time.Millisecond))
	got = telemetry.snapshot()
	if got.ReadTPS != 0 || got.RowsRead != 9 {
		t.Fatalf("idle interval = %+v, want zero rate and 9 rows", got)
	}
	telemetry.recordRead(5)
	telemetry.startInterval(start.Add(10 * time.Second))
	telemetry.recordRead(2)
	telemetry.sample(start.Add(11 * time.Second))
	if got := telemetry.snapshot(); got.RowsRead != 16 || got.ReadTPS != 2 {
		t.Fatalf("new interval included paused rows or lost totals: %+v", got)
	}
}
