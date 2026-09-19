package main

import (
	"context"
	"testing"
	"time"
)

func TestQueue1TelemetryReportsBufferedBatchesAndTransactions(t *testing.T) {
	batches := make(chan []Transaction, defaultQueue1Capacity)
	batches <- make([]Transaction, 3)
	batches <- make([]Transaction, 3)

	var telemetry queue1Telemetry
	telemetry.start(batches, 3)
	measurements := telemetry.snapshot(time.Now())
	if measurements.capacity != defaultQueue1Capacity || measurements.depthBatches != 2 ||
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
	if blocked.blockedSenders != 1 || blocked.depthBatches != 1 || blocked.queuedTransactions != 1 ||
		blocked.enqueuedBatchesTotal != 0 {
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
	if completed.blockedSenders != 0 || completed.blockedMs <= 0 || completed.depthBatches != 1 ||
		completed.enqueuedBatchesTotal != 1 || completed.enqueuedTransactionsTotal != 1 {
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
	if completed.blockedSenders != 0 || completed.blockedMs <= 0 ||
		completed.enqueuedBatchesTotal != 0 || completed.enqueuedTransactionsTotal != 0 {
		t.Fatalf("cancelled queue measurements = %+v", completed)
	}

	telemetry.reset()
	reset := telemetry.snapshot(time.Now())
	if reset.capacity != 0 || reset.depthBatches != 0 ||
		reset.queuedTransactions != 0 || reset.blockedSenders != 0 ||
		reset.oldestBlockedSenderMs != 0 || reset.blockedMs != 0 ||
		reset.enqueuedBatchesTotal != 0 || reset.enqueuedTransactionsTotal != 0 ||
		reset.dequeuedBatchesTotal != 0 || reset.dequeuedTransactionsTotal != 0 ||
		reset.inputBatchesPerSecond != 0 || reset.inputTransactionsPerSecond != 0 ||
		reset.outputBatchesPerSecond != 0 || reset.outputTransactionsPerSecond != 0 {
		t.Fatalf("reset queue measurements = %+v", reset)
	}
}

func TestQueue1TelemetryCountsSuccessfulSendAndSamplesWindow(t *testing.T) {
	batches := make(chan []Transaction, 2)
	var telemetry queue1Telemetry
	telemetry.start(batches, 2)
	if !telemetry.send(context.Background(), batches, make([]Transaction, 2)) {
		t.Fatal("first send failed")
	}
	if !telemetry.send(context.Background(), batches, make([]Transaction, 3)) {
		t.Fatal("second send failed")
	}
	telemetry.recordDequeue(len(<-batches))

	beforeSample := telemetry.snapshot(time.Now())
	if beforeSample.enqueuedBatchesTotal != 2 || beforeSample.enqueuedTransactionsTotal != 5 ||
		beforeSample.dequeuedBatchesTotal != 1 || beforeSample.dequeuedTransactionsTotal != 2 ||
		beforeSample.inputBatchesPerSecond != 0 || beforeSample.outputBatchesPerSecond != 0 {
		t.Fatalf("before sample = %+v", beforeSample)
	}

	telemetry.sample(500 * time.Millisecond)
	first := telemetry.snapshot(time.Now())
	if first.inputBatchesPerSecond != 4 || first.inputTransactionsPerSecond != 10 ||
		first.outputBatchesPerSecond != 2 || first.outputTransactionsPerSecond != 4 {
		t.Fatalf("first window = %+v", first)
	}
	telemetry.sample(500 * time.Millisecond)
	second := telemetry.snapshot(time.Now())
	if second.enqueuedBatchesTotal != 2 || second.dequeuedBatchesTotal != 1 ||
		second.inputBatchesPerSecond != 0 || second.inputTransactionsPerSecond != 0 ||
		second.outputBatchesPerSecond != 0 || second.outputTransactionsPerSecond != 0 {
		t.Fatalf("empty window = %+v", second)
	}

	telemetry.recordDequeue(len(<-batches))
	telemetry.reset()
	telemetry.sample(time.Second)
	reset := telemetry.snapshot(time.Now())
	if reset.enqueuedBatchesTotal != 0 || reset.enqueuedTransactionsTotal != 0 ||
		reset.dequeuedBatchesTotal != 0 || reset.dequeuedTransactionsTotal != 0 ||
		reset.inputBatchesPerSecond != 0 || reset.inputTransactionsPerSecond != 0 ||
		reset.outputBatchesPerSecond != 0 || reset.outputTransactionsPerSecond != 0 {
		t.Fatalf("reset window = %+v", reset)
	}
}

func TestQueue1TelemetryUnbufferedHandoffCountsOnlyAfterSend(t *testing.T) {
	batches := make(chan []Transaction)
	var telemetry queue1Telemetry
	telemetry.start(batches, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.send(ctx, batches, make([]Transaction, 2))
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.capacity != 0 || blocked.depthBatches != 0 || blocked.enqueuedBatchesTotal != 0 {
		t.Fatalf("blocked unbuffered queue = %+v", blocked)
	}
	batch := <-batches
	telemetry.recordDequeue(len(batch))
	if ok := <-sent; !ok {
		t.Fatal("unbuffered send failed after receive")
	}
	telemetry.sample(time.Second)
	handoff := telemetry.snapshot(time.Now())
	if handoff.depthBatches != 0 || handoff.enqueuedBatchesTotal != 1 ||
		handoff.enqueuedTransactionsTotal != 2 || handoff.dequeuedBatchesTotal != 1 ||
		handoff.dequeuedTransactionsTotal != 2 || handoff.inputBatchesPerSecond != 1 ||
		handoff.outputBatchesPerSecond != 1 {
		t.Fatalf("unbuffered handoff = %+v", handoff)
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
