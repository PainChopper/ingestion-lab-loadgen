package main

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
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
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		readerBatches <- []Transaction{{ClientID: "pending"}}
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
		synctest.Wait()
		if got := readerChannel.snapshot(time.Now()).receivedBatchesTotal; got != 1 {
			t.Fatalf("Throttler received %d batches, want 1", got)
		}
		blocked := senderChannel.snapshot(time.Now())
		if blocked.capacity != 0 || blocked.depthBatches != 0 || blocked.bufferedTransactions != 0 ||
			blocked.blockedSenders != 1 || blocked.sentBatchesTotal != 0 {
			t.Fatalf("blocked Sender channel = %+v", blocked)
		}
		cancel()
		<-stage.done
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
	})
}

func TestStartThrottlerMeasuresSuccessfulUnbufferedHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
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
		synctest.Wait()
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
		<-stage.done
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
	})
}

func TestStartThrottlerControlUpdateEndsBlockedWaitWithoutAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
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
		synctest.Wait()
		stage.update(throttlerSettings{installed: true, requestedTPS: 0})
		synctest.Wait()
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
		<-stage.done
		select {
		case _, ok := <-senderBatches:
			if !ok {
				t.Fatal("Throttler closed caller-owned Sender channel")
			}
		default:
		}
	})
}

func TestStartThrottlerPacesByTransactions(t *testing.T) {
	for _, test := range []struct {
		name      string
		batchSize int
		wantWait  time.Duration
	}{
		{name: "one transaction", batchSize: 1, wantWait: 40 * time.Millisecond},
		{name: "five transactions", batchSize: 5, wantWait: 200 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
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
					if elapsed := time.Since(started); elapsed != test.wantWait {
						t.Fatalf("batch of %d left after %v, want %v", test.batchSize, elapsed, test.wantWait)
					}
				case <-time.After(time.Second):
					t.Fatal("paced batch was not delivered")
				}
				waitForThrottlerDone(t, stage.done)
			})
		})
	}
}

func TestStartThrottlerZeroWakesOnControlUpdate(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings throttlerSettings
		wantWait time.Duration
	}{
		{name: "positive TPS", settings: throttlerSettings{requestedTPS: 400, installed: true}, wantWait: 2500 * time.Microsecond},
		{name: "bypass", settings: throttlerSettings{requestedTPS: 0, installed: false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
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
				time.Sleep(40 * time.Millisecond)
				synctest.Wait()
				select {
				case <-senderBatches:
					t.Fatal("zero TPS forwarded a batch")
				default:
				}
				if got := senderChannel.snapshot(time.Now()); got.blockedSenders != 0 || got.blockedMs != 0 ||
					got.sentBatchesTotal != 0 {
					t.Fatalf("zero TPS Sender channel = %+v", got)
				}
				resumedAt := time.Now()
				stage.update(test.settings)
				select {
				case batch := <-senderBatches:
					if len(batch) != 1 || batch[0].ClientID != "held" {
						t.Fatalf("batch after update = %v", batch)
					}
				case <-time.After(time.Second):
					t.Fatal("held batch did not wake")
				}
				if elapsed := time.Since(resumedAt); elapsed != test.wantWait {
					t.Fatalf("wait after resume = %v, want %v", elapsed, test.wantWait)
				}
				cancel()
				waitForThrottlerDone(t, stage.done)
			})
		})
	}
}

func TestStartThrottlerStartsFreshAfterZeroTPSWithIdleInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction)
		senderBatches := make(chan []Transaction, 1)
		var readerChannel, senderChannel channelTelemetry
		stage := throttler{
			readerBatches: readerBatches,
			senderBatches: senderBatches,
			readerChannel: &readerChannel,
			senderChannel: &senderChannel,
		}
		settings := throttlerSettings{requestedTPS: 25, installed: true}
		stage.start(t.Context(), settings)
		batch := []Transaction{{ClientID: "paced"}}
		readerBatches <- batch
		<-senderBatches

		stage.update(throttlerSettings{requestedTPS: 0, installed: true})
		time.Sleep(time.Second)
		stage.update(settings)
		resumedAt := time.Now()
		readerBatches <- batch
		close(readerBatches)
		<-senderBatches
		<-stage.done
		if elapsed := time.Since(resumedAt); elapsed != 40*time.Millisecond {
			t.Fatalf("first batch after idle zero TPS waited %v, want 40ms", elapsed)
		}
	})
}

func TestStartThrottlerDoesNotAccumulateCreditWhileOutputBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
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
		synctest.Wait()
		if got := senderChannel.snapshot(time.Now()).blockedSenders; got != 1 {
			t.Fatalf("blocked senders = %d, want 1 before freeing output", got)
		}
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
			if elapsed := time.Since(firstReceived); elapsed != 40*time.Millisecond {
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
	})
}

func TestStartThrottlerLimitsCreditAfterInputIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction, 2)
		senderBatches := make(chan []Transaction, 2)
		var readerChannel, senderChannel channelTelemetry
		stage := throttler{
			readerBatches: readerBatches,
			senderBatches: senderBatches,
			readerChannel: &readerChannel,
			senderChannel: &senderChannel,
		}
		stage.start(t.Context(), throttlerSettings{requestedTPS: 25, installed: true})
		batch := []Transaction{{ClientID: "paced"}}
		readerBatches <- batch
		<-senderBatches
		time.Sleep(5 * time.Second)

		started := time.Now()
		readerBatches <- batch
		readerBatches <- batch
		close(readerBatches)
		<-senderBatches
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("first batch after idle waited %v, want immediate send", elapsed)
		}
		<-senderBatches
		if elapsed := time.Since(started); elapsed != 40*time.Millisecond {
			t.Errorf("second batch after idle waited %v, want 40ms", elapsed)
		}
		<-stage.done
	})
}

func TestStartThrottlerRateUpdateKeepsElapsedSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction, 2)
		senderBatches := make(chan []Transaction, 1)
		var readerChannel, senderChannel channelTelemetry
		stage := throttler{
			readerBatches: readerBatches,
			senderBatches: senderBatches,
			readerChannel: &readerChannel,
			senderChannel: &senderChannel,
		}
		senderBatches <- []Transaction{{ClientID: "occupies output"}}
		readerBatches <- make([]Transaction, 1000)
		readerBatches <- make([]Transaction, 1000)
		close(readerBatches)
		stage.start(t.Context(), throttlerSettings{requestedTPS: 200_000, installed: true})
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		if got := senderChannel.snapshot(time.Now()).blockedSenders; got != 1 {
			t.Fatalf("at first deadline blocked senders = %d, want 1", got)
		}

		stage.update(throttlerSettings{requestedTPS: 400_000, installed: true})
		started := time.Now()
		<-senderBatches
		if batch := <-senderBatches; len(batch) != 1000 {
			t.Errorf("forwarded batch length = %d, want 1000", len(batch))
		}
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("already paced batch waited %v after rate update, want immediate send", elapsed)
		}
		<-senderBatches
		if elapsed := time.Since(started); elapsed != 2500*time.Microsecond {
			t.Errorf("next batch after blocked handoff waited %v, want 2.5ms", elapsed)
		}
		<-stage.done
		if got := senderChannel.snapshot(time.Now()).blockedSenders; got != 0 {
			t.Errorf("blocked senders after handoff = %d, want 0", got)
		}
	})
}

func TestStartThrottlerClosesItsOwnBlockedSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		senderBatches := make(chan []Transaction)
		var readerChannel, senderChannel channelTelemetry
		stage := throttler{
			readerBatches: readerBatches,
			senderBatches: senderBatches,
			readerChannel: &readerChannel,
			senderChannel: &senderChannel,
		}
		other := senderChannel.beginBlockedSend(senderBatches, []Transaction{{ClientID: "other"}})
		time.Sleep(20 * time.Millisecond)
		readerBatches <- []Transaction{{ClientID: "throttled"}}
		stage.start(ctx, throttlerSettings{installed: false})
		synctest.Wait()
		stage.update(throttlerSettings{installed: true})
		synctest.Wait()
		got := senderChannel.snapshot(time.Now())
		cancel()
		<-stage.done
		other.wait(ctx)
		if got.blockedSenders != 1 || got.oldestBlockedSenderMs != 20 {
			t.Fatalf("after update blocked senders = %d, oldest = %dms, want the sender blocked for 20ms",
				got.blockedSenders, got.oldestBlockedSenderMs)
		}
	})
}

func waitForThrottlerDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Throttler did not stop")
	}
}
