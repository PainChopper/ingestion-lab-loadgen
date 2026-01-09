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

	burstMode := time.After(3 * time.Second)
	reducePulse := time.After(6 * time.Second)

	var batchCount int

	for {
		select {
		case <-events:
			batchCount += rand.Intn(gs.limit())
		case <-burstMode:
			gs.mode = Burst
		case <-reducePulse:
			loadTicker.Stop()
			gs.pulse = 500 * time.Millisecond
			loadTicker = time.NewTicker(gs.pulse)
			events = loadTicker.C
		case t := <-windows:
			fmt.Printf("%d batches handled at %s\n", batchCount, t.Format("15:04:05"))
			batchCount = 0
		}
	}
}
