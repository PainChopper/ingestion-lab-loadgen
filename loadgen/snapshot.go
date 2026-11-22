package main

import (
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"go.uber.org/zap"
)

type runtimeSnapshot struct {
	CapturedAtMs  int64             `json:"capturedAtMs"`
	Run           runSnapshot       `json:"run"`
	Reader        readerSnapshot    `json:"reader"`
	Throttler     throttlerSnapshot `json:"throttler"`
	Sender        senderSnapshot    `json:"sender"`
	ReaderChannel channelSnapshot   `json:"readerChannel"`
	SenderChannel channelSnapshot   `json:"senderChannel"`
}

type runSnapshot struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type readerSnapshot struct {
	Workers      int                `json:"workers"`
	WorkerStates readerWorkerStates `json:"workerStates"`
	RowsRead     int64              `json:"rowsRead"`
	SourceError  *readerSourceError `json:"sourceError"`
}

type readerSourceError struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Message   string `json:"message"`
}

type throttlerSnapshot struct {
	RequestedTps int  `json:"requestedTps"`
	Installed    bool `json:"installed"`
}

type senderSnapshot struct {
	Workers      int                `json:"workers"`
	WorkerStates senderWorkerStates `json:"workerStates"`
}

type channelSnapshot struct {
	DepthBatches            int   `json:"depthBatches"`
	BlockedSenders          int   `json:"blockedSenders"`
	OldestBlockedSenderMs   int64 `json:"oldestBlockedSenderMs"`
	BlockedMs               int64 `json:"blockedMs"`
	InputTransactionsTotal  int64 `json:"inputTransactionsTotal"`
	OutputTransactionsTotal int64 `json:"outputTransactionsTotal"`
}

func snapshotChannel(measurements pipeline.QueueSnapshot) channelSnapshot {
	return channelSnapshot{
		DepthBatches:            measurements.DepthBatches,
		BlockedSenders:          measurements.BlockedSenders,
		OldestBlockedSenderMs:   measurements.OldestBlockedSenderMS,
		BlockedMs:               measurements.BlockedMS,
		InputTransactionsTotal:  measurements.SentTransactionsTotal,
		OutputTransactionsTotal: measurements.ReceivedTransactionsTotal,
	}
}

const snapshotLogInterval = time.Minute

func snapshotLogFields(snapshot runtimeSnapshot, config config) []zap.Field {
	sourceErrorOperation := ""
	sourceErrorPath := ""
	if snapshot.Reader.SourceError != nil {
		sourceErrorOperation = snapshot.Reader.SourceError.Operation
		sourceErrorPath = snapshot.Reader.SourceError.Path
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.Int64("captured_at_ms", snapshot.CapturedAtMs),
		zap.String("state", string(snapshot.Run.State)),
		zap.Int64("elapsed_ms", snapshot.Run.ElapsedMs),
		zap.Int64("total_transactions", snapshot.Run.TotalTransactions),
		zap.Int64("reader_rows_read", snapshot.Reader.RowsRead),
		zap.Int("reader_workers", snapshot.Reader.Workers),
		zap.Int("reader_live_workers", len(snapshot.Reader.WorkerStates)),
		zap.Int("throttler_requested_tps", snapshot.Throttler.RequestedTps),
		zap.Bool("installed", snapshot.Throttler.Installed),
		zap.Int("reader_channel_capacity", config.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", snapshot.ReaderChannel.DepthBatches),
		zap.Int64("reader_channel_input_transactions_total", snapshot.ReaderChannel.InputTransactionsTotal),
		zap.Int64("reader_channel_output_transactions_total", snapshot.ReaderChannel.OutputTransactionsTotal),
		zap.Int("reader_channel_blocked_senders", snapshot.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", snapshot.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", snapshot.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", config.SenderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", snapshot.SenderChannel.DepthBatches),
		zap.Int64("sender_channel_input_transactions_total", snapshot.SenderChannel.InputTransactionsTotal),
		zap.Int64("sender_channel_output_transactions_total", snapshot.SenderChannel.OutputTransactionsTotal),
		zap.Int("sender_channel_blocked_senders", snapshot.SenderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", snapshot.SenderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", snapshot.SenderChannel.BlockedMs),
		zap.Int("sender_workers", snapshot.Sender.Workers),
		zap.Int("sender_live_workers", len(snapshot.Sender.WorkerStates)),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_path", sourceErrorPath),
	}
}
