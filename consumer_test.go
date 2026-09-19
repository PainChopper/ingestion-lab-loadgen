package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumeBatchesCountsTransactions(t *testing.T) {
	batches := make(chan []Transaction)
	var consumed atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(context.Background(), batches, &consumed)
	}()

	batch := make([]Transaction, 2)
	batches <- batch
	close(batches)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish")
	}

	if got := consumed.Load(); got != 2 {
		t.Fatalf("consumed transactions = %d, want 2", got)
	}
}
