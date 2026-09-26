package main

import "testing"

func TestControlPlaneDispatchForwardsCommand(t *testing.T) {
	plane := newControlPlane(10)
	if cap(plane.requests) != 10 {
		t.Fatalf("request capacity = %d, want 10", cap(plane.requests))
	}
	result := commandResult{status: commandConflict}
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := <-plane.requests
		if request.kind != cmdSetThrottlerInstallationMode || request.value != 42 || request.textValue != "bypass" {
			t.Errorf("request = %+v", request)
		}
		if cap(request.commandReply) != 1 {
			t.Errorf("command reply capacity = %d, want 1", cap(request.commandReply))
		}
		request.commandReply <- result
	}()

	if got := plane.dispatch(cmdSetThrottlerInstallationMode, 42, "bypass"); got != result {
		t.Errorf("result = %+v, want %+v", got, result)
	}
	<-done
}

func TestControlPlaneSnapshotForwardsReply(t *testing.T) {
	plane := newControlPlane(10)
	want := statusSnapshot{Run: runSnapshot{State: runStatePaused}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := <-plane.requests
		if request.kind != getSnapshot {
			t.Errorf("request kind = %d, want %d", request.kind, getSnapshot)
		}
		if cap(request.snapshotReply) != 1 {
			t.Errorf("snapshot reply capacity = %d, want 1", cap(request.snapshotReply))
		}
		request.snapshotReply <- want
	}()

	if got := plane.snapshot(); got.Run != want.Run {
		t.Errorf("run snapshot = %+v, want %+v", got.Run, want.Run)
	}
	<-done
}

func TestControlPlaneDispatchAsyncForwardsCommand(t *testing.T) {
	plane := newControlPlane(10)
	plane.dispatchAsync(cmdPause)
	request := <-plane.requests
	if request.kind != cmdPause || request.commandReply != nil {
		t.Errorf("request = %+v", request)
	}
}

func testControlPlane(requests chan request) controlPlane {
	return controlPlane{requests: requests}
}
