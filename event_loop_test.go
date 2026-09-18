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
	<-reply
	if starts != 1 {
		t.Fatalf("starts after first Run = %v, want 1", starts)
	}

	requests <- request{kind: cmdRun}
	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	<-reply
	if starts != 1 {
		t.Fatalf("starts after repeated Run = %v, want 1", starts)
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
