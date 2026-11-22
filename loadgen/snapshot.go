package main

import (
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"go.uber.org/zap"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func snapshotChannel(measurements pipeline.QueueSnapshot) api.ChannelSnapshot {
	return api.ChannelSnapshot{
		DepthBatches:            measurements.DepthBatches,
		BlockedSenders:          measurements.BlockedSenders,
		OldestBlockedSenderMs:   measurements.OldestBlockedSenderMS,
		BlockedMs:               measurements.BlockedMS,
		InputTransactionsTotal:  measurements.SentTransactionsTotal,
		OutputTransactionsTotal: measurements.ReceivedTransactionsTotal,
	}
}

const snapshotLogInterval = time.Minute

func snapshotLogFields(snapshot api.Snapshot, cfg config.Config) []zap.Field {
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
		zap.Int("reader_channel_capacity", cfg.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", snapshot.ReaderChannel.DepthBatches),
		zap.Int64("reader_channel_input_transactions_total", snapshot.ReaderChannel.InputTransactionsTotal),
		zap.Int64("reader_channel_output_transactions_total", snapshot.ReaderChannel.OutputTransactionsTotal),
		zap.Int("reader_channel_blocked_senders", snapshot.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", snapshot.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", snapshot.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", cfg.SenderChannel.Capacity),
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
