package main

import (
	"context"
	"testing"
	"time"
)

func TestReaderChannelTelemetryReportsBufferedBatchesAndTransactions(t *testing.T) {
	policy := testPolicy(t)
	batches := make(chan []Transaction, policy.ReaderChannel.Capacity.Default)
	batches <- make([]Transaction, 3)
	batches <- make([]Transaction, 3)

	var telemetry readerChannelTelemetry
	telemetry.start(batches, 3)
	measurements := telemetry.snapshot(time.Now())
	if measurements.capacity != policy.ReaderChannel.Capacity.Default || measurements.depthBatches != 2 ||
		measurements.bufferedTransactions != 6 || measurements.blockedSenders != 0 ||
		measurements.oldestBlockedSenderMs != 0 || measurements.blockedMs != 0 {
		t.Fatalf("readerChannel measurements = %+v", measurements)
	}
}

func TestReaderChannelTelemetryMeasuresBlockedSendUntilConsumerReceives(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry readerChannelTelemetry
	telemetry.start(batches, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.send(ctx, batches, []Transaction{{}})
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.blockedSenders != 1 || blocked.depthBatches != 1 || blocked.bufferedTransactions != 1 ||
		blocked.sentBatchesTotal != 0 {
		t.Fatalf("blocked readerChannel measurements = %+v", blocked)
	}
	time.Sleep(10 * time.Millisecond)

	<-batches
	select {
	case ok := <-sent:
		if !ok {
			t.Fatal("blocked send was cancelled after readerChannel was drained")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked send did not complete after readerChannel was drained")
	}

	completed := telemetry.snapshot(time.Now())
	if completed.blockedSenders != 0 || completed.blockedMs <= 0 || completed.depthBatches != 1 ||
		completed.sentBatchesTotal != 1 || completed.sentTransactionsTotal != 1 {
		t.Fatalf("completed readerChannel measurements = %+v", completed)
	}
}

func TestReaderChannelTelemetrySnapshotAccumulatesSubMillisecondBlockedDurations(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	telemetry := readerChannelTelemetry{
		blockedAt: now.Add(-800 * time.Microsecond),
		blockedMs: 800 * time.Microsecond,
	}

	active := telemetry.snapshot(now)
	if active.blockedSenders != 1 || active.oldestBlockedSenderMs != 0 || active.blockedMs != 1 {
		t.Fatalf("active readerChannel measurements = %+v", active)
	}

	telemetry.finishBlocked(now)
	completed := telemetry.snapshot(now)
	if completed.blockedSenders != 0 || completed.oldestBlockedSenderMs != 0 || completed.blockedMs != 1 {
		t.Fatalf("completed readerChannel measurements = %+v", completed)
	}
}

func TestReaderChannelTelemetryClearMeasurementsRetainsAttachmentAndDetachRemovesIt(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry readerChannelTelemetry
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
		completed.sentBatchesTotal != 0 || completed.sentTransactionsTotal != 0 {
		t.Fatalf("cancelled readerChannel measurements = %+v", completed)
	}

	telemetry.clearMeasurements()
	cleared := telemetry.snapshot(time.Now())
	if cleared.capacity != 1 || cleared.depthBatches != 1 ||
		cleared.bufferedTransactions != 1 || cleared.blockedSenders != 0 ||
		cleared.oldestBlockedSenderMs != 0 || cleared.blockedMs != 0 ||
		cleared.sentBatchesTotal != 0 || cleared.sentTransactionsTotal != 0 ||
		cleared.receivedBatchesTotal != 0 || cleared.receivedTransactionsTotal != 0 ||
		cleared.inputBatchesPerSecond != 0 || cleared.inputTransactionsPerSecond != 0 ||
		cleared.outputBatchesPerSecond != 0 || cleared.outputTransactionsPerSecond != 0 {
		t.Fatalf("cleared readerChannel measurements = %+v", cleared)
	}

	<-batches
	if drained := telemetry.snapshot(time.Now()); drained.depthBatches != 0 {
		t.Fatalf("drained readerChannel depth = %d, want 0", drained.depthBatches)
	}
	telemetry.detach()
	detached := telemetry.snapshot(time.Now())
	if detached.capacity != 0 || detached.depthBatches != 0 ||
		detached.bufferedTransactions != 0 || detached.blockedSenders != 0 ||
		detached.oldestBlockedSenderMs != 0 || detached.blockedMs != 0 ||
		detached.sentBatchesTotal != 0 || detached.sentTransactionsTotal != 0 ||
		detached.receivedBatchesTotal != 0 || detached.receivedTransactionsTotal != 0 ||
		detached.inputBatchesPerSecond != 0 || detached.inputTransactionsPerSecond != 0 ||
		detached.outputBatchesPerSecond != 0 || detached.outputTransactionsPerSecond != 0 {
		t.Fatalf("detached readerChannel measurements = %+v", detached)
	}
}

func TestReaderChannelTelemetryCountsSuccessfulSendAndSamplesWindow(t *testing.T) {
	batches := make(chan []Transaction, 2)
	var telemetry readerChannelTelemetry
	telemetry.start(batches, 2)
	if !telemetry.send(context.Background(), batches, make([]Transaction, 2)) {
		t.Fatal("first send failed")
	}
	if !telemetry.send(context.Background(), batches, make([]Transaction, 3)) {
		t.Fatal("second send failed")
	}
	telemetry.recordReceive(len(<-batches))

	beforeSample := telemetry.snapshot(time.Now())
	if beforeSample.sentBatchesTotal != 2 || beforeSample.sentTransactionsTotal != 5 ||
		beforeSample.receivedBatchesTotal != 1 || beforeSample.receivedTransactionsTotal != 2 ||
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
	if second.sentBatchesTotal != 2 || second.receivedBatchesTotal != 1 ||
		second.inputBatchesPerSecond != 0 || second.inputTransactionsPerSecond != 0 ||
		second.outputBatchesPerSecond != 0 || second.outputTransactionsPerSecond != 0 {
		t.Fatalf("empty window = %+v", second)
	}

	telemetry.recordReceive(len(<-batches))
	telemetry.clearMeasurements()
	telemetry.sample(time.Second)
	reset := telemetry.snapshot(time.Now())
	if reset.sentBatchesTotal != 0 || reset.sentTransactionsTotal != 0 ||
		reset.receivedBatchesTotal != 0 || reset.receivedTransactionsTotal != 0 ||
		reset.inputBatchesPerSecond != 0 || reset.inputTransactionsPerSecond != 0 ||
		reset.outputBatchesPerSecond != 0 || reset.outputTransactionsPerSecond != 0 {
		t.Fatalf("reset window = %+v", reset)
	}
}

func TestReaderChannelTelemetryUnbufferedHandoffCountsOnlyAfterSend(t *testing.T) {
	batches := make(chan []Transaction)
	var telemetry readerChannelTelemetry
	telemetry.start(batches, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.send(ctx, batches, make([]Transaction, 2))
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.capacity != 0 || blocked.depthBatches != 0 || blocked.sentBatchesTotal != 0 {
		t.Fatalf("blocked unbuffered readerChannel = %+v", blocked)
	}
	batch := <-batches
	telemetry.recordReceive(len(batch))
	if ok := <-sent; !ok {
		t.Fatal("unbuffered send failed after receive")
	}
	telemetry.sample(time.Second)
	handoff := telemetry.snapshot(time.Now())
	if handoff.depthBatches != 0 || handoff.sentBatchesTotal != 1 ||
		handoff.sentTransactionsTotal != 2 || handoff.receivedBatchesTotal != 1 ||
		handoff.receivedTransactionsTotal != 2 || handoff.inputBatchesPerSecond != 1 ||
		handoff.outputBatchesPerSecond != 1 {
		t.Fatalf("unbuffered handoff = %+v", handoff)
	}
}

func waitForBlockedSender(t *testing.T, telemetry *readerChannelTelemetry) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		if telemetry.snapshot(time.Now()).blockedSenders == 1 {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("sender did not begin waiting on a full readerChannel")
		}
	}
}
