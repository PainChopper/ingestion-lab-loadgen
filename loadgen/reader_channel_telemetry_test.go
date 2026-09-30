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

	var telemetry channelTelemetry
	telemetry.start(batches, 3)
	measurements := telemetry.snapshot(time.Now())
	if measurements.capacity != policy.ReaderChannel.Capacity.Default || measurements.depthBatches != 2 ||
		measurements.bufferedTransactions != 6 || measurements.blockedSenders != 0 ||
		measurements.oldestBlockedSenderMs != 0 || measurements.blockedMs != 0 {
		t.Fatalf("readerChannel measurements = %+v", measurements)
	}
}

func TestReaderChannelTelemetryMeasuresBlockedSendUntilThrottlerReceives(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry channelTelemetry
	telemetry.start(batches, 1)
	ctx := t.Context()
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

func TestReaderChannelTelemetryTrySendCountsImmediateSendWithoutBlockedMeasurement(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var telemetry channelTelemetry
	telemetry.start(batches, 1)

	if !telemetry.trySend(batches, []Transaction{{}}) {
		t.Fatal("immediate send failed")
	}

	measurements := telemetry.snapshot(time.Now())
	if measurements.blockedSenders != 0 || measurements.blockedMs != 0 ||
		measurements.sentBatchesTotal != 1 || measurements.sentTransactionsTotal != 1 {
		t.Fatalf("immediate send measurements = %+v", measurements)
	}
}

func TestReaderChannelTelemetrySnapshotAccumulatesSubMillisecondBlockedDurations(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	telemetry := channelTelemetry{
		measurements: channelTelemetryMeasurements{
			blockedAt: map[uint64]time.Time{1: now.Add(-800 * time.Microsecond)},
			blockedMs: 800 * time.Microsecond,
		},
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

func TestReaderChannelTelemetryTracksConcurrentBlockedWritersIndependently(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	var telemetry channelTelemetry
	first := telemetry.startBlocked(now.Add(-3 * time.Millisecond))
	second := telemetry.startBlocked(now.Add(-2 * time.Millisecond))
	active := telemetry.snapshot(now)
	if active.blockedSenders != 2 || active.oldestBlockedSenderMs != 3 || active.blockedMs != 5 {
		t.Fatalf("two writers = %+v", active)
	}

	telemetry.finishBlockedWriter(first, now)
	remaining := telemetry.snapshot(now.Add(time.Millisecond))
	if remaining.blockedSenders != 1 || remaining.oldestBlockedSenderMs != 3 || remaining.blockedMs != 6 {
		t.Fatalf("remaining writer = %+v", remaining)
	}
	telemetry.finishBlockedWriter(second, now.Add(time.Millisecond))
	completed := telemetry.snapshot(now.Add(2 * time.Millisecond))
	if completed.blockedSenders != 0 || completed.oldestBlockedSenderMs != 0 || completed.blockedMs != 6 {
		t.Fatalf("completed writers = %+v", completed)
	}
}

func TestReaderChannelTelemetryConcurrentSendsKeepSecondWriterBlocked(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}
	var telemetry channelTelemetry
	telemetry.start(batches, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 2)
	for range 2 {
		go func() { sent <- telemetry.send(ctx, batches, []Transaction{{}}) }()
	}
	waitForBlockedSenders(t, &telemetry, 2)
	<-batches
	if !<-sent {
		t.Fatal("first writer did not send")
	}
	waitForBlockedSenders(t, &telemetry, 1)
	cancel()
	if <-sent {
		t.Fatal("second writer sent after cancellation")
	}
	if remaining := telemetry.snapshot(time.Now()); remaining.blockedSenders != 0 || remaining.sentBatchesTotal != 1 {
		t.Fatalf("after cancellation = %+v", remaining)
	}
}

func TestReaderChannelTelemetryAccountsForWriterRacingIntoLastFreeSlot(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var telemetry channelTelemetry
	telemetry.start(batches, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 2)
	for range 2 {
		go func() { sent <- telemetry.send(ctx, batches, []Transaction{{}}) }()
	}
	waitForBlockedSender(t, &telemetry)
	if len(batches) != 1 || !<-sent {
		t.Fatal("first writer did not fill the last free slot")
	}
	cancel()
	if <-sent {
		t.Fatal("second writer unexpectedly sent after cancellation")
	}
	if snapshot := telemetry.snapshot(time.Now()); snapshot.blockedSenders != 0 || snapshot.sentBatchesTotal != 1 {
		t.Fatalf("after race = %+v", snapshot)
	}
}

func TestReaderChannelTelemetryClearMeasurementsRetainsAttachmentAndDetachRemovesIt(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry channelTelemetry
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
		cleared.sentBatchesPerSecond != 0 || cleared.sentTransactionsPerSecond != 0 ||
		cleared.receivedBatchesPerSecond != 0 || cleared.receivedTransactionsPerSecond != 0 {
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
		detached.sentBatchesPerSecond != 0 || detached.sentTransactionsPerSecond != 0 ||
		detached.receivedBatchesPerSecond != 0 || detached.receivedTransactionsPerSecond != 0 {
		t.Fatalf("detached readerChannel measurements = %+v", detached)
	}
}

func TestReaderChannelTelemetryCountsSuccessfulSendAndSamplesWindow(t *testing.T) {
	batches := make(chan []Transaction, 2)
	var telemetry channelTelemetry
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
		beforeSample.sentBatchesPerSecond != 0 || beforeSample.receivedBatchesPerSecond != 0 {
		t.Fatalf("before sample = %+v", beforeSample)
	}

	telemetry.sample(500 * time.Millisecond)
	first := telemetry.snapshot(time.Now())
	if first.sentBatchesPerSecond != 4 || first.sentTransactionsPerSecond != 10 ||
		first.receivedBatchesPerSecond != 2 || first.receivedTransactionsPerSecond != 4 {
		t.Fatalf("first window = %+v", first)
	}
	telemetry.sample(500 * time.Millisecond)
	second := telemetry.snapshot(time.Now())
	if second.sentBatchesTotal != 2 || second.receivedBatchesTotal != 1 ||
		second.sentBatchesPerSecond != 0 || second.sentTransactionsPerSecond != 0 ||
		second.receivedBatchesPerSecond != 0 || second.receivedTransactionsPerSecond != 0 {
		t.Fatalf("empty window = %+v", second)
	}

	telemetry.recordReceive(len(<-batches))
	telemetry.clearMeasurements()
	telemetry.sample(time.Second)
	reset := telemetry.snapshot(time.Now())
	if reset.sentBatchesTotal != 0 || reset.sentTransactionsTotal != 0 ||
		reset.receivedBatchesTotal != 0 || reset.receivedTransactionsTotal != 0 ||
		reset.sentBatchesPerSecond != 0 || reset.sentTransactionsPerSecond != 0 ||
		reset.receivedBatchesPerSecond != 0 || reset.receivedTransactionsPerSecond != 0 {
		t.Fatalf("reset window = %+v", reset)
	}
}

func TestReaderChannelTelemetryUnbufferedHandoffCountsOnlyAfterSend(t *testing.T) {
	batches := make(chan []Transaction)
	var telemetry channelTelemetry
	telemetry.start(batches, 2)
	ctx := t.Context()
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
		handoff.receivedTransactionsTotal != 2 || handoff.sentBatchesPerSecond != 1 ||
		handoff.receivedBatchesPerSecond != 1 {
		t.Fatalf("unbuffered handoff = %+v", handoff)
	}
}

func waitForBlockedSender(t *testing.T, telemetry *channelTelemetry) {
	waitForBlockedSenders(t, telemetry, 1)
}

func waitForBlockedSenders(t *testing.T, telemetry *channelTelemetry, want int) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		if telemetry.snapshot(time.Now()).blockedSenders == want {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("sender did not begin waiting on a full readerChannel")
		}
	}
}
