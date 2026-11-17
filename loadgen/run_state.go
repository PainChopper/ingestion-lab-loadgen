package main

type runState string

const (
	runStateStarting runState = "starting"
	runStateRunning  runState = "running"
	runStateStopping runState = "stopping"
	runStateFaulted  runState = "faulted"
)
