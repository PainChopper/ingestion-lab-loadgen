package main

import (
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

var readerActivityWire = [...]api.ReaderActivity{
	pipeline.ReaderIdle:    api.ReaderIdle,
	pipeline.ReaderReading: api.ReaderReading,
	pipeline.ReaderBlocked: api.ReaderBlocked,
}

func snapshotReaderWorkers(states []pipeline.ReaderWorkerState) []api.ReaderWorkerState {
	result := make([]api.ReaderWorkerState, 0, len(states))
	for _, state := range states {
		result = append(result, api.ReaderWorkerState{
			Activity: readerActivityWire[state.Activity], Draining: state.Draining,
		})
	}
	return result
}

var senderActivityWire = [...]api.SenderActivity{
	pipeline.SenderIdle:     api.SenderIdle,
	pipeline.SenderInFlight: api.SenderInFlight,
	pipeline.SenderBackoff:  api.SenderBackoff,
}

func snapshotSenderWorkers(states []pipeline.SenderWorkerState) []api.SenderWorkerState {
	result := make([]api.SenderWorkerState, 0, len(states))
	for _, state := range states {
		result = append(result, api.SenderWorkerState{
			Activity: senderActivityWire[state.Activity], Draining: state.Draining,
		})
	}
	return result
}

// Counts belong to presentation: the pipeline and wire retain individual states.
type readerWorkerCounts struct {
	LiveWorkers, IdleWorkers, ReadingWorkers, BlockedWorkers int
	DrainingWorkers                                          int
}

func countReaderWorkers(states []api.ReaderWorkerState) readerWorkerCounts {
	counts := readerWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case api.ReaderIdle:
			counts.IdleWorkers++
		case api.ReaderReading:
			counts.ReadingWorkers++
		case api.ReaderBlocked:
			counts.BlockedWorkers++
		}
		if state.Draining {
			counts.DrainingWorkers++
		}
	}
	return counts
}

type senderWorkerCounts struct {
	LiveWorkers, IdleWorkers, InFlightWorkers, BackoffWorkers int
	DrainingWorkers                                           int
}

func countSenderWorkers(states []api.SenderWorkerState) senderWorkerCounts {
	counts := senderWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case api.SenderIdle:
			counts.IdleWorkers++
		case api.SenderInFlight:
			counts.InFlightWorkers++
		case api.SenderBackoff:
			counts.BackoffWorkers++
		}
		if state.Draining {
			counts.DrainingWorkers++
		}
	}
	return counts
}
