package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestSenderPoolRetryLogWritesEventWithoutClientID(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatalf("newApplicationLogger() error = %v", err)
	}

	clientMarker := "client-id-marker"

	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	config.Retry.DelaysMS = []int{1}
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, logger)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 2 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: clientMarker}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()

	line := output.String()
	if !strings.Contains(line, "event=batch_delivery_retry") {
		t.Fatalf("retry event was not written")
	}
	if strings.Contains(line, clientMarker) {
		t.Fatal("batch ClientID was written to the retry log")
	}
}

func waitForSenderCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("Sender condition was not reached")
		default:
		}
	}
}

func TestSenderPoolRetriesFailureUntilSuccess(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	config.Retry.DelaysMS = []int{1}
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 4 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: "invalid"}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()
	if attempts.Load() != 4 {
		t.Fatalf("failure attempts=%d, want 4", attempts.Load())
	}
}

func TestSenderPoolAggregateSnapshotPartitionsActivities(t *testing.T) {
	pool := &senderPool{workers: []*senderWorker{
		{}, {draining: true},
		{busy: true}, {busy: true, draining: true},
		{busy: true, backoff: true}, {busy: true, backoff: true, draining: true},
	}}

	snapshot := pool.aggregateSnapshot()
	if snapshot.liveWorkers != 6 || snapshot.idleWorkers != 2 || snapshot.inFlightWorkers != 2 || snapshot.backoffWorkers != 2 {
		t.Fatalf("activity partition = %+v", snapshot)
	}
	if snapshot.drainingWorkers != 3 || snapshot.drainingIdleWorkers != 1 || snapshot.drainingInFlightWorkers != 1 || snapshot.drainingBackoffWorkers != 1 {
		t.Fatalf("draining intersections = %+v", snapshot)
	}
}

func TestSenderPoolCancellationStopsRetry(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	entered := make(chan struct{})
	var attempts atomic.Int64
	pool.attempt = func(ctx context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		attempts.Add(1)
		close(entered)
		<-ctx.Done()
		return senderAttemptCanceled
	}

	batches <- []Transaction{{ClientID: "canceled"}}
	<-entered
	<-pool.stop()
	if attempts.Load() != 1 {
		t.Fatalf("canceled attempts = %d, want 1", attempts.Load())
	}
	if consumed.Load() != 0 {
		t.Fatalf("canceled batch was counted as completed: consumed=%d", consumed.Load())
	}
}

func TestSenderPoolParentCancellationStopsRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		pool := startSenderPool(parent, batches, &channel, &consumed, 1, config.API, config.Retry, nil)
		pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
			return senderAttemptFailure
		}

		batches <- []Transaction{{ClientID: "canceled-parent"}}
		synctest.Wait()
		if pool.aggregateSnapshot().backoffWorkers != 1 {
			t.Fatal("Sender did not enter retry backoff")
		}
		cancel()
		<-pool.stop()
		if consumed.Load() != 0 {
			t.Fatalf("parent-canceled batch was counted as completed: consumed=%d", consumed.Load())
		}
	})
}

func TestSenderPoolBackpressureWhenAllWorkersRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		config.Retry.JitterPercent = 0
		pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
		pool.attempt = func(_ context.Context, _ []Transaction, attempt int) senderAttemptOutcome {
			if attempt == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}

		batches <- []Transaction{{ClientID: "first"}}
		batches <- []Transaction{{ClientID: "second"}}
		synctest.Wait()
		batches <- []Transaction{{ClientID: "third"}}
		if received := channel.snapshot(time.Now()).receivedBatchesTotal; received != 2 || len(batches) != 1 {
			t.Fatalf("all-worker backpressure received=%d queued=%d, want 2 and 1", received, len(batches))
		}

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if consumed.Load() != 3 {
			t.Fatalf("completed=%d, want 3", consumed.Load())
		}
		<-pool.stop()
	})
}

func TestSenderPoolReconcilesUpDownUpWithoutLosingAcceptedBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		entered <- struct{}{}
		<-release
		return senderAttemptSuccess
	}

	batches <- []Transaction{{ClientID: "A"}}
	batches <- []Transaction{{ClientID: "B"}}
	<-entered
	<-entered
	drainingWorker := pool.workers[1]
	pool.reconcile(1)
	if got := pool.aggregateSnapshot(); got.liveWorkers != 2 || got.drainingWorkers != 1 {
		t.Fatalf("after scale down = %+v, want two live and one draining", got)
	}
	pool.reconcile(2)
	if got := pool.aggregateSnapshot(); got.liveWorkers != 2 || got.drainingWorkers != 0 {
		t.Fatalf("after reactivation = %+v, want two live and zero draining", got)
	}
	if pool.workers[1] != drainingWorker {
		t.Fatal("scale up replaced the live draining worker")
	}
	close(release)
	batches <- []Transaction{{ClientID: "C"}}
	<-entered
	<-pool.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 3 {
		t.Fatalf("received batches = %d, want 3", got)
	}
	if got := consumed.Load(); got != 3 {
		t.Fatalf("completed transactions = %d, want 3", got)
	}
	if got := pool.aggregateSnapshot().liveWorkers; got != 0 {
		t.Fatalf("live workers after stop = %d, want 0", got)
	}
}

func TestSenderPoolScaleDownJoinsIdleWorkerBeforeNextReceive(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan string, 1)
	pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		entered <- batch[0].ClientID
		return senderAttemptSuccess
	}
	pool.reconcile(1)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 1 || snapshot.drainingWorkers != 0 {
		t.Fatalf("after idle downscale = %+v, want one active worker", snapshot)
	}
	batches <- []Transaction{{ClientID: "after-scale-down"}}
	if clientID := <-entered; clientID != "after-scale-down" {
		t.Fatalf("attempted batch = %q, want after-scale-down", clientID)
	}
	<-pool.stop()
	if got := consumed.Load(); got != 1 {
		t.Fatalf("completed transactions = %d, want 1", got)
	}
}

func TestSenderPoolReplacementRestoresDesiredWorkerCount(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)

	pool.reconcile(1)
	pool.reconcile(2)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 2 || snapshot.idleWorkers != 2 {
		t.Fatalf("replacement workers = %+v, want two idle workers", snapshot)
	}
	<-pool.stop()
}

func TestSenderPoolReadyBatchCannotEnterMarkedDrainingWorker(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan struct {
		clientID      string
		firstReleased bool
	}, 2)
	releaseFirst := make(chan struct{})
	pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		clientID := batch[0].ClientID
		var firstReleased bool
		if clientID == "after-mark" {
			select {
			case <-releaseFirst:
				firstReleased = true
			default:
			}
		}
		entered <- struct {
			clientID      string
			firstReleased bool
		}{clientID: clientID, firstReleased: firstReleased}
		if clientID == "first" {
			<-releaseFirst
		}
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "first"}}
	if attempt := <-entered; attempt.clientID != "first" {
		t.Fatalf("first attempted batch = %q, want first", attempt.clientID)
	}

	// Hold dispatch at the assignment lock while drain wake and a batch are both ready.
	pool.mu.Lock()
	draining := pool.workers[1]
	pool.desired = 1
	draining.draining = true
	draining.wake <- struct{}{}
	batches <- []Transaction{{ClientID: "after-mark"}}
	pool.mu.Unlock()
	<-draining.done
	close(releaseFirst)
	attempt := <-entered
	<-pool.stop()
	if attempt.clientID != "after-mark" || !attempt.firstReleased {
		t.Fatalf("after-mark attempt = %+v, want after-mark after first release", attempt)
	}
	if got := consumed.Load(); got != 2 {
		t.Fatalf("completed transactions = %d, want 2", got)
	}
}

func TestSenderPoolRetainsBatchBeyondConfiguredDelayList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.JitterPercent = 0
		pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
		var attempts atomic.Int64
		previous := time.Now()
		delays := make(chan time.Duration, 6)
		pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
			if attempts.Load() != 0 {
				delays <- time.Since(previous)
			}
			previous = time.Now()
			if attempts.Add(1) == 7 {
				return senderAttemptSuccess
			}
			return senderAttemptFailure
		}
		batches <- []Transaction{{ClientID: "failure"}}
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if snapshot := pool.aggregateSnapshot(); snapshot.idleWorkers != 1 || snapshot.backoffWorkers != 0 {
			t.Fatalf("successful sender snapshot = %+v", snapshot)
		}
		<-pool.stop()
		if got := attempts.Load(); got != 7 {
			t.Fatalf("attempts = %d, want 7", got)
		}
		gotDelays := make([]time.Duration, 0, 6)
		for range 6 {
			gotDelays = append(gotDelays, <-delays)
		}
		wantDelays := []time.Duration{250, 500, 1_000, 2_000, 5_000, 5_000}
		for index := range wantDelays {
			wantDelays[index] *= time.Millisecond
		}
		if !slices.Equal(gotDelays, wantDelays) {
			t.Fatalf("retry delays = %v, want %v", gotDelays, wantDelays)
		}
	})
}

func TestSenderPoolStopWaitsForAcceptedBatch(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "accepted"}}
	<-entered
	done := pool.stop()
	select {
	case <-done:
		t.Fatal("pool stopped before accepted batch completed")
	default:
	}
	close(release)
	<-done
	if consumed.Load() != 1 || pool.aggregateSnapshot().liveWorkers != 0 {
		t.Fatalf("after join: consumed=%d live workers=%d", consumed.Load(), pool.aggregateSnapshot().liveWorkers)
	}
}

func TestSenderPoolRetryDeadlineAndReconcileWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry channelTelemetry
		var completed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		config.Retry.JitterPercent = 0
		pool := startSenderPool(
			context.Background(), batches, &telemetry, &completed,
			1, config.API, config.Retry, nil,
		)
		defer func() { <-pool.stop() }()
		var attempts atomic.Int64
		pool.attempt = func(_ context.Context, batch []Transaction, number int) senderAttemptOutcome {
			if batch[0].ClientID != "held" || int64(number) != attempts.Load()+1 {
				t.Fatal("retry changed batch or attempt number")
			}
			attempts.Add(1)
			if number == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}
		batches <- []Transaction{{ClientID: "held"}}
		synctest.Wait()
		time.Sleep(10 * time.Minute)
		pool.reconcile(1)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("reconcile shortened retry delay")
		}
		time.Sleep(50*time.Minute - time.Nanosecond)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("retry started before its original deadline")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if attempts.Load() != 2 || completed.Load() != 1 {
			t.Fatalf("at deadline attempts=%d completed=%d", attempts.Load(), completed.Load())
		}
	})
}
