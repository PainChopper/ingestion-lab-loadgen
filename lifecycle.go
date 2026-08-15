package main

type runState string

const runStateIdle = "idle"
const runStateRunning = "running"
const runStatePaused = "paused"
const runStateResetting = "resetting"

type lifecycle struct {
	state runState
}

func newLifecycle() *lifecycle {
	return &lifecycle{state: runStateIdle}
}

func (lifecycle *lifecycle) currentState() runState {
	return lifecycle.state
}

func (lifecycle *lifecycle) run() bool {
	if lifecycle.state == runStateIdle || lifecycle.state == runStatePaused {
		lifecycle.state = runStateRunning
		return true
	}
	return false
}

func (lifecycle *lifecycle) pause() bool {
	if lifecycle.state == runStateRunning {
		lifecycle.state = runStatePaused
		return true
	}
	return false
}

func (lifecycle *lifecycle) reset() bool {
	if lifecycle.state == runStateRunning {
		lifecycle.state = runStateResetting
		return true
	}
	return false
}
