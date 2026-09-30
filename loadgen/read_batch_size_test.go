package main

import (
	"context"
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
			commands := make(chan runtimeCommand, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), testConfig(t)).ServeHTTP(recorder, request)
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
						command.receiptReply <- runtimeCommandReceipt{status: commandAccepted}
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
	requests := make(chan runtimeCommand, 3)
	control := testControlPlane(requests)
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
	snapshot := func() runtimeStatus {
		t.Helper()
		return control.status()
	}
	execute := func(command runtimeCommand, want runtimeCommandStatus) {
		t.Helper()
		if result := control.execute(command); result.status != want {
			t.Fatalf("command %+v = %+v, want status %d", command, result, want)
		}
	}
	if got := snapshot().Reader.ReadBatchSize; got != testConfig(t).Reader.ReadBatchSize.Default {
		t.Fatalf("default size = %d, want %d", got, testConfig(t).Reader.ReadBatchSize.Default)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 25_000}, commandAccepted)
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("configured size = %d, want 25000", got)
	}
	if result := control.execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 25_001}); result.status != commandConflict {
		t.Fatalf("invalid direct command status = %d, want conflict", result.status)
	}
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("size changed after invalid direct command: %d", got)
	}
	execute(runtimeCommand{kind: cmdRun}, commandAccepted)
	if got := <-startedSizes; got != 25_000 {
		t.Fatalf("reader size = %d, want 25000", got)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 30_000}, commandConflict)
	if got := snapshot().Reader.ReadBatchSize; got != 25_000 {
		t.Fatalf("size changed during Run: %d", got)
	}
	control.executeAsync(runtimeCommand{kind: cmdPause})
	if got := snapshot().Run.State; got != runStatePaused {
		t.Fatalf("state after Pause = %s", got)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 30_000}, commandConflict)
	execute(runtimeCommand{kind: cmdReset}, commandAccepted)
	if got := snapshot(); got.Run.State != runStateIdle || got.Reader.ReadBatchSize != 25_000 {
		t.Fatalf("snapshot after Reset = %+v", got)
	}
	execute(runtimeCommand{kind: cmdRun}, commandAccepted)
	if got := <-startedSizes; got != 25_000 {
		t.Fatalf("reader size after Reset = %d, want 25000", got)
	}
}
