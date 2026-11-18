package pipeline

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestThrottlerPassesBatchesWithoutClosingOutput(t *testing.T) {
	readerBatches := make(chan []Transaction, 2)
	want := [][]Transaction{
		{{ClientID: "first"}, {ClientID: "second"}},
		{{ClientID: "third"}},
	}
	for _, batch := range want {
		readerBatches <- batch
	}
	close(readerBatches)

	var readerChannel, outputTelemetry batchQueue
	telemetry := &readerChannel
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.ch = senderBatches
	senderChannel.batchSize = 0
	readerChannel.ch = readerBatches
	readerChannel.batchSize = 0
	stage := startThrottler(context.Background(), &readerChannel, senderChannel, Rate{Installed: false})
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
	if got.ReceivedBatchesTotal != 2 || got.ReceivedTransactionsTotal != 3 {
		t.Fatalf("Reader channel receives = %+v, want 2 batches and 3 transactions", got)
	}
}

func TestThrottlerCancelWhileWaitingForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readerBatches := make(chan []Transaction)
	var readerChannel, outputTelemetry batchQueue
	senderChannel := &outputTelemetry
	senderBatches := make(chan []Transaction)
	senderChannel.ch = senderBatches
	senderChannel.batchSize = 0
	readerChannel.ch = readerBatches
	readerChannel.batchSize = 0
	stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{Installed: false})
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

func TestThrottlerCancelWhileWaitingForOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		readerBatches <- []Transaction{{ClientID: "pending"}}
		var readerChannel, outputTelemetry batchQueue
		senderChannel := &outputTelemetry
		senderBatches := make(chan []Transaction)
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{Installed: false})
		synctest.Wait()
		if got := readerChannel.snapshot(time.Now()).ReceivedBatchesTotal; got != 1 {
			t.Fatalf("Throttler received %d batches, want 1", got)
		}
		blocked := senderChannel.snapshot(time.Now())
		if blocked.DepthBatches != 0 || blocked.BufferedTransactions != 0 ||
			blocked.BlockedSenders != 1 || blocked.SentBatchesTotal != 0 {
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
		if completed.BlockedSenders != 0 || completed.SentBatchesTotal != 0 || completed.SentTransactionsTotal != 0 {
			t.Fatalf("cancelled Sender channel = %+v", completed)
		}
	})
}

func TestThrottlerMeasuresSuccessfulUnbufferedHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		readerBatches <- make([]Transaction, 3)
		var readerChannel, outputTelemetry batchQueue
		senderChannel := &outputTelemetry
		senderBatches := make(chan []Transaction)
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{Installed: false})
		synctest.Wait()
		time.Sleep(5 * time.Millisecond)
		before := senderChannel.snapshot(time.Now())
		if before.DepthBatches != 0 || before.BufferedTransactions != 0 ||
			before.BlockedSenders != 1 || before.OldestBlockedSenderMS <= 0 || before.BlockedMS <= 0 ||
			before.SentBatchesTotal != 0 || before.SentTransactionsPerSecond != 0 {
			t.Fatalf("Sender channel before handoff = %+v", before)
		}
		if batch := <-senderBatches; len(batch) != 3 {
			t.Fatalf("Sender batch size = %d, want 3", len(batch))
		}
		close(readerBatches)
		<-stage.done
		senderChannel.sample(time.Second)
		after := senderChannel.snapshot(time.Now())
		if after.BlockedSenders != 0 || after.OldestBlockedSenderMS != 0 || after.BlockedMS <= 0 ||
			after.SentBatchesTotal != 1 || after.SentTransactionsTotal != 3 ||
			after.SentBatchesPerSecond != 1 || after.SentTransactionsPerSecond != 3 {
			t.Fatalf("Sender channel after handoff = %+v", after)
		}
		senderChannel.sample(time.Second)
		if empty := senderChannel.snapshot(time.Now()); empty.SentBatchesPerSecond != 0 ||
			empty.SentTransactionsPerSecond != 0 || empty.SentTransactionsTotal != 3 {
			t.Fatalf("Sender channel after empty window = %+v", empty)
		}
	})
}

func TestThrottlerControlUpdateEndsBlockedWaitWithoutAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		readerBatches <- []Transaction{{ClientID: "held"}}
		var readerChannel, outputTelemetry batchQueue
		senderChannel := &outputTelemetry
		senderBatches := make(chan []Transaction)
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{Installed: false})
		synctest.Wait()
		stage.update(Rate{Installed: true, TPS: 0})
		synctest.Wait()
		paused := senderChannel.snapshot(time.Now())
		if paused.BlockedSenders != 0 || paused.SentBatchesTotal != 0 || paused.SentTransactionsTotal != 0 {
			t.Fatalf("Sender channel after zero TPS = %+v", paused)
		}
		senderChannel.sample(time.Second)
		if got := senderChannel.snapshot(time.Now()); got.SentTransactionsPerSecond != 0 {
			t.Fatalf("Sender sent rate after zero TPS = %v, want 0", got.SentTransactionsPerSecond)
		}
		stage.update(Rate{Installed: false})
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

func TestThrottlerPacesByTransactions(t *testing.T) {
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
				var readerChannel, outputTelemetry batchQueue
				senderChannel := &outputTelemetry
				started := time.Now()
				senderBatches := make(chan []Transaction)
				senderChannel.ch = senderBatches
				senderChannel.batchSize = 0
				readerChannel.ch = readerBatches
				readerChannel.batchSize = 0
				stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{TPS: 25, Installed: true})
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

func TestThrottlerZeroWakesOnControlUpdate(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings Rate
		wantWait time.Duration
	}{
		{name: "positive TPS", settings: Rate{TPS: 400, Installed: true}, wantWait: 2500 * time.Microsecond},
		{name: "bypass", settings: Rate{TPS: 0, Installed: false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				readerBatches := make(chan []Transaction, 1)
				readerBatches <- []Transaction{{ClientID: "held"}}
				var readerChannel, outputTelemetry batchQueue
				senderChannel := &outputTelemetry
				senderBatches := make(chan []Transaction)
				senderChannel.ch = senderBatches
				senderChannel.batchSize = 0
				readerChannel.ch = readerBatches
				readerChannel.batchSize = 0
				stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{TPS: 0, Installed: true})
				time.Sleep(40 * time.Millisecond)
				synctest.Wait()
				select {
				case <-senderBatches:
					t.Fatal("zero TPS forwarded a batch")
				default:
				}
				if got := senderChannel.snapshot(time.Now()); got.BlockedSenders != 0 || got.BlockedMS != 0 ||
					got.SentBatchesTotal != 0 {
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

func TestThrottlerStartsFreshAfterZeroTPSWithIdleInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction)
		senderBatches := make(chan []Transaction, 1)
		var readerChannel, senderChannel batchQueue
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		settings := Rate{TPS: 25, Installed: true}
		stage := startThrottler(t.Context(), &readerChannel, &senderChannel, settings)
		batch := []Transaction{{ClientID: "paced"}}
		readerBatches <- batch
		<-senderBatches

		stage.update(Rate{TPS: 0, Installed: true})
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

func TestThrottlerDoesNotAccumulateCreditWhileOutputBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 2)
		readerBatches <- []Transaction{{ClientID: "first"}}
		readerBatches <- []Transaction{{ClientID: "second"}}
		var readerChannel, outputTelemetry batchQueue
		senderChannel := &outputTelemetry
		senderBatches := make(chan []Transaction)
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		stage := startThrottler(ctx, &readerChannel, senderChannel, Rate{TPS: 25, Installed: true})
		time.Sleep(120 * time.Millisecond)
		synctest.Wait()
		if got := senderChannel.snapshot(time.Now()).BlockedSenders; got != 1 {
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

func TestThrottlerLimitsCreditAfterInputIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction, 2)
		senderBatches := make(chan []Transaction, 2)
		var readerChannel, senderChannel batchQueue
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		stage := startThrottler(t.Context(), &readerChannel, &senderChannel, Rate{TPS: 25, Installed: true})
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

func TestThrottlerRateUpdateKeepsElapsedSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		readerBatches := make(chan []Transaction, 2)
		senderBatches := make(chan []Transaction, 1)
		var readerChannel, senderChannel batchQueue
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		senderBatches <- []Transaction{{ClientID: "occupies output"}}
		readerBatches <- make([]Transaction, 1000)
		readerBatches <- make([]Transaction, 1000)
		close(readerBatches)
		stage := startThrottler(t.Context(), &readerChannel, &senderChannel, Rate{TPS: 200_000, Installed: true})
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		if got := senderChannel.snapshot(time.Now()).BlockedSenders; got != 1 {
			t.Fatalf("at first deadline blocked senders = %d, want 1", got)
		}

		stage.update(Rate{TPS: 400_000, Installed: true})
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
		if got := senderChannel.snapshot(time.Now()).BlockedSenders; got != 0 {
			t.Errorf("blocked senders after handoff = %d, want 0", got)
		}
	})
}

func TestThrottlerClosesItsOwnBlockedSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		readerBatches := make(chan []Transaction, 1)
		senderBatches := make(chan []Transaction)
		var readerChannel, senderChannel batchQueue
		readerChannel.ch = readerBatches
		readerChannel.batchSize = 0
		senderChannel.ch = senderBatches
		senderChannel.batchSize = 0
		other := senderChannel.beginBlockedSend([]Transaction{{ClientID: "other"}})
		time.Sleep(20 * time.Millisecond)
		readerBatches <- []Transaction{{ClientID: "throttled"}}
		stage := startThrottler(ctx, &readerChannel, &senderChannel, Rate{Installed: false})
		synctest.Wait()
		stage.update(Rate{Installed: true})
		synctest.Wait()
		got := senderChannel.snapshot(time.Now())
		cancel()
		<-stage.done
		other.wait(ctx)
		if got.BlockedSenders != 1 || got.OldestBlockedSenderMS != 20 {
			t.Fatalf("after update blocked senders = %d, oldest = %dms, want the sender blocked for 20ms",
				got.BlockedSenders, got.OldestBlockedSenderMS)
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
