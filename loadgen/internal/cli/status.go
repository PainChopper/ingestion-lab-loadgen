package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func formatSnapshot(snapshot api.Snapshot, config api.ConfigResponse) string {
	reader := countReaderWorkers(snapshot.Reader.WorkerStates)
	sender := countSenderWorkers(snapshot.Sender.WorkerStates)
	sourcePath := sanitizeSnapshotValue(config.SourcePath)
	if sourcePath == "" {
		sourcePath = "none"
	}

	sourceError := "none"
	if snapshot.Reader.SourceError != nil {
		sourceError = fmt.Sprintf(
			"%s %s: %s",
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Operation),
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Path),
			sanitizeSnapshotValue(snapshot.Reader.SourceError.Message),
		)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "INGESTION LAB LOADGEN STATUS\n")
	fmt.Fprintf(&output, "State:                    %s\n", sanitizeSnapshotValue(string(snapshot.Run.State)))
	fmt.Fprintf(&output, "Elapsed:                  %s\n", time.Duration(snapshot.Run.ElapsedMs)*time.Millisecond)
	fmt.Fprintf(&output, "Transactions:             %d\n\n", snapshot.Run.TotalTransactions)
	fmt.Fprintf(&output, "Throttler\n")
	fmt.Fprintf(&output, "Requested TPS:            %d\n", snapshot.Throttler.RequestedTps)
	fmt.Fprintf(&output, "Average admitted TPS:     %.1f\n", averageTPS(snapshot.SenderChannel.InputTransactionsTotal, snapshot.Run.ElapsedMs))
	fmt.Fprintf(&output, "Installed:                %t\n\n", snapshot.Throttler.Installed)
	fmt.Fprintf(&output, "Reader\n")
	fmt.Fprintf(
		&output,
		"Workers:                  configured=%d live=%d reading=%d idle=%d blocked=%d draining=%d\n",
		snapshot.Reader.Workers,
		reader.liveWorkers,
		reader.readingWorkers,
		reader.idleWorkers,
		reader.blockedWorkers,
		reader.drainingWorkers,
	)
	fmt.Fprintf(&output, "Average read TPS:         %.1f\n", averageTPS(snapshot.Reader.RowsRead, snapshot.Run.ElapsedMs))
	fmt.Fprintf(&output, "Rows read:                %d\n", snapshot.Reader.RowsRead)
	fmt.Fprintf(&output, "Source:                   %s\n", sourcePath)
	fmt.Fprintf(&output, "Source error:             %s\n\n", sourceError)
	writeSnapshotChannel(
		&output,
		"Reader channel",
		snapshot.ReaderChannel,
		config.ReaderChannelCapacity,
		config.ReadBatchSize,
		snapshot.Run.ElapsedMs,
	)
	writeSnapshotChannel(
		&output,
		"Sender channel",
		snapshot.SenderChannel,
		config.SenderChannelCapacity,
		config.ReadBatchSize,
		snapshot.Run.ElapsedMs,
	)
	fmt.Fprintf(&output, "Sender\n")
	fmt.Fprintf(
		&output,
		"Workers:                  configured=%d live=%d in-flight=%d idle=%d backoff=%d draining=%d\n",
		snapshot.Sender.Workers,
		sender.liveWorkers,
		sender.inFlightWorkers,
		sender.idleWorkers,
		sender.backoffWorkers,
		sender.drainingWorkers,
	)
	return output.String()
}

func sanitizeSnapshotValue(value string) string {
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

func writeSnapshotChannel(
	output *strings.Builder,
	title string,
	channel api.ChannelSnapshot,
	capacity int,
	batchSize int,
	elapsedMs int64,
) {
	fmt.Fprintf(output, "%s\n", title)
	fmt.Fprintf(
		output,
		"Queue:                    %d/%d batches; approximately %.0f transactions\n",
		channel.DepthBatches,
		capacity,
		float64(channel.DepthBatches)*float64(batchSize),
	)
	fmt.Fprintf(
		output,
		"Average input/output TPS: %.1f / %.1f\n",
		averageTPS(channel.InputTransactionsTotal, elapsedMs),
		averageTPS(channel.OutputTransactionsTotal, elapsedMs),
	)
	fmt.Fprintf(
		output,
		"Blocked:                  senders=%d oldest=%dms total=%dms\n\n",
		channel.BlockedSenders,
		channel.OldestBlockedSenderMs,
		channel.BlockedMs,
	)
}

func averageTPS(total, elapsedMs int64) float64 {
	if elapsedMs <= 0 {
		return 0
	}
	return float64(total) * 1000 / float64(elapsedMs)
}
