package main

import (
	"fmt"
	"math/rand"
	"time"
)

const (
	windowLength = 1 * time.Second
	pulse        = 100 * time.Millisecond

	normalLimit = 5
	burstLimit  = 500
)

type command struct {
	mode  *Mode
	pulse *time.Duration
}

type state struct {
	mode  Mode
	pulse time.Duration
}

func (g state) limit() int {
	if g.mode == Normal {
		return normalLimit
	}
	return burstLimit
}

type Mode int

const (
	Normal Mode = iota
	Burst
)

func main() {

	gs := state{
		mode:  Normal,
		pulse: pulse,
	}

	loadTicker := time.NewTicker(gs.pulse)
	defer loadTicker.Stop()
	metricsTicker := time.NewTicker(windowLength)
	defer metricsTicker.Stop()

	loadEvents := loadTicker.C
	metrics := metricsTicker.C
	commands := make(chan command)
	go startCommandDriver(commands)

	var batchCount int

	for {
		select {
		case <-loadEvents:
			batchCount += rand.Intn(gs.limit())
		case cmd := <-commands:
			if cmd.mode != nil {
				gs.mode = *cmd.mode
			}
			if cmd.pulse != nil && *cmd.pulse != gs.pulse {
				loadTicker.Stop()
				gs.pulse = *cmd.pulse
				loadTicker = time.NewTicker(gs.pulse)
				loadEvents = loadTicker.C
			}
		case t := <-metrics:
			fmt.Printf("%d batches handled at %s\n", batchCount, t.Format("15:04:05"))
			batchCount = 0
		}
	}
}
