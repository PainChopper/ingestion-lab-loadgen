package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"
)

const runtimeSummaryInterval = time.Minute

func formatRuntimeStatusCard(status runtimeStatus) string {
	sourceDirectory := sanitizeRuntimeStatusValue(status.Reader.SourceDirectory)
	if sourceDirectory == "" {
		sourceDirectory = "none"
	}

	sourceError := "none"
	if status.Reader.SourceError != nil {
		sourceError = fmt.Sprintf(
			"%s/%s %s: %s",
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Category),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Operation),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.RelativePath),
			sanitizeRuntimeStatusValue(status.Reader.SourceError.Message),
		)
	}

	var card strings.Builder
	fmt.Fprintf(&card, "INGESTION LAB LOADGEN STATUS\n")
	fmt.Fprintf(&card, "State:                    %s\n", sanitizeRuntimeStatusValue(string(status.Run.State)))
	fmt.Fprintf(&card, "Elapsed:                  %s\n", time.Duration(status.Run.ElapsedMs)*time.Millisecond)
	fmt.Fprintf(&card, "Transactions:             %d\n\n", status.Run.TotalTransactions)
	fmt.Fprintf(&card, "Throttler\n")
	fmt.Fprintf(&card, "Requested TPS:            %d\n", status.Throttler.RequestedTps)
	fmt.Fprintf(&card, "Admitted TPS:             %.1f\n", status.Throttler.AdmittedTps)
	fmt.Fprintf(&card, "Mode:                     %s\n\n", sanitizeRuntimeStatusValue(status.Throttler.InstallationMode))
	fmt.Fprintf(&card, "Reader\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d reading=%d idle=%d blocked=%d draining=%d\n",
		status.Reader.Workers,
		status.Reader.LiveWorkers,
		status.Reader.ReadingWorkers,
		status.Reader.IdleWorkers,
		status.Reader.BlockedWorkers,
		status.Reader.DrainingWorkers,
	)
	fmt.Fprintf(&card, "Read TPS:                 %.1f\n", status.Reader.ReadTps)
	fmt.Fprintf(&card, "Rows read:                %d\n", status.Reader.RowsRead)
	fmt.Fprintf(&card, "Source:                   %s\n", sourceDirectory)
	fmt.Fprintf(&card, "Source error:             %s\n\n", sourceError)
	writeRuntimeStatusChannel(&card, "Reader channel", status.ReaderChannel)
	writeRuntimeStatusChannel(&card, "Sender channel", status.SenderChannel)
	fmt.Fprintf(&card, "Sender\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d in-flight=%d idle=%d backoff=%d draining=%d\n",
		status.Sender.Workers,
		status.Sender.LiveWorkers,
		status.Sender.InFlightWorkers,
		status.Sender.IdleWorkers,
		status.Sender.BackoffWorkers,
		status.Sender.DrainingWorkers,
	)
	return card.String()
}

func sanitizeRuntimeStatusValue(value string) string {
	var sanitized strings.Builder
	for _, character := range value {
		if !unicode.IsControl(character) {
			sanitized.WriteRune(character)
			continue
		}
		if character <= 0xff {
			fmt.Fprintf(&sanitized, "\\x%02x", character)
			continue
		}
		fmt.Fprintf(&sanitized, "\\u%04x", character)
	}
	return sanitized.String()
}

func writeRuntimeStatusChannel(card *strings.Builder, title string, channel runtimeChannelStatus) {
	fmt.Fprintf(card, "%s\n", title)
	fmt.Fprintf(
		card,
		"Queue:                    %d/%d batches; %d transactions\n",
		channel.DepthBatches,
		channel.Capacity,
		channel.BufferedTransactions,
	)
	fmt.Fprintf(
		card,
		"Input / output TPS:       %.1f / %.1f\n",
		channel.SentTransactionsPerSecond,
		channel.ReceivedTransactionsPerSecond,
	)
	fmt.Fprintf(
		card,
		"Blocked:                  senders=%d oldest=%dms total=%dms\n\n",
		channel.BlockedSenders,
		channel.OldestBlockedSenderMs,
		channel.BlockedMs,
	)
}

func runtimeSummaryFields(status runtimeStatus) []zap.Field {
	sourceErrorCategory := ""
	sourceErrorOperation := ""
	sourceErrorRelativePath := ""
	if status.Reader.SourceError != nil {
		sourceErrorCategory = status.Reader.SourceError.Category
		sourceErrorOperation = status.Reader.SourceError.Operation
		sourceErrorRelativePath = status.Reader.SourceError.RelativePath
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.String("state", string(status.Run.State)),
		zap.Int64("elapsed_ms", status.Run.ElapsedMs),
		zap.Int64("total_transactions", status.Run.TotalTransactions),
		zap.Float64("reader_read_tps", status.Reader.ReadTps),
		zap.Int64("reader_rows_read", status.Reader.RowsRead),
		zap.Int("reader_workers", status.Reader.Workers),
		zap.Int("reader_live_workers", status.Reader.LiveWorkers),
		zap.Int("reader_reading_workers", status.Reader.ReadingWorkers),
		zap.Int("reader_blocked_workers", status.Reader.BlockedWorkers),
		zap.Int("reader_draining_workers", status.Reader.DrainingWorkers),
		zap.Int("throttler_requested_tps", status.Throttler.RequestedTps),
		zap.Float64("throttler_admitted_tps", status.Throttler.AdmittedTps),
		zap.String("throttler_mode", status.Throttler.InstallationMode),
		zap.Int("reader_channel_capacity", status.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", status.ReaderChannel.DepthBatches),
		zap.Int("reader_channel_buffered_transactions", status.ReaderChannel.BufferedTransactions),
		zap.Float64("reader_channel_input_tps", status.ReaderChannel.SentTransactionsPerSecond),
		zap.Float64("reader_channel_output_tps", status.ReaderChannel.ReceivedTransactionsPerSecond),
		zap.Int("reader_channel_blocked_senders", status.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", status.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", status.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", status.SenderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", status.SenderChannel.DepthBatches),
		zap.Int("sender_channel_buffered_transactions", status.SenderChannel.BufferedTransactions),
		zap.Float64("sender_channel_input_tps", status.SenderChannel.SentTransactionsPerSecond),
		zap.Float64("sender_channel_output_tps", status.SenderChannel.ReceivedTransactionsPerSecond),
		zap.Int("sender_channel_blocked_senders", status.SenderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", status.SenderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", status.SenderChannel.BlockedMs),
		zap.Int("sender_workers", status.Sender.Workers),
		zap.Int("sender_live_workers", status.Sender.LiveWorkers),
		zap.Int("sender_in_flight_workers", status.Sender.InFlightWorkers),
		zap.Int("sender_backoff_workers", status.Sender.BackoffWorkers),
		zap.Int("sender_draining_workers", status.Sender.DrainingWorkers),
		zap.String("source_error_category", sourceErrorCategory),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_relative_path", sourceErrorRelativePath),
	}
}
