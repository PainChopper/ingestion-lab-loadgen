package main

import "testing"

func TestRunFromIdle(t *testing.T) {
	state := runState(runStateIdle)
	if !state.run() {
		t.Error("run() from idle = false, want true")
	}
	if state != runStateRunning {
		t.Errorf("state after run() = %v, want %v", state, runStateRunning)
	}
}

func TestRunFromRunningIsNoop(t *testing.T) {
	state := runState(runStateRunning)
	if state.run() {
		t.Error("run() from running = true, want false")
	}
	if state != runStateRunning {
		t.Errorf("state after run() = %v, want %v", state, runStateRunning)
	}
}

func TestPauseFromRunning(t *testing.T) {
	state := runState(runStateRunning)
	if !state.pause() {
		t.Error("pause() from running = false, want true")
	}
	if state != runStatePaused {
		t.Errorf("state after pause() from running = %v, want %v", state, runStatePaused)
	}
}

func TestPauseFromPausedIsNoop(t *testing.T) {
	state := runState(runStatePaused)
	if state.pause() {
		t.Error("pause() from paused = true, want false")
	}
	if state != runStatePaused {
		t.Errorf("state after pause() from paused = %v, want %v", state, runStatePaused)
	}
}

func TestRunFromPaused(t *testing.T) {
	state := runState(runStatePaused)
	if !state.run() {
		t.Error("run() from paused = false, want true")
	}
	if state != runStateRunning {
		t.Errorf("state after run() from paused = %v, want %v", state, runStateRunning)
	}
}

func TestResetFromRunning(t *testing.T) {
	state := runState(runStateRunning)
	if state.reset() {
		t.Error("reset() from running = true, want false")
	}
	if state != runStateRunning {
		t.Errorf("state after reset() from running = %v, want %v", state, runStateRunning)
	}
}

func TestResetFromPaused(t *testing.T) {
	state := runState(runStatePaused)
	if !state.reset() {
		t.Error("reset() from paused = false, want true")
	}
	if state != runStateResetting {
		t.Errorf("state after reset() from paused = %v, want %v", state, runStateResetting)
	}
}

func TestResetFromIdleIsNoop(t *testing.T) {
	state := runState(runStateIdle)
	if state.reset() {
		t.Error("reset() from idle = true, want false")
	}
	if state != runStateIdle {
		t.Errorf("state after reset() from idle = %v, want %v", state, runStateIdle)
	}
}

func TestResetFromResettingIsNoop(t *testing.T) {
	state := runState(runStateResetting)
	if state.reset() {
		t.Error("reset() from resetting = true, want false")
	}
	if state != runStateResetting {
		t.Errorf("state after reset() from resetting = %v, want %v", state, runStateResetting)
	}
}

func TestCompleteResetFromResetting(t *testing.T) {
	state := runState(runStateResetting)
	if !state.completeReset() {
		t.Error("completeReset() from resetting = false, want true")
	}
	if state != runStateIdle {
		t.Errorf("state after completeReset() from resetting = %v, want %v", state, runStateIdle)
	}
}
