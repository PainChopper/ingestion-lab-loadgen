package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestRunRemoteCLIMapsSetActionsAndConfirmsSnapshot(t *testing.T) {
	for _, test := range []struct {
		target        string
		requestAction string
		value         any
		update        func(*api.Snapshot)
	}{
		{target: "readers", requestAction: "set-reader-workers", value: 3, update: func(snapshot *api.Snapshot) { snapshot.Reader.Workers = 3 }},
		{target: "senders", requestAction: "set-sender-workers", value: 4, update: func(snapshot *api.Snapshot) { snapshot.Sender.Workers = 4 }},
		{target: "tps", requestAction: "set-requested-tps", value: 0, update: func(snapshot *api.Snapshot) { snapshot.Throttler.RequestedTps = 0 }},
		{target: "tps", requestAction: "set-requested-tps", value: 2000001, update: func(snapshot *api.Snapshot) { snapshot.Throttler.RequestedTps = 2000001 }},
		{target: "throttle", requestAction: "set-throttler-installed", value: false, update: func(snapshot *api.Snapshot) { snapshot.Throttler.Installed = false }},
		{target: "throttle", requestAction: "set-throttler-installed", value: true, update: func(snapshot *api.Snapshot) { snapshot.Throttler.Installed = true }},
	} {
		t.Run(test.target+"/"+fmt.Sprint(test.value), func(t *testing.T) {
			snapshot := testRemoteSnapshot(t)
			test.update(&snapshot)
			var requestBody api.CommandRequest
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests = append(requests, request.Method+" "+request.URL.Path)
				switch request.URL.Path {
				case api.CommandsPath:
					if request.Method != http.MethodPost {
						t.Errorf("method = %s, want POST", request.Method)
						http.Error(writer, "unexpected method", http.StatusInternalServerError)
						return
					}
					if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
						t.Errorf("decode command: %v", err)
						http.Error(writer, "invalid command", http.StatusInternalServerError)
						return
					}
				case api.SnapshotPath:
					if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
						t.Errorf("encode snapshot: %v", err)
					}
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			command, err := parseCLI([]string{"set", test.target, fmt.Sprint(test.value), "--url", server.URL})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := runRemoteCLI(context.Background(), command, &output); err != nil {
				t.Fatalf("runRemoteCLI: %v", err)
			}
			if requestBody.Action != test.requestAction || string(requestBody.Value) != mustMarshalJSON(t, test.value) {
				t.Fatalf("request = %+v", requestBody)
			}
			wantOutput := "action=set target=" + test.target + " value=" + fmt.Sprint(test.value) + " state=running\n"
			if output.String() != wantOutput {
				t.Fatalf("stdout = %q, want %q", output.String(), wantOutput)
			}
			if !reflect.DeepEqual(requests, []string{"POST " + api.CommandsPath, "GET " + api.SnapshotPath}) {
				t.Fatalf("set requests = %v", requests)
			}
		})
	}
}

func TestRunCLISetRequiresExplicitTargetValue(t *testing.T) {
	for _, command := range []struct {
		target, value, section, field string
		valid, wrongType, mismatch    json.RawMessage
	}{
		{target: "readers", value: "3", section: "reader", field: "workers", valid: json.RawMessage(`3`), wrongType: json.RawMessage(`true`), mismatch: json.RawMessage(`4`)},
		{target: "senders", value: "4", section: "sender", field: "workers", valid: json.RawMessage(`4`), wrongType: json.RawMessage(`false`), mismatch: json.RawMessage(`5`)},
		{target: "tps", value: "0", section: "throttler", field: "requestedTps", valid: json.RawMessage(`0`), wrongType: json.RawMessage(`false`), mismatch: json.RawMessage(`1`)},
		{target: "throttle", value: "false", section: "throttler", field: "installed", valid: json.RawMessage(`false`), wrongType: json.RawMessage(`0`), mismatch: json.RawMessage(`true`)},
	} {
		t.Run(command.target, func(t *testing.T) {
			for _, shape := range []string{"explicit", "only target", "missing", "missing section", "null", "string", "wrong type", "mismatch"} {
				t.Run(shape, func(t *testing.T) {
					root := map[string]json.RawMessage{}
					if err := json.Unmarshal([]byte(mustMarshalJSON(t, testRemoteSnapshot(t))), &root); err != nil {
						t.Fatal(err)
					}
					section := map[string]json.RawMessage{}
					if err := json.Unmarshal(root[command.section], &section); err != nil {
						t.Fatal(err)
					}
					section[command.field] = command.valid
					switch shape {
					case "missing":
						delete(section, command.field)
					case "null":
						section[command.field] = json.RawMessage(`null`)
					case "string":
						section[command.field] = json.RawMessage(mustMarshalJSON(t, command.value))
					case "wrong type":
						section[command.field] = command.wrongType
					case "mismatch":
						section[command.field] = command.mismatch
					}
					root[command.section] = json.RawMessage(mustMarshalJSON(t, section))
					if shape == "missing section" {
						delete(root, command.section)
					}
					if shape == "only target" {
						root = map[string]json.RawMessage{
							"run":           json.RawMessage(`{"state":"running"}`),
							command.section: json.RawMessage(mustMarshalJSON(t, map[string]json.RawMessage{command.field: command.valid})),
						}
					}
					body := mustMarshalJSON(t, root)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == api.SnapshotPath {
							_, _ = w.Write([]byte(body))
						}
					}))
					defer server.Close()
					var stdout, stderr bytes.Buffer
					code := runCLI([]string{"set", command.target, command.value, "--url", server.URL}, &stdout, &stderr)
					if shape == "explicit" || shape == "only target" {
						if code != 0 || !strings.Contains(stdout.String(), "target="+command.target+" value="+command.value) || stderr.Len() != 0 {
							t.Fatalf("explicit value: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
						}
					} else if code != cliExitRemote || stdout.Len() != 0 || stderr.Len() == 0 {
						t.Fatalf("invalid target field: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
					}
					if shape == "mismatch" && command.target == "readers" && !strings.Contains(stderr.String(), "verification snapshot reader.workers") {
						t.Fatalf("missing mismatch field diagnostic: %q", stderr.String())
					}
				})
			}
		})
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %v: %v", value, err)
	}
	return string(encoded)
}

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

func TestRunRemoteCLIStatusFetchesConfigAndSnapshot(t *testing.T) {
	configBody := mustMarshalJSON(t, newConfigResponse(testConfig(t)))
	snapshotBody := mustMarshalJSON(t, testRemoteSnapshot(t))
	for _, test := range []struct {
		name, endpoint, body string
		status               int
		wantErr              string
		shortBody            bool
	}{
		{name: "success"},
		{name: "invalid snapshot", endpoint: api.SnapshotPath, body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: "GET " + api.SnapshotPath + ": decode snapshot"},
		{name: "invalid config", endpoint: api.ConfigPath, body: `{"unexpected":true}`, wantErr: "GET " + api.ConfigPath + ": decode config"},
		{name: "snapshot HTTP error", endpoint: api.SnapshotPath, status: http.StatusServiceUnavailable, wantErr: "HTTP 503 Service Unavailable GET " + api.SnapshotPath},
		{name: "config HTTP error", endpoint: api.ConfigPath, status: http.StatusServiceUnavailable, wantErr: "HTTP 503 Service Unavailable GET " + api.ConfigPath},
		{name: "truncated snapshot", endpoint: api.SnapshotPath, body: `{`, shortBody: true, wantErr: "read GET " + api.SnapshotPath},
		{name: "truncated config", endpoint: api.ConfigPath, body: `{`, shortBody: true, wantErr: "read GET " + api.ConfigPath},
		{name: "snapshot transport error", endpoint: api.SnapshotPath, wantErr: "GET " + api.SnapshotPath},
		{name: "config transport error", endpoint: api.ConfigPath, wantErr: "GET " + api.ConfigPath},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestsMu sync.Mutex
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestsMu.Lock()
				requests = append(requests, r.URL.Path)
				requestsMu.Unlock()
				w.Header().Set("Connection", "close")
				if r.Method != http.MethodGet || (r.URL.Path != api.SnapshotPath && r.URL.Path != api.ConfigPath) {
					t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				if r.URL.Path != test.endpoint {
					body := snapshotBody
					if r.URL.Path == api.ConfigPath {
						body = configBody
					}
					_, _ = w.Write([]byte(body))
					return
				}
				if strings.Contains(test.name, "transport") {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Errorf("hijack: %v", err)
						return
					}
					if err := connection.Close(); err != nil {
						t.Errorf("close: %v", err)
					}
					return
				}
				if test.shortBody {
					w.Header().Set("Content-Length", "100")
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			var stdout bytes.Buffer
			err := runRemoteCLI(t.Context(), cliCommand{remoteAction: "status", remoteURL: server.URL}, &stdout)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || stdout.Len() != 0 {
					t.Fatalf("error=%v stdout=%q", err, stdout.String())
				}
				if test.shortBody && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("body read error = %v, want unexpected EOF", err)
				}
				if test.status != 0 && err.Error() != test.wantErr {
					t.Fatalf("empty HTTP error = %q, want %q", err, test.wantErr)
				}
			} else if err != nil || stdout.String() != formatSnapshot(testRemoteSnapshot(t), newConfigResponse(testConfig(t))) {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
			wantRequests := []string{api.ConfigPath, api.SnapshotPath}
			if test.endpoint == api.ConfigPath {
				wantRequests = []string{api.ConfigPath}
			}
			requestsMu.Lock()
			defer requestsMu.Unlock()
			if !reflect.DeepEqual(requests, wantRequests) {
				t.Fatalf("requests=%v, want %v", requests, wantRequests)
			}
		})
	}
}

func TestRunRemoteCLISetReportsPOSTTransportError(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Method + " " + r.URL.Path
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack connection: %v", err)
			return
		}
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	}))
	defer server.Close()
	command, err := parseCLI([]string{"set", "readers", "1", "--url", server.URL})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err = runRemoteCLI(t.Context(), command, &stdout)
	if !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "POST "+api.CommandsPath) || stdout.Len() != 0 {
		t.Fatalf("error=%v stdout=%q, want POST transport failure", err, stdout.String())
	}
	if got := <-received; got != "POST "+api.CommandsPath {
		t.Fatalf("request = %q, want POST %s", got, api.CommandsPath)
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
