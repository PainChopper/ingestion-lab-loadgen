package main

import (
	"sync/atomic"
	"testing"
)

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	var starts int
	onProduce := func() {
		starts++
	}
	requests := startEventLoopForTest(t, onProduce)

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

func TestPauseCommand(t *testing.T) {
	var starts int
	onProduce := func() {
		starts++
	}
	requests := startEventLoopForTest(t, onProduce)

	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdRun}
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	pausedSnapshot := <-reply
	if pausedSnapshot.RunState != runStatePaused {
		t.Fatalf("state after Pause = %v, want %v", pausedSnapshot.RunState, runStatePaused)
	}
	if starts != 1 {
		t.Fatalf("starts after Run and Pause = %v, want 1", starts)
	}

	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	resumedSnapshot := <-reply
	if resumedSnapshot.RunState != runStateRunning {
		t.Fatalf("state after Resume  = %v, want %v", resumedSnapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after Run and Pause and Run = %v, want 1", starts)
	}
}

func startEventLoopForTest(t *testing.T, onProduce func()) chan<- request {
	t.Helper()

	requests := make(chan request, 3)
	batches := make(chan []Transaction)
	done := make(chan struct{})
	var consumerStarted atomic.Bool
	state := controlState{lifecycle: newLifecycle()}

	produce := func() (<-chan []Transaction, error) {
		onProduce()
		consumerStarted.Store(true)
		return batches, nil
	}

	go func() {
		defer close(done)
		state.eventLoop(requests, nil, NewMetrics(), produce)
	}()

	t.Cleanup(func() {
		if !consumerStarted.Load() {
			close(requests)
			close(batches)
			<-done
			return
		}
		close(batches)
		<-done
		close(requests)
	})

	return requests
}
