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
	senderBatches, done, _ := startThrottler(context.Background(), readerBatches, &telemetry, throttlerSettings{mode: throttlerBypass})
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
	senderBatches, done, _ := startThrottler(ctx, readerBatches, &telemetry, throttlerSettings{mode: throttlerBypass})
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
	senderBatches, done, _ := startThrottler(ctx, readerBatches, &telemetry, throttlerSettings{mode: throttlerBypass})
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

func TestStartThrottlerPacesByTransactions(t *testing.T) {
	for _, test := range []struct {
		name        string
		batchSize   int
		minimumWait time.Duration
	}{
		{name: "one transaction", batchSize: 1, minimumWait: 30 * time.Millisecond},
		{name: "five transactions", batchSize: 5, minimumWait: 170 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			readerBatches := make(chan []Transaction, 1)
			readerBatches <- make([]Transaction, test.batchSize)
			close(readerBatches)
			var telemetry readerChannelTelemetry
			started := time.Now()
			senderBatches, done, _ := startThrottler(
				ctx, readerBatches, &telemetry,
				throttlerSettings{requestedTPS: 25, mode: throttlerInstalled},
			)
			select {
			case batch := <-senderBatches:
				if len(batch) != test.batchSize {
					t.Fatalf("batch length = %d, want %d", len(batch), test.batchSize)
				}
				if elapsed := time.Since(started); elapsed < test.minimumWait {
					t.Fatalf("batch of %d left after %v, want at least %v", test.batchSize, elapsed, test.minimumWait)
				}
			case <-time.After(time.Second):
				t.Fatal("paced batch was not delivered")
			}
			waitForThrottlerDone(t, done)
		})
	}
}

func TestStartThrottlerZeroWakesOnControlUpdate(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings throttlerSettings
	}{
		{name: "positive TPS", settings: throttlerSettings{requestedTPS: 400, mode: throttlerInstalled}},
		{name: "bypass", settings: throttlerSettings{requestedTPS: 0, mode: throttlerBypass}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			readerBatches := make(chan []Transaction, 1)
			readerBatches <- []Transaction{{ClientID: "held"}}
			var telemetry readerChannelTelemetry
			senderBatches, done, updates := startThrottler(
				ctx, readerBatches, &telemetry,
				throttlerSettings{requestedTPS: 0, mode: throttlerInstalled},
			)
			select {
			case <-senderBatches:
				t.Fatal("zero TPS forwarded a batch")
			case <-time.After(40 * time.Millisecond):
			}
			applied := make(chan struct{})
			updates <- throttlerUpdate{settings: test.settings, applied: applied}
			<-applied
			select {
			case batch := <-senderBatches:
				if len(batch) != 1 || batch[0].ClientID != "held" {
					t.Fatalf("batch after update = %v", batch)
				}
			case <-time.After(time.Second):
				t.Fatal("held batch did not wake")
			}
			cancel()
			waitForThrottlerDone(t, done)
		})
	}
}

func TestStartThrottlerDoesNotAccumulateCreditWhileOutputBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerBatches := make(chan []Transaction, 2)
	readerBatches <- []Transaction{{ClientID: "first"}}
	readerBatches <- []Transaction{{ClientID: "second"}}
	var telemetry readerChannelTelemetry
	senderBatches, done, _ := startThrottler(
		ctx, readerBatches, &telemetry,
		throttlerSettings{requestedTPS: 25, mode: throttlerInstalled},
	)
	time.Sleep(120 * time.Millisecond)
	select {
	case batch := <-senderBatches:
		if batch[0].ClientID != "first" {
			t.Fatalf("first batch = %v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("first batch did not arrive")
	}
	firstReceived := time.Now()
	select {
	case batch := <-senderBatches:
		if elapsed := time.Since(firstReceived); elapsed < 30*time.Millisecond {
			t.Fatalf("second batch arrived after %v: %v", elapsed, batch)
		}
		if batch[0].ClientID != "second" {
			t.Fatalf("second batch = %v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("second batch did not arrive")
	}
	cancel()
	waitForThrottlerDone(t, done)
}

func waitForThrottlerDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Throttler did not stop")
	}
}
