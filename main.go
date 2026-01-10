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
	windowTicker := time.NewTicker(windowLength)
	defer windowTicker.Stop()

	events := loadTicker.C
	windows := windowTicker.C
	commands := make(chan command)

	var batchCount int

	for {
		select {
		case <-events:
			batchCount += rand.Intn(gs.limit())
		case cmd := <-commands:
			if cmd.mode != nil {
				gs.mode = *cmd.mode
			}
			if cmd.pulse != nil && *cmd.pulse != gs.pulse {
				loadTicker.Stop()
				gs.pulse = *cmd.pulse
				loadTicker = time.NewTicker(gs.pulse)
				events = loadTicker.C
			}
		case t := <-windows:
			fmt.Printf("%d batches handled at %s\n", batchCount, t.Format("15:04:05"))
			batchCount = 0
		}
	}
}
