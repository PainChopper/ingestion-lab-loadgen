package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SnapshotPath is the runtime snapshot endpoint.
const SnapshotPath = "/api/loadgen/snapshot"

// ConfigPath is the startup configuration endpoint.
const ConfigPath = "/api/loadgen/config"

// CommandsPath is the live commands endpoint.
const CommandsPath = "/api/loadgen/commands"

// ConfigResponse holds the immutable startup settings exposed to API clients.
type ConfigResponse struct {
	// ReaderWorkersMax is the upper bound on configured Reader workers.
	ReaderWorkersMax int `json:"readerWorkersMax"`
	// SenderWorkersMax is the upper bound on configured Sender workers.
	SenderWorkersMax int `json:"senderWorkersMax"`
	// RequestedTpsMax is the upper bound on the target transaction rate.
	RequestedTpsMax int `json:"requestedTpsMax"`
	// ReadBatchSize is the configured batch size in transactions.
	ReadBatchSize int `json:"readBatchSize"`
	// ReaderChannelCapacity is the Reader queue capacity in batches.
	ReaderChannelCapacity int `json:"readerChannelCapacity"`
	// SenderChannelCapacity is the Sender queue capacity in batches.
	SenderChannelCapacity int `json:"senderChannelCapacity"`
	// SourcePath is the configured source path or glob.
	SourcePath string `json:"sourcePath"`
}

// CommandRequest is a request to apply a live setting.
type CommandRequest struct {
	// Action identifies the setting to update.
	Action string `json:"action"`
	// Value carries the requested value for that setting.
	Value json.RawMessage `json:"value"`
}

// DecodeSnapshot decodes a single snapshot and validates the run state.
func DecodeSnapshot(body io.Reader) (Snapshot, error) {
	snapshot, err := decodeStrictJSON[Snapshot](body, "snapshot")
	if err != nil {
		return Snapshot{}, err
	}
	switch snapshot.Run.State {
	case RunStateStarting, RunStateRunning, RunStateStopping, RunStateFaulted:
	default:
		return Snapshot{}, fmt.Errorf("decode snapshot: invalid run state %q", snapshot.Run.State)
	}
	return snapshot, nil
}

// DecodeConfig decodes a single startup configuration response.
func DecodeConfig(body io.Reader) (ConfigResponse, error) {
	return decodeStrictJSON[ConfigResponse](body, "config")
}

func decodeStrictJSON[T any](body io.Reader, name string) (T, error) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var value *T
	var zero T
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode %s: %w", name, err)
	}
	if value == nil {
		return zero, fmt.Errorf("decode %s: expected object", name)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return zero, fmt.Errorf("decode %s: trailing JSON", name)
	}
	return *value, nil
}

const (
	// SetReaderWorkers identifies a request to change the Reader worker count.
	SetReaderWorkers = "set-reader-workers"
	// SetSenderWorkers identifies a request to change the Sender worker count.
	SetSenderWorkers = "set-sender-workers"
	// SetRequestedTPS identifies a request to change the requested transaction rate.
	SetRequestedTPS = "set-requested-tps"
	// SetThrottlerInstalled identifies a request to enable or bypass pacing.
	SetThrottlerInstalled = "set-throttler-installed"
)

// DecodeCommand decodes a single command request, rejecting unknown fields and trailing JSON.
func DecodeCommand(body io.Reader) (CommandRequest, error) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var request CommandRequest
	if err := decoder.Decode(&request); err != nil {
		return CommandRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CommandRequest{}, errors.New("trailing JSON")
	}
	return request, nil
}
