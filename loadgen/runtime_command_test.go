package main

import "testing"

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
	if got := executeRuntimeCommand(control, command); got != want {
		t.Errorf("receipt = %+v, want %+v", got, want)
	}
	<-done
}

func TestRequestRuntimeStatusForwardsReply(t *testing.T) {
	control := make(chan runtimeCommand, 10)
	want := runtimeStatus{Run: runtimeRunStatus{State: runStatePaused}}
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

	if got := requestRuntimeStatus(control); got.Run != want.Run {
		t.Errorf("run status = %+v, want %+v", got.Run, want.Run)
	}
	<-done
}
