package main

import "testing"

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	starts := 0
	startPipeline := func() {
		starts++
	}

	state := controlState{lifecycle: newLifecycle()}
	state.handleRunCommand(startPipeline)
	state.handleRunCommand(startPipeline)

	if starts != 1 {
		t.Errorf("starts = %v, want %v", starts, 1)
	}
}

func TestEventLoopStartsIdleWithoutPipeline(t *testing.T) {
	starts := 0
	startPipeline := func() {
		starts++
	}
	snapshots := make(chan<- statusSnapshot)


}
