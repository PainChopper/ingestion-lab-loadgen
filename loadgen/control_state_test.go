package main

import (
	"slices"
	"testing"
)

func TestNewControlStateInitializesControls(t *testing.T) {
	for _, name := range []string{"nonzero initial values", "zero initial values"} {
		t.Run(name, func(t *testing.T) {
			loaded := testConfig(t)
			loaded.Reader.ReadBatchSize.Initial = 2000
			loaded.Reader.Workers.Initial = 3
			loaded.Sender.Workers.Initial = 7
			loaded.SenderChannel.Capacity.Initial = 4
			loaded.Throttler.InstallationMode.Initial = throttlerBypass
			if name == "zero initial values" {
				loaded.ReaderChannel.Capacity.Initial = 0
				loaded.SenderChannel.Capacity.Initial = 0
				loaded.Throttler.RequestedTPS.Initial = 0
			}
			state := newControlState(loaded, nil)
			got := []int{
				state.controls.readBatchSize, state.controls.readerWorkers,
				state.controls.readerChannelCapacity, state.controls.senderChannelCapacity,
				state.controls.requestedTPS, state.controls.senderWorkers,
			}
			want := []int{
				loaded.Reader.ReadBatchSize.Initial, loaded.Reader.Workers.Initial,
				loaded.ReaderChannel.Capacity.Initial, loaded.SenderChannel.Capacity.Initial,
				loaded.Throttler.RequestedTPS.Initial, loaded.Sender.Workers.Initial,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("initial controls = %v, want %v", got, want)
			}
			if got := state.controls.installationMode; got != loaded.Throttler.InstallationMode.Initial {
				t.Fatalf("initial mode = %q, want %q", got, loaded.Throttler.InstallationMode.Initial)
			}
		})
	}
}
