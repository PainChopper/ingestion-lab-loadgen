package main

import "time"

type cmdType int

const (
	setMode cmdType = iota
	setPulse
)

type command struct {
	kind  cmdType
	mode  mode
	pulse time.Duration
}

func startCommandDriver(out chan command) {
	time.Sleep(3 * time.Second)
	out <- command{kind: setMode, mode: burst}
	time.Sleep(3 * time.Second)
	pulse := 500 * time.Millisecond
	out <- command{kind: setPulse, pulse: pulse}
}
