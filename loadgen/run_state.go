package main

type runState string

const runStateIdle = "idle"
const runStateRunning = "running"
const runStatePaused = "paused"
const runStateFaulted = "faulted"
const runStateResetting = "resetting"

func (state *runState) run() bool {
	if *state == runStateIdle || *state == runStatePaused {
		*state = runStateRunning
		return true
	}
	return false
}

func (state *runState) pause() bool {
	if *state == runStateRunning {
		*state = runStatePaused
		return true
	}
	return false
}

func (state *runState) reset() bool {
	if *state == runStatePaused || *state == runStateFaulted {
		*state = runStateResetting
		return true
	}
	return false
}

func (state *runState) fault() bool {
	if *state == runStateRunning || *state == runStatePaused {
		*state = runStateFaulted
		return true
	}
	return false
}

func (state *runState) faultStart() bool {
	if *state == runStateIdle {
		*state = runStateFaulted
		return true
	}
	return false
}

func (state *runState) completeReset() bool {
	if *state == runStateResetting {
		*state = runStateIdle
		return true
	}
	return false
}
