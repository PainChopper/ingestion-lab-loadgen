//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestProduceBatchesReadsNestedDefaultParquet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data", "MBD-mini", "trx", "fold=0", "input.parquet")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create nested fixture directory: %v", err)
	}
	if err := parquet.WriteFile(path, []Transaction{{ClientID: "nested fixture"}}); err != nil {
		t.Fatalf("write nested parquet fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var telemetry readerTelemetry
	var readerChannelTelemetry readerChannelTelemetry
	policy := testPolicy(t)
	pattern := filepath.Join(dir, "data", "MBD-mini", "trx", "fold=*", "*.parquet")
	batches, err := produceBatches(ctx, pattern, 1, policy.ReaderChannel.Capacity.Default, &telemetry, &readerChannelTelemetry)
	if err != nil {
		t.Fatalf("start producer with default pattern: %v", err)
	}

	select {
	case batch := <-batches:
		if len(batch) != 1 || batch[0].ClientID != "nested fixture" {
			t.Fatalf("batch = %v, want nested fixture row", batch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested parquet row was not produced")
	}
	cancel()
	for range batches {
	}
}

func TestProduceBatchesRejectsEmptyAndInvalidPatterns(t *testing.T) {
	for _, test := range []struct {
		name    string
		pattern string
		badGlob bool
	}{
		{name: "empty pattern", pattern: ""},
		{name: "invalid pattern", pattern: "[", badGlob: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var telemetry readerTelemetry
			var readerChannelTelemetry readerChannelTelemetry
			batches, err := produceBatches(context.Background(), test.pattern, 1, 1, &telemetry, &readerChannelTelemetry)
			if err == nil || batches != nil {
				t.Fatalf("produceBatches(%q) = (%v, %v), want nil channel and error", test.pattern, batches, err)
			}
			if test.badGlob && !errors.Is(err, filepath.ErrBadPattern) {
				t.Fatalf("error = %v, want bad glob pattern", err)
			}
		})
	}
}

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
	var readerChannelTelemetry readerChannelTelemetry
	telemetry.startInterval(time.Now())
	policy := testPolicy(t)
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), policy.Reader.ReadBatchSize.Default, policy.ReaderChannel.Capacity.Default, &telemetry, &readerChannelTelemetry)
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
	var readerChannelTelemetry readerChannelTelemetry
	policy := testPolicy(t)
	batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), policy.Reader.ReadBatchSize.Min, policy.ReaderChannel.Capacity.Default, &telemetry, &readerChannelTelemetry)
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

func TestProduceBatchesUsesConfiguredReaderChannelCapacity(t *testing.T) {
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
			var readerChannelTelemetry readerChannelTelemetry
			batches, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), 1, capacity, &telemetry, &readerChannelTelemetry)
			if err != nil {
				cancel()
				t.Fatalf("start producer: %v", err)
			}
			if got := cap(batches); got != capacity {
				cancel()
				t.Fatalf("producer channel capacity = %d, want %d", got, capacity)
			}
			if got := readerChannelTelemetry.snapshot(time.Now()).capacity; got != capacity {
				cancel()
				t.Fatalf("telemetry capacity = %d, want %d", got, capacity)
			}
			cancel()
			for range batches {
			}
		})
	}
}
