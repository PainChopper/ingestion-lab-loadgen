//go:build integration

package main

import (
	"context"
	"fmt"
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
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), defaultReadBatchSize, defaultQueue1Capacity, &telemetry, &queueTelemetry)
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
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), 1000, defaultQueue1Capacity, &telemetry, &queueTelemetry)
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

func TestProduceBatchesUsesConfiguredQueueCapacity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.parquet")
	rows := []Transaction{{ClientID: "synthetic"}}
	if err := parquet.WriteFile(path, rows); err != nil {
		t.Fatalf("write parquet fixture: %v", err)
	}

	for _, capacity := range []int{0, 1, 8_192} {
		t.Run(fmt.Sprintf("capacity-%d", capacity), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var telemetry readerTelemetry
			var queueTelemetry queue1Telemetry
			batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), 1, capacity, &telemetry, &queueTelemetry)
			if err != nil {
				cancel()
				t.Fatalf("start producer: %v", err)
			}
			if got := cap(batches); got != capacity {
				cancel()
				t.Fatalf("producer channel capacity = %d, want %d", got, capacity)
			}
			if got := queueTelemetry.snapshot(time.Now()).capacity; got != capacity {
				cancel()
				t.Fatalf("telemetry capacity = %d, want %d", got, capacity)
			}
			cancel()
			for range batches {
			}
		})
	}
}
