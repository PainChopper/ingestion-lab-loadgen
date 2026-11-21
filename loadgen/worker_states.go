package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"loadgen/internal/pipeline"
)

type readerWorkerStates []pipeline.ReaderWorkerState

func (states *readerWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("reader.workerStates: %w", err)
	}
	result := make(readerWorkerStates, 0, len(items))
	for index, item := range items {
		activity := pipeline.ReaderActivity(*item.Activity)
		switch activity {
		case pipeline.ReaderIdle, pipeline.ReaderReading, pipeline.ReaderBlocked:
		default:
			return fmt.Errorf("reader.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, pipeline.ReaderWorkerState{Activity: activity, Draining: *item.Draining})
	}
	*states = result
	return nil
}

type senderWorkerStates []pipeline.SenderWorkerState

func (states *senderWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("sender.workerStates: %w", err)
	}
	result := make(senderWorkerStates, 0, len(items))
	for index, item := range items {
		activity := pipeline.SenderActivity(*item.Activity)
		switch activity {
		case pipeline.SenderIdle, pipeline.SenderInFlight, pipeline.SenderBackoff:
		default:
			return fmt.Errorf("sender.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, pipeline.SenderWorkerState{Activity: activity, Draining: *item.Draining})
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

func countReaderWorkers(states []pipeline.ReaderWorkerState) readerWorkerCounts {
	counts := readerWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case pipeline.ReaderIdle:
			counts.IdleWorkers++
		case pipeline.ReaderReading:
			counts.ReadingWorkers++
		case pipeline.ReaderBlocked:
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

func countSenderWorkers(states []pipeline.SenderWorkerState) senderWorkerCounts {
	counts := senderWorkerCounts{LiveWorkers: len(states)}
	for _, state := range states {
		switch state.Activity {
		case pipeline.SenderIdle:
			counts.IdleWorkers++
		case pipeline.SenderInFlight:
			counts.InFlightWorkers++
		case pipeline.SenderBackoff:
			counts.BackoffWorkers++
		}
		if state.Draining {
			counts.DrainingWorkers++
		}
	}
	return counts
}
