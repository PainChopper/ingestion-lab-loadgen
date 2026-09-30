package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"
)

const runtimeSummaryInterval = time.Minute

type runtimeSummary struct {
	state             runState
	elapsedMs         int64
	totalTransactions int64

	reader        runtimeReaderStatus
	throttler     runtimeThrottlerStatus
	sender        runtimeSenderStatus
	readerChannel runtimeChannelStatus
	senderChannel runtimeChannelStatus
	sourceError   *readerSourceError
}

func runtimeSummaryFromStatus(status runtimeStatus) runtimeSummary {
	return runtimeSummary{
		state:             status.Run.State,
		elapsedMs:         status.Run.ElapsedMs,
		totalTransactions: status.Run.TotalTransactions,
		reader:            status.Reader,
		throttler:         status.Throttler,
		sender:            status.Sender,
		readerChannel:     status.ReaderChannel,
		senderChannel:     status.SenderChannel,
		sourceError:       status.Reader.SourceError,
	}
}

func formatRuntimeStatusCard(summary runtimeSummary) string {
	sourceDirectory := sanitizeRuntimeStatusValue(summary.reader.SourceDirectory)
	if sourceDirectory == "" {
		sourceDirectory = "none"
	}

	sourceError := "none"
	if summary.sourceError != nil {
		sourceError = fmt.Sprintf(
			"%s/%s %s: %s",
			sanitizeRuntimeStatusValue(summary.sourceError.Category),
			sanitizeRuntimeStatusValue(summary.sourceError.Operation),
			sanitizeRuntimeStatusValue(summary.sourceError.RelativePath),
			sanitizeRuntimeStatusValue(summary.sourceError.Message),
		)
	}

	var card strings.Builder
	fmt.Fprintf(&card, "INGESTION LAB LOADGEN STATUS\n")
	fmt.Fprintf(&card, "State:                    %s\n", sanitizeRuntimeStatusValue(string(summary.state)))
	fmt.Fprintf(&card, "Elapsed:                  %s\n", time.Duration(summary.elapsedMs)*time.Millisecond)
	fmt.Fprintf(&card, "Transactions:             %d\n\n", summary.totalTransactions)
	fmt.Fprintf(&card, "Throttler\n")
	fmt.Fprintf(&card, "Requested TPS:            %d\n", summary.throttler.RequestedTps)
	fmt.Fprintf(&card, "Admitted TPS:             %.1f\n", summary.throttler.AdmittedTps)
	fmt.Fprintf(&card, "Mode:                     %s\n\n", sanitizeRuntimeStatusValue(summary.throttler.InstallationMode))
	fmt.Fprintf(&card, "Reader\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d reading=%d idle=%d blocked=%d draining=%d\n",
		summary.reader.Workers,
		summary.reader.LiveWorkers,
		summary.reader.ReadingWorkers,
		summary.reader.IdleWorkers,
		summary.reader.BlockedWorkers,
		summary.reader.DrainingWorkers,
	)
	fmt.Fprintf(&card, "Read TPS:                 %.1f\n", summary.reader.ReadTps)
	fmt.Fprintf(&card, "Rows read:                %d\n", summary.reader.RowsRead)
	fmt.Fprintf(&card, "Source:                   %s\n", sourceDirectory)
	fmt.Fprintf(&card, "Source error:             %s\n\n", sourceError)
	writeRuntimeStatusChannel(&card, "Reader channel", summary.readerChannel)
	writeRuntimeStatusChannel(&card, "Sender channel", summary.senderChannel)
	fmt.Fprintf(&card, "Sender\n")
	fmt.Fprintf(
		&card,
		"Workers:                  configured=%d live=%d in-flight=%d idle=%d backoff=%d draining=%d\n",
		summary.sender.Workers,
		summary.sender.LiveWorkers,
		summary.sender.InFlightWorkers,
		summary.sender.IdleWorkers,
		summary.sender.BackoffWorkers,
		summary.sender.DrainingWorkers,
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

func runtimeSummaryFields(summary runtimeSummary) []zap.Field {
	sourceErrorCategory := ""
	sourceErrorOperation := ""
	sourceErrorRelativePath := ""
	if summary.sourceError != nil {
		sourceErrorCategory = summary.sourceError.Category
		sourceErrorOperation = summary.sourceError.Operation
		sourceErrorRelativePath = summary.sourceError.RelativePath
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.String("state", string(summary.state)),
		zap.Int64("elapsed_ms", summary.elapsedMs),
		zap.Int64("total_transactions", summary.totalTransactions),
		zap.Float64("reader_read_tps", summary.reader.ReadTps),
		zap.Int64("reader_rows_read", summary.reader.RowsRead),
		zap.Int("reader_workers", summary.reader.Workers),
		zap.Int("reader_live_workers", summary.reader.LiveWorkers),
		zap.Int("reader_reading_workers", summary.reader.ReadingWorkers),
		zap.Int("reader_blocked_workers", summary.reader.BlockedWorkers),
		zap.Int("reader_draining_workers", summary.reader.DrainingWorkers),
		zap.Int("throttler_requested_tps", summary.throttler.RequestedTps),
		zap.Float64("throttler_admitted_tps", summary.throttler.AdmittedTps),
		zap.String("throttler_mode", summary.throttler.InstallationMode),
		zap.Int("reader_channel_capacity", summary.readerChannel.Capacity),
		zap.Int("reader_channel_depth_batches", summary.readerChannel.DepthBatches),
		zap.Int("reader_channel_buffered_transactions", summary.readerChannel.BufferedTransactions),
		zap.Float64("reader_channel_input_tps", summary.readerChannel.SentTransactionsPerSecond),
		zap.Float64("reader_channel_output_tps", summary.readerChannel.ReceivedTransactionsPerSecond),
		zap.Int("reader_channel_blocked_senders", summary.readerChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", summary.readerChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", summary.readerChannel.BlockedMs),
		zap.Int("sender_channel_capacity", summary.senderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", summary.senderChannel.DepthBatches),
		zap.Int("sender_channel_buffered_transactions", summary.senderChannel.BufferedTransactions),
		zap.Float64("sender_channel_input_tps", summary.senderChannel.SentTransactionsPerSecond),
		zap.Float64("sender_channel_output_tps", summary.senderChannel.ReceivedTransactionsPerSecond),
		zap.Int("sender_channel_blocked_senders", summary.senderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", summary.senderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", summary.senderChannel.BlockedMs),
		zap.Int("sender_workers", summary.sender.Workers),
		zap.Int("sender_live_workers", summary.sender.LiveWorkers),
		zap.Int("sender_in_flight_workers", summary.sender.InFlightWorkers),
		zap.Int("sender_backoff_workers", summary.sender.BackoffWorkers),
		zap.Int("sender_draining_workers", summary.sender.DrainingWorkers),
		zap.String("source_error_category", sourceErrorCategory),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_relative_path", sourceErrorRelativePath),
	}
}
