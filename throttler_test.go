package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestStartThrottlerPassesBatchesAndClosesOutput(t *testing.T) {
	readerBatches := make(chan []Transaction, 2)
	want := [][]Transaction{
		{{ClientID: "first"}, {ClientID: "second"}},
		{{ClientID: "third"}},
	}
	for _, batch := range want {
		readerBatches <- batch
	}
	close(readerBatches)

	var telemetry readerChannelTelemetry
	senderBatches, done := startThrottler(context.Background(), readerBatches, &telemetry)
	if got := cap(senderBatches); got != 0 {
		t.Fatalf("Sender channel capacity = %d, want 0", got)
	}
	for index, batch := range want {
		select {
		case got, ok := <-senderBatches:
			if !ok || !reflect.DeepEqual(got, batch) {
				t.Fatalf("batch %d = %v, open = %t, want %v", index, got, ok, batch)
			}
		case <-time.After(time.Second):
			t.Fatalf("batch %d was not forwarded", index)
		}
	}
	waitForThrottlerDone(t, done)
	select {
	case _, ok := <-senderBatches:
		if ok {
			t.Fatal("Sender channel remained open after input close")
		}
	default:
		t.Fatal("Sender channel was not closed after input close")
	}

	got := telemetry.snapshot(time.Now())
	if got.receivedBatchesTotal != 2 || got.receivedTransactionsTotal != 3 {
		t.Fatalf("Reader channel receives = %+v, want 2 batches and 3 transactions", got)
	}
}

func TestStartThrottlerCancelWhileWaitingForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction)
	var telemetry readerChannelTelemetry
	senderBatches, done := startThrottler(ctx, readerBatches, &telemetry)
	cancel()
	waitForThrottlerDone(t, done)
	if _, ok := <-senderBatches; ok {
		t.Fatal("Sender channel remained open after cancellation")
	}
}

func TestStartThrottlerCancelWhileWaitingForOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction, 1)
	readerBatches <- []Transaction{{ClientID: "pending"}}
	var telemetry readerChannelTelemetry
	senderBatches, done := startThrottler(ctx, readerBatches, &telemetry)
	deadline := time.After(time.Second)
	for telemetry.snapshot(time.Now()).receivedBatchesTotal != 1 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("Throttler did not accept the Reader batch")
		}
	}
	cancel()
	waitForThrottlerDone(t, done)
	if _, ok := <-senderBatches; ok {
		t.Fatal("Sender channel remained open after blocked send was cancelled")
	}
}

func waitForThrottlerDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Throttler did not stop")
	}
}
