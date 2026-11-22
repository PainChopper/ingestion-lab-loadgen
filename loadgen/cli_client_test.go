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
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestRunRemoteCLIMapsSetActionsAndConfirmsSnapshot(t *testing.T) {
	for _, test := range []struct {
		target        string
		requestAction string
		value         any
		update        func(*runtimeSnapshot)
	}{
		{target: "readers", requestAction: "set-reader-workers", value: 3, update: func(snapshot *runtimeSnapshot) { snapshot.Reader.Workers = 3 }},
		{target: "senders", requestAction: "set-sender-workers", value: 4, update: func(snapshot *runtimeSnapshot) { snapshot.Sender.Workers = 4 }},
		{target: "tps", requestAction: "set-requested-tps", value: 0, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.RequestedTps = 0 }},
		{target: "tps", requestAction: "set-requested-tps", value: 2000001, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.RequestedTps = 2000001 }},
		{target: "throttle", requestAction: "set-throttler-installed", value: false, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = false }},
		{target: "throttle", requestAction: "set-throttler-installed", value: true, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = true }},
	} {
		t.Run(test.target+"/"+fmt.Sprint(test.value), func(t *testing.T) {
			snapshot := testRemoteSnapshot(t)
			test.update(&snapshot)
			var requestBody commandRequest
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests = append(requests, request.Method+" "+request.URL.Path)
				switch request.URL.Path {
				case commandsPath:
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
				case snapshotPath:
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
			if !reflect.DeepEqual(requests, []string{"POST " + commandsPath, "GET " + snapshotPath}) {
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
						if r.URL.Path == snapshotPath {
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

func TestDecodeStrictSnapshotValidatesJSONAndState(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantErr    bool
	}{
		{name: "partial status", body: `{"run":{"state":"running"}}`},
		{name: "starting", body: `{"run":{"state":"starting"}}`},
		{name: "stopping", body: `{"run":{"state":"stopping"}}`},
		{name: "faulted", body: `{"run":{"state":"faulted"}}`},
		{name: "missing and null unrelated values", body: `{"run":{"state":"running"},"reader":null,"throttler":{"requestedTps":null,"installed":null}}`},
		{name: "missing state", body: `{"run":{}}`, wantErr: true},
		{name: "null state", body: `{"run":{"state":null}}`, wantErr: true},
		{name: "invalid state", body: `{"run":{"state":"invalid"}}`, wantErr: true},
		{name: "old reader rate", body: `{"run":{"state":"running"},"reader":{"readTps":1}}`, wantErr: true},
		{name: "old throttler rate", body: `{"run":{"state":"running"},"throttler":{"admittedTps":1}}`, wantErr: true},
		{name: "old input rate", body: `{"run":{"state":"running"},"readerChannel":{"inputTransactionsPerSecond":1}}`, wantErr: true},
		{name: "old output rate", body: `{"run":{"state":"running"},"senderChannel":{"outputTransactionsPerSecond":1}}`, wantErr: true},
		{name: "reader batch size", body: `{"run":{"state":"running"},"reader":{"readBatchSize":1}}`, wantErr: true},
		{name: "source directory", body: `{"run":{"state":"running"},"reader":{"sourceDirectory":"data"}}`, wantErr: true},
		{name: "reader capacity", body: `{"run":{"state":"running"},"readerChannel":{"capacity":1}}`, wantErr: true},
		{name: "sender capacity", body: `{"run":{"state":"running"},"senderChannel":{"capacity":1}}`, wantErr: true},
		{name: "reader buffered transactions", body: `{"run":{"state":"running"},"readerChannel":{"bufferedTransactions":1}}`, wantErr: true},
		{name: "sender buffered transactions", body: `{"run":{"state":"running"},"senderChannel":{"bufferedTransactions":1}}`, wantErr: true},
		{name: "source error category", body: `{"run":{"state":"running"},"reader":{"sourceError":{"category":"source"}}}`, wantErr: true},
		{name: "source error relative path", body: `{"run":{"state":"running"},"reader":{"sourceError":{"relativePath":"a.parquet"}}}`, wantErr: true},
		{name: "embedded config", body: `{"run":{"state":"running"},"config":{}}`, wantErr: true},
		{name: "unknown root field", body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: true},
		{name: "unknown nested field", body: `{"run":{"state":"running"},"reader":{"unexpected":true}}`, wantErr: true},
		{name: "wrong known type", body: `{"run":{"state":"running"},"throttler":{"requestedTps":"0"}}`, wantErr: true},
		{name: "array root", body: `[]`, wantErr: true},
		{name: "null root", body: `null`, wantErr: true},
		{name: "two objects", body: `{"run":{"state":"running"}} {}`, wantErr: true},
		{name: "trailing null", body: `{"run":{"state":"running"}} null`, wantErr: true},
		{name: "trailing garbage", body: `{"run":{"state":"running"}} broken`, wantErr: true},
		{name: "malformed JSON", body: `{"run":`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeStrictSnapshot(strings.NewReader(test.body))
			if (err != nil) != test.wantErr {
				t.Fatalf("decode error=%v, wantErr=%v", err, test.wantErr)
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

func TestDecodeStrictConfig(t *testing.T) {
	data, err := os.ReadFile("testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStrictConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := configResponse{
		ReaderWorkersMax: 7, SenderWorkersMax: 32, RequestedTpsMax: 4000000,
		ReadBatchSize: 20, ReaderChannelCapacity: 51, SenderChannelCapacity: 71, SourcePath: "reader-source",
	}
	if got != want {
		t.Fatalf("decoded config = %+v, want %+v", got, want)
	}
	for _, test := range []struct{ name, body string }{
		{name: "unknown field", body: `{"initial":1}`},
		{name: "wrong type", body: `{"readBatchSize":"20"}`},
		{name: "array", body: `[]`},
		{name: "null", body: `null`},
		{name: "malformed", body: `{`},
		{name: "second object", body: string(data) + `{}`},
		{name: "trailing null", body: string(data) + `null`},
		{name: "trailing garbage", body: string(data) + `broken`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeStrictConfig(strings.NewReader(test.body)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestDecodeStrictJSONRejectsNullSnapshot(t *testing.T) {
	_, err := decodeStrictJSON[runtimeSnapshot](strings.NewReader(`null`), "snapshot")
	if err == nil || err.Error() != "decode snapshot: expected object" {
		t.Fatalf("decode error = %v, want expected object", err)
	}
}

func TestFormatSnapshot(t *testing.T) {
	status := runtimeSnapshot{
		Run: runSnapshot{State: runStateFaulted, ElapsedMs: 1250, TotalTransactions: 42},
		Reader: readerSnapshot{
			Workers: 3, WorkerStates: []readerWorkerState{
				{Activity: readerWireReading}, {Activity: readerWireIdle, Draining: true},
			}, RowsRead: 99,
			SourceError: &readerSourceError{Operation: "read", Path: "broken.parquet", Message: "corrupt"},
		},
		Throttler:     throttlerSnapshot{RequestedTps: 100, Installed: true},
		ReaderChannel: channelSnapshot{DepthBatches: 2, InputTransactionsTotal: 1, OutputTransactionsTotal: 2},
		SenderChannel: channelSnapshot{DepthBatches: 1, BlockedSenders: 1, OldestBlockedSenderMs: 7, BlockedMs: 8, InputTransactionsTotal: 3, OutputTransactionsTotal: 4},
		Sender: senderSnapshot{Workers: 5, WorkerStates: []senderWorkerState{
			{Activity: senderWireInFlight}, {Activity: senderWireInFlight},
			{Activity: senderWireIdle}, {Activity: senderWireBackoff, Draining: true},
		}},
	}

	got := formatSnapshot(status, configResponse{ReadBatchSize: 10, ReaderChannelCapacity: 3, SenderChannelCapacity: 4})
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
	var output strings.Builder
	writeSnapshotChannel(&output, "Reader channel", channelSnapshot{
		DepthBatches: maxExactJSONInteger,
	}, maxExactJSONInteger, 1500, 1000)
	if !strings.Contains(output.String(), "approximately 135107988821114") ||
		!strings.Contains(output.String(), " transactions\n") {
		t.Fatalf("large transaction estimate overflowed or changed format: %s", output.String())
	}
}

func TestFormatSnapshotSanitizesSnapshotStrings(t *testing.T) {
	status := runtimeSnapshot{
		Run: runSnapshot{State: runState("running\n\x1b[2J")},
		Reader: readerSnapshot{
			SourceError: &readerSourceError{
				Operation: "operation\t",
				Path:      "path\x7f",
				Message:   "message\n\x1b[0m",
			},
		},
	}

	got := formatSnapshot(status, configResponse{SourcePath: "source\r\n\x1b[31m"})
	if strings.IndexFunc(got, func(character rune) bool {
		return character != '\n' && unicode.IsControl(character)
	}) >= 0 {
		t.Fatalf("card contains unsafe control byte: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(formatSnapshot(runtimeSnapshot{
		Run:       runSnapshot{State: runStateRunning},
		Throttler: throttlerSnapshot{Installed: true},
	}, configResponse{}), "\n") || !strings.HasSuffix(got, "\n") {
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
	card := formatSnapshot(runtimeSnapshot{
		Run: runSnapshot{State: runStateStarting},
	}, configResponse{})
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
		{name: "invalid snapshot", endpoint: snapshotPath, body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: "GET " + snapshotPath + ": decode snapshot"},
		{name: "invalid config", endpoint: configPath, body: `{"unexpected":true}`, wantErr: "GET " + configPath + ": decode config"},
		{name: "snapshot HTTP error", endpoint: snapshotPath, status: http.StatusServiceUnavailable, wantErr: "HTTP 503 Service Unavailable GET " + snapshotPath},
		{name: "config HTTP error", endpoint: configPath, status: http.StatusServiceUnavailable, wantErr: "HTTP 503 Service Unavailable GET " + configPath},
		{name: "truncated snapshot", endpoint: snapshotPath, body: `{`, shortBody: true, wantErr: "read GET " + snapshotPath},
		{name: "truncated config", endpoint: configPath, body: `{`, shortBody: true, wantErr: "read GET " + configPath},
		{name: "snapshot transport error", endpoint: snapshotPath, wantErr: "GET " + snapshotPath},
		{name: "config transport error", endpoint: configPath, wantErr: "GET " + configPath},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				w.Header().Set("Connection", "close")
				if r.Method != http.MethodGet || (r.URL.Path != snapshotPath && r.URL.Path != configPath) {
					t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				if r.URL.Path != test.endpoint {
					body := snapshotBody
					if r.URL.Path == configPath {
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
			wantRequests := []string{configPath, snapshotPath}
			if test.endpoint == configPath {
				wantRequests = []string{configPath}
			}
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
	if !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "POST "+commandsPath) || stdout.Len() != 0 {
		t.Fatalf("error=%v stdout=%q, want POST transport failure", err, stdout.String())
	}
	if got := <-received; got != "POST "+commandsPath {
		t.Fatalf("request = %q, want POST %s", got, commandsPath)
	}
}

func TestFormatSnapshotBeforeRunShowsZeroAverages(t *testing.T) {
	snapshot := runtimeSnapshot{Reader: readerSnapshot{RowsRead: 9}, SenderChannel: channelSnapshot{InputTransactionsTotal: 12, OutputTransactionsTotal: 6}}
	got := formatSnapshot(snapshot, configResponse{})
	for _, line := range []string{"Average read TPS:         0.0", "Average admitted TPS:     0.0", "Average input/output TPS: 0.0 / 0.0"} {
		if !strings.Contains(got, line) {
			t.Fatalf("missing %q in %q", line, got)
		}
	}
}
