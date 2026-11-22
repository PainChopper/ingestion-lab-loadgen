package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/control"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"
)

func TestConfigEndpointIndependentOfRuntime(t *testing.T) {
	want := testStartupConfig(t)
	for _, state := range []api.RunState{api.RunStateStarting, api.RunStateRunning, api.RunStateStopping, api.RunStateFaulted} {
		t.Run(string(state), func(t *testing.T) {
			startup := want
			requests := 0
			port := testRuntime{
				snapshot: func(context.Context) (api.Snapshot, error) {
					requests++
					if state == api.RunStateFaulted {
						return api.Snapshot{}, errTestRuntimeUnavailable
					}
					return api.Snapshot{Run: api.RunSnapshot{State: state}}, nil
				},
				execute: func(context.Context, control.Command) (bool, error) {
					requests++
					return false, nil
				},
			}
			mux := newTestMux(t, port, startup, zap.NewNop())
			// The response must retain the values captured before later owner changes.
			startup.ReadBatchSize++
			recorder := httptest.NewRecorder()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			mux.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, api.ConfigPath, nil))
			var got api.ConfigResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" || got != want {
				t.Fatalf("config response = %d %v %+v, want %+v", recorder.Code, recorder.Header(), got, want)
			}
			var fields map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 7 || requests != 0 {
				t.Fatalf("config fields=%v runtime requests=%d", fields, requests)
			}
		})
	}
}

func TestConfigEndpointHeadAndUnsupportedMethod(t *testing.T) {
	mux := newTestMux(t, testRuntime{}, testStartupConfig(t), zap.NewNop())
	server := httptest.NewServer(mux)
	defer server.Close()
	response, err := server.Client().Head(server.URL + api.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || len(body) != 0 {
		t.Fatalf("HEAD config = %d %v %q", response.StatusCode, response.Header, body)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, api.ConfigPath, nil))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST config = %d %v", recorder.Code, recorder.Header())
	}
}

func TestCommandsHandlerRejectsGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, api.CommandsPath, nil)
	rec := httptest.NewRecorder()

	newTestMux(t, testRuntime{}, testStartupConfig(t), zap.NewNop()).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
	allow := response.Header.Get("Allow")
	if allow != http.MethodPost {
		t.Errorf("allow header = %q, want %q", allow, http.MethodPost)
	}
	if rec.Body.String() != "Method Not Allowed\n" {
		t.Errorf("405 body=%q", rec.Body.String())
	}
}

func TestCommandsHandlerRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed-json", "malformed-json"},
		{"unknown-action", `{"action":"unknown-action","value":2}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var commands []control.Command
			body := strings.NewReader(test.body)
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, body)
			rec := httptest.NewRecorder()
			newTestMux(t, recordCommands(&commands), testStartupConfig(t), zap.NewNop()).ServeHTTP(rec, req)
			response := rec.Result()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusBadRequest)
			}
			if len(commands) != 0 {
				t.Fatal("invalid command was dispatched")
			}
		})
	}
}

func TestCommandsHandlerRejectsOversizedBody(t *testing.T) {
	const command = `{"action":"set-reader-workers","value":2}`
	const limit = 4096
	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON exceeds limit", "!" + strings.Repeat("x", limit)},
		{"JSON exceeds limit", `{"action":"` + strings.Repeat("x", limit) + `","value":2}`},
		{"trailing whitespace exceeds limit", command + strings.Repeat(" ", limit+1-len(command))},
	}
	handler := newTestMux(t, testRuntime{}, testStartupConfig(t), zap.NewNop())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

func TestCommandsHandlerRejectsBodyReadFailure(t *testing.T) {
	var commands []control.Command
	req := httptest.NewRequest(http.MethodPost, api.CommandsPath, nil)
	req.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
	rec := httptest.NewRecorder()
	commandsHandler(recordCommands(&commands)).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "invalid request body\n" || len(commands) != 0 {
		t.Fatalf("status=%d body=%q dispatched=%d", rec.Code, rec.Body.String(), len(commands))
	}
}

func TestThrottlerCommandValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      int
		kind      string
		installed bool
		value     int
	}{
		{name: "TPS missing", body: `{"action":"set-requested-tps"}`, want: http.StatusBadRequest},
		{name: "TPS null", body: `{"action":"set-requested-tps","value":null}`, want: http.StatusBadRequest},
		{name: "TPS fractional", body: `{"action":"set-requested-tps","value":100.5}`, want: http.StatusBadRequest},
		{name: "TPS string", body: `{"action":"set-requested-tps","value":"100"}`, want: http.StatusBadRequest},
		{name: "TPS negative", body: `{"action":"set-requested-tps","value":-100}`, want: http.StatusBadRequest},
		{name: "TPS over max", body: `{"action":"set-requested-tps","value":4100000}`, want: http.StatusBadRequest},
		{name: "TPS extra field", body: `{"action":"set-requested-tps","value":2000000,"other":1}`, want: http.StatusBadRequest},
		{name: "TPS trailing JSON", body: `{"action":"set-requested-tps","value":2000000}{}`, want: http.StatusBadRequest},
		{name: "mode missing", body: `{"action":"set-throttler-installed"}`, want: http.StatusBadRequest},
		{name: "mode null", body: `{"action":"set-throttler-installed","value":null}`, want: http.StatusBadRequest},
		{name: "mode number", body: `{"action":"set-throttler-installed","value":1}`, want: http.StatusBadRequest},
		{name: "quoted bool", body: `{"action":"set-throttler-installed","value":"false"}`, want: http.StatusBadRequest},
		{name: "installed trailing JSON", body: `{"action":"set-throttler-installed","value":false}{}`, want: http.StatusBadRequest},
		{name: "mode extra field", body: `{"action":"set-throttler-installed","value":false,"other":1}`, want: http.StatusBadRequest},
		{name: "TPS zero", body: `{"action":"set-requested-tps","value":0}`, want: http.StatusOK, kind: api.SetRequestedTPS},
		{name: "TPS arbitrary integer", body: `{"action":"set-requested-tps","value":2000001}`, want: http.StatusOK, kind: api.SetRequestedTPS, value: 2000001},
		{name: "TPS maximum", body: `{"action":"set-requested-tps","value":4000000}`, want: http.StatusOK, kind: api.SetRequestedTPS, value: 4000000},
		{name: "mode installed", body: `{"action":"set-throttler-installed","value":true}`, want: http.StatusOK, kind: api.SetThrottlerInstalled, installed: true},
		{name: "mode bypass", body: `{"action":"set-throttler-installed","value":false}`, want: http.StatusOK, kind: api.SetThrottlerInstalled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var commands []control.Command
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			newTestMux(t, recordCommands(&commands), testStartupConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest {
				if len(commands) != 0 {
					t.Fatalf("invalid command was dispatched: %+v", commands)
				}
				return
			}
			if len(commands) != 1 {
				t.Fatalf("dispatched %d commands, want one", len(commands))
			}
			command := commands[0]
			if command.Action != test.kind ||
				command.Action == api.SetRequestedTPS && command.Value != test.value ||
				command.Action == api.SetThrottlerInstalled && command.Installed != test.installed {
				t.Errorf("command = %+v, want action %q, value %d, installed %t", command, test.kind, test.value, test.installed)
			}
		})
	}
}

func TestSenderCommandValidation(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
		kind       string
		value      int
	}{
		{"workers minimum", `{"action":"set-sender-workers","value":1}`, http.StatusOK, api.SetSenderWorkers, 1},
		{"workers initial", `{"action":"set-sender-workers","value":32}`, http.StatusOK, api.SetSenderWorkers, 32},
		{"missing", `{"action":"set-sender-workers"}`, http.StatusBadRequest, "", 0},
		{"null", `{"action":"set-sender-workers","value":null}`, http.StatusBadRequest, "", 0},
		{"fractional", `{"action":"set-sender-workers","value":1.5}`, http.StatusBadRequest, "", 0},
		{"string", `{"action":"set-sender-workers","value":"1"}`, http.StatusBadRequest, "", 0},
		{"under minimum", `{"action":"set-sender-workers","value":0}`, http.StatusBadRequest, "", 0},
		{"over maximum", `{"action":"set-sender-workers","value":33}`, http.StatusBadRequest, "", 0},
		{"extra key", `{"action":"set-sender-workers","value":1,"other":1}`, http.StatusBadRequest, "", 0},
		{"trailing JSON", `{"action":"set-sender-workers","value":1}{}`, http.StatusBadRequest, "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var commands []control.Command
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			newTestMux(t, recordCommands(&commands), testStartupConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest {
				if len(commands) != 0 {
					t.Fatalf("invalid command was dispatched: %+v", commands)
				}
				return
			}
			if len(commands) != 1 {
				t.Fatalf("dispatched %d commands, want one", len(commands))
			}
			command := commands[0]
			if command.Action != test.kind || command.Value != test.value {
				t.Errorf("command = %+v, want action %q and value %d", command, test.kind, test.value)
			}
		})
	}
}

func TestReaderWorkersCommandValidation(t *testing.T) {
	const command = `{"action":"set-reader-workers","value":1}`
	tests := []struct {
		name, body string
		want       int
		value      int
	}{
		{"minimum", command, http.StatusOK, 1},
		{"body at the limit", command + strings.Repeat(" ", 4096-len(command)), http.StatusOK, 1},
		{"maximum", `{"action":"set-reader-workers","value":7}`, http.StatusOK, 7},
		{"zero", `{"action":"set-reader-workers","value":0}`, http.StatusBadRequest, 0},
		{"eight", `{"action":"set-reader-workers","value":8}`, http.StatusBadRequest, 0},
		{"missing", `{"action":"set-reader-workers"}`, http.StatusBadRequest, 0},
		{"null", `{"action":"set-reader-workers","value":null}`, http.StatusBadRequest, 0},
		{"fractional", `{"action":"set-reader-workers","value":1.5}`, http.StatusBadRequest, 0},
		{"string", `{"action":"set-reader-workers","value":"1"}`, http.StatusBadRequest, 0},
		{"extra key", `{"action":"set-reader-workers","value":1,"other":1}`, http.StatusBadRequest, 0},
		{"trailing JSON", `{"action":"set-reader-workers","value":1}{}`, http.StatusBadRequest, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var commands []control.Command
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			newTestMux(t, recordCommands(&commands), testStartupConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest {
				if len(commands) != 0 {
					t.Fatalf("invalid command was dispatched: %+v", commands)
				}
				return
			}
			if len(commands) != 1 {
				t.Fatalf("dispatched %d commands, want one", len(commands))
			}
			command := commands[0]
			if command.Action != api.SetReaderWorkers || command.Value != test.value {
				t.Errorf("command = %+v, want Reader workers %d", command, test.value)
			}
		})
	}
}

func TestDeveloperRoutes(t *testing.T) {
	requests := 0
	port := testRuntime{
		snapshot: func(context.Context) (api.Snapshot, error) {
			requests++
			return api.Snapshot{}, errTestRuntimeUnavailable
		},
		execute: func(context.Context, control.Command) (bool, error) {
			requests++
			return false, errTestRuntimeUnavailable
		},
	}
	mux := newTestMux(t, port, testStartupConfig(t), zap.NewNop())
	// Swagger uses the platform MIME database for JavaScript assets.
	javascriptContentType := mime.TypeByExtension(".js")
	tests := []struct {
		name        string
		path        string
		contentType string
		contains    string
	}{
		{"spec", developerOpenAPIPath, "application/json", `"openapi": "3.0.3"`},
		{"swagger", developerSwaggerUIPath, "text/html; charset=utf-8", `swagger-ui-bundle.js`},
		{"swagger initializer", developerSwaggerUIPath + "swagger-initializer.js", javascriptContentType, developerOpenAPIPath},
		{"swagger bundle", developerSwaggerUIPath + "swagger-ui-bundle.js", javascriptContentType, "SwaggerUIBundle"},
		{"swagger stylesheet", developerSwaggerUIPath + "swagger-ui.css", "text/css; charset=utf-8", "swagger-ui"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
			if !bytes.Contains(recorder.Body.Bytes(), []byte(test.contains)) {
				t.Errorf("body does not contain %q", test.contains)
			}
		})
	}
	if requests != 0 {
		t.Fatal("developer GET dispatched a runtime command")
	}
	specRecorder := httptest.NewRecorder()
	mux.ServeHTTP(specRecorder, httptest.NewRequest(http.MethodGet, developerOpenAPIPath, nil))
	if !bytes.Equal(specRecorder.Body.Bytes(), readOpenAPISpec(t)) {
		t.Error("served OpenAPI document differs from the checked-in spec")
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, developerSwaggerUIPath+"swagger-initializer.js", nil))
	for _, setting := range []string{`tryItOutEnabled: blankToUndefinedBool('true')`, `validatorUrl: blankToUndefined('none')`} {
		if !strings.Contains(recorder.Body.String(), setting) {
			t.Errorf("Swagger UI config is missing %q", setting)
		}
	}
}

func TestServeMuxHEADUsesSnapshotAndReturnsNoBody(t *testing.T) {
	mux := newTestMux(t, snapshotRuntime(api.Snapshot{Run: api.RunSnapshot{State: api.RunStateRunning}}), testStartupConfig(t), zap.NewNop())
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, test := range []struct {
		path, allow string
		status      int
	}{
		{path: api.SnapshotPath, status: http.StatusOK},
		{path: api.CommandsPath, status: http.StatusMethodNotAllowed, allow: http.MethodPost},
	} {
		response, err := server.Client().Head(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || len(body) != 0 || response.StatusCode != test.status || response.Header.Get("Allow") != test.allow {
			t.Fatalf("HEAD %s: status=%d body=%q allow=%q read=%v close=%v", test.path, response.StatusCode, body, response.Header.Get("Allow"), err, closeErr)
		}
		if test.status == http.StatusOK && response.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("HEAD snapshot Content-Type=%q", response.Header.Get("Content-Type"))
		}
	}
}

func TestHTTPWaitFailuresReturnServiceUnavailable(t *testing.T) {
	cfg := testStartupConfig(t)
	for _, path := range []string{api.SnapshotPath, api.CommandsPath} {
		for _, cause := range []string{"cancel", "deadline", "event loop exit"} {
			t.Run(path+"/"+cause, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
					defer cancel()
					loopDone := make(chan struct{})
					awaitFailure := func(ctx context.Context) error {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-loopDone:
							return errTestRuntimeUnavailable
						}
					}
					port := testRuntime{
						snapshot: func(ctx context.Context) (api.Snapshot, error) { return api.Snapshot{}, awaitFailure(ctx) },
						execute:  func(ctx context.Context, _ control.Command) (bool, error) { return false, awaitFailure(ctx) },
					}
					mux := newTestMux(t, port, cfg, zap.NewNop())
					method, body := http.MethodGet, ""
					if path == api.CommandsPath {
						method, body = http.MethodPost, `{"action":"set-reader-workers","value":1}`
					}
					request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
					recorder := httptest.NewRecorder()
					returned := make(chan struct{})
					go func() {
						mux.ServeHTTP(recorder, request)
						close(returned)
					}()
					synctest.Wait()
					select {
					case <-returned:
						t.Fatal("handler returned before the runtime wait failed")
					default:
					}
					switch cause {
					case "cancel":
						cancel()
					case "deadline":
						time.Sleep(time.Millisecond)
					case "event loop exit":
						close(loopDone)
					}
					<-returned
					if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "runtime unavailable\n" {
						t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
					}
				})
			})
		}
	}
}

func TestHTTPCommandReceiptsHaveEmptyBody(t *testing.T) {
	for _, test := range []struct {
		name    string
		applied bool
	}{
		{name: "accepted", applied: true},
		{name: "conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			applied := test.applied
			port := testRuntime{execute: func(context.Context, control.Command) (bool, error) { return applied, nil }}
			mux := newTestMux(t, port, testStartupConfig(t), zap.NewNop())
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(`{"action":"set-reader-workers","value":1}`)))
			want := http.StatusOK
			if !applied {
				want = http.StatusConflict
			}
			if recorder.Code != want || recorder.Body.Len() != 0 {
				t.Fatalf("applied=%t status=%d body=%q", applied, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestServeMuxHasNoLocalIngest(t *testing.T) {
	mux := newTestMux(t, testRuntime{}, testStartupConfig(t), zap.NewNop())
	for _, path := range []string{"/internal/test/ingest", "/internal/ingest", api.SnapshotPath + "/extra", api.CommandsPath + "/extra"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, recorder.Code)
		}
	}
}

type failingSnapshotWriter struct{ *httptest.ResponseRecorder }

func (w failingSnapshotWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestConfigEncodingFailureLogsItsOwnEventAndCause(t *testing.T) {
	core, events := observer.New(zap.WarnLevel)
	configHandler(api.ConfigResponse{}, zap.New(core)).ServeHTTP(
		failingSnapshotWriter{ResponseRecorder: httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, api.ConfigPath, nil),
	)
	entries := events.FilterMessage("config response encoding failed").All()
	if len(entries) != 1 || entries[0].ContextMap()["event"] != "config_response_failed" || entries[0].ContextMap()["error"] != io.ErrClosedPipe.Error() {
		t.Fatalf("encoding failure log=%v", entries)
	}
}

func TestSnapshotEncodingFailureLogsItsOwnEventAndCause(t *testing.T) {
	port := snapshotRuntime(api.Snapshot{Run: api.RunSnapshot{State: api.RunStateRunning}})
	core, events := observer.New(zap.WarnLevel)
	snapshotHandler(port, zap.New(core)).ServeHTTP(
		failingSnapshotWriter{ResponseRecorder: httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil),
	)
	entries := events.FilterMessage("snapshot response encoding failed").All()
	if len(entries) != 1 || entries[0].ContextMap()["event"] != "snapshot_response_failed" || entries[0].ContextMap()["error"] != io.ErrClosedPipe.Error() {
		t.Fatalf("encoding failure log=%v", entries)
	}
}

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	expected := api.Snapshot{
		Run: api.RunSnapshot{State: api.RunStateRunning, TotalTransactions: 46, ElapsedMs: 1234},
		Reader: api.ReaderSnapshot{
			Workers: 1, WorkerStates: []api.ReaderWorkerState{
				{Activity: api.ReaderReading}, {Activity: api.ReaderReading, Draining: true},
			},
			RowsRead:    47,
			SourceError: &api.SourceError{Operation: "read", Path: "input.parquet", Message: "corrupt parquet"},
		},
		Throttler:     api.ThrottlerSnapshot{RequestedTps: 200, Installed: true},
		Sender:        api.SenderSnapshot{Workers: 32, WorkerStates: []api.SenderWorkerState{}},
		ReaderChannel: api.ChannelSnapshot{DepthBatches: 6, BlockedSenders: 1, OldestBlockedSenderMs: 12, BlockedMs: 34, InputTransactionsTotal: 3, OutputTransactionsTotal: 1},
		SenderChannel: api.ChannelSnapshot{DepthBatches: 4, BlockedSenders: 2, OldestBlockedSenderMs: 13, BlockedMs: 35, InputTransactionsTotal: 4, OutputTransactionsTotal: 2},
	}
	rec := httptest.NewRecorder()
	snapshotHandler(snapshotRuntime(expected), zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reply code = %d, want %d", rec.Code, http.StatusOK)
	}
	var actual api.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if got := actual; !reflect.DeepEqual(got, expected) {
		t.Errorf("actual = %+v, want %+v", got, expected)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["config"]; exists {
		t.Fatal("snapshot contains immutable config")
	}
	reader := raw["reader"].(map[string]any)
	if reader["workers"] != float64(1) || len(reader["workerStates"].([]any)) != 2 {
		t.Fatalf("shrink must retain target and both live workers: %v", reader)
	}
	if states, ok := raw["sender"].(map[string]any)["workerStates"].([]any); !ok || len(states) != 0 {
		t.Fatalf("empty Sender must be [], got %v", raw["sender"])
	}
}

func TestSnapshotHandlerIncludesZeroAndNullValues(t *testing.T) {
	port := snapshotRuntime(api.Snapshot{Run: api.RunSnapshot{State: api.RunStateStarting}, Reader: api.ReaderSnapshot{WorkerStates: []api.ReaderWorkerState{}}, Sender: api.SenderSnapshot{WorkerStates: []api.SenderWorkerState{}}})
	recorder := httptest.NewRecorder()
	snapshotHandler(port, zap.NewNop()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil))
	var root map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	var actual api.Snapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if want := (api.Snapshot{Run: api.RunSnapshot{State: api.RunStateStarting}, Reader: api.ReaderSnapshot{WorkerStates: []api.ReaderWorkerState{}}, Sender: api.SenderSnapshot{WorkerStates: []api.SenderWorkerState{}}}); !reflect.DeepEqual(actual, want) {
		t.Errorf("zero snapshot = %+v, want %+v", actual, want)
	}
	var run, reader map[string]json.RawMessage
	if err := json.Unmarshal(root["run"], &run); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		t.Fatal(err)
	}
	var sender map[string]json.RawMessage
	if err := json.Unmarshal(root["sender"], &sender); err != nil {
		t.Fatal(err)
	}
	if string(reader["workerStates"]) != "[]" || string(sender["workerStates"]) != "[]" {
		t.Fatalf("starting worker lists = %s / %s", reader["workerStates"], sender["workerStates"])
	}
	if string(run["elapsedMs"]) != "0" || string(reader["rowsRead"]) != "0" || string(reader["sourceError"]) != "null" {
		t.Errorf("starting snapshot body = %v", root)
	}
	var throttler map[string]json.RawMessage
	if err := json.Unmarshal(root["throttler"], &throttler); err != nil {
		t.Fatal(err)
	}
	if string(throttler["installed"]) != "false" {
		t.Errorf("zero snapshot installed = %s, want false", throttler["installed"])
	}
}

func TestSnapshotHandlerRejectsPost(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestMux(t, testRuntime{}, testStartupConfig(t), zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.SnapshotPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if rec.Body.String() != "Method Not Allowed\n" {
		t.Errorf("405 body=%q", rec.Body.String())
	}
}

var errTestRuntimeUnavailable = errors.New("runtime unavailable")

type testRuntime struct {
	snapshot func(context.Context) (api.Snapshot, error)
	execute  func(context.Context, control.Command) (bool, error)
	valid    func(control.Command) bool
}

func (rt testRuntime) Snapshot(ctx context.Context) (api.Snapshot, error) {
	if rt.snapshot == nil {
		return api.Snapshot{}, errTestRuntimeUnavailable
	}
	return rt.snapshot(ctx)
}

func (rt testRuntime) Execute(ctx context.Context, command control.Command) (bool, error) {
	if rt.execute == nil {
		return false, errTestRuntimeUnavailable
	}
	return rt.execute(ctx, command)
}

func (rt testRuntime) ValidCommand(command control.Command) bool {
	return rt.valid(command)
}

func snapshotRuntime(snapshot api.Snapshot) testRuntime {
	return testRuntime{snapshot: func(context.Context) (api.Snapshot, error) { return snapshot, nil }}
}

func recordCommands(commands *[]control.Command) testRuntime {
	return testRuntime{execute: func(_ context.Context, command control.Command) (bool, error) {
		*commands = append(*commands, command)
		return true, nil
	}}
}

func newTestMux(t *testing.T, port testRuntime, cfg api.ConfigResponse, logger *zap.Logger) http.Handler {
	t.Helper()
	var limits config.Config
	limits.Reader.Workers.Max = cfg.ReaderWorkersMax
	limits.Sender.Workers.Max = cfg.SenderWorkersMax
	limits.Throttler.RequestedTPS.Max = cfg.RequestedTpsMax
	port.valid = control.New(limits, pipeline.Options{}, logger).ValidCommand
	return NewHandler(port, Options{
		Startup:     cfg,
		Metrics:     promhttp.HandlerFor(prometheus.NewRegistry(), promhttp.HandlerOpts{}),
		OpenAPISpec: readOpenAPISpec(t),
		Logger:      logger,
	})
}

func testStartupConfig(t *testing.T) api.ConfigResponse {
	t.Helper()
	file, err := os.Open("../../testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	cfg, err := api.DecodeConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
