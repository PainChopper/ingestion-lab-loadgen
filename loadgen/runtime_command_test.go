package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestExecuteRuntimeCommandForwardsCommandAndReceipt(t *testing.T) {
	control := make(chan runtimeRequest, 10)
	const want = false
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := (<-control).(controlRequest)
		command := request.command
		if command.kind != setThrottlerInstalled || command.value != 42 || command.installed != false {
			t.Errorf("command = %+v", command)
		}
		if cap(request.reply) != 1 {
			t.Errorf("receipt reply capacity = %d, want 1", cap(request.reply))
		}
		request.reply <- want
	}()

	command := runtimeCommand{kind: setThrottlerInstalled, value: 42, installed: false}
	if got, err := executeRuntimeCommand(t.Context(), runtimeRequests{queue: control}, command); err != nil || got != want {
		t.Errorf("receipt = %+v, err=%v, want %+v", got, err, want)
	}
	<-done
}

func TestRequestRuntimeStatusForwardsReply(t *testing.T) {
	control := make(chan runtimeRequest, 10)
	want := runtimeSnapshot{Run: runSnapshot{State: runStateStopping}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := (<-control).(snapshotRequest)
		if cap(request.reply) != 1 {
			t.Errorf("snapshot reply capacity = %d, want 1", cap(request.reply))
		}
		request.reply <- want
	}()

	if got, err := requestSnapshot(t.Context(), runtimeRequests{queue: control}); err != nil || got.Run != want.Run {
		t.Errorf("run status = %+v, err=%v, want %+v", got.Run, err, want.Run)
	}
	<-done
}

func TestRuntimeRequestsUnblockAtBothWaits(t *testing.T) {
	for _, stage := range []string{"send", "reply"} {
		for _, cause := range []string{"request canceled", "event loop exited"} {
			t.Run(stage+"/"+cause, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					control := make(chan runtimeRequest)
					done := make(chan struct{})
					requests := runtimeRequests{queue: control, done: done}
					results := make(chan error, 2)
					go func() {
						_, err := requestSnapshot(ctx, requests)
						results <- err
					}()
					go func() {
						_, err := executeRuntimeCommand(ctx, requests, runtimeCommand{kind: setReaderWorkers, value: 2})
						results <- err
					}()
					var pending []runtimeRequest
					if stage == "reply" {
						pending = []runtimeRequest{<-control, <-control}
					}
					synctest.Wait()
					want := errRuntimeUnavailable
					if cause == "request canceled" {
						want = context.Canceled
						cancel()
					} else {
						close(done)
					}
					for range 2 {
						if err := <-results; !errors.Is(err, want) {
							t.Fatalf("request returned %v, want %v", err, want)
						}
					}
					for _, request := range pending {
						switch request := request.(type) {
						case snapshotRequest:
							request.reply <- runtimeSnapshot{}
						case controlRequest:
							request.reply <- true
						}
					}
				})
			})
		}
	}
}

func TestRuntimeRequestPreservesReadyReceiptWhenEventLoopCloses(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "applied", want: true},
		{name: "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 64 {
				done := make(chan struct{})
				reply := make(chan bool, 1)
				// Both select cases are ready before the handler waits for the reply.
				reply <- tc.want
				close(done)
				if got, err := receiveRuntimeReply(t.Context(), done, reply); err != nil || got != tc.want {
					t.Fatalf("already published receipt = %+v, error = %v; want %+v", got, err, tc.want)
				}
				if _, err := receiveRuntimeReply(t.Context(), done, reply); !errors.Is(err, errRuntimeUnavailable) {
					t.Fatalf("closed loop without a receipt = %v, want runtime unavailable", err)
				}
			}
		})
	}
}
