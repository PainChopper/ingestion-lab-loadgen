package main

import (
	"time"

	"loadgen/internal/pipeline"

	"go.uber.org/zap"
)

type runtimeSnapshot struct {
	Run           runSnapshot       `json:"run"`
	Reader        readerSnapshot    `json:"reader"`
	Throttler     throttlerSnapshot `json:"throttler"`
	Sender        senderSnapshot    `json:"sender"`
	ReaderChannel channelSnapshot   `json:"readerChannel"`
	SenderChannel channelSnapshot   `json:"senderChannel"`
	Config        configSnapshot    `json:"config"`
}

type runSnapshot struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type readerSnapshot struct {
	Workers         int                `json:"workers"`
	WorkerStates    readerWorkerStates `json:"workerStates"`
	ReadBatchSize   int                `json:"readBatchSize"`
	ReadTps         float64            `json:"readTps"`
	RowsRead        int64              `json:"rowsRead"`
	SourceDirectory string             `json:"sourceDirectory"`
	SourceError     *readerSourceError `json:"sourceError"`
}

type readerSourceError struct {
	Category     string `json:"category"`
	Operation    string `json:"operation"`
	RelativePath string `json:"relativePath"`
	Message      string `json:"message"`
}

type throttlerSnapshot struct {
	RequestedTps int     `json:"requestedTps"`
	AdmittedTps  float64 `json:"admittedTps"`
	Installed    bool    `json:"installed"`
}

type senderSnapshot struct {
	Workers      int                `json:"workers"`
	WorkerStates senderWorkerStates `json:"workerStates"`
}

type channelSnapshot struct {
	Capacity                      int     `json:"capacity"`
	DepthBatches                  int     `json:"depthBatches"`
	BufferedTransactions          int     `json:"bufferedTransactions"`
	BlockedSenders                int     `json:"blockedSenders"`
	OldestBlockedSenderMs         int64   `json:"oldestBlockedSenderMs"`
	BlockedMs                     int64   `json:"blockedMs"`
	SentTransactionsPerSecond     float64 `json:"inputTransactionsPerSecond"`
	ReceivedTransactionsPerSecond float64 `json:"outputTransactionsPerSecond"`
}

type configSnapshot struct {
	ReaderWorkers         workersConfig `json:"readerWorkers"`
	SenderWorkers         workersConfig `json:"senderWorkers"`
	ThrottlerRequestedTPS tpsConfig     `json:"throttlerRequestedTps"`
}

func snapshotChannel(measurements pipeline.QueueSnapshot, capacity int) channelSnapshot {
	return channelSnapshot{
		Capacity:                      capacity,
		DepthBatches:                  measurements.DepthBatches,
		BufferedTransactions:          measurements.BufferedTransactions,
		BlockedSenders:                measurements.BlockedSenders,
		OldestBlockedSenderMs:         measurements.OldestBlockedSenderMS,
		BlockedMs:                     measurements.BlockedMS,
		SentTransactionsPerSecond:     measurements.SentTransactionsPerSecond,
		ReceivedTransactionsPerSecond: measurements.ReceivedTransactionsPerSecond,
	}
}

func snapshotConfig(config config) configSnapshot {
	return configSnapshot{
		ReaderWorkers:         config.Reader.Workers,
		SenderWorkers:         config.Sender.Workers,
		ThrottlerRequestedTPS: config.Throttler.RequestedTPS,
	}
}

const snapshotLogInterval = time.Minute

func snapshotLogFields(snapshot runtimeSnapshot) []zap.Field {
	sourceErrorCategory := ""
	sourceErrorOperation := ""
	sourceErrorRelativePath := ""
	if snapshot.Reader.SourceError != nil {
		sourceErrorCategory = snapshot.Reader.SourceError.Category
		sourceErrorOperation = snapshot.Reader.SourceError.Operation
		sourceErrorRelativePath = snapshot.Reader.SourceError.RelativePath
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.String("state", string(snapshot.Run.State)),
		zap.Int64("elapsed_ms", snapshot.Run.ElapsedMs),
		zap.Int64("total_transactions", snapshot.Run.TotalTransactions),
		zap.Float64("reader_read_tps", snapshot.Reader.ReadTps),
		zap.Int64("reader_rows_read", snapshot.Reader.RowsRead),
		zap.Int("reader_workers", snapshot.Reader.Workers),
		zap.Int("reader_live_workers", len(snapshot.Reader.WorkerStates)),
		zap.Int("throttler_requested_tps", snapshot.Throttler.RequestedTps),
		zap.Float64("throttler_admitted_tps", snapshot.Throttler.AdmittedTps),
		zap.Bool("installed", snapshot.Throttler.Installed),
		zap.Int("reader_channel_capacity", snapshot.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", snapshot.ReaderChannel.DepthBatches),
		zap.Int("reader_channel_buffered_transactions", snapshot.ReaderChannel.BufferedTransactions),
		zap.Float64("reader_channel_input_tps", snapshot.ReaderChannel.SentTransactionsPerSecond),
		zap.Float64("reader_channel_output_tps", snapshot.ReaderChannel.ReceivedTransactionsPerSecond),
		zap.Int("reader_channel_blocked_senders", snapshot.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", snapshot.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", snapshot.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", snapshot.SenderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", snapshot.SenderChannel.DepthBatches),
		zap.Int("sender_channel_buffered_transactions", snapshot.SenderChannel.BufferedTransactions),
		zap.Float64("sender_channel_input_tps", snapshot.SenderChannel.SentTransactionsPerSecond),
		zap.Float64("sender_channel_output_tps", snapshot.SenderChannel.ReceivedTransactionsPerSecond),
		zap.Int("sender_channel_blocked_senders", snapshot.SenderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", snapshot.SenderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", snapshot.SenderChannel.BlockedMs),
		zap.Int("sender_workers", snapshot.Sender.Workers),
		zap.Int("sender_live_workers", len(snapshot.Sender.WorkerStates)),
		zap.String("source_error_category", sourceErrorCategory),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_relative_path", sourceErrorRelativePath),
	}
}
