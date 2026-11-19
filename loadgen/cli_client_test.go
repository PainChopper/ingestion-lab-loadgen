package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		{target: "throttle", requestAction: "set-throttler-installed", value: false, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = false }},
		{target: "throttle", requestAction: "set-throttler-installed", value: true, update: func(snapshot *runtimeSnapshot) { snapshot.Throttler.Installed = true }},
	} {
		t.Run(test.target+"/"+fmt.Sprint(test.value), func(t *testing.T) {
			snapshot := testRemoteSnapshot(t)
			test.update(&snapshot)
			var requestBody commandRequest
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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

func testRemoteSnapshot(t *testing.T) runtimeSnapshot {
	t.Helper()
	return runtimeSnapshot{
		Run:    runSnapshot{State: runStateRunning},
		Config: snapshotConfig(testConfig(t)),
	}
}

func TestFormatSnapshot(t *testing.T) {
	status := runtimeSnapshot{
		Run: runSnapshot{State: runStateFaulted, ElapsedMs: 1250, TotalTransactions: 42},
		Reader: readerSnapshot{
			Workers: 3, LiveWorkers: 2, ReadingWorkers: 1, IdleWorkers: 1, BlockedWorkers: 0,
			DrainingWorkers: 1, ReadTps: 12.34, RowsRead: 99,
			SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "broken.parquet", Message: "corrupt"},
		},
		Throttler:     throttlerSnapshot{RequestedTps: 100, AdmittedTps: 9.87, Installed: true},
		ReaderChannel: channelSnapshot{Capacity: 3, DepthBatches: 2, BufferedTransactions: 20, SentTransactionsPerSecond: 1.25, ReceivedTransactionsPerSecond: 2.5},
		SenderChannel: channelSnapshot{Capacity: 4, DepthBatches: 1, BufferedTransactions: 10, BlockedSenders: 1, OldestBlockedSenderMs: 7, BlockedMs: 8, SentTransactionsPerSecond: 3.75, ReceivedTransactionsPerSecond: 4.5},
		Sender:        senderSnapshot{Workers: 5, LiveWorkers: 4, InFlightWorkers: 2, IdleWorkers: 1, BackoffWorkers: 1, DrainingWorkers: 1},
	}

	got := formatSnapshot(status)
	want := "INGESTION LAB LOADGEN STATUS\n" +
		"State:                    faulted\n" +
		"Elapsed:                  1.25s\n" +
		"Transactions:             42\n\n" +
		"Throttler\n" +
		"Requested TPS:            100\n" +
		"Admitted TPS:             9.9\n" +
		"Installed:                true\n\n" +
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
}

func TestFormatSnapshotSanitizesSnapshotStrings(t *testing.T) {
	status := runtimeSnapshot{
		Run: runSnapshot{State: runState("running\n\x1b[2J")},
		Reader: readerSnapshot{
			SourceDirectory: "source\r\n\x1b[31m",
			SourceError: &readerSourceError{
				Category:     "category\x00",
				Operation:    "operation\t",
				RelativePath: "path\x7f",
				Message:      "message\n\x1b[0m",
			},
		},
	}

	got := formatSnapshot(status)
	if strings.IndexFunc(got, func(character rune) bool {
		return character != '\n' && unicode.IsControl(character)
	}) >= 0 {
		t.Fatalf("card contains unsafe control byte: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(formatSnapshot(runtimeSnapshot{
		Run:       runSnapshot{State: runStateRunning},
		Throttler: throttlerSnapshot{Installed: true},
	}), "\n") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("card layout changed or has no final newline: %q", got)
	}
	for _, want := range []string{
		`State:                    running\x0a\x1b[2J`,
		`Source:                   source\x0d\x0a\x1b[31m`,
		`Source error:             category\x00/operation\x09 path\x7f: message\x0a\x1b[0m`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing sanitized value %q: %q", want, got)
		}
	}
}

func TestFormatSnapshotStartingSourceGolden(t *testing.T) {
	card := formatSnapshot(runtimeSnapshot{
		Run: runSnapshot{State: runStateStarting},
	})
	if !strings.Contains(card, "Source:                   none\nSource error:             none\n\nReader channel\n") {
		t.Fatalf("idle source fields are not adjacent golden values: %q", card)
	}
}

func TestRunRemoteCLIStatusFetchesOneSnapshot(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantErr    bool
	}{
		{name: "success", body: mustMarshalJSON(t, testRemoteSnapshot(t))},
		{name: "invalid schema", body: `{"run":{"state":"running"},"unexpected":true}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != snapshotPath {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, snapshotPath)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			var stdout bytes.Buffer
			err := runRemoteCLI(t.Context(), cliCommand{remoteAction: "status", remoteURL: server.URL}, &stdout)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "decode snapshot") || stdout.Len() != 0 {
					t.Fatalf("error=%v stdout=%q", err, stdout.String())
				}
			} else if err != nil || !strings.HasPrefix(stdout.String(), "INGESTION LAB LOADGEN STATUS\n") {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
			if requests != 1 {
				t.Fatalf("requests=%d, want 1", requests)
			}
		})
	}
}
