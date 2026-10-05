package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
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
				commandsHandler(commands, testConfig(t), nil).ServeHTTP(recorder, request)
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
						command.receiptReply <- runtimeCommandReceipt{}
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
	harness := startActualChannelEventLoopForTest(t)
	if err := parquet.WriteFile(harness.state.config.Source.Path, make([]Transaction, 4_000)); err != nil {
		t.Fatal(err)
	}
	harness.state.controls.requestedTPS = 0
	requests := harness.requests
	control := requests
	snapshot := func() runtimeStatus {
		t.Helper()
		return requestRuntimeStatus(control)
	}
	execute := func(command runtimeCommand, wantRejected bool) {
		t.Helper()
		if result := executeRuntimeCommand(control, command); result.rejected != wantRejected {
			t.Fatalf("command %+v = %+v, want rejected %t", command, result, wantRejected)
		}
	}
	if got := snapshot().Reader.ReadBatchSize; got != testConfig(t).Reader.ReadBatchSize.Initial {
		t.Fatalf("initial size = %d, want %d", got, testConfig(t).Reader.ReadBatchSize.Initial)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 2_000}, false)
	if got := snapshot().Reader.ReadBatchSize; got != 2_000 {
		t.Fatalf("configured size = %d, want 2000", got)
	}
	if result := executeRuntimeCommand(control, runtimeCommand{kind: cmdSetReadBatchSize, value: 2_001}); !result.rejected {
		t.Fatalf("invalid direct command rejected = %t, want true", result.rejected)
	}
	if got := snapshot().Reader.ReadBatchSize; got != 2_000 {
		t.Fatalf("size changed after invalid direct command: %d", got)
	}
	execute(runtimeCommand{kind: cmdRun}, false)
	select {
	case batch := <-harness.nextReader(t):
		if len(batch) != 2_000 {
			t.Fatalf("real Reader batch size = %d, want 2000", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("Reader did not emit a configured batch")
	}
	if got := snapshot().Reader.ReadBatchSize; got != 2_000 {
		t.Fatalf("reader size = %d, want 2000", got)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 3_000}, true)
	if got := snapshot().Reader.ReadBatchSize; got != 2_000 {
		t.Fatalf("size changed during Run: %d", got)
	}
	control <- runtimeCommand{kind: cmdPause}
	if got := snapshot().Run.State; got != runStatePaused {
		t.Fatalf("state after Pause = %s", got)
	}
	execute(runtimeCommand{kind: cmdSetReadBatchSize, value: 3_000}, true)
	execute(runtimeCommand{kind: cmdReset}, false)
	if got := snapshot(); got.Run.State != runStateIdle || got.Reader.ReadBatchSize != 2_000 {
		t.Fatalf("snapshot after Reset = %+v", got)
	}
	execute(runtimeCommand{kind: cmdRun}, false)
	select {
	case batch := <-harness.nextReader(t):
		if len(batch) != 2_000 {
			t.Fatalf("real Reader batch size = %d, want 2000", len(batch))
		}
	case <-time.After(time.Second):
		t.Fatal("Reader did not emit a configured batch")
	}
	if got := snapshot().Reader.ReadBatchSize; got != 2_000 {
		t.Fatalf("reader size after Reset = %d, want 2000", got)
	}
}
