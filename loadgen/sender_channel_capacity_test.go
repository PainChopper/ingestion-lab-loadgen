package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSenderChannelCapacityValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "missing", body: `{"action":"set-sender-channel-capacity"}`, want: http.StatusBadRequest},
		{name: "null", body: `{"action":"set-sender-channel-capacity","value":null}`, want: http.StatusBadRequest},
		{name: "fractional", body: `{"action":"set-sender-channel-capacity","value":1.5}`, want: http.StatusBadRequest},
		{name: "string", body: `{"action":"set-sender-channel-capacity","value":"1"}`, want: http.StatusBadRequest},
		{name: "negative", body: `{"action":"set-sender-channel-capacity","value":-1}`, want: http.StatusBadRequest},
		{
			name: "minimum integer",
			body: `{"action":"set-sender-channel-capacity","value":` + strconv.Itoa(math.MinInt) + `}`,
			want: http.StatusBadRequest,
		},
		{name: "off list", body: `{"action":"set-sender-channel-capacity","value":3}`, want: http.StatusBadRequest},
		{
			name: "extra field",
			body: `{"action":"set-sender-channel-capacity","value":1,"extra":true}`,
			want: http.StatusBadRequest,
		},
		{name: "trailing JSON", body: `{"action":"set-sender-channel-capacity","value":1}{}`, want: http.StatusBadRequest},
		{name: "zero", body: `{"action":"set-sender-channel-capacity","value":0}`, want: http.StatusOK},
		{name: "one", body: `{"action":"set-sender-channel-capacity","value":1}`, want: http.StatusOK},
		{name: "maximum", body: `{"action":"set-sender-channel-capacity","value":8192}`, want: http.StatusOK},
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
					if command.kind != cmdSetSenderChannelCapacity {
						t.Errorf("command kind = %v, want sender channel capacity", command.kind)
					}
					command.receiptReply <- runtimeCommandReceipt{status: commandAccepted}
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

func TestSenderChannelCapacityIdleOnlyAppliesToThrottlerAndPersistsAfterReset(t *testing.T) {
	for _, capacity := range []int{0, 1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			requests := make(chan runtimeCommand, 3)
			control := requests
			metrics := make(chan time.Time)
			read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
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
				return requestRuntimeStatus(control)
			}
			execute := func(command runtimeCommand, want runtimeCommandStatus) {
				t.Helper()
				if result := executeRuntimeCommand(control, command); result.status != want {
					t.Fatalf("command %+v = %+v, want status %d", command, result, want)
				}
			}

			if got := snapshot().SenderChannel.Capacity; got != 0 {
				t.Fatalf("default capacity = %d, want 0", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: capacity}, commandAccepted)
			if got := snapshot().SenderChannel.Capacity; got != capacity {
				t.Fatalf("idle capacity = %d, want %d", got, capacity)
			}

			if result := executeRuntimeCommand(control, runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 3}); result.status != commandConflict {
				t.Fatalf("invalid direct command status = %d, want conflict", result.status)
			}
			execute(runtimeCommand{kind: cmdRun}, commandAccepted)
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot = %+v", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 4}, commandConflict)
			control <- runtimeCommand{kind: cmdPause}
			if got := snapshot().Run.State; got != runStatePaused {
				t.Fatalf("state after Pause = %s", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 4}, commandConflict)
			execute(runtimeCommand{kind: cmdReset}, commandAccepted)
			if got := snapshot(); got.Run.State != runStateIdle || got.SenderChannel.Capacity != capacity {
				t.Fatalf("reset snapshot = %+v", got)
			}
			execute(runtimeCommand{kind: cmdRun}, commandAccepted)
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot after Reset = %+v", got)
			}
		})
	}
}

func TestValidSenderChannelCapacityAcceptsOnlyConfiguredSteps(t *testing.T) {
	for _, value := range []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1_024, 2_048, 4_096, 8_192} {
		if !validSenderChannelCapacity(testConfig(t), value) {
			t.Errorf("value %d is rejected", value)
		}
	}
	for _, value := range []int{math.MinInt, -1, 3, 8_193, 16_384} {
		if validSenderChannelCapacity(testConfig(t), value) {
			t.Errorf("value %d is accepted", value)
		}
	}
}
