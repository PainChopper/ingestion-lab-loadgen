package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestElapsedMsUsesRunStartAndAccumulatedTime(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state := controlState{lifecycle: newLifecycle()}
	if got := state.elapsedMs(start); got != 0 {
		t.Fatalf("idle elapsed = %d, want 0", got)
	}

	state.lifecycle.run()
	state.runStartedAt = start
	if got := state.elapsedMs(start.Add(1250 * time.Millisecond)); got != 1250 {
		t.Fatalf("running elapsed = %d, want 1250", got)
	}

	state.lifecycle.pause()
	state.pauseElapsed(start.Add(1250 * time.Millisecond))
	if got := state.elapsedMs(start.Add(10 * time.Second)); got != 1250 {
		t.Fatalf("paused elapsed = %d, want 1250", got)
	}

	state.lifecycle.run()
	state.runStartedAt = start.Add(10 * time.Second)
	if got := state.elapsedMs(start.Add(10*time.Second + 750*time.Millisecond)); got != 2000 {
		t.Fatalf("resumed elapsed = %d, want 2000", got)
	}
}

func TestRunFailureRetryAndResetUpdateStartError(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	batches := make(chan []Transaction)
	var starts int
	produce := func(ctx context.Context) (<-chan []Transaction, error) {
		starts++
		switch starts {
		case 1:
			return nil, errors.New("first failure")
		case 2:
			return nil, errors.New("second failure")
		}
		go func() {
			defer close(batches)
			<-ctx.Done()
		}()
		return batches, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, produce)
	reply := make(chan commandResult, 1)
	snapshotReply := make(chan statusSnapshot, 1)

	for index, want := range []string{"first failure", "second failure"} {
		requests <- request{kind: cmdRun, commandReply: reply}
		if result := <-reply; result.err == nil || result.err.Error() != want {
			t.Fatalf("Run error = %v, want %q", result.err, want)
		}
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		snapshot := <-snapshotReply
		if snapshot.RunState != runStateIdle || snapshot.ElapsedMs != 0 || snapshot.StartError == nil || *snapshot.StartError != want {
			t.Fatalf("failed Run snapshot = %+v, want idle, zero elapsed, %q", snapshot, want)
		}
		if index == 0 {
			requests <- request{kind: cmdReset, commandReply: reply}
			if result := <-reply; result.status != commandAccepted {
				t.Fatalf("Reset status = %v, want accepted", result.status)
			}
			requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
			if snapshot := <-snapshotReply; snapshot.StartError != nil || snapshot.ElapsedMs != 0 {
				t.Fatalf("Reset snapshot = %+v, want cleared error and elapsed", snapshot)
			}
		}
	}

	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil {
		t.Fatalf("retry Run error = %v, want nil", result.err)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStateRunning || snapshot.StartError != nil {
		t.Fatalf("successful Run snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil || starts != 3 {
		t.Fatalf("repeated Run = %+v, producer starts = %d, want nil error and 3 starts", result, starts)
	}
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStatePaused || snapshot.ElapsedMs < 0 {
		t.Fatalf("Pause snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("paused Reset status = %v, want accepted", result.status)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStateIdle || snapshot.ElapsedMs != 0 {
		t.Fatalf("paused Reset snapshot = %+v", snapshot)
	}
}

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	var starts int
	onProduce := func() {
		starts++
	}
	requests, _, _ := startEventLoopForTest(t, onProduce)

	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	runningSnapshot := <-reply
	if runningSnapshot.RunState != runStateRunning {
		t.Fatalf("state after first Run = %v, want %v", runningSnapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after first Run = %v, want 1", starts)
	}

	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	repeatedRunSnapshot := <-reply
	if repeatedRunSnapshot.RunState != runStateRunning {
		t.Fatalf("state after second Run = %v, want %v", repeatedRunSnapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after repeated Run = %v, want 1", starts)
	}
}

func TestPauseStopsConsumptionUntilRun(t *testing.T) {
	var starts int
	requests, batches, metrics := startEventLoopForTest(t, func() { starts++ })

	requests <- request{kind: cmdRun}
	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("first batch was not received after Run")
	}
	waitForTransactions(t, requests, metrics, 1)

	// The snapshot reply confirms that Pause was handled before the next batch.
	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	select {
	case <-reply:
	case <-time.After(time.Second):
		t.Fatal("Pause was not handled")
	}

	secondBatch := []Transaction{{}}
	select {
	case batches <- secondBatch:
		t.Fatal("second batch was consumed during Pause")
	case <-time.After(250 * time.Millisecond):
	}

	requests <- request{kind: cmdRun}
	select {
	case batches <- secondBatch:
	case <-time.After(time.Second):
		t.Fatal("second batch was not consumed after Run")
	}
	waitForTransactions(t, requests, metrics, 2)
	if starts != 1 {
		t.Fatalf("producer starts = %v, want 1", starts)
	}
}

func TestResetFromPausedStopsProducerClearsProgressAndStartsFreshRun(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	firstBatches := make(chan []Transaction, 1)
	firstProducerReady := make(chan struct{})
	firstBatchQueued := make(chan struct{})
	freshBatches := make(chan []Transaction)
	var starts int

	produce := func(ctx context.Context) (<-chan []Transaction, error) {
		starts++
		if starts == 1 {
			go func() {
				defer close(firstBatches)
				select {
				case <-firstProducerReady:
				case <-ctx.Done():
					return
				}
				select {
				case firstBatches <- []Transaction{{}}:
				case <-ctx.Done():
					return
				}
				close(firstBatchQueued)
				select {
				case firstBatches <- []Transaction{{}}:
				case <-ctx.Done():
				}
			}()
			return firstBatches, nil
		}

		go func() {
			defer close(freshBatches)
			<-ctx.Done()
		}()
		return freshBatches, nil
	}

	startCustomEventLoopForTest(t, requests, metrics, produce)
	requests <- request{kind: cmdRun}
	firstBatches <- []Transaction{{}}
	waitForTransactions(t, requests, metrics, 1)

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	close(firstProducerReady)
	select {
	case <-firstBatchQueued:
	case <-time.After(time.Second):
		t.Fatal("producer did not fill its queue after Pause")
	}

	resetReply := make(chan commandResult, 1)
	requests <- request{kind: cmdReset, commandReply: resetReply}
	select {
	case result := <-resetReply:
		if result.status != commandAccepted {
			t.Fatalf("reset status = %v, want %v", result.status, commandAccepted)
		}
	case <-time.After(time.Second):
		t.Fatal("Reset did not complete")
	}
	select {
	case _, ok := <-firstBatches:
		if ok {
			t.Fatal("old producer queue retained a batch after Reset")
		}
	case <-time.After(time.Second):
		t.Fatal("old producer channel was not closed after Reset")
	}
	waitForState(t, requests, runStateIdle)
	waitForTransactions(t, requests, metrics, 0)
	requests <- request{kind: cmdReset, commandReply: resetReply}
	select {
	case result := <-resetReply:
		if result.status != commandAccepted {
			t.Fatalf("second reset status = %v, want %v", result.status, commandAccepted)
		}
	case <-time.After(time.Second):
		t.Fatal("second Reset did not complete")
	}
	waitForState(t, requests, runStateIdle)

	requests <- request{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	if starts != 2 {
		t.Fatalf("producer starts after Reset and Run = %v, want 2", starts)
	}
	select {
	case freshBatches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("fresh producer batch was not consumed")
	}
	waitForTransactions(t, requests, metrics, 1)
}

func TestResetDuringRunReturnsConflictAndPreservesPipeline(t *testing.T) {
	var starts int
	requests, batches, metrics := startEventLoopForTest(t, func() { starts++ })
	handler := commandsHandler(requests)
	runRequest := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"run"}`))
	handler.ServeHTTP(httptest.NewRecorder(), runRequest)
	waitForState(t, requests, runStateRunning)

	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("first batch was not consumed")
	}
	waitForTransactions(t, requests, metrics, 1)

	resetRecorder := httptest.NewRecorder()
	resetRequest := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"reset"}`))
	handler.ServeHTTP(resetRecorder, resetRequest)
	if resetRecorder.Code != http.StatusConflict {
		t.Fatalf("Reset status code = %v, want %v", resetRecorder.Code, http.StatusConflict)
	}
	waitForState(t, requests, runStateRunning)
	waitForTransactions(t, requests, metrics, 1)
	if starts != 1 {
		t.Fatalf("producer starts after rejected Reset = %v, want 1", starts)
	}

	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("consumer stopped after rejected Reset")
	}
	waitForTransactions(t, requests, metrics, 2)
}

func startEventLoopForTest(t *testing.T, onProduce func()) (chan<- request, chan<- []Transaction, chan<- time.Time) {
	t.Helper()

	requests := make(chan request, 3)
	batches := make(chan []Transaction)
	metrics := make(chan time.Time)
	produce := func(ctx context.Context) (<-chan []Transaction, error) {
		onProduce()
		go func() {
			defer close(batches)
			<-ctx.Done()
		}()
		return batches, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, produce)

	return requests, batches, metrics
}

func startCustomEventLoopForTest(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	produce func(context.Context) (<-chan []Transaction, error),
) {
	t.Helper()

	done := make(chan struct{})
	state := controlState{lifecycle: newLifecycle()}
	go func() {
		defer close(done)
		state.eventLoop(requests, metrics, NewMetrics(), produce)
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})
}

func waitForState(t *testing.T, requests chan<- request, want runState) {
	t.Helper()

	reply := make(chan statusSnapshot, 1)
	select {
	case requests <- request{kind: getSnapshot, snapshotReply: reply}:
	case <-time.After(time.Second):
		t.Fatalf("state did not reach %v", want)
	}
	select {
	case snapshot := <-reply:
		if snapshot.RunState != want {
			t.Fatalf("state = %v, want %v", snapshot.RunState, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("state did not reach %v", want)
	}
}

func waitForTransactions(t *testing.T, requests chan<- request, metrics chan<- time.Time, want int64) {
	t.Helper()

	reply := make(chan statusSnapshot, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case metrics <- time.Now():
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
		select {
		case requests <- request{kind: getSnapshot, snapshotReply: reply}:
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
		select {
		case snapshot := <-reply:
			if snapshot.TotalTransactions == want {
				return
			}
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
	}
}
