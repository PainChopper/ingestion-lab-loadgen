package main

import (
	"testing"
	"time"
)

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

func startEventLoopForTest(t *testing.T, onProduce func()) (chan<- request, chan<- []Transaction, chan<- time.Time) {
	t.Helper()

	requests := make(chan request, 3)
	batches := make(chan []Transaction)
	metrics := make(chan time.Time)
	done := make(chan struct{})
	state := controlState{lifecycle: newLifecycle()}

	produce := func() (<-chan []Transaction, error) {
		onProduce()
		return batches, nil
	}

	go func() {
		defer close(done)
		state.eventLoop(requests, metrics, NewMetrics(), produce)
	}()

	t.Cleanup(func() {
		close(requests)
		close(batches)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	return requests, batches, metrics
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
