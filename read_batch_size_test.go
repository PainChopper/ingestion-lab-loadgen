package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadBatchSizeCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing", `{"action":"set-read-batch-size"}`, http.StatusBadRequest},
		{"null", `{"action":"set-read-batch-size","value":null}`, http.StatusBadRequest},
		{"fractional", `{"action":"set-read-batch-size","value":25000.5}`, http.StatusBadRequest},
		{"string", `{"action":"set-read-batch-size","value":"25000"}`, http.StatusBadRequest},
		{"below-minimum", `{"action":"set-read-batch-size","value":0}`, http.StatusBadRequest},
		{"above-maximum", `{"action":"set-read-batch-size","value":101000}`, http.StatusBadRequest},
		{"off-step", `{"action":"set-read-batch-size","value":25001}`, http.StatusBadRequest},
		{"minimum", `{"action":"set-read-batch-size","value":1000}`, http.StatusOK},
		{"maximum", `{"action":"set-read-batch-size","value":100000}`, http.StatusOK},
		{"unrelated-action-value", `{"action":"pause","value":"ignored"}`, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan request, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(commands, testPolicy(t)).ServeHTTP(recorder, request)
			}()
			if test.want == http.StatusOK {
				select {
				case command := <-commands:
					if test.name == "unrelated-action-value" {
						if command.kind != cmdPause {
							t.Errorf("command kind = %v, want Pause", command.kind)
						}
					} else {
						if command.kind != cmdSetReadBatchSize {
							t.Errorf("command kind = %v, want set size", command.kind)
						}
						command.commandReply <- commandResult{status: commandAccepted}
					}
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
				t.Errorf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusBadRequest && len(commands) != 0 {
				t.Error("invalid command was dispatched")
			}
		})
	}
}

func TestReadBatchSizeIdleOnlyAndPersistsAfterReset(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	startedSizes := make(chan int, 2)
	read := func(ctx context.Context, _ chan<- []Transaction, size, _ int) (readerRun, error) {
		startedSizes <- size
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
		}()
		return readerRun{done: done, reconcile: func(int) {}}, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, read)
	commands := commandsHandler(requests, testPolicy(t))
	snapshot := func() statusSnapshot {
		t.Helper()
		recorder := httptest.NewRecorder()
		snapshotHandler(requests).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
		var value statusSnapshot
		if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	post := func(body string, want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		commands.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(body)))
		if recorder.Code != want {
			t.Fatalf("POST %s = %d, want %d", body, recorder.Code, want)
		}
	}
	setSize := `{"action":"set-read-batch-size","value":25000}`
	if got := snapshot().Reader.ReadBatchSize; got != testPolicy(t).Reader.ReadBatchSize.Default {
		t.Fatalf("default size = %d, want %d", got, testPolicy(t).Reader.ReadBatchSize.Default)
	}
	post(setSize, http.StatusOK)
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("configured size = %d, want 25000", got)
	}
	ownerReply := make(chan commandResult, 1)
	requests <- request{kind: cmdSetReadBatchSize, value: 25_001, commandReply: ownerReply}
	if result := <-ownerReply; result.status != commandConflict {
		t.Fatalf("invalid direct command status = %d, want conflict", result.status)
	}
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("size changed after invalid direct command: %d", got)
	}
	post(`{"action":"run"}`, http.StatusOK)
	if got := <-startedSizes; got != 25_000 {
		t.Fatalf("reader size = %d, want 25000", got)
	}
	post(`{"action":"set-read-batch-size","value":30000}`, http.StatusConflict)
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("size changed during Run: %d", got)
	}
	post(`{"action":"pause"}`, http.StatusOK)
	if got := snapshot().Run.State; got != runStatePaused {
		t.Fatalf("state after Pause = %s", got)
	}
	post(`{"action":"set-read-batch-size","value":30000}`, http.StatusConflict)
	post(`{"action":"reset"}`, http.StatusOK)
	if got := snapshot(); got.Run.State != runStateIdle || got.Reader.ReadBatchSize != 25_000 {
		t.Fatalf("snapshot after Reset = %+v", got)
	}
	post(`{"action":"run"}`, http.StatusOK)
	if got := <-startedSizes; got != 25_000 {
		t.Fatalf("reader size after Reset = %d, want 25000", got)
	}
}
