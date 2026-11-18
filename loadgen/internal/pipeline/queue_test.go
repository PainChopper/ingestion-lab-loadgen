package pipeline

import (
	"context"
	"testing"
	"time"
)

func TestReaderChannelTelemetryReportsBufferedBatchesAndTransactions(t *testing.T) {
	batches := make(chan []Transaction, 2)
	batches <- make([]Transaction, 3)
	batches <- make([]Transaction, 3)

	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 3
	measurements := telemetry.snapshot(time.Now())
	if measurements.DepthBatches != 2 ||
		measurements.BufferedTransactions != 6 || measurements.BlockedSenders != 0 ||
		measurements.OldestBlockedSenderMS != 0 || measurements.BlockedMS != 0 {
		t.Fatalf("readerChannel measurements = %+v", measurements)
	}
}

func TestReaderChannelTelemetryMeasuresBlockedSendUntilThrottlerReceives(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}

	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 1
	ctx := t.Context()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.beginBlockedSend([]Transaction{{}}).wait(ctx)
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.BlockedSenders != 1 || blocked.DepthBatches != 1 || blocked.BufferedTransactions != 1 ||
		blocked.SentBatchesTotal != 0 {
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
	if completed.BlockedSenders != 0 || completed.BlockedMS <= 0 || completed.DepthBatches != 1 ||
		completed.SentBatchesTotal != 1 || completed.SentTransactionsTotal != 1 {
		t.Fatalf("completed readerChannel measurements = %+v", completed)
	}
}

func TestReaderChannelTelemetryTrySendCountsImmediateSendWithoutBlockedMeasurement(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 1

	if !telemetry.trySend([]Transaction{{}}) {
		t.Fatal("immediate send failed")
	}

	measurements := telemetry.snapshot(time.Now())
	if measurements.BlockedSenders != 0 || measurements.BlockedMS != 0 ||
		measurements.SentBatchesTotal != 1 || measurements.SentTransactionsTotal != 1 {
		t.Fatalf("immediate send measurements = %+v", measurements)
	}
}

func TestReaderChannelTelemetrySnapshotAccumulatesSubMillisecondBlockedDurations(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	telemetry := batchQueue{
		measurements: queueMeasurements{
			blockedAt: map[uint64]time.Time{1: now.Add(-800 * time.Microsecond)},
			blocked:   800 * time.Microsecond,
		},
	}

	active := telemetry.snapshot(now)
	if active.BlockedSenders != 1 || active.OldestBlockedSenderMS != 0 || active.BlockedMS != 1 {
		t.Fatalf("active readerChannel measurements = %+v", active)
	}

	telemetry.finishBlockedWriter(1, now)
	completed := telemetry.snapshot(now)
	if completed.BlockedSenders != 0 || completed.OldestBlockedSenderMS != 0 || completed.BlockedMS != 1 {
		t.Fatalf("completed readerChannel measurements = %+v", completed)
	}
}

func TestReaderChannelTelemetryTracksConcurrentBlockedWritersIndependently(t *testing.T) {
	now := time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC)
	var telemetry batchQueue
	first := telemetry.startBlocked(now.Add(-3 * time.Millisecond))
	second := telemetry.startBlocked(now.Add(-2 * time.Millisecond))
	active := telemetry.snapshot(now)
	if active.BlockedSenders != 2 || active.OldestBlockedSenderMS != 3 || active.BlockedMS != 5 {
		t.Fatalf("two writers = %+v", active)
	}

	telemetry.finishBlockedWriter(first, now)
	remaining := telemetry.snapshot(now.Add(time.Millisecond))
	if remaining.BlockedSenders != 1 || remaining.OldestBlockedSenderMS != 3 || remaining.BlockedMS != 6 {
		t.Fatalf("remaining writer = %+v", remaining)
	}
	telemetry.finishBlockedWriter(second, now.Add(time.Millisecond))
	completed := telemetry.snapshot(now.Add(2 * time.Millisecond))
	if completed.BlockedSenders != 0 || completed.OldestBlockedSenderMS != 0 || completed.BlockedMS != 6 {
		t.Fatalf("completed writers = %+v", completed)
	}
}

func TestReaderChannelTelemetryConcurrentSendsKeepSecondWriterBlocked(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{}}
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan bool, 2)
	for range 2 {
		go func() { sent <- telemetry.beginBlockedSend([]Transaction{{}}).wait(ctx) }()
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
	if remaining := telemetry.snapshot(time.Now()); remaining.BlockedSenders != 0 || remaining.SentBatchesTotal != 1 {
		t.Fatalf("after cancellation = %+v", remaining)
	}
}

func TestReaderChannelTelemetryCountsSuccessfulSendAndSamplesWindow(t *testing.T) {
	batches := make(chan []Transaction, 2)
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 2
	if !telemetry.trySend(make([]Transaction, 2)) {
		t.Fatal("first send failed")
	}
	if !telemetry.trySend(make([]Transaction, 3)) {
		t.Fatal("second send failed")
	}
	telemetry.recordReceive(len(<-batches))

	beforeSample := telemetry.snapshot(time.Now())
	if beforeSample.SentBatchesTotal != 2 || beforeSample.SentTransactionsTotal != 5 ||
		beforeSample.ReceivedBatchesTotal != 1 || beforeSample.ReceivedTransactionsTotal != 2 ||
		beforeSample.SentBatchesPerSecond != 0 || beforeSample.ReceivedBatchesPerSecond != 0 {
		t.Fatalf("before sample = %+v", beforeSample)
	}

	telemetry.sample(500 * time.Millisecond)
	first := telemetry.snapshot(time.Now())
	if first.SentBatchesPerSecond != 4 || first.SentTransactionsPerSecond != 10 ||
		first.ReceivedBatchesPerSecond != 2 || first.ReceivedTransactionsPerSecond != 4 {
		t.Fatalf("first window = %+v", first)
	}
	telemetry.sample(500 * time.Millisecond)
	second := telemetry.snapshot(time.Now())
	if second.SentBatchesTotal != 2 || second.ReceivedBatchesTotal != 1 ||
		second.SentBatchesPerSecond != 0 || second.SentTransactionsPerSecond != 0 ||
		second.ReceivedBatchesPerSecond != 0 || second.ReceivedTransactionsPerSecond != 0 {
		t.Fatalf("empty window = %+v", second)
	}
}

func TestReaderChannelTelemetryUnbufferedHandoffCountsOnlyAfterSend(t *testing.T) {
	batches := make(chan []Transaction)
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 2
	ctx := t.Context()
	sent := make(chan bool, 1)
	go func() {
		sent <- telemetry.beginBlockedSend(make([]Transaction, 2)).wait(ctx)
	}()

	waitForBlockedSender(t, &telemetry)
	blocked := telemetry.snapshot(time.Now())
	if blocked.DepthBatches != 0 || blocked.SentBatchesTotal != 0 {
		t.Fatalf("blocked unbuffered readerChannel = %+v", blocked)
	}
	batch := <-batches
	telemetry.recordReceive(len(batch))
	if ok := <-sent; !ok {
		t.Fatal("unbuffered send failed after receive")
	}
	telemetry.sample(time.Second)
	handoff := telemetry.snapshot(time.Now())
	if handoff.DepthBatches != 0 || handoff.SentBatchesTotal != 1 ||
		handoff.SentTransactionsTotal != 2 || handoff.ReceivedBatchesTotal != 1 ||
		handoff.ReceivedTransactionsTotal != 2 || handoff.SentBatchesPerSecond != 1 ||
		handoff.ReceivedBatchesPerSecond != 1 {
		t.Fatalf("unbuffered handoff = %+v", handoff)
	}
}

func waitForBlockedSender(t *testing.T, telemetry *batchQueue) {
	waitForBlockedSenders(t, telemetry, 1)
}

func waitForBlockedSenders(t *testing.T, telemetry *batchQueue, want int) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		if telemetry.snapshot(time.Now()).BlockedSenders == want {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("sender did not begin waiting on a full readerChannel")
		}
	}
}
