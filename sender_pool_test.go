package main

import (
	"sync/atomic"
	"testing"
	"time"
)

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

func TestSenderPoolReconcilesUpDownUpWithoutLosingAcceptedBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, 0, 0, testPolicy(t).Sender.Retry)
	entered := make(chan int, 3)
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	pool.attempt = func(_ []Transaction, ordinal, _, _, _ int) bool {
		entered <- ordinal
		<-release[ordinal]
		return true
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
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, 0, 0, testPolicy(t).Sender.Retry)
	entered := make(chan int, 1)
	pool.attempt = func(_ []Transaction, ordinal, _, _, _ int) bool {
		entered <- ordinal
		return true
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

func TestSenderPoolReadyBatchCannotEnterMarkedDrainingWorker(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 2, 0, 0, testPolicy(t).Sender.Retry)
	entered := make(chan int, 2)
	releaseFirst := make(chan struct{})
	pool.attempt = func(batch []Transaction, ordinal, _, _, _ int) bool {
		entered <- ordinal
		if batch[0].ClientID == "first" {
			<-releaseFirst
		}
		return true
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

func TestSenderPoolRetriesAndClearsStickyTerminalErrorOnSuccess(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, 0, 100, testPolicy(t).Sender.Retry)
	var attempts atomic.Int64
	var backoffs atomic.Int64
	pool.attempt = func(_ []Transaction, _, _, _, _ int) bool {
		return attempts.Add(1) == 4
	}
	pool.wait = func(time.Duration) { backoffs.Add(1) }
	batches <- []Transaction{{ClientID: "failure"}}
	waitForSenderCondition(t, func() bool { return telemetry.snapshot().terminalBatches == 1 })
	if slot := telemetry.snapshot().workerSlots[0]; !slot.TerminalError || slot.Activity != "idle" {
		t.Fatalf("terminal slot = %+v", slot)
	}
	batches <- []Transaction{{ClientID: "success"}}
	waitForSenderCondition(t, func() bool { return telemetry.snapshot().terminalBatches == 2 })
	if slot := telemetry.snapshot().workerSlots[0]; slot.TerminalError {
		t.Fatalf("success did not clear terminal error: %+v", slot)
	}
	<-pool.stop()
	if got := attempts.Load(); got != 4 {
		t.Fatalf("attempts = %d, want 4", got)
	}
	if got := backoffs.Load(); got != 2 {
		t.Fatalf("backoffs = %d, want 2", got)
	}
}

func TestSenderPoolStopWaitsForAcceptedBatch(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var telemetry senderTelemetry
	var consumed atomic.Int64
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, 0, 0, testPolicy(t).Sender.Retry)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ []Transaction, _, _, _, _ int) bool {
		close(entered)
		<-release
		return true
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
	retry := testPolicy(t).Sender.Retry
	pool := startSenderPool(batches, &channel, &telemetry, &consumed, 1, 0, 0, retry)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ []Transaction, _, _, _, _ int) bool {
		close(entered)
		<-release
		return true
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

	resumed := startSenderPool(batches, &channel, &telemetry, &consumed, 1, 0, 0, retry)
	waitForSenderCondition(t, func() bool { return consumed.Load() == 2 })
	<-resumed.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 2 {
		t.Fatalf("received batches after resume = %d, want 2", got)
	}
}
