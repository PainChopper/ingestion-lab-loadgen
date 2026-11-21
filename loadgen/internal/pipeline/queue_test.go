package pipeline

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestBatchQueueReportsDepthBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	batches <- make([]Transaction, 3)
	batches <- make([]Transaction, 3)

	var telemetry batchQueue
	telemetry.ch = batches
	measurements := telemetry.snapshot(time.Now())
	if measurements.DepthBatches != 2 {
		t.Fatalf("readerChannel measurements = %+v", measurements)
	}
}

func TestBatchQueueMeasuresBlockedSendUntilQueueReceives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{}}

		var telemetry batchQueue
		telemetry.ch = batches
		ctx := t.Context()
		sent := make(chan bool, 1)
		go func() {
			sent <- telemetry.beginBlockedSend([]Transaction{{}}).wait(ctx)
		}()

		assertBlockedSender(t, &telemetry)
		blocked := telemetry.snapshot(time.Now())
		if blocked.DepthBatches != 1 ||
			blocked.SentBatchesTotal != 0 || blocked.SentTransactionsTotal != 0 {
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
		if completed.BlockedSenders != 0 || completed.BlockedMS != 10 || completed.DepthBatches != 1 ||
			completed.SentBatchesTotal != 1 || completed.SentTransactionsTotal != 1 {
			t.Fatalf("completed readerChannel measurements = %+v", completed)
		}
	})
}

func TestBatchQueueSnapshotAccumulatesSubMillisecondBlockedDurations(t *testing.T) {
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

func TestBatchQueueTracksConcurrentBlockedWritersIndependently(t *testing.T) {
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

func TestBatchQueueConcurrentSendsKeepSecondWriterBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{}}
		var telemetry batchQueue
		telemetry.ch = batches
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sent := make(chan bool, 2)
		for range 2 {
			go func() { sent <- telemetry.beginBlockedSend([]Transaction{{}}).wait(ctx) }()
		}
		assertBlockedSenders(t, &telemetry, 2)
		<-batches
		if !<-sent {
			t.Fatal("first writer did not send")
		}
		assertBlockedSenders(t, &telemetry, 1)
		cancel()
		if <-sent {
			t.Fatal("second writer sent after cancellation")
		}
		if remaining := telemetry.snapshot(time.Now()); remaining.BlockedSenders != 0 || remaining.SentBatchesTotal != 1 {
			t.Fatalf("after cancellation = %+v", remaining)
		}
	})
}

func TestBatchQueueCountsSuccessfulTransfers(t *testing.T) {
	batches := make(chan []Transaction, 2)
	var telemetry batchQueue
	telemetry.ch = batches
	if !telemetry.trySend(make([]Transaction, 3)) {
		t.Fatal("first send failed")
	}
	if !telemetry.trySend(make([]Transaction, 2)) {
		t.Fatal("second send failed")
	}
	telemetry.recordReceive(len(<-batches))

	beforeSample := telemetry.snapshot(time.Now())
	if beforeSample.BlockedSenders != 0 || beforeSample.BlockedMS != 0 ||
		beforeSample.SentBatchesTotal != 2 || beforeSample.SentTransactionsTotal != 5 ||
		beforeSample.ReceivedBatchesTotal != 1 || beforeSample.ReceivedTransactionsTotal != 3 {
		t.Fatalf("before sample = %+v", beforeSample)
	}

}

func TestBatchQueueUnbufferedHandoffCountsOnlyAfterSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry batchQueue
		telemetry.ch = batches
		ctx := t.Context()
		sent := make(chan bool, 1)
		go func() {
			sent <- telemetry.beginBlockedSend(make([]Transaction, 2)).wait(ctx)
		}()

		assertBlockedSender(t, &telemetry)
		blocked := telemetry.snapshot(time.Now())
		if blocked.DepthBatches != 0 || blocked.SentBatchesTotal != 0 || blocked.SentTransactionsTotal != 0 {
			t.Fatalf("blocked unbuffered readerChannel = %+v", blocked)
		}
		batch := <-batches
		telemetry.recordReceive(len(batch))
		if ok := <-sent; !ok {
			t.Fatal("unbuffered send failed after receive")
		}
		handoff := telemetry.snapshot(time.Now())
		if handoff.DepthBatches != 0 || handoff.SentBatchesTotal != 1 ||
			handoff.SentTransactionsTotal != 2 || handoff.ReceivedBatchesTotal != 1 ||
			handoff.ReceivedTransactionsTotal != 2 {
			t.Fatalf("unbuffered handoff = %+v", handoff)
		}
	})
}
