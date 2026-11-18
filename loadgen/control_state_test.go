package main

import (
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewControlStateInitializesControls(t *testing.T) {
	for _, name := range []string{"nonzero initial values", "zero initial values"} {
		t.Run(name, func(t *testing.T) {
			loaded := testConfig(t)
			loaded.Reader.ReadBatchSize.Initial = 2000
			loaded.Reader.Workers.Initial = 3
			loaded.Sender.Workers.Initial = 7
			loaded.SenderChannel.Capacity.Initial = 4
			loaded.Throttler.Installed.Initial = false
			if name == "zero initial values" {
				loaded.ReaderChannel.Capacity.Initial = 0
				loaded.SenderChannel.Capacity.Initial = 0
				loaded.Throttler.RequestedTPS.Initial = 0
			}
			state := newControlState(loaded, zap.NewNop())
			if got := state.run.state; got != runStateStarting {
				t.Fatalf("initial run state = %q, want %q", got, runStateStarting)
			}
			snapshot := state.runtimeStatusAt(nil, time.Now())
			got := []int{
				snapshot.Reader.ReadBatchSize, snapshot.Reader.Workers,
				snapshot.ReaderChannel.Capacity, snapshot.SenderChannel.Capacity,
				snapshot.Throttler.RequestedTps, snapshot.Sender.Workers,
			}
			want := []int{
				loaded.Reader.ReadBatchSize.Initial, loaded.Reader.Workers.Initial,
				loaded.ReaderChannel.Capacity.Initial, loaded.SenderChannel.Capacity.Initial,
				loaded.Throttler.RequestedTPS.Initial, loaded.Sender.Workers.Initial,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("initial controls = %v, want %v", got, want)
			}
			if got := state.controls.installed; got != loaded.Throttler.Installed.Initial {
				t.Fatalf("initial installed = %t, want %t", got, loaded.Throttler.Installed.Initial)
			}
		})
	}
}
