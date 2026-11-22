// Package api defines the wire contract shared by the LoadGen HTTP server and CLI.
package api

// Snapshot is a point-in-time view of the LoadGen run and its pipeline.
type Snapshot struct {
	// CapturedAtMs is the time since the controller started, in milliseconds.
	CapturedAtMs int64 `json:"capturedAtMs"`
	// Run holds overall progress.
	Run RunSnapshot `json:"run"`
	// Reader holds Reader pool activity.
	Reader ReaderSnapshot `json:"reader"`
	// Throttler holds the applied pacing settings.
	Throttler ThrottlerSnapshot `json:"throttler"`
	// Sender holds Sender pool activity.
	Sender SenderSnapshot `json:"sender"`
	// ReaderChannel holds telemetry for the queue after the Reader.
	ReaderChannel ChannelSnapshot `json:"readerChannel"`
	// SenderChannel holds telemetry for the queue before the Sender.
	SenderChannel ChannelSnapshot `json:"senderChannel"`
}

// RunSnapshot reports run state and delivery progress.
type RunSnapshot struct {
	// State is the current run phase.
	State RunState `json:"state"`
	// TotalTransactions counts transactions confirmed by the Receiver.
	TotalTransactions int64 `json:"totalTransactions"`
	// ElapsedMs is the time since the pipeline started, in milliseconds.
	ElapsedMs int64 `json:"elapsedMs"`
}

// ReaderSnapshot reports Reader activity and source errors.
type ReaderSnapshot struct {
	// Workers is the configured worker count.
	Workers int `json:"workers"`
	// WorkerStates lists live states, including draining workers.
	WorkerStates ReaderWorkerStates `json:"workerStates"`
	// RowsRead is the number of rows read from the source.
	RowsRead int64 `json:"rowsRead"`
	// SourceError is the source failure, when one occurred.
	SourceError *SourceError `json:"sourceError"`
}

// SourceError describes a Parquet source failure.
type SourceError struct {
	// Operation is the source operation that failed.
	Operation string `json:"operation"`
	// Path is the source path associated with the failure.
	Path string `json:"path"`
	// Message describes the source failure.
	Message string `json:"message"`
}

// ThrottlerSnapshot reports the live pacing settings.
type ThrottlerSnapshot struct {
	// RequestedTps is the configured target transactions per second.
	RequestedTps int `json:"requestedTps"`
	// Installed reports whether pacing is enabled.
	Installed bool `json:"installed"`
}

// SenderSnapshot reports Sender activity.
type SenderSnapshot struct {
	// Workers is the configured worker count.
	Workers int `json:"workers"`
	// WorkerStates lists live states, including draining workers.
	WorkerStates SenderWorkerStates `json:"workerStates"`
}

// ChannelSnapshot reports queue occupancy, blocked time and transaction counters.
type ChannelSnapshot struct {
	// DepthBatches is the number of queued batches.
	DepthBatches int `json:"depthBatches"`
	// BlockedSenders counts producers blocked while adding a batch.
	BlockedSenders int `json:"blockedSenders"`
	// OldestBlockedSenderMs is the age of the oldest blocked producer, in milliseconds.
	OldestBlockedSenderMs int64 `json:"oldestBlockedSenderMs"`
	// BlockedMs is the cumulative time producers spent blocked, in milliseconds.
	BlockedMs int64 `json:"blockedMs"`
	// InputTransactionsTotal counts transactions queued.
	InputTransactionsTotal int64 `json:"inputTransactionsTotal"`
	// OutputTransactionsTotal counts transactions dequeued.
	OutputTransactionsTotal int64 `json:"outputTransactionsTotal"`
}

// RunState is the current phase of the run.
type RunState string

const (
	// RunStateStarting indicates that source discovery is in progress.
	RunStateStarting RunState = "starting"
	// RunStateRunning indicates that the pipeline is active.
	RunStateRunning RunState = "running"
	// RunStateStopping indicates that shutdown is draining the pipeline.
	RunStateStopping RunState = "stopping"
	// RunStateFaulted indicates that a failure stopped the run.
	RunStateFaulted RunState = "faulted"
)
