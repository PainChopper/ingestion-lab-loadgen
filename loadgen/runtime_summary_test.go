package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode"

	"go.uber.org/zap"
)

func TestFormatRuntimeStatusCard(t *testing.T) {
	summary := runtimeSummaryFromStatus(runtimeStatus{
		Run: runtimeRunStatus{State: runStateFaulted, ElapsedMs: 1250, TotalTransactions: 42},
		Reader: runtimeReaderStatus{
			Workers: 3, LiveWorkers: 2, ReadingWorkers: 1, IdleWorkers: 1, BlockedWorkers: 0,
			DrainingWorkers: 1, ReadTps: 12.34, RowsRead: 99,
			SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "broken.parquet", Message: "corrupt"},
		},
		Throttler:     runtimeThrottlerStatus{RequestedTps: 100, AdmittedTps: 9.87, InstallationMode: throttlerInstalled},
		ReaderChannel: runtimeChannelStatus{Capacity: 3, DepthBatches: 2, BufferedTransactions: 20, SentTransactionsPerSecond: 1.25, ReceivedTransactionsPerSecond: 2.5},
		SenderChannel: runtimeChannelStatus{Capacity: 4, DepthBatches: 1, BufferedTransactions: 10, BlockedSenders: 1, OldestBlockedSenderMs: 7, BlockedMs: 8, SentTransactionsPerSecond: 3.75, ReceivedTransactionsPerSecond: 4.5},
		Sender:        runtimeSenderStatus{Workers: 5, LiveWorkers: 4, InFlightWorkers: 2, IdleWorkers: 1, BackoffWorkers: 1, DrainingWorkers: 1},
	})

	got := formatRuntimeStatusCard(summary)
	want := "INGESTION LAB LOADGEN STATUS\n" +
		"State:                    faulted\n" +
		"Elapsed:                  1.25s\n" +
		"Transactions:             42\n\n" +
		"Throttler\n" +
		"Requested TPS:            100\n" +
		"Admitted TPS:             9.9\n" +
		"Mode:                     installed\n\n" +
		"Reader\n" +
		"Workers:                  configured=3 live=2 reading=1 idle=1 blocked=0 draining=1\n" +
		"Read TPS:                 12.3\n" +
		"Rows read:                99\n" +
		"Source:                   none\n" +
		"Source error:             source/read broken.parquet: corrupt\n\n" +
		"Reader channel\n" +
		"Queue:                    2/3 batches; 20 transactions\n" +
		"Input / output TPS:       1.2 / 2.5\n" +
		"Blocked:                  senders=0 oldest=0ms total=0ms\n\n" +
		"Sender channel\n" +
		"Queue:                    1/4 batches; 10 transactions\n" +
		"Input / output TPS:       3.8 / 4.5\n" +
		"Blocked:                  senders=1 oldest=7ms total=8ms\n\n" +
		"Sender\n" +
		"Workers:                  configured=5 live=4 in-flight=2 idle=1 backoff=1 draining=1\n"
	if got != want {
		t.Fatalf("card = %q, want %q", got, want)
	}
	if strings.Contains(got, "\x1b") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("card contains ANSI or has no final newline: %q", got)
	}
}

func TestFormatRuntimeStatusCardSanitizesSnapshotStrings(t *testing.T) {
	summary := runtimeSummaryFromStatus(runtimeStatus{
		Run: runtimeRunStatus{State: runState("running\n\x1b[2J")},
		Reader: runtimeReaderStatus{
			SourceDirectory: "source\r\n\x1b[31m",
			SourceError: &readerSourceError{
				Category:     "category\x00",
				Operation:    "operation\t",
				RelativePath: "path\x7f",
				Message:      "message\n\x1b[0m",
			},
		},
		Throttler: runtimeThrottlerStatus{InstallationMode: "mode\n\x1b"},
	})

	got := formatRuntimeStatusCard(summary)
	if strings.IndexFunc(got, func(character rune) bool {
		return character != '\n' && unicode.IsControl(character)
	}) >= 0 {
		t.Fatalf("card contains unsafe control byte: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(formatRuntimeStatusCard(runtimeSummaryFromStatus(runtimeStatus{
		Run:       runtimeRunStatus{State: runStateRunning},
		Throttler: runtimeThrottlerStatus{InstallationMode: throttlerInstalled},
	})), "\n") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("card layout changed or has no final newline: %q", got)
	}
	for _, want := range []string{
		`State:                    running\x0a\x1b[2J`,
		`Source:                   source\x0d\x0a\x1b[31m`,
		`Source error:             category\x00/operation\x09 path\x7f: message\x0a\x1b[0m`,
		`Mode:                     mode\x0a\x1b`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing sanitized value %q: %q", want, got)
		}
	}
}

func TestFormatRuntimeStatusCardIdleSourceGolden(t *testing.T) {
	card := formatRuntimeStatusCard(runtimeSummaryFromStatus(runtimeStatus{
		Run: runtimeRunStatus{State: runStateIdle},
	}))
	if !strings.Contains(card, "Source:                   none\nSource error:             none\n\nReader channel\n") {
		t.Fatalf("idle source fields are not adjacent golden values: %q", card)
	}
}

func TestRunRemoteCLIStatusFetchesOneSnapshot(t *testing.T) {
	snapshot := testRemoteSnapshot(t)
	snapshot.Run.ElapsedMs = 1000
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != snapshotPath {
			t.Fatalf("request = %s %s, want GET %s", request.Method, request.URL.Path, snapshotPath)
		}
		_, _ = writer.Write(encoded)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := runRemoteCLI(context.Background(), cliCommand{remoteAction: "status", remoteURL: server.URL}, &stdout); err != nil {
		t.Fatalf("runRemoteCLI: %v", err)
	}
	if requests != 1 || !strings.HasPrefix(stdout.String(), "INGESTION LAB LOADGEN STATUS\n") {
		t.Fatalf("requests=%d stdout=%q", requests, stdout.String())
	}
}

func TestParseStatusCLI(t *testing.T) {
	command, err := parseCLI([]string{"status", "--url", "https://example.test/"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if command.remoteAction != "status" || command.remoteURL != "https://example.test" {
		t.Fatalf("command = %+v", command)
	}
	help, err := parseCLI([]string{"status", "--help"})
	if err != nil || !help.showUsage {
		t.Fatalf("status help = %+v, %v", help, err)
	}
	for _, args := range [][]string{{"status", "--watch"}, {"status", "now"}, {"status", "--url", "ftp://example.test"}} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("parseCLI(%q) accepted invalid arguments", args)
		}
	}
}

func TestRunCLIStatusErrorSemantics(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if got := runCLI([]string{"status", "--url", "ftp://example.test"}, &stdout, &stderr); got != cliExitUsage {
		t.Fatalf("invalid URL exit = %d, want %d", got, cliExitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: ingestion-lab-loadgen status [--url <url>]") {
		t.Fatalf("invalid URL stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "conflict", http.StatusConflict)
	}))
	defer server.Close()
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"status", "--url", server.URL}, &stdout, &stderr); got != cliExitHTTP {
		t.Fatalf("HTTP exit = %d, want %d", got, cliExitHTTP)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 409") {
		t.Fatalf("HTTP stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}

	malformedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"unexpected":true}`))
	}))
	defer malformedServer.Close()
	stdout.Reset()
	stderr.Reset()
	if got := runCLI([]string{"status", "--url", malformedServer.URL}, &stdout, &stderr); got != cliExitRemote {
		t.Fatalf("malformed snapshot exit = %d, want %d", got, cliExitRemote)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "decode snapshot") {
		t.Fatalf("malformed snapshot stdout/stderr = %q / %q", stdout.String(), stderr.String())
	}
}

func TestRuntimeSummaryLogfmtFieldsAndCadence(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatal(err)
	}
	state := newTestControlState(t)
	state.logger = logger
	requests := make(chan runtimeCommand)
	metrics := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoop(requests, metrics, NewPrometheusMetrics(), func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
			readerDone := make(chan struct{})
			go func() {
				<-ctx.Done()
				close(readerDone)
			}()
			return readerRun{done: readerDone, reconcile: func(int) {}}, nil
		})
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	reply := make(chan runtimeCommandReceipt, 1)
	requests <- runtimeCommand{kind: cmdRun, receiptReply: reply}
	if result := <-reply; result.status != commandAccepted || result.err != nil {
		t.Fatalf("run = %+v", result)
	}
	output.Reset()
	metrics <- time.Now()
	awaitRuntimeSummaryMetric(t, requests)
	if output.Len() != 0 {
		t.Fatalf("summary emitted before one-minute deadline: %q", output.String())
	}
	afterDeadline := time.Now().Add(2 * runtimeSummaryInterval)
	metrics <- afterDeadline
	awaitRuntimeSummaryMetric(t, requests)
	line := output.String()
	if strings.Count(line, "\n") != 1 || strings.Contains(line, "runtime_snapshot") || strings.Contains(line, "INGESTION LAB") {
		t.Fatalf("summary line = %q", line)
	}
	for _, field := range []string{"event=runtime_summary", "state=running", "elapsed_ms=", "total_transactions=", "reader_channel_capacity=", "sender_channel_capacity=", "source_error_category="} {
		if !strings.Contains(line, field) {
			t.Fatalf("summary missing %q: %q", field, line)
		}
	}
	metrics <- afterDeadline.Add(time.Millisecond)
	awaitRuntimeSummaryMetric(t, requests)
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("duplicate summary before next deadline: %q", output.String())
	}
}

func awaitRuntimeSummaryMetric(t *testing.T, requests chan<- runtimeCommand) {
	t.Helper()
	reply := make(chan runtimeStatus, 1)
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}
	<-reply
}

func TestInfoLoggerExcludesWorkerAndSourceDebugEvents(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("reader worker started", zap.String("event", "reader_worker_started"))
	logger.Debug("reader source opened", zap.String("event", "reader_source_opened"))
	logger.Debug("sender worker stopped", zap.String("event", "sender_worker_stopped"))
	if output.Len() != 0 {
		t.Fatalf("info logger emitted debug lifecycle events: %q", output.String())
	}
}
