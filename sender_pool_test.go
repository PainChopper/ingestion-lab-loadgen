package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSenderPoolRetryLogExcludesRequestMarkers(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatalf("newApplicationLogger() error = %v", err)
	}

	urlMarker := "url-credential-and-query-marker"
	headerMarker := "header-value-marker"
	bodyMarker := "body-payload-marker"
	clientMarker := "client-id-marker"
	requestURL := "https://" + urlMarker + "@example.test/ingest?token=" + urlMarker
	headers := http.Header{"Authorization": {headerMarker}}
	body := []byte(bodyMarker)
	if requestURL == "" || len(headers) == 0 || len(body) == 0 {
		t.Fatal("test request markers were not initialized")
	}

	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry, logger)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int, int) senderAttemptOutcome {
		if attempts.Add(1) == 2 {
			return senderAttemptSuccess
		}
		return senderAttemptTerminalFailure
	}
	pool.wait = func(context.Context, time.Duration) bool { return true }

	batches <- []Transaction{{ClientID: clientMarker}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()

	line := output.String()
	if !strings.Contains(line, "event=batch_delivery_retry") {
		t.Fatalf("retry event was not written")
	}
	for _, marker := range []string{urlMarker, headerMarker, bodyMarker, clientMarker} {
		if strings.Contains(line, marker) {
			t.Fatal("request marker was written to the log")
		}
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

func TestSenderPoolRetriesTerminalFailureUntilSuccess(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	var attempts atomic.Int64
	var backoffs atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int, int) senderAttemptOutcome {
		if attempts.Add(1) == 4 {
			return senderAttemptSuccess
		}
		return senderAttemptTerminalFailure
	}
	pool.wait = func(context.Context, time.Duration) bool {
		backoffs.Add(1)
		return true
	}

	batches <- []Transaction{{ClientID: "invalid"}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()
	if attempts.Load() != 4 || backoffs.Load() != 3 {
		t.Fatalf("terminal failure attempts=%d backoffs=%d, want 4 and 3", attempts.Load(), backoffs.Load())
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
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	entered := make(chan struct{})
	var attempts atomic.Int64
	pool.attempt = func(ctx context.Context, _ []Transaction, _, _ int) senderAttemptOutcome {
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
	if consumed.Load() != 0 || telemetry.snapshot().completedBatches != 0 {
		t.Fatalf("canceled batch was counted as completed: consumed=%d telemetry=%+v", consumed.Load(), telemetry.snapshot())
	}
}

func TestSenderPoolBackpressureWhenAllWorkersRetry(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, policy.API, policy.Retry)
	backoffEntered := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	pool.attempt = func(_ context.Context, _ []Transaction, _, attempt int) senderAttemptOutcome {
		if attempt == 1 {
			return senderAttemptRetryableFailure
		}
		return senderAttemptSuccess
	}
	pool.wait = func(ctx context.Context, _ time.Duration) bool {
		backoffEntered <- struct{}{}
		select {
		case <-ctx.Done():
			return false
		case <-release:
			return true
		}
	}

	batches <- []Transaction{{ClientID: "first"}}
	batches <- []Transaction{{ClientID: "second"}}
	<-backoffEntered
	<-backoffEntered
	batches <- []Transaction{{ClientID: "third"}}
	if received := channel.snapshot(time.Now()).receivedBatchesTotal; received != 2 || len(batches) != 1 {
		t.Fatalf("all-worker backpressure received=%d queued=%d, want 2 and 1", received, len(batches))
	}

	release <- struct{}{}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	waitForSenderCondition(t, func() bool { return channel.snapshot(time.Now()).receivedBatchesTotal == 3 })
	release <- struct{}{}
	release <- struct{}{}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 3 })
	<-pool.stop()
}

func TestSenderPoolReconcilesUpDownUpWithoutLosingAcceptedBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, policy.API, policy.Retry)
	entered := make(chan int, 3)
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	pool.attempt = func(_ context.Context, _ []Transaction, ordinal, _ int) senderAttemptOutcome {
		entered <- ordinal
		<-release[ordinal]
		return senderAttemptSuccess
	}

	batches <- []Transaction{{ClientID: "A"}}
	batches <- []Transaction{{ClientID: "B"}}
	first, second := <-entered, <-entered
	if first == second {
		t.Fatalf("two batches entered one worker: %d, %d", first, second)
	}
	drainingWorker := pool.workers[1]
	pool.reconcile(1)
	if got := telemetry.snapshot(); got.liveWorkers != 2 || got.drainingWorkers != 1 {
		t.Fatalf("after scale down = %+v, want two live and one draining", got)
	}
	pool.reconcile(2)
	if got := telemetry.snapshot(); got.liveWorkers != 2 || got.drainingWorkers != 0 {
		t.Fatalf("after reactivation = %+v, want two live and zero draining", got)
	}
	if pool.workers[1] != drainingWorker {
		t.Fatal("scale up replaced the live draining worker")
	}
	close(release[1])
	batches <- []Transaction{{ClientID: "C"}}
	if ordinal := <-entered; ordinal != 1 {
		t.Fatalf("reactivated batch entered worker %d, want worker 1", ordinal)
	}
	close(release[0])
	<-pool.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 3 {
		t.Fatalf("received batches = %d, want 3", got)
	}
	if got := consumed.Load(); got != 3 {
		t.Fatalf("completed transactions = %d, want 3", got)
	}
	if got := telemetry.snapshot().liveWorkers; got != 0 {
		t.Fatalf("live workers after stop = %d, want 0", got)
	}
}

func TestSenderPoolScaleDownJoinsIdleWorkerBeforeNextReceive(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, policy.API, policy.Retry)
	entered := make(chan int, 1)
	pool.attempt = func(_ context.Context, _ []Transaction, ordinal, _ int) senderAttemptOutcome {
		entered <- ordinal
		return senderAttemptSuccess
	}
	pool.reconcile(1)
	if snapshot := telemetry.snapshot(); snapshot.liveWorkers != 1 || snapshot.drainingWorkers != 0 {
		t.Fatalf("after idle downscale = %+v, want one active worker", snapshot)
	}
	batches <- []Transaction{{ClientID: "after-scale-down"}}
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("batch entered worker %d after scale-down, want worker 0", ordinal)
	}
	<-pool.stop()
}

func TestSenderPoolReplacementGetsNewWorkerID(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, policy.API, policy.Retry)

	pool.reconcile(1)
	pool.reconcile(2)
	slots := telemetry.snapshot().workerSlots
	if len(slots) != 2 || slots[0].WorkerID != 0 || slots[1].WorkerID != 2 {
		t.Fatalf("replacement worker IDs = %+v, want 0 and 2", slots)
	}
	<-pool.stop()
}

func TestSenderPoolReadyBatchCannotEnterMarkedDrainingWorker(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, policy.API, policy.Retry)
	entered := make(chan int, 2)
	releaseFirst := make(chan struct{})
	pool.attempt = func(_ context.Context, batch []Transaction, ordinal, _ int) senderAttemptOutcome {
		entered <- ordinal
		if batch[0].ClientID == "first" {
			<-releaseFirst
		}
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "first"}}
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("first batch entered worker %d, want worker 0", ordinal)
	}

	// Hold dispatch at the assignment lock while drain wake and a batch are both ready.
	pool.mu.Lock()
	draining := pool.workers[1]
	pool.desired = 1
	draining.draining = true
	pool.telemetry.setLifecycle(1, "draining")
	draining.wake <- struct{}{}
	batches <- []Transaction{{ClientID: "after-mark"}}
	pool.mu.Unlock()
	<-draining.done
	close(releaseFirst)
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("batch after drain mark entered worker %d, want worker 0", ordinal)
	}
	<-pool.stop()
	if got := consumed.Load(); got != 2 {
		t.Fatalf("completed transactions = %d, want 2", got)
	}
}

func TestSenderPoolRetainsBatchBeyondConfiguredDelayList(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	policy.Retry.JitterPercent = 0
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	var attempts atomic.Int64
	var backoffs atomic.Int64
	pool.attempt = func(_ context.Context, _ []Transaction, _, _ int) senderAttemptOutcome {
		if attempts.Add(1) == 7 {
			return senderAttemptSuccess
		}
		return senderAttemptRetryableFailure
	}
	delays := make(chan time.Duration, 6)
	pool.wait = func(_ context.Context, delay time.Duration) bool {
		delays <- delay
		backoffs.Add(1)
		return true
	}
	batches <- []Transaction{{ClientID: "failure"}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	if slot := telemetry.snapshot().workerSlots[0]; slot.TerminalError || slot.Activity != "idle" {
		t.Fatalf("successful slot = %+v", slot)
	}
	<-pool.stop()
	if got := attempts.Load(); got != 7 {
		t.Fatalf("attempts = %d, want 7", got)
	}
	if got := backoffs.Load(); got != 6 {
		t.Fatalf("backoffs = %d, want 6", got)
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
}

func TestSenderPoolStopWaitsForAcceptedBatch(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _, _ int) senderAttemptOutcome {
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
	if consumed.Load() != 1 || telemetry.snapshot().liveWorkers != 0 {
		t.Fatalf("after join: consumed=%d sender=%+v", consumed.Load(), telemetry.snapshot())
	}
}

func TestSenderPoolStopLeavesReadyBatchForResume(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	policy := testPolicy(t).Sender
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	policy.API.URL = server.URL
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}

	batches <- []Transaction{{ClientID: "accepted"}}
	<-entered
	pool.intakeMu.Lock()
	// Leave one worker idle with a ready batch while intake is held.
	pool.reconcile(2)
	batches <- []Transaction{{ClientID: "ready"}}
	stopped := make(chan (<-chan struct{}), 1)
	go func() { stopped <- pool.stop() }()
	<-pool.ctx.Done()
	select {
	case <-stopped:
		t.Fatal("stop crossed the intake boundary while intake was held")
	default:
	}
	pool.intakeMu.Unlock()
	done := <-stopped
	received := channel.snapshot(time.Now()).receivedBatchesTotal
	if len(batches) != 1 || received != 1 {
		t.Fatalf("after stop boundary: queued=%d received=%d, want one of each", len(batches), received)
	}
	select {
	case <-done:
		t.Fatal("stop completed before the accepted batch")
	default:
	}
	close(release)
	<-done
	if consumed.Load() != 1 || len(batches) != 1 {
		t.Fatalf("after stop: completed=%d queued=%d, want one of each", consumed.Load(), len(batches))
	}

	resumed := startSenderPool(batches, &channel, &telemetry, &consumed, 1, policy.API, policy.Retry)
	waitForSenderCondition(t, func() bool { return consumed.Load() == 2 })
	<-resumed.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 2 {
		t.Fatalf("received batches after resume = %d, want 2", got)
	}
}
