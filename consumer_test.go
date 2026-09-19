package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumeBatchesRecordsReceiveAtReceive(t *testing.T) {
	batches := make(chan []Transaction)
	var consumed atomic.Int64
	var telemetry readerChannelTelemetry
	telemetry.start(batches, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(context.Background(), batches, &consumed, &telemetry)
	}()

	batch := make([]Transaction, 2)
	batches <- batch
	close(batches)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish")
	}

	telemetry.sample(time.Second)
	got := telemetry.snapshot(time.Now())
	if got.receivedBatchesTotal != 1 || got.receivedTransactionsTotal != 2 ||
		got.outputBatchesPerSecond != 1 || got.outputTransactionsPerSecond != 2 ||
		consumed.Load() != 2 {
		t.Fatalf("consumer measurements = %+v, consumed = %d", got, consumed.Load())
	}
}
