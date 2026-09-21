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
	var channelTelemetry channelTelemetry
	policy := testPolicy(t)
	pattern := filepath.Join(dir, "data", "MBD-mini", "trx", "fold=*", "*.parquet")
	batches := make(chan []Transaction, policy.ReaderChannel.Capacity.Default)
	channelTelemetry.start(batches, 1)
	done, err := produceBatches(ctx, pattern, 1, batches, &telemetry, &channelTelemetry)
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
	<-done
	drain(batches)
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
			var channelTelemetry channelTelemetry
			batches := make(chan []Transaction, 1)
			done, err := produceBatches(context.Background(), test.pattern, 1, batches, &telemetry, &channelTelemetry)
			if err == nil || done != nil {
				t.Fatalf("produceBatches(%q) = (%v, %v), want nil done and error", test.pattern, done, err)
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
	var channelTelemetry channelTelemetry
	telemetry.startInterval(time.Now())
	policy := testPolicy(t)
	batches := make(chan []Transaction, policy.ReaderChannel.Capacity.Default)
	channelTelemetry.start(batches, policy.Reader.ReadBatchSize.Default)
	done, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), policy.Reader.ReadBatchSize.Default, batches, &telemetry, &channelTelemetry)
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
	<-done
	drain(batches)
}

func TestProduceBatchesKeepsSourcePerFileVisitAndPreservesEmittedBatches(t *testing.T) {
	const batchSize = 1_000

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "part-000.parquet")
	secondPath := filepath.Join(dir, "part-001.parquet")
	firstRows := producerFixtureTransactions("first", 2*batchSize)
	secondRows := producerFixtureTransactions("second", batchSize)
	writeProducerFixture(t, firstPath, firstRows)
	writeProducerFixture(t, secondPath, secondRows)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var telemetry readerTelemetry
	var channelTelemetry channelTelemetry
	batches := make(chan []Transaction)
	channelTelemetry.start(batches, batchSize)
	done, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), batchSize, batches, &telemetry, &channelTelemetry)
	if err != nil {
		t.Fatalf("start producer: %v", err)
	}

	first := receiveProducerBatch(t, batches, batchSize)
	firstIDs := producerFixtureIDs(first)
	waitForReaderSource(t, &telemetry, 2*batchSize, filepath.ToSlash(firstPath))

	second := receiveProducerBatch(t, batches, batchSize)
	assertProducerFixtureIDs(t, second, producerFixtureIDs(firstRows[batchSize:]))
	assertProducerFixtureIDs(t, first, firstIDs)
	waitForReaderSource(t, &telemetry, 3*batchSize, filepath.ToSlash(secondPath))

	third := receiveProducerBatch(t, batches, batchSize)
	assertProducerFixtureIDs(t, third, producerFixtureIDs(secondRows))
	assertProducerFixtureIDs(t, first, firstIDs)

	cancel()
	<-done
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
	var channelTelemetry channelTelemetry
	policy := testPolicy(t)
	batches := make(chan []Transaction, policy.ReaderChannel.Capacity.Default)
	channelTelemetry.start(batches, policy.Reader.ReadBatchSize.Min)
	done, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), policy.Reader.ReadBatchSize.Min, batches, &telemetry, &channelTelemetry)
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
	<-done
	drain(batches)
}

func TestProduceBatchesPreservesBatchOrderAndOwnershipAcrossResiduals(t *testing.T) {
	const batchSize = 1_000

	for _, residual := range []int{1, 500, 999} {
		t.Run(fmt.Sprintf("residual-%d", residual), func(t *testing.T) {
			dir := t.TempDir()
			firstRows := producerFixtureTransactions("first", residual)
			secondRows := producerFixtureTransactions("second", batchSize)
			writeProducerFixture(t, filepath.Join(dir, "part-000.parquet"), firstRows)
			writeProducerFixture(t, filepath.Join(dir, "part-001.parquet"), secondRows)

			want := append([]Transaction{}, firstRows...)
			want = append(want, secondRows...)
			want = append(want, firstRows...)
			want = append(want, secondRows...)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var telemetry readerTelemetry
			var channelTelemetry channelTelemetry
			batches := make(chan []Transaction, 1)
			channelTelemetry.start(batches, batchSize)
			done, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), batchSize, batches, &telemetry, &channelTelemetry)
			if err != nil {
				t.Fatalf("start producer: %v", err)
			}

			first := receiveProducerBatch(t, batches, batchSize)
			firstIDs := producerFixtureIDs(first)
			assertProducerFixtureIDs(t, first, producerFixtureIDs(want[:batchSize]))

			second := receiveProducerBatch(t, batches, batchSize)
			assertProducerFixtureIDs(t, second, producerFixtureIDs(want[batchSize:2*batchSize]))
			assertProducerFixtureIDs(t, first, firstIDs)

			cancel()
			<-done
			drain(batches)
		})
	}
}

func writeProducerFixture(t *testing.T, path string, rows []Transaction) {
	t.Helper()
	if err := parquet.WriteFile(path, rows); err != nil {
		t.Fatalf("write parquet fixture %q: %v", path, err)
	}
}

func producerFixtureTransactions(prefix string, count int) []Transaction {
	rows := make([]Transaction, count)
	for index := range rows {
		rows[index].ClientID = fmt.Sprintf("%s-%d", prefix, index)
	}
	return rows
}

func receiveProducerBatch(t *testing.T, batches <-chan []Transaction, wantSize int) []Transaction {
	t.Helper()
	select {
	case batch := <-batches:
		if len(batch) != wantSize {
			t.Fatalf("batch size = %d, want %d", len(batch), wantSize)
		}
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not emit a batch")
		return nil
	}
}

func waitForReaderSource(t *testing.T, telemetry *readerTelemetry, wantRows int, wantSource string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		got := telemetry.snapshot()
		if got.rowsRead >= int64(wantRows) {
			if got.source == nil || *got.source != wantSource {
				t.Fatalf("source after %d rows = %v, want %q", got.rowsRead, got.source, wantSource)
			}
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("rows read = %d, want at least %d", got.rowsRead, wantRows)
		}
	}
}

func producerFixtureIDs(rows []Transaction) []string {
	ids := make([]string, len(rows))
	for index := range rows {
		ids[index] = rows[index].ClientID
	}
	return ids
}

func assertProducerFixtureIDs(t *testing.T, rows []Transaction, want []string) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("row count = %d, want %d", len(rows), len(want))
	}
	for index := range want {
		if rows[index].ClientID != want[index] {
			t.Fatalf("row %d client ID = %q, want %q", index, rows[index].ClientID, want[index])
		}
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
			var channelTelemetry channelTelemetry
			batches := make(chan []Transaction, capacity)
			channelTelemetry.start(batches, 1)
			done, err := produceBatches(ctx, filepath.Join(dir, "*.parquet"), 1, batches, &telemetry, &channelTelemetry)
			if err != nil {
				cancel()
				t.Fatalf("start producer: %v", err)
			}
			if got := cap(batches); got != capacity {
				cancel()
				t.Fatalf("producer channel capacity = %d, want %d", got, capacity)
			}
			if got := channelTelemetry.snapshot(time.Now()).capacity; got != capacity {
				cancel()
				t.Fatalf("telemetry capacity = %d, want %d", got, capacity)
			}
			cancel()
			<-done
			drain(batches)
		})
	}
}
