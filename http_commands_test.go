package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCommandsHandlerDispatches(t *testing.T) {
	tests := []struct {
		action       string
		expectedKind requestKind
	}{
		{"run", cmdRun},
		{"pause", cmdPause},
		{"reset", cmdReset},
	}

	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			commands := make(chan request, 1)
			body := strings.NewReader(`{"action":"` + test.action + `"}`)
			req := httptest.NewRequest(http.MethodPost, commandsPath, body)
			rec := httptest.NewRecorder()

			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(rec, req)
			}()

			var cmd request
			select {
			case cmd = <-commands:
			case <-time.After(time.Second):
				t.Fatal("command was not dispatched")
			}
			if test.expectedKind == cmdRun || test.expectedKind == cmdReset {
				cmd.commandReply <- commandResult{status: commandAccepted}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("command handler did not return")
			}
			response := rec.Result()
			if response.StatusCode != http.StatusOK {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusOK)
			}

			if cmd.kind != test.expectedKind {
				t.Errorf("command kind = %v, want %v", cmd.kind, test.expectedKind)
			}
		})
	}
}

func TestCommandsHandlerReportsRunStartError(t *testing.T) {
	commands := make(chan request, 1)
	request := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"run"}`))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(recorder, request)
	}()

	command := <-commands
	command.commandReply <- commandResult{err: errors.New("missing parquet")}
	<-done

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reply code = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	if body := recorder.Body.String(); body != "{\"error\":\"missing parquet\"}\n" {
		t.Errorf("reply body = %q", body)
	}
}

func TestCommandsHandlerRejectsGet(t *testing.T) {
	commands := make(chan request, 1)
	req := httptest.NewRequest(http.MethodGet, commandsPath, nil)
	rec := httptest.NewRecorder()

	commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(rec, req)
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
		{"unknown-action", `{"action":"unknown-action"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan request, 1)
			body := strings.NewReader(test.body)
			req := httptest.NewRequest(http.MethodPost, commandsPath, body)
			rec := httptest.NewRecorder()
			commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(rec, req)
			response := rec.Result()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestThrottlerCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
		kind requestKind
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
		{name: "mode missing", body: `{"action":"set-throttler-installation-mode"}`, want: http.StatusBadRequest},
		{name: "mode null", body: `{"action":"set-throttler-installation-mode","value":null}`, want: http.StatusBadRequest},
		{name: "mode number", body: `{"action":"set-throttler-installation-mode","value":1}`, want: http.StatusBadRequest},
		{name: "mode unknown", body: `{"action":"set-throttler-installation-mode","value":"other"}`, want: http.StatusBadRequest},
		{name: "mode extra field", body: `{"action":"set-throttler-installation-mode","value":"bypass","other":1}`, want: http.StatusBadRequest},
		{name: "TPS zero", body: `{"action":"set-requested-tps","value":0}`, want: http.StatusOK, kind: cmdSetRequestedTPS},
		{name: "TPS default", body: `{"action":"set-requested-tps","value":2000000}`, want: http.StatusOK, kind: cmdSetRequestedTPS},
		{name: "TPS maximum", body: `{"action":"set-requested-tps","value":4000000}`, want: http.StatusOK, kind: cmdSetRequestedTPS},
		{name: "mode installed", body: `{"action":"set-throttler-installation-mode","value":"installed"}`, want: http.StatusOK, kind: cmdSetThrottlerInstallationMode},
		{name: "mode bypass", body: `{"action":"set-throttler-installation-mode","value":"bypass"}`, want: http.StatusOK, kind: cmdSetThrottlerInstallationMode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan request, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			policy := testPolicy(t)
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), policy).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case command := <-commands:
					if command.kind != test.kind {
						t.Errorf("command kind = %v, want %v", command.kind, test.kind)
					}
					command.commandReply <- commandResult{status: commandAccepted}
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
		kind       requestKind
		value      int
	}{
		{"workers minimum", `{"action":"set-sender-workers","value":1}`, http.StatusOK, cmdSetSenderWorkers, 1},
		{"workers default", `{"action":"set-sender-workers","value":32}`, http.StatusOK, cmdSetSenderWorkers, 32},
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
			commands := make(chan request, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case command := <-commands:
					if command.kind != test.kind || command.value != test.value {
						t.Errorf("command = %+v, want kind %v value %d", command, test.kind, test.value)
					}
					command.commandReply <- commandResult{}
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
	tests := []struct {
		name, body string
		want       int
		value      int
	}{
		{"minimum", `{"action":"set-reader-workers","value":1}`, http.StatusOK, 1},
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
			commands := make(chan request, 1)
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(recorder, req)
			}()
			if test.want == http.StatusOK {
				select {
				case command := <-commands:
					if command.kind != cmdSetReaderWorkers || command.value != test.value {
						t.Errorf("command = %+v, want Reader workers %d", command, test.value)
					}
					command.commandReply <- commandResult{status: commandAccepted}
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
