package main

import (
	"errors"
	"testing"
)

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	var starts int
	batches := make(chan []Transaction)
	commands := make(chan request, 3)
	done := make(chan struct{})
	consumerStarted := false
	state := controlState{lifecycle: newLifecycle()}
	produce := func() (<-chan []Transaction, error) {
		starts++
		consumerStarted = true
		return batches, nil
	}
	go func() {
		state.eventLoop(commands, nil, NewMetrics(), produce)
		close(done)
	}()
	defer func() {
		if !consumerStarted {
			close(commands)
			close(batches)
			<-done
			return
		}
		close(batches)
		<-done
		close(commands)
	}()

	reply := make(chan statusSnapshot, 1)
	commands <- request{kind: getSnapshot, snapshotReply: reply}
	snapshot := <-reply
	if snapshot.RunState != runStateIdle {
		t.Fatalf("initial state = %v, want %v", snapshot.RunState, runStateIdle)
	}
	if starts != 0 {
		t.Fatalf("initial starts = %v, want 0", starts)
	}

	commands <- request{kind: cmdRun}
	commands <- request{kind: getSnapshot, snapshotReply: reply}
	snapshot = <-reply
	if snapshot.RunState != runStateRunning {
		t.Fatalf("state after first Run = %v, want %v", snapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after first Run = %v, want 1", starts)
	}

	commands <- request{kind: cmdRun}
	commands <- request{kind: cmdRun}
	commands <- request{kind: getSnapshot, snapshotReply: reply}
	snapshot = <-reply
	if snapshot.RunState != runStateRunning {
		t.Fatalf("state after repeated Run = %v, want %v", snapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after repeated Run = %v, want 1", starts)
	}
}

func TestEventLoopRetriesRunAfterPreparationError(t *testing.T) {
	var attempts int
	batches := make(chan []Transaction)
	commands := make(chan request, 2)
	done := make(chan struct{})
	consumerStarted := false
	state := controlState{lifecycle: newLifecycle()}
	produce := func() (<-chan []Transaction, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("preparation failed")
		}
		consumerStarted = true
		return batches, nil
	}
	go func() {
		state.eventLoop(commands, nil, NewMetrics(), produce)
		close(done)
	}()
	defer func() {
		if !consumerStarted {
			close(commands)
			close(batches)
			<-done
			return
		}
		close(batches)
		<-done
		close(commands)
	}()

	reply := make(chan statusSnapshot, 1)
	commands <- request{kind: cmdRun}
	commands <- request{kind: getSnapshot, snapshotReply: reply}
	snapshot := <-reply
	if snapshot.RunState != runStateIdle {
		t.Fatalf("state after failed Run = %v, want %v", snapshot.RunState, runStateIdle)
	}
	if attempts != 1 {
		t.Fatalf("attempts after failed Run = %v, want 1", attempts)
	}

	commands <- request{kind: cmdRun}
	commands <- request{kind: getSnapshot, snapshotReply: reply}
	snapshot = <-reply
	if snapshot.RunState != runStateRunning {
		t.Fatalf("state after retry = %v, want %v", snapshot.RunState, runStateRunning)
	}
	if attempts != 2 {
		t.Fatalf("attempts after retry = %v, want 2", attempts)
	}
}
