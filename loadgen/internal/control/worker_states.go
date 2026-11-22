package control

import (
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"
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
