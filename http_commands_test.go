package main

import (
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
				commandsHandler(commands).ServeHTTP(rec, req)
			}()

			var cmd request
			select {
			case cmd = <-commands:
			case <-time.After(time.Second):
				t.Fatal("command was not dispatched")
			}
			if test.expectedKind == cmdReset {
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

func TestCommandsHandlerRejectsGet(t *testing.T) {
	commands := make(chan request, 1)
	req := httptest.NewRequest(http.MethodGet, commandsPath, nil)
	rec := httptest.NewRecorder()

	commandsHandler(commands).ServeHTTP(rec, req)
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
			commandsHandler(commands).ServeHTTP(rec, req)
			response := rec.Result()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
