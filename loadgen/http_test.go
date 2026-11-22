package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestConfigEndpointIndependentOfRuntime(t *testing.T) {
	data, err := os.ReadFile("testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	var want api.ConfigResponse
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.Reader.Workers.Max = want.ReaderWorkersMax
	cfg.Sender.Workers.Max = want.SenderWorkersMax
	cfg.Throttler.RequestedTPS.Max = want.RequestedTpsMax
	cfg.Reader.ReadBatchSize = want.ReadBatchSize
	cfg.ReaderChannel.Capacity = want.ReaderChannelCapacity
	cfg.SenderChannel.Capacity = want.SenderChannelCapacity
	cfg.Source.Path = want.SourcePath
	for _, state := range []api.RunState{api.RunStateStarting, api.RunStateRunning, api.RunStateStopping, api.RunStateFaulted} {
		t.Run(string(state), func(t *testing.T) {
			owner := newControlState(cfg, zap.NewNop())
			owner.run.state = state
			requests := make(chan runtimeRequest, 1)
			done := make(chan struct{})
			if state == api.RunStateFaulted {
				close(done)
			}
			mux := newServeMux(runtimeRequests{queue: requests, done: done}, newPrometheusMetrics(), owner.config, zap.NewNop())
			// The response must retain the values captured before later owner changes.
			owner.config.Reader.ReadBatchSize++
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
			if len(fields) != 7 || len(requests) != 0 {
				t.Fatalf("config fields=%v runtime requests=%d", fields, len(requests))
			}
		})
	}
}

func TestConfigEndpointHeadAndUnsupportedMethod(t *testing.T) {
	mux := newServeMux(runtimeRequests{}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
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
	commands := make(chan runtimeRequest, 1)
	req := httptest.NewRequest(http.MethodGet, api.CommandsPath, nil)
	rec := httptest.NewRecorder()

	newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, req)
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
			commands := make(chan runtimeRequest, 1)
			body := strings.NewReader(test.body)
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, body)
			rec := httptest.NewRecorder()
			newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, req)
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
	done := make(chan struct{})
	close(done)
	handler := newServeMux(runtimeRequests{done: done}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
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
	commands := make(chan runtimeRequest, 1)
	req := httptest.NewRequest(http.MethodPost, api.CommandsPath, nil)
	req.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
	rec := httptest.NewRecorder()
	commandsHandler(runtimeRequests{queue: commands}, testConfig(t)).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "invalid request body\n" || len(commands) != 0 {
		t.Fatalf("status=%d body=%q dispatched=%d", rec.Code, rec.Body.String(), len(commands))
	}
}

func TestThrottlerCommandValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      int
		kind      runtimeCommandKind
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
		{name: "TPS zero", body: `{"action":"set-requested-tps","value":0}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "TPS arbitrary integer", body: `{"action":"set-requested-tps","value":2000001}`, want: http.StatusOK, kind: setRequestedTPS, value: 2000001},
		{name: "TPS maximum", body: `{"action":"set-requested-tps","value":4000000}`, want: http.StatusOK, kind: setRequestedTPS, value: 4000000},
		{name: "mode installed", body: `{"action":"set-throttler-installed","value":true}`, want: http.StatusOK, kind: setThrottlerInstalled, installed: true},
		{name: "mode bypass", body: `{"action":"set-throttler-installed","value":false}`, want: http.StatusOK, kind: setThrottlerInstalled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			config := testConfig(t)
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), config, zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != test.kind {
						t.Errorf("command kind = %v, want %v", command.kind, test.kind)
					}
					if command.kind == setRequestedTPS && command.value != test.value {
						t.Errorf("TPS value = %d, want %d", command.value, test.value)
					}
					if command.kind == setThrottlerInstalled && command.installed != test.installed {
						t.Errorf("installed = %t, want %t", command.installed, test.installed)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("command handler did not return")
			}
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest && len(commands) != 0 {
				t.Fatal("invalid command was dispatched")
			}
		})
	}
}

func TestSenderCommandValidation(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
		kind       runtimeCommandKind
		value      int
	}{
		{"workers minimum", `{"action":"set-sender-workers","value":1}`, http.StatusOK, setSenderWorkers, 1},
		{"workers initial", `{"action":"set-sender-workers","value":32}`, http.StatusOK, setSenderWorkers, 32},
		{"missing", `{"action":"set-sender-workers"}`, http.StatusBadRequest, 0, 0},
		{"null", `{"action":"set-sender-workers","value":null}`, http.StatusBadRequest, 0, 0},
		{"fractional", `{"action":"set-sender-workers","value":1.5}`, http.StatusBadRequest, 0, 0},
		{"string", `{"action":"set-sender-workers","value":"1"}`, http.StatusBadRequest, 0, 0},
		{"under minimum", `{"action":"set-sender-workers","value":0}`, http.StatusBadRequest, 0, 0},
		{"over maximum", `{"action":"set-sender-workers","value":33}`, http.StatusBadRequest, 0, 0},
		{"extra key", `{"action":"set-sender-workers","value":1,"other":1}`, http.StatusBadRequest, 0, 0},
		{"trailing JSON", `{"action":"set-sender-workers","value":1}{}`, http.StatusBadRequest, 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != test.kind || command.value != test.value {
						t.Errorf("command = %+v, want kind %v value %d", command, test.kind, test.value)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("command handler did not return")
			}
			if recorder.Code != test.want || len(commands) != 0 {
				t.Fatalf("status = %d, queued commands = %d, want %d and zero", recorder.Code, len(commands), test.want)
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
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, api.CommandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				newServeMux(runtimeRequests{queue: commands}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != setReaderWorkers || command.value != test.value {
						t.Errorf("command = %+v, want Reader workers %d", command, test.value)
					}
					dispatch.reply <- true
				case <-time.After(time.Second):
					t.Fatal("valid Reader command was not dispatched")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Reader command handler did not return")
			}
			if recorder.Code != test.want || len(commands) != 0 {
				t.Fatalf("status = %d, queued = %d, want %d and zero", recorder.Code, len(commands), test.want)
			}
		})
	}
}

func TestDeveloperRoutes(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
	mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
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
	if len(requests) != 0 {
		t.Fatal("developer GET dispatched a runtime command")
	}
	specRecorder := httptest.NewRecorder()
	mux.ServeHTTP(specRecorder, httptest.NewRequest(http.MethodGet, developerOpenAPIPath, nil))
	if !bytes.Equal(specRecorder.Body.Bytes(), openAPISpec) {
		t.Error("served OpenAPI document differs from embedded bytes")
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
	requests := make(chan runtimeRequest)
	mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	server := httptest.NewServer(mux)
	defer server.Close()
	go func() {
		(<-requests).(snapshotRequest).reply <- api.Snapshot{Run: api.RunSnapshot{State: api.RunStateRunning}}
	}()
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
	cfg := testConfig(t)
	for _, path := range []string{api.SnapshotPath, api.CommandsPath} {
		for _, stage := range []string{"send", "reply"} {
			for _, cause := range []string{"cancel", "deadline", "event loop exit"} {
				t.Run(path+"/"+stage+"/"+cause, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
						defer cancel()
						queue := make(chan runtimeRequest)
						loopDone := make(chan struct{})
						mux := newServeMux(runtimeRequests{queue: queue, done: loopDone}, newPrometheusMetrics(), cfg, zap.NewNop())
						method, body := http.MethodGet, ""
						if path == api.CommandsPath {
							method, body = http.MethodPost, `{"action":"set-reader-workers","value":1}`
						}
						request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
						recorder := httptest.NewRecorder()
						returned := make(chan struct{})
						go func() { mux.ServeHTTP(recorder, request); close(returned) }()
						if stage == "reply" {
							<-queue
						}
						synctest.Wait()
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
			requests := make(chan runtimeRequest)
			mux := newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
			go func() { (<-requests).(controlRequest).reply <- applied }()
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

func TestStartHTTPServerLogsActualListenerAddress(t *testing.T) {
	cfg := testConfig(t)
	// The loader still rejects port zero; directly start the listener to distinguish actual from configured address.
	cfg.HTTP.Addr = "127.0.0.1:0"
	core, logs := observer.New(zap.InfoLevel)
	server, done, err := startHTTPServer(runtimeRequests{}, newPrometheusMetrics(), cfg, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	entries := logs.FilterMessage("service started").All()
	if len(entries) != 1 {
		t.Fatalf("service start logs=%d", len(entries))
	}
	fields := entries[0].ContextMap()
	addr, ok := fields["http_addr"].(string)
	if !ok || addr == cfg.HTTP.Addr || fields["event"] != "service_started" {
		t.Fatalf("start fields=%v", fields)
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatal(err)
	}
}

func TestServeMuxHasNoLocalIngest(t *testing.T) {
	mux := newServeMux(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), testConfig(t), zap.NewNop())
	for _, path := range []string{"/internal/test/ingest", "/internal/ingest", api.SnapshotPath + "/extra", api.CommandsPath + "/extra"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, recorder.Code)
		}
	}
}

func TestStartHTTPServerUsesConfiguredAddressAndShutsDown(t *testing.T) {
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := available.Addr().String()
	if err := available.Close(); err != nil {
		t.Fatal(err)
	}
	loaded := testConfig(t)
	loaded.HTTP.Addr = addr
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := shutdownHTTPServer(ctx, server); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("HTTP server did not exit")
		}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("configured address not released after shutdown: %v", err)
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("shutdown released %s", addr)
	})
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + addr + api.CommandsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("configured server GET = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
	t.Logf("configured server %s answered GET %s with %d", addr, api.CommandsPath, response.StatusCode)
}

func TestStartHTTPServerReturnsOccupiedAddressError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	loaded := testConfig(t)
	loaded.HTTP.Addr = listener.Addr().String()
	server, done, err := startHTTPServer(runtimeRequests{queue: make(chan runtimeRequest)}, newPrometheusMetrics(), loaded, zap.NewNop())
	if server != nil || done != nil {
		t.Fatal("failed startup returned server or done channel")
	}
	var listenError *net.OpError
	if !errors.As(err, &listenError) || listenError.Op != "listen" {
		t.Fatalf("startup error = %v, want wrapped listen error", err)
	}
	t.Logf("occupied address %s returned %v", loaded.HTTP.Addr, err)
}

func TestShutdownHTTPServerClosesAnActiveRequestAtDeadline(t *testing.T) {
	entered := make(chan struct{})
	left := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	server.Start()
	t.Cleanup(server.Close)
	requestCtx, cancelRequest := context.WithTimeout(t.Context(), time.Second)
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-requestCtx.Done():
		t.Fatal("HTTP request did not enter handler")
	}
	shutdownCtx, cancelShutdown := context.WithDeadline(t.Context(), time.Now())
	defer cancelShutdown()
	if err := shutdownHTTPServer(shutdownCtx, server.Config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-left:
	case <-requestCtx.Done():
		t.Fatal("HTTP handler remained active past force-close")
	}
	if err := <-result; err == nil || requestCtx.Err() != nil {
		t.Fatalf("request error = %v, client context = %v, want server-side close", err, requestCtx.Err())
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
	requests := make(chan runtimeRequest)
	go func() {
		(<-requests).(snapshotRequest).reply <- api.Snapshot{Run: api.RunSnapshot{State: api.RunStateRunning}}
	}()
	core, events := observer.New(zap.WarnLevel)
	snapshotHandler(runtimeRequests{queue: requests}, zap.New(core)).ServeHTTP(
		failingSnapshotWriter{ResponseRecorder: httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil),
	)
	entries := events.FilterMessage("snapshot response encoding failed").All()
	if len(entries) != 1 || entries[0].ContextMap()["event"] != "snapshot_response_failed" || entries[0].ContextMap()["error"] != io.ErrClosedPipe.Error() {
		t.Fatalf("encoding failure log=%v", entries)
	}
}

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	requests := make(chan runtimeRequest, 1)
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
	go func() { (<-requests).(snapshotRequest).reply <- expected }()
	snapshotHandler(runtimeRequests{queue: requests}, zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil))
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
	requests := make(chan runtimeRequest, 1)
	go func() {
		(<-requests).(snapshotRequest).reply <- api.Snapshot{Run: api.RunSnapshot{State: api.RunStateStarting}, Reader: api.ReaderSnapshot{WorkerStates: []api.ReaderWorkerState{}}, Sender: api.SenderSnapshot{WorkerStates: []api.SenderWorkerState{}}}
	}()
	recorder := httptest.NewRecorder()
	snapshotHandler(runtimeRequests{queue: requests}, zap.NewNop()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil))
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
	requests := make(chan runtimeRequest, 1)
	rec := httptest.NewRecorder()
	newServeMux(runtimeRequests{queue: requests}, newPrometheusMetrics(), testConfig(t), zap.NewNop()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.SnapshotPath, nil))
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
