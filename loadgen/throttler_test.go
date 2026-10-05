package main

import (
	"context"
	"reflect"
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

	var readerChannel, outputTelemetry channelTelemetry
	telemetry := &readerChannel
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(context.Background(), throttlerSettings{installed: false})
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
	waitForThrottlerDone(t, stage.done)
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

func TestStartThrottlerCancelWhileWaitingForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction)
	var readerChannel, outputTelemetry channelTelemetry
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(ctx, throttlerSettings{installed: false})
	cancel()
	waitForThrottlerDone(t, stage.done)
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
	var readerChannel, outputTelemetry channelTelemetry
	telemetry := &readerChannel
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(ctx, throttlerSettings{installed: false})
	deadline := time.After(time.Second)
	for telemetry.snapshot(time.Now()).receivedBatchesTotal != 1 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("Throttler did not accept the Reader batch")
		}
	}
	waitForBlockedSender(t, senderChannel)
	blocked := senderChannel.snapshot(time.Now())
	if blocked.capacity != 0 || blocked.depthBatches != 0 || blocked.bufferedTransactions != 0 ||
		blocked.blockedSenders != 1 || blocked.sentBatchesTotal != 0 {
		t.Fatalf("blocked Sender channel = %+v", blocked)
	}
	cancel()
	waitForThrottlerDone(t, stage.done)
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
	var readerChannel, outputTelemetry channelTelemetry
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(ctx, throttlerSettings{installed: false})
	waitForBlockedSender(t, senderChannel)
	time.Sleep(5 * time.Millisecond)
	before := senderChannel.snapshot(time.Now())
	if before.capacity != 0 || before.depthBatches != 0 || before.bufferedTransactions != 0 ||
		before.blockedSenders != 1 || before.oldestBlockedSenderMs <= 0 || before.blockedMs <= 0 ||
		before.sentBatchesTotal != 0 || before.sentTransactionsPerSecond != 0 {
		t.Fatalf("Sender channel before handoff = %+v", before)
	}
	if batch := <-senderBatches; len(batch) != 3 {
		t.Fatalf("Sender batch size = %d, want 3", len(batch))
	}
	close(readerBatches)
	waitForThrottlerDone(t, stage.done)
	senderChannel.sample(time.Second)
	after := senderChannel.snapshot(time.Now())
	if after.blockedSenders != 0 || after.oldestBlockedSenderMs != 0 || after.blockedMs <= 0 ||
		after.sentBatchesTotal != 1 || after.sentTransactionsTotal != 3 ||
		after.sentBatchesPerSecond != 1 || after.sentTransactionsPerSecond != 3 {
		t.Fatalf("Sender channel after handoff = %+v", after)
	}
	senderChannel.sample(time.Second)
	if empty := senderChannel.snapshot(time.Now()); empty.sentBatchesPerSecond != 0 ||
		empty.sentTransactionsPerSecond != 0 || empty.sentTransactionsTotal != 3 {
		t.Fatalf("Sender channel after empty window = %+v", empty)
	}
}

func TestStartThrottlerControlUpdateEndsBlockedWaitWithoutAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerBatches := make(chan []Transaction, 1)
	readerBatches <- []Transaction{{ClientID: "held"}}
	var readerChannel, outputTelemetry channelTelemetry
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(ctx, throttlerSettings{installed: false})
	waitForBlockedSender(t, senderChannel)
	stage.update(throttlerSettings{installed: true, requestedTPS: 0})
	waitForBlockedSenders(t, senderChannel, 0)
	paused := senderChannel.snapshot(time.Now())
	if paused.blockedSenders != 0 || paused.sentBatchesTotal != 0 || paused.sentTransactionsTotal != 0 {
		t.Fatalf("Sender channel after zero TPS = %+v", paused)
	}
	senderChannel.sample(time.Second)
	if got := senderChannel.snapshot(time.Now()); got.sentTransactionsPerSecond != 0 {
		t.Fatalf("Sender sent rate after zero TPS = %v, want 0", got.sentTransactionsPerSecond)
	}
	stage.update(throttlerSettings{installed: false})
	if batch := <-senderBatches; len(batch) != 1 || batch[0].ClientID != "held" {
		t.Fatalf("retained batch after zero TPS = %v", batch)
	}
	cancel()
	waitForThrottlerDone(t, stage.done)
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
			var readerChannel, outputTelemetry channelTelemetry
			senderChannel := &outputTelemetry
			started := time.Now()
			senderBatches := make(chan []Transaction)
			senderChannel.start(senderBatches, 0)
			stage := throttler{
				readerChannel: &readerChannel,
				senderChannel: senderChannel,
				readerBatches: readerBatches,
				senderBatches: senderBatches,
			}
			stage.start(ctx, throttlerSettings{requestedTPS: 25, installed: true})
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
			waitForThrottlerDone(t, stage.done)
		})
	}
}

func TestStartThrottlerZeroWakesOnControlUpdate(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings throttlerSettings
	}{
		{name: "positive TPS", settings: throttlerSettings{requestedTPS: 400, installed: true}},
		{name: "bypass", settings: throttlerSettings{requestedTPS: 0, installed: false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			readerBatches := make(chan []Transaction, 1)
			readerBatches <- []Transaction{{ClientID: "held"}}
			var readerChannel, outputTelemetry channelTelemetry
			senderChannel := &outputTelemetry
			senderBatches := make(chan []Transaction)
			senderChannel.start(senderBatches, 0)
			stage := throttler{
				readerChannel: &readerChannel,
				senderChannel: senderChannel,
				readerBatches: readerBatches,
				senderBatches: senderBatches,
			}
			stage.start(ctx, throttlerSettings{requestedTPS: 0, installed: true})
			select {
			case <-senderBatches:
				t.Fatal("zero TPS forwarded a batch")
			case <-time.After(40 * time.Millisecond):
			}
			if got := senderChannel.snapshot(time.Now()); got.blockedSenders != 0 || got.blockedMs != 0 ||
				got.sentBatchesTotal != 0 {
				t.Fatalf("zero TPS Sender channel = %+v", got)
			}
			stage.update(test.settings)
			select {
			case batch := <-senderBatches:
				if len(batch) != 1 || batch[0].ClientID != "held" {
					t.Fatalf("batch after update = %v", batch)
				}
			case <-time.After(time.Second):
				t.Fatal("held batch did not wake")
			}
			cancel()
			waitForThrottlerDone(t, stage.done)
		})
	}
}

func TestStartThrottlerDoesNotAccumulateCreditWhileOutputBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readerBatches := make(chan []Transaction, 2)
	readerBatches <- []Transaction{{ClientID: "first"}}
	readerBatches <- []Transaction{{ClientID: "second"}}
	var readerChannel, outputTelemetry channelTelemetry
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.start(senderBatches, 0)
	stage := throttler{
		readerChannel: &readerChannel,
		senderChannel: senderChannel,
		readerBatches: readerBatches,
		senderBatches: senderBatches,
	}
	stage.start(ctx, throttlerSettings{requestedTPS: 25, installed: true})
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
	waitForThrottlerDone(t, stage.done)
}

func waitForThrottlerDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Throttler did not stop")
	}
}
