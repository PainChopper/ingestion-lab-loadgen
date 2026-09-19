package main

import (
	"context"
	"testing"
	"time"
)

func TestQueue1TelemetryReportsBufferedBatchesAndTransactions(t *testing.T) {
	batches := make(chan []Transaction, batchReadAheadCapacity)
	batches <- make([]Transaction, 3)
	batches <- make([]Transaction, 3)

	var telemetry queue1Telemetry
	telemetry.start(batches, 3)
	measurements := telemetry.snapshot(time.Now())
	if measurements.capacity != batchReadAheadCapacity || measurements.depthBatches != 2 ||
		measurements.queuedTransactions != 6 || measurements.blockedSenders != 0 ||
		measurements.oldestBlockedSenderMs != 0 || measurements.blockedMs != 0 {
		t.Fatalf("queue measurements = %+v", measurements)
	}
}

func TestQueue1TelemetryMeasuresBlockedSendUntilConsumerReceives(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry queue1Telemetry
	telemetry.start(batches, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.send(ctx, batches, []Transaction{{}})
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.blockedSenders != 1 || blocked.depthBatches != 1 || blocked.queuedTransactions != 1 {
		t.Fatalf("blocked queue measurements = %+v", blocked)
	}
	time.Sleep(10 * time.Millisecond)

	<-batches
	select {
	case ok := <-sent:
		if !ok {
			t.Fatal("blocked send was cancelled after queue was drained")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked send did not complete after queue was drained")
	}

	completed := telemetry.snapshot(time.Now())
	if completed.blockedSenders != 0 || completed.blockedMs <= 0 || completed.depthBatches != 1 {
		t.Fatalf("completed queue measurements = %+v", completed)
	}
}

func TestQueue1TelemetrySnapshotAccumulatesSubMillisecondBlockedDurations(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	telemetry := queue1Telemetry{
		blockedAt: now.Add(-800 * time.Microsecond),
		blockedMs: 800 * time.Microsecond,
	}

	active := telemetry.snapshot(now)
	if active.blockedSenders != 1 || active.oldestBlockedSenderMs != 0 || active.blockedMs != 1 {
		t.Fatalf("active queue measurements = %+v", active)
	}

	telemetry.finishBlocked(now)
	completed := telemetry.snapshot(now)
	if completed.blockedSenders != 0 || completed.oldestBlockedSenderMs != 0 || completed.blockedMs != 1 {
		t.Fatalf("completed queue measurements = %+v", completed)
	}
}

func TestQueue1TelemetryRecordsCancelledBlockedSendAndReset(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry queue1Telemetry
	telemetry.start(batches, 1)
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.send(ctx, batches, []Transaction{{}})
	}()

	waitForBlockedSender(t, &telemetry)
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case ok := <-sent:
		if ok {
			t.Fatal("cancelled blocked send succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled blocked send did not complete")
	}

	completed := telemetry.snapshot(time.Now())
	if completed.blockedSenders != 0 || completed.blockedMs <= 0 {
		t.Fatalf("cancelled queue measurements = %+v", completed)
	}

	telemetry.reset()
	reset := telemetry.snapshot(time.Now())
	if reset.capacity != batchReadAheadCapacity || reset.depthBatches != 0 ||
		reset.queuedTransactions != 0 || reset.blockedSenders != 0 ||
		reset.oldestBlockedSenderMs != 0 || reset.blockedMs != 0 {
		t.Fatalf("reset queue measurements = %+v", reset)
	}
}

func waitForBlockedSender(t *testing.T, telemetry *queue1Telemetry) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		if telemetry.snapshot(time.Now()).blockedSenders == 1 {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("sender did not begin waiting on a full queue")
		}
	}
}
