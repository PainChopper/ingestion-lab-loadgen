package cli

import "github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"

// Counts belong to presentation: the pipeline and wire retain individual states.
type readerWorkerCounts struct {
	liveWorkers, idleWorkers, readingWorkers, blockedWorkers int
	drainingWorkers                                          int
}

func countReaderWorkers(states []api.ReaderWorkerState) readerWorkerCounts {
	counts := readerWorkerCounts{liveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case api.ReaderIdle:
			counts.idleWorkers++
		case api.ReaderReading:
			counts.readingWorkers++
		case api.ReaderBlocked:
			counts.blockedWorkers++
		}
		if state.Draining {
			counts.drainingWorkers++
		}
	}
	return counts
}

type senderWorkerCounts struct {
	liveWorkers, idleWorkers, inFlightWorkers, backoffWorkers int
	drainingWorkers                                           int
}

func countSenderWorkers(states []api.SenderWorkerState) senderWorkerCounts {
	counts := senderWorkerCounts{liveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case api.SenderIdle:
			counts.idleWorkers++
		case api.SenderInFlight:
			counts.inFlightWorkers++
		case api.SenderBackoff:
			counts.backoffWorkers++
		}
		if state.Draining {
			counts.drainingWorkers++
		}
	}
	return counts
}
