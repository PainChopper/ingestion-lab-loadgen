package main

import "testing"

func TestRuntimeControlExecuteForwardsCommandAndReceipt(t *testing.T) {
	control := newControlPlane(10)
	if cap(control.requests) != 10 {
		t.Fatalf("request capacity = %d, want 10", cap(control.requests))
	}
	want := runtimeCommandReceipt{status: commandConflict}
	done := make(chan struct{})

	go func() {
		defer close(done)
		command := <-control.requests
		if command.kind != cmdSetThrottlerInstallationMode || command.value != 42 || command.textValue != "bypass" {
			t.Errorf("command = %+v", command)
		}
		if cap(command.receiptReply) != 1 {
			t.Errorf("receipt reply capacity = %d, want 1", cap(command.receiptReply))
		}
		command.receiptReply <- want
	}()

	command := runtimeCommand{kind: cmdSetThrottlerInstallationMode, value: 42, textValue: "bypass"}
	if got := control.execute(command); got != want {
		t.Errorf("receipt = %+v, want %+v", got, want)
	}
	<-done
}

func TestRuntimeControlStatusForwardsReply(t *testing.T) {
	control := newControlPlane(10)
	want := runtimeStatus{Run: runtimeRunStatus{State: runStatePaused}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		command := <-control.requests
		if command.kind != getRuntimeStatus {
			t.Errorf("command kind = %d, want %d", command.kind, getRuntimeStatus)
		}
		if cap(command.statusReply) != 1 {
			t.Errorf("status reply capacity = %d, want 1", cap(command.statusReply))
		}
		command.statusReply <- want
	}()

	if got := control.status(); got.Run != want.Run {
		t.Errorf("run status = %+v, want %+v", got.Run, want.Run)
	}
	<-done
}

func TestRuntimeControlExecuteAsyncForwardsCommandWithoutReceipt(t *testing.T) {
	control := newControlPlane(10)
	control.executeAsync(runtimeCommand{kind: cmdPause})
	command := <-control.requests
	if command.kind != cmdPause || command.receiptReply != nil {
		t.Errorf("command = %+v", command)
	}
}

func testControlPlane(requests chan runtimeCommand) controlPlane {
	return controlPlane{requests: requests}
}
