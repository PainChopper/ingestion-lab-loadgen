package main

import "github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"

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
