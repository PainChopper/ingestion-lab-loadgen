//go:build integration

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestProduceBatchesRecordsActualParquetReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.parquet")
	rows := make([]Transaction, 1000)
	for i := range rows {
		rows[i].ClientID = "synthetic"
	}
	if err := parquet.WriteFile(path, rows); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var telemetry readerTelemetry
	var queueTelemetry queue1Telemetry
	telemetry.startInterval(time.Now())
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), defaultReadBatchSize, &telemetry, &queueTelemetry)
	if err != nil {
		t.Fatalf("start producer: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		got := telemetry.snapshot()
		if got.rowsRead >= 2000 {
			if got.source == nil || *got.source != filepath.ToSlash(path) {
				t.Fatalf("source = %v, want %q", got.source, filepath.ToSlash(path))
			}
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("read rows = %d, want repeated source reads", got.rowsRead)
		}
	}

	cancel()
	for range batches {
	}
}

func TestProduceBatchesUsesConfiguredSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.parquet")
	rows := make([]Transaction, 1500)
	for i := range rows {
		rows[i].ClientID = "synthetic"
	}
	if err := parquet.WriteFile(path, rows); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var telemetry readerTelemetry
	var queueTelemetry queue1Telemetry
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), 1000, &telemetry, &queueTelemetry)
	if err != nil {
		t.Fatalf("start producer: %v", err)
	}
	for range 3 {
		select {
		case batch := <-batches:
			if len(batch) != 1000 {
				t.Fatalf("batch length = %d, want 1000", len(batch))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("configured batch was not produced")
		}
	}
	cancel()
	for range batches {
	}
}
