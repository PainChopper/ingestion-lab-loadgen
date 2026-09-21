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
	var senderChannel channelTelemetry
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(context.Background(), batches, &senderChannel, &consumed)
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
	got := senderChannel.snapshot(time.Now())
	if got.receivedBatchesTotal != 1 || got.receivedTransactionsTotal != 2 ||
		got.receivedBatchesPerSecond != 0 || got.receivedTransactionsPerSecond != 0 {
		t.Fatalf("Sender channel receives before sample = %+v", got)
	}
	senderChannel.sample(time.Second)
	got = senderChannel.snapshot(time.Now())
	if got.receivedBatchesPerSecond != 1 || got.receivedTransactionsPerSecond != 2 {
		t.Fatalf("Sender channel output window = %+v", got)
	}
}

func TestConsumeBatchesCompletesAcceptedBatchAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batches := make(chan []Transaction)
	var consumed atomic.Int64
	var senderChannel channelTelemetry
	enteredConsume := make(chan struct{})
	allowCompletion := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatchesWith(ctx, batches, &senderChannel, &consumed, func(*Transaction) {
			close(enteredConsume)
			<-allowCompletion
		})
	}()

	go func() {
		batches <- []Transaction{{}}
	}()
	<-enteredConsume
	if got := senderChannel.snapshot(time.Now()); got.receivedBatchesTotal != 1 || got.receivedTransactionsTotal != 1 {
		t.Fatalf("accepted batch telemetry = %+v, want one received batch and transaction", got)
	}

	cancel()
	select {
	case <-done:
		t.Fatal("consumer stopped before completing the accepted batch")
	default:
	}

	close(allowCompletion)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish the accepted batch")
	}
	if got := consumed.Load(); got != 1 {
		t.Fatalf("consumed transactions = %d, want 1", got)
	}
}
