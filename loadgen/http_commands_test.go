package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCommandsHandlerRejectsGet(t *testing.T) {
	commands := make(chan runtimeRequest, 1)
	req := httptest.NewRequest(http.MethodGet, commandsPath, nil)
	rec := httptest.NewRecorder()

	commandsHandler(runtimeRequests{queue: commands}, testConfig(t)).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
	allow := response.Header.Get("Allow")
	if allow != http.MethodPost {
		t.Errorf("allow header = %q, want %q", allow, http.MethodPost)
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
			req := httptest.NewRequest(http.MethodPost, commandsPath, body)
			rec := httptest.NewRecorder()
			commandsHandler(runtimeRequests{queue: commands}, testConfig(t)).ServeHTTP(rec, req)
			response := rec.Result()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusBadRequest)
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
		{"JSON exceeds limit", `{"action":"` + strings.Repeat("x", limit) + `","value":2}`},
		{"trailing whitespace exceeds limit", command + strings.Repeat(" ", limit+1-len(command))},
	}
	done := make(chan struct{})
	close(done)
	handler := commandsHandler(runtimeRequests{done: done}, testConfig(t))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

func TestThrottlerCommandValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      int
		kind      runtimeCommandKind
		installed bool
	}{
		{name: "TPS missing", body: `{"action":"set-requested-tps"}`, want: http.StatusBadRequest},
		{name: "TPS null", body: `{"action":"set-requested-tps","value":null}`, want: http.StatusBadRequest},
		{name: "TPS fractional", body: `{"action":"set-requested-tps","value":100.5}`, want: http.StatusBadRequest},
		{name: "TPS string", body: `{"action":"set-requested-tps","value":"100"}`, want: http.StatusBadRequest},
		{name: "TPS negative", body: `{"action":"set-requested-tps","value":-100}`, want: http.StatusBadRequest},
		{name: "TPS off step", body: `{"action":"set-requested-tps","value":101}`, want: http.StatusBadRequest},
		{name: "TPS over max", body: `{"action":"set-requested-tps","value":4100000}`, want: http.StatusBadRequest},
		{name: "TPS extra field", body: `{"action":"set-requested-tps","value":100,"other":1}`, want: http.StatusBadRequest},
		{name: "TPS trailing JSON", body: `{"action":"set-requested-tps","value":100}{}`, want: http.StatusBadRequest},
		{name: "mode missing", body: `{"action":"set-throttler-installed"}`, want: http.StatusBadRequest},
		{name: "mode null", body: `{"action":"set-throttler-installed","value":null}`, want: http.StatusBadRequest},
		{name: "mode number", body: `{"action":"set-throttler-installed","value":1}`, want: http.StatusBadRequest},
		{name: "mode unknown", body: `{"action":"set-throttler-installed","value":"other"}`, want: http.StatusBadRequest},
		{name: "old installed string", body: `{"action":"set-throttler-installed","value":"installed"}`, want: http.StatusBadRequest},
		{name: "old bypass string", body: `{"action":"set-throttler-installed","value":"bypass"}`, want: http.StatusBadRequest},
		{name: "quoted bool", body: `{"action":"set-throttler-installed","value":"false"}`, want: http.StatusBadRequest},
		{name: "installed trailing JSON", body: `{"action":"set-throttler-installed","value":false}{}`, want: http.StatusBadRequest},
		{name: "mode extra field", body: `{"action":"set-throttler-installed","value":"bypass","other":1}`, want: http.StatusBadRequest},
		{name: "TPS zero", body: `{"action":"set-requested-tps","value":0}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "TPS initial", body: `{"action":"set-requested-tps","value":2000000}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "TPS maximum", body: `{"action":"set-requested-tps","value":4000000}`, want: http.StatusOK, kind: setRequestedTPS},
		{name: "mode installed", body: `{"action":"set-throttler-installed","value":true}`, want: http.StatusOK, kind: setThrottlerInstalled, installed: true},
		{name: "mode bypass", body: `{"action":"set-throttler-installed","value":false}`, want: http.StatusOK, kind: setThrottlerInstalled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan runtimeRequest, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			config := testConfig(t)
			go func() {
				defer close(done)
				commandsHandler(runtimeRequests{queue: commands}, config).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case request := <-commands:
					dispatch := request.(controlRequest)
					command := dispatch.command
					if command.kind != test.kind {
						t.Errorf("command kind = %v, want %v", command.kind, test.kind)
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
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(runtimeRequests{queue: commands}, testConfig(t)).ServeHTTP(recorder, req)
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
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(runtimeRequests{queue: commands}, testConfig(t)).ServeHTTP(recorder, req)
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
