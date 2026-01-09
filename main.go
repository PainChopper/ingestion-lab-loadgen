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

type genState struct {
	mode  Mode
	pulse time.Duration
}

func (g genState) limit() int {
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

type Params struct {
	mode Mode
}

func main() {

	gs := genState{
		mode:  Normal,
		pulse: pulse,
	}

	loadTicker := time.NewTicker(gs.pulse)
	defer loadTicker.Stop()
	windowTicker := time.NewTicker(windowLength)
	defer windowTicker.Stop()

	events := loadTicker.C
	windows := windowTicker.C

	control := make(chan Params)

	var batchCount int

	for {
		select {
		case <-events:
			batchCount += rand.Intn(gs.limit())
		case c := <-control:
			gs.mode = c.mode
		case t := <-windows:
			fmt.Printf("%d batches handled at %s\n", batchCount, t.Format("15:04:05"))
			batchCount = 0
		}
	}
}
