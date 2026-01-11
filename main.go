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

type Mode int

const (
	Normal Mode = iota
	Burst
)

type generatorState struct {
	mode  Mode
	pulse time.Duration
}

func (g generatorState) tickLimit() int {
	if g.mode == Normal {
		return normalLimit
	}
	return burstLimit
}

func main() {
	gs := generatorState{
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
			batchCount += rand.Intn(gs.tickLimit())
		case cmd := <-commands:
			switch cmd.kind {
			case setMode:
				gs.mode = cmd.mode
			case setPulse:
				loadTicker.Stop()
				gs.pulse = cmd.pulse
				loadTicker = time.NewTicker(gs.pulse)
				loadEvents = loadTicker.C
			}
		case t := <-metrics:
			fmt.Printf("%d batches handled at %s\n", batchCount, t.Format("15:04:05"))
			batchCount = 0
		}
	}
}
