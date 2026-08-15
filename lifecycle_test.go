package main

import "testing"

func newRunningLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	lifecycle := newLifecycle()
	if !lifecycle.run() {
		t.Fatal("setup: run() from idle = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateRunning {
		t.Fatalf("setup: state after run() from idle = %v, want %v", state, runStateRunning)
	}
	return lifecycle
}

func newPausedLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	lifecycle := newRunningLifecycle(t)
	if !lifecycle.pause() {
		t.Fatal("setup: pause() from running = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStatePaused {
		t.Fatalf("setup: state after pause() from running = %v, want %v", state, runStatePaused)
	}
	return lifecycle
}

func newResettingLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	lifecycle := newRunningLifecycle(t)
	if !lifecycle.reset() {
		t.Fatal("setup: reset() from running = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateResetting {
		t.Fatalf("setup: state after reset() from running = %v, want %v", state, runStateResetting)
	}
	return lifecycle
}

func TestLifecycleInitialState(t *testing.T) {
	lifecycle := newLifecycle()
	state := lifecycle.currentState()
	if state != runStateIdle {
		t.Errorf("initial state = %v, want %v", state, runStateIdle)
	}
}

func TestRunFromIdle(t *testing.T) {
	lifecycle := newLifecycle()
	if !lifecycle.run() {
		t.Error("run() from idle = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateRunning {
		t.Errorf("state after run() = %v, want %v", state, runStateRunning)
	}
}

func TestRunFromRunningIsNoop(t *testing.T) {
	lifecycle := newRunningLifecycle(t)
	if lifecycle.run() {
		t.Error("run() from running = true, want false")
	}
	state := lifecycle.currentState()
	if state != runStateRunning {
		t.Errorf("state after run() = %v, want %v", state, runStateRunning)
	}
}

func TestPauseFromRunning(t *testing.T) {
	lifecycle := newRunningLifecycle(t)
	if !lifecycle.pause() {
		t.Error("pause() from running = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStatePaused {
		t.Errorf("state after pause() from running = %v, want %v", state, runStatePaused)
	}
}

func TestPauseFromPausedIsNoop(t *testing.T) {
	lifecycle := newPausedLifecycle(t)
	if lifecycle.pause() {
		t.Error("pause() from paused = true, want false")
	}
	state := lifecycle.currentState()
	if state != runStatePaused {
		t.Errorf("state after pause() from paused = %v, want %v", state, runStatePaused)
	}
}

func TestRunFromPaused(t *testing.T) {
	lifecycle := newPausedLifecycle(t)
	if !lifecycle.run() {
		t.Error("run() from paused = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateRunning {
		t.Errorf("state after run() from paused = %v, want %v", state, runStateRunning)
	}
}

func TestResetFromRunning(t *testing.T) {
	lifecycle := newRunningLifecycle(t)
	if !lifecycle.reset() {
		t.Error("reset() from running = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateResetting {
		t.Errorf("state after reset() from running = %v, want %v", state, runStateResetting)
	}
}

func TestResetFromPaused(t *testing.T) {
	lifecycle := newPausedLifecycle(t)
	if !lifecycle.reset() {
		t.Error("reset() from paused = false, want true")
	}
	state := lifecycle.currentState()
	if state != runStateResetting {
		t.Errorf("state after reset() from paused = %v, want %v", state, runStateResetting)
	}
}

func TestResetFromIdleIsNoop(t *testing.T) {
	lifecycle := newLifecycle()
	if lifecycle.reset() {
		t.Error("reset() from idle = true, want false")
	}
	state := lifecycle.currentState()
	if state != runStateIdle {
		t.Errorf("state after reset() from idle = %v, want %v", state, runStateIdle)
	}
}

func TestResetFromResettingIsNoop(t *testing.T) {
	lifecycle := newResettingLifecycle(t)
	if lifecycle.reset() {
		t.Error("reset() from resetting = true, want false")
	}
	state := lifecycle.currentState()
	if state != runStateResetting {
		t.Errorf("state after reset() from resetting = %v, want %v", state, runStateResetting)
	}
}
