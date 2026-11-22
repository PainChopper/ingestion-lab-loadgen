package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

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
					if err := json.Unmarshal([]byte(mustMarshalJSON(t, testRemoteSnapshot())), &root); err != nil {
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
					code := Run([]string{"set", command.target, command.value, "--url", server.URL}, &stdout, &stderr, nil)
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

func TestRunRemoteCLIStatusFetchesConfigAndSnapshot(t *testing.T) {
	configBody := readTestConfigJSON(t)
	snapshotBody := []byte(mustMarshalJSON(t, testRemoteSnapshot()))
	const wantStatus = `INGESTION LAB LOADGEN STATUS
State:                    running
Elapsed:                  0s
Transactions:             0

Throttler
Requested TPS:            0
Average admitted TPS:     0.0
Installed:                false

Reader
Workers:                  configured=0 live=0 reading=0 idle=0 blocked=0 draining=0
Average read TPS:         0.0
Rows read:                0
Source:                   reader-source
Source error:             none

Reader channel
Queue:                    0/51 batches; approximately 0 transactions
Average input/output TPS: 0.0 / 0.0
Blocked:                  senders=0 oldest=0ms total=0ms

Sender channel
Queue:                    0/71 batches; approximately 0 transactions
Average input/output TPS: 0.0 / 0.0
Blocked:                  senders=0 oldest=0ms total=0ms

Sender
Workers:                  configured=0 live=0 in-flight=0 idle=0 backoff=0 draining=0
`
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
					_, _ = w.Write(body)
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
			err := runRemoteCLI(t.Context(), command{remoteAction: "status", remoteURL: server.URL}, &stdout)
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
			} else if err != nil || stdout.String() != wantStatus {
				t.Fatalf("error=%v stdout=%q, want %q", err, stdout.String(), wantStatus)
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
	cmd := command{
		remoteAction: "set",
		remoteURL:    server.URL,
		remoteSet:    remoteSetCommand{requestAction: api.SetReaderWorkers, value: 1},
	}
	var stdout bytes.Buffer
	err := runRemoteCLI(t.Context(), cmd, &stdout)
	if !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "POST "+api.CommandsPath) || stdout.Len() != 0 {
		t.Fatalf("error=%v stdout=%q, want POST transport failure", err, stdout.String())
	}
	if got := <-received; got != "POST "+api.CommandsPath {
		t.Fatalf("request = %q, want POST %s", got, api.CommandsPath)
	}
}

func testRemoteSnapshot() api.Snapshot {
	return api.Snapshot{
		Run:    api.RunSnapshot{State: api.RunStateRunning},
		Reader: api.ReaderSnapshot{WorkerStates: []api.ReaderWorkerState{}},
		Sender: api.SenderSnapshot{WorkerStates: []api.SenderWorkerState{}},
	}
}

func readTestConfigJSON(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
