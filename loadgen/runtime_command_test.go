package main

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestExecuteRuntimeCommandForwardsCommandAndReceipt(t *testing.T) {
	control := make(chan runtimeCommand, 10)
	want := runtimeCommandReceipt{rejected: true}
	done := make(chan struct{})

	go func() {
		defer close(done)
		command := <-control
		if command.kind != cmdSetThrottlerInstalled || command.value != 42 || command.installed != false {
			t.Errorf("command = %+v", command)
		}
		if cap(command.receiptReply) != 1 {
			t.Errorf("receipt reply capacity = %d, want 1", cap(command.receiptReply))
		}
		command.receiptReply <- want
	}()

	command := runtimeCommand{kind: cmdSetThrottlerInstalled, value: 42, installed: false}
	if got, err := executeRuntimeCommand(t.Context(), runtimeRequests{commands: control}, command); err != nil || got != want {
		t.Errorf("receipt = %+v, err=%v, want %+v", got, err, want)
	}
	<-done
}

func TestRequestRuntimeStatusForwardsReply(t *testing.T) {
	control := make(chan runtimeCommand, 10)
	want := runtimeStatus{Run: runtimeRunStatus{State: runStateStopping}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		command := <-control
		if command.kind != getRuntimeStatus {
			t.Errorf("command kind = %d, want %d", command.kind, getRuntimeStatus)
		}
		if cap(command.statusReply) != 1 {
			t.Errorf("status reply capacity = %d, want 1", cap(command.statusReply))
		}
		command.statusReply <- want
	}()

	if got, err := requestRuntimeStatus(t.Context(), runtimeRequests{commands: control}); err != nil || got.Run != want.Run {
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
					control := make(chan runtimeCommand)
					done := make(chan struct{})
					requests := runtimeRequests{commands: control, done: done}
					results := make(chan error, 2)
					go func() {
						_, err := requestRuntimeStatus(ctx, requests)
						results <- err
					}()
					go func() {
						_, err := executeRuntimeCommand(ctx, requests, runtimeCommand{kind: cmdSetReaderWorkers, value: 2})
						results <- err
					}()
					var pending []runtimeCommand
					if stage == "reply" {
						pending = []runtimeCommand{<-control, <-control}
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
					for _, command := range pending {
						if command.kind == getRuntimeStatus {
							command.respondStatus(runtimeStatus{})
						} else {
							command.respond(runtimeCommandReceipt{})
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
		want runtimeCommandReceipt
	}{
		{name: "applied"},
		{name: "conflict", want: runtimeCommandReceipt{rejected: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 64 {
				done := make(chan struct{})
				reply := make(chan runtimeCommandReceipt, 1)
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
