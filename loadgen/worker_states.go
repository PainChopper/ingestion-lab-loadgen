package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"
)

type readerWireActivity string

const (
	readerWireIdle    readerWireActivity = "idle"
	readerWireReading readerWireActivity = "reading"
	readerWireBlocked readerWireActivity = "blocked"
)

type readerWorkerState struct {
	Activity readerWireActivity `json:"activity"`
	Draining bool               `json:"draining"`
}

var readerActivityWire = [...]readerWireActivity{
	pipeline.ReaderIdle:    readerWireIdle,
	pipeline.ReaderReading: readerWireReading,
	pipeline.ReaderBlocked: readerWireBlocked,
}

func snapshotReaderWorkers(states []pipeline.ReaderWorkerState) readerWorkerStates {
	result := make(readerWorkerStates, 0, len(states))
	for _, state := range states {
		result = append(result, readerWorkerState{
			Activity: readerActivityWire[state.Activity], Draining: state.Draining,
		})
	}
	return result
}

type readerWorkerStates []readerWorkerState

func (states *readerWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("reader.workerStates: %w", err)
	}
	result := make(readerWorkerStates, 0, len(items))
	for index, item := range items {
		activity := readerWireActivity(*item.Activity)
		if !slices.Contains(readerActivityWire[:], activity) {
			return fmt.Errorf("reader.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, readerWorkerState{Activity: activity, Draining: *item.Draining})
	}
	*states = result
	return nil
}

type senderWireActivity string

const (
	senderWireIdle     senderWireActivity = "idle"
	senderWireInFlight senderWireActivity = "inFlight"
	senderWireBackoff  senderWireActivity = "backoff"
)

type senderWorkerState struct {
	Activity senderWireActivity `json:"activity"`
	Draining bool               `json:"draining"`
}

var senderActivityWire = [...]senderWireActivity{
	pipeline.SenderIdle:     senderWireIdle,
	pipeline.SenderInFlight: senderWireInFlight,
	pipeline.SenderBackoff:  senderWireBackoff,
}

func snapshotSenderWorkers(states []pipeline.SenderWorkerState) senderWorkerStates {
	result := make(senderWorkerStates, 0, len(states))
	for _, state := range states {
		result = append(result, senderWorkerState{
			Activity: senderActivityWire[state.Activity], Draining: state.Draining,
		})
	}
	return result
}

type senderWorkerStates []senderWorkerState

func (states *senderWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("sender.workerStates: %w", err)
	}
	result := make(senderWorkerStates, 0, len(items))
	for index, item := range items {
		activity := senderWireActivity(*item.Activity)
		if !slices.Contains(senderActivityWire[:], activity) {
			return fmt.Errorf("sender.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, senderWorkerState{Activity: activity, Draining: *item.Draining})
	}
	*states = result
	return nil
}

type workerStateItem struct {
	Activity *string `json:"activity"`
	Draining *bool   `json:"draining"`
}

func decodeWorkerStateItems(data []byte) ([]workerStateItem, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var items []workerStateItem
	if err := decoder.Decode(&items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, errors.New("must be an array")
	}
	for index, item := range items {
		if item.Activity == nil || item.Draining == nil {
			return nil, fmt.Errorf("item %d requires activity and draining", index)
		}
	}
	return items, nil
}

// Counts belong to presentation: the pipeline and wire retain individual states.
type readerWorkerCounts struct {
	LiveWorkers, IdleWorkers, ReadingWorkers, BlockedWorkers int
	DrainingWorkers                                          int
}

func countReaderWorkers(states []readerWorkerState) readerWorkerCounts {
	counts := readerWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case readerWireIdle:
			counts.IdleWorkers++
		case readerWireReading:
			counts.ReadingWorkers++
		case readerWireBlocked:
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

func countSenderWorkers(states []senderWorkerState) senderWorkerCounts {
	counts := senderWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case senderWireIdle:
			counts.IdleWorkers++
		case senderWireInFlight:
			counts.InFlightWorkers++
		case senderWireBackoff:
			counts.BackoffWorkers++
		}
		if state.Draining {
			counts.DrainingWorkers++
		}
	}
	return counts
}
