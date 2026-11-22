package cli

import (
	"strings"
	"testing"
	"unicode"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestFormatSnapshot(t *testing.T) {
	status := api.Snapshot{
		Run: api.RunSnapshot{State: api.RunStateFaulted, ElapsedMs: 1250, TotalTransactions: 42},
		Reader: api.ReaderSnapshot{
			Workers: 3, WorkerStates: []api.ReaderWorkerState{
				{Activity: api.ReaderReading}, {Activity: api.ReaderIdle, Draining: true},
			}, RowsRead: 99,
			SourceError: &api.SourceError{Operation: "read", Path: "broken.parquet", Message: "corrupt"},
		},
		Throttler:     api.ThrottlerSnapshot{RequestedTps: 100, Installed: true},
		ReaderChannel: api.ChannelSnapshot{DepthBatches: 2, InputTransactionsTotal: 1, OutputTransactionsTotal: 2},
		SenderChannel: api.ChannelSnapshot{DepthBatches: 1, BlockedSenders: 1, OldestBlockedSenderMs: 7, BlockedMs: 8, InputTransactionsTotal: 3, OutputTransactionsTotal: 4},
		Sender: api.SenderSnapshot{Workers: 5, WorkerStates: []api.SenderWorkerState{
			{Activity: api.SenderInFlight}, {Activity: api.SenderInFlight},
			{Activity: api.SenderIdle}, {Activity: api.SenderBackoff, Draining: true},
		}},
	}

	got := formatSnapshot(status, api.ConfigResponse{ReadBatchSize: 10, ReaderChannelCapacity: 3, SenderChannelCapacity: 4})
	want := "INGESTION LAB LOADGEN STATUS\n" +
		"State:                    faulted\n" +
		"Elapsed:                  1.25s\n" +
		"Transactions:             42\n\n" +
		"Throttler\n" +
		"Requested TPS:            100\n" +
		"Average admitted TPS:     2.4\n" +
		"Installed:                true\n\n" +
		"Reader\n" +
		"Workers:                  configured=3 live=2 reading=1 idle=1 blocked=0 draining=1\n" +
		"Average read TPS:         79.2\n" +
		"Rows read:                99\n" +
		"Source:                   none\n" +
		"Source error:             read broken.parquet: corrupt\n\n" +
		"Reader channel\n" +
		"Queue:                    2/3 batches; approximately 20 transactions\n" +
		"Average input/output TPS: 0.8 / 1.6\n" +
		"Blocked:                  senders=0 oldest=0ms total=0ms\n\n" +
		"Sender channel\n" +
		"Queue:                    1/4 batches; approximately 10 transactions\n" +
		"Average input/output TPS: 2.4 / 3.2\n" +
		"Blocked:                  senders=1 oldest=7ms total=8ms\n\n" +
		"Sender\n" +
		"Workers:                  configured=5 live=4 in-flight=2 idle=1 backoff=1 draining=1\n"
	if got != want {
		t.Fatalf("card = %q, want %q", got, want)
	}
}

func TestWriteSnapshotChannelLargeTransactionEstimate(t *testing.T) {
	const maxExactJSONInteger = 1<<53 - 1
	var output strings.Builder
	writeSnapshotChannel(&output, "Reader channel", api.ChannelSnapshot{
		DepthBatches: maxExactJSONInteger,
	}, maxExactJSONInteger, 1500, 1000)
	if !strings.Contains(output.String(), "approximately 135107988821114") ||
		!strings.Contains(output.String(), " transactions\n") {
		t.Fatalf("large transaction estimate overflowed or changed format: %s", output.String())
	}
}

func TestFormatSnapshotSanitizesSnapshotStrings(t *testing.T) {
	status := api.Snapshot{
		Run: api.RunSnapshot{State: api.RunState("running\n\x1b[2J")},
		Reader: api.ReaderSnapshot{
			SourceError: &api.SourceError{
				Operation: "operation\t",
				Path:      "path\x7f",
				Message:   "message\n\x1b[0m",
			},
		},
	}

	got := formatSnapshot(status, api.ConfigResponse{SourcePath: "source\r\n\x1b[31m"})
	if strings.IndexFunc(got, func(character rune) bool {
		return character != '\n' && unicode.IsControl(character)
	}) >= 0 {
		t.Fatalf("card contains unsafe control byte: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(formatSnapshot(api.Snapshot{
		Run:       api.RunSnapshot{State: api.RunStateRunning},
		Throttler: api.ThrottlerSnapshot{Installed: true},
	}, api.ConfigResponse{}), "\n") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("card layout changed or has no final newline: %q", got)
	}
	for _, want := range []string{
		`State:                    running\x0a\x1b[2J`,
		`Source:                   source\x0d\x0a\x1b[31m`,
		`Source error:             operation\x09 path\x7f: message\x0a\x1b[0m`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing sanitized value %q: %q", want, got)
		}
	}
}

func TestFormatSnapshotStartingSourceGolden(t *testing.T) {
	card := formatSnapshot(api.Snapshot{
		Run: api.RunSnapshot{State: api.RunStateStarting},
	}, api.ConfigResponse{})
	if !strings.Contains(card, "Source:                   none\nSource error:             none\n\nReader channel\n") {
		t.Fatalf("idle source fields are not adjacent golden values: %q", card)
	}
}

func TestFormatSnapshotBeforeRunShowsZeroAverages(t *testing.T) {
	snapshot := api.Snapshot{Reader: api.ReaderSnapshot{RowsRead: 9}, SenderChannel: api.ChannelSnapshot{InputTransactionsTotal: 12, OutputTransactionsTotal: 6}}
	got := formatSnapshot(snapshot, api.ConfigResponse{})
	for _, line := range []string{"Average read TPS:         0.0", "Average admitted TPS:     0.0", "Average input/output TPS: 0.0 / 0.0"} {
		if !strings.Contains(got, line) {
			t.Fatalf("missing %q in %q", line, got)
		}
	}
}
