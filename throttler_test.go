package main

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestStartThrottlerPassesBatchesWithoutClosingOutput(t *testing.T) {
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
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, _ := startThrottler(
		context.Background(),
		readerBatches,
		senderBatches,
		&telemetry,
		&senderChannel,
		throttlerSettings{mode: throttlerBypass},
	)
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
		if !ok {
			t.Fatal("Throttler closed caller-owned Sender channel")
		}
	default:
	}

	got := telemetry.snapshot(time.Now())
	if got.receivedBatchesTotal != 2 || got.receivedTransactionsTotal != 3 {
		t.Fatalf("Reader channel receives = %+v, want 2 batches and 3 transactions", got)
	}
}

func TestStartThrottlerUsesConfiguredSenderChannelCapacity(t *testing.T) {
	for _, capacity := range []int{0, 1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			readerBatches := make(chan []Transaction)
			var readerChannel readerChannelTelemetry
			var senderChannel readerChannelTelemetry
			senderBatches := make(chan []Transaction, capacity)
			senderChannel.start(senderBatches, 0)
			done, _ := startThrottler(
				ctx,
				readerBatches,
				senderBatches,
				&readerChannel,
				&senderChannel,
				throttlerSettings{mode: throttlerBypass},
			)
			if got := cap(senderBatches); got != capacity {
				t.Fatalf("Sender channel capacity = %d, want %d", got, capacity)
			}
			if got := senderChannel.snapshot(time.Now()).capacity; got != capacity {
				t.Fatalf("Sender telemetry capacity = %d, want %d", got, capacity)
			}
			cancel()
			waitForThrottlerDone(t, done)
		})
	}
}

func TestStartThrottlerCancelWhileWaitingForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction)
	var telemetry readerChannelTelemetry
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, _ := startThrottler(
		ctx,
		readerBatches,
		senderBatches,
		&telemetry,
		&senderChannel,
		throttlerSettings{mode: throttlerBypass},
	)
	cancel()
	waitForThrottlerDone(t, done)
	select {
	case _, ok := <-senderBatches:
		if !ok {
			t.Fatal("Throttler closed caller-owned Sender channel")
		}
	default:
	}
}

func TestStartThrottlerCancelWhileWaitingForOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction, 1)
	readerBatches <- []Transaction{{ClientID: "pending"}}
	var telemetry readerChannelTelemetry
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, _ := startThrottler(
		ctx,
		readerBatches,
		senderBatches,
		&telemetry,
		&senderChannel,
		throttlerSettings{mode: throttlerBypass},
	)
	deadline := time.After(time.Second)
	for telemetry.snapshot(time.Now()).receivedBatchesTotal != 1 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("Throttler did not accept the Reader batch")
		}
	}
	waitForBlockedSender(t, &senderChannel)
	blocked := senderChannel.snapshot(time.Now())
	if blocked.capacity != 0 || blocked.depthBatches != 0 || blocked.bufferedTransactions != 0 ||
		blocked.blockedSenders != 1 || blocked.sentBatchesTotal != 0 {
		t.Fatalf("blocked Sender channel = %+v", blocked)
	}
	cancel()
	waitForThrottlerDone(t, done)
	select {
	case _, ok := <-senderBatches:
		if !ok {
			t.Fatal("Throttler closed caller-owned Sender channel")
		}
	default:
	}
	completed := senderChannel.snapshot(time.Now())
	if completed.blockedSenders != 0 || completed.sentBatchesTotal != 0 || completed.sentTransactionsTotal != 0 {
		t.Fatalf("cancelled Sender channel = %+v", completed)
	}
}

func TestStartThrottlerMeasuresSuccessfulUnbufferedHandoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerBatches := make(chan []Transaction, 1)
	readerBatches <- make([]Transaction, 3)
	var readerChannel readerChannelTelemetry
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, _ := startThrottler(
		ctx,
		readerBatches,
		senderBatches,
		&readerChannel,
		&senderChannel,
		throttlerSettings{mode: throttlerBypass},
	)
	waitForBlockedSender(t, &senderChannel)
	time.Sleep(5 * time.Millisecond)
	before := senderChannel.snapshot(time.Now())
	if before.capacity != 0 || before.depthBatches != 0 || before.bufferedTransactions != 0 ||
		before.blockedSenders != 1 || before.oldestBlockedSenderMs <= 0 || before.blockedMs <= 0 ||
		before.sentBatchesTotal != 0 || before.inputTransactionsPerSecond != 0 {
		t.Fatalf("Sender channel before handoff = %+v", before)
	}
	if batch := <-senderBatches; len(batch) != 3 {
		t.Fatalf("Sender batch size = %d, want 3", len(batch))
	}
	close(readerBatches)
	waitForThrottlerDone(t, done)
	senderChannel.sample(time.Second)
	after := senderChannel.snapshot(time.Now())
	if after.blockedSenders != 0 || after.oldestBlockedSenderMs != 0 || after.blockedMs <= 0 ||
		after.sentBatchesTotal != 1 || after.sentTransactionsTotal != 3 ||
		after.inputBatchesPerSecond != 1 || after.inputTransactionsPerSecond != 3 {
		t.Fatalf("Sender channel after handoff = %+v", after)
	}
	senderChannel.sample(time.Second)
	if empty := senderChannel.snapshot(time.Now()); empty.inputBatchesPerSecond != 0 ||
		empty.inputTransactionsPerSecond != 0 || empty.sentTransactionsTotal != 3 {
		t.Fatalf("Sender channel after empty window = %+v", empty)
	}
}

func TestStartThrottlerControlUpdateEndsBlockedWaitWithoutAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerBatches := make(chan []Transaction, 1)
	readerBatches <- []Transaction{{}}
	var readerChannel readerChannelTelemetry
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, updates := startThrottler(
		ctx,
		readerBatches,
		senderBatches,
		&readerChannel,
		&senderChannel,
		throttlerSettings{mode: throttlerBypass},
	)
	waitForBlockedSender(t, &senderChannel)
	applied := make(chan struct{})
	updates <- throttlerUpdate{
		settings: throttlerSettings{mode: throttlerBypass, paused: true},
		applied:  applied,
	}
	<-applied
	paused := senderChannel.snapshot(time.Now())
	if paused.blockedSenders != 0 || paused.sentBatchesTotal != 0 || paused.sentTransactionsTotal != 0 {
		t.Fatalf("Sender channel after Pause = %+v", paused)
	}
	senderChannel.sample(time.Second)
	if got := senderChannel.snapshot(time.Now()); got.inputTransactionsPerSecond != 0 {
		t.Fatalf("Sender input rate after Pause = %v, want 0", got.inputTransactionsPerSecond)
	}
	cancel()
	waitForThrottlerDone(t, done)
	select {
	case _, ok := <-senderBatches:
		if !ok {
			t.Fatal("Throttler closed caller-owned Sender channel")
		}
	default:
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
			var senderChannel readerChannelTelemetry
			started := time.Now()
			senderBatches := make(chan []Transaction)
			senderChannel.start(senderBatches, 0)
			done, _ := startThrottler(
				ctx,
				readerBatches,
				senderBatches,
				&telemetry,
				&senderChannel,
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

func TestThrottlerPolicyWholeBatchPacingInterval(t *testing.T) {
	const (
		batchSize    = 1_000
		requestedTPS = 2_000
	)

	interval := time.Duration(batchSize) * time.Second / time.Duration(requestedTPS)
	if interval != 500*time.Millisecond {
		t.Fatalf("whole-batch pacing interval = %v, want 500ms", interval)
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
			var senderChannel readerChannelTelemetry
			senderBatches := make(chan []Transaction)
			senderChannel.start(senderBatches, 0)
			done, updates := startThrottler(
				ctx,
				readerBatches,
				senderBatches,
				&telemetry,
				&senderChannel,
				throttlerSettings{requestedTPS: 0, mode: throttlerInstalled},
			)
			select {
			case <-senderBatches:
				t.Fatal("zero TPS forwarded a batch")
			case <-time.After(40 * time.Millisecond):
			}
			if got := senderChannel.snapshot(time.Now()); got.blockedSenders != 0 || got.blockedMs != 0 ||
				got.sentBatchesTotal != 0 {
				t.Fatalf("zero TPS Sender channel = %+v", got)
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
	var senderChannel readerChannelTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	done, _ := startThrottler(
		ctx,
		readerBatches,
		senderBatches,
		&telemetry,
		&senderChannel,
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
