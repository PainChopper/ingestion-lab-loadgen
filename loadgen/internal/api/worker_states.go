package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ReaderActivity is the current activity of a Reader worker.
type ReaderActivity string

const (
	// ReaderIdle indicates that a Reader is idle.
	ReaderIdle ReaderActivity = "idle"
	// ReaderReading indicates that a Reader is reading its source.
	ReaderReading ReaderActivity = "reading"
	// ReaderBlocked indicates that a Reader is blocked on output.
	ReaderBlocked ReaderActivity = "blocked"
)

// ReaderWorkerState describes a live Reader worker.
type ReaderWorkerState struct {
	// Activity is the worker's current activity.
	Activity ReaderActivity `json:"activity"`
	// Draining reports whether the worker is retiring.
	Draining bool `json:"draining"`
}

// ReaderWorkerStates is a list of Reader worker states.
type ReaderWorkerStates []ReaderWorkerState

// UnmarshalJSON validates Reader worker states.
func (states *ReaderWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("reader.workerStates: %w", err)
	}
	result := make(ReaderWorkerStates, 0, len(items))
	for index, item := range items {
		activity := ReaderActivity(*item.Activity)
		switch activity {
		case ReaderIdle, ReaderReading, ReaderBlocked:
		default:
			return fmt.Errorf("reader.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, ReaderWorkerState{Activity: activity, Draining: *item.Draining})
	}
	*states = result
	return nil
}

// SenderActivity is the current activity of a Sender worker.
type SenderActivity string

const (
	// SenderIdle indicates that a Sender is idle.
	SenderIdle SenderActivity = "idle"
	// SenderInFlight indicates that a Sender is attempting delivery.
	SenderInFlight SenderActivity = "inFlight"
	// SenderBackoff indicates that a Sender is waiting to retry.
	SenderBackoff SenderActivity = "backoff"
)

// SenderWorkerState describes a live Sender worker.
type SenderWorkerState struct {
	// Activity is the worker's current activity.
	Activity SenderActivity `json:"activity"`
	// Draining reports whether the worker is retiring.
	Draining bool `json:"draining"`
}

// SenderWorkerStates is a list of Sender worker states.
type SenderWorkerStates []SenderWorkerState

// UnmarshalJSON validates Sender worker states.
func (states *SenderWorkerStates) UnmarshalJSON(data []byte) error {
	items, err := decodeWorkerStateItems(data)
	if err != nil {
		return fmt.Errorf("sender.workerStates: %w", err)
	}
	result := make(SenderWorkerStates, 0, len(items))
	for index, item := range items {
		activity := SenderActivity(*item.Activity)
		switch activity {
		case SenderIdle, SenderInFlight, SenderBackoff:
		default:
			return fmt.Errorf("sender.workerStates[%d]: invalid activity %q", index, activity)
		}
		result = append(result, SenderWorkerState{Activity: activity, Draining: *item.Draining})
	}
	*states = result
	return nil
}

type workerStateItem struct {
	// Activity is the worker's current activity.
	Activity *string `json:"activity"`
	// Draining reports whether the worker is retiring.
	Draining *bool `json:"draining"`
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
