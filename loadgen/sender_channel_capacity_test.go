package main

import (
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
					command.receiptReply <- runtimeCommandReceipt{}
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
			harness := startActualChannelEventLoopForTest(t)
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

			if got := snapshot().SenderChannel.Capacity; got != 0 {
				t.Fatalf("initial capacity = %d, want 0", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: capacity}, false)
			if got := snapshot().SenderChannel.Capacity; got != capacity {
				t.Fatalf("idle capacity = %d, want %d", got, capacity)
			}

			if result := executeRuntimeCommand(control, runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 3}); !result.rejected {
				t.Fatalf("invalid direct command rejected = %t, want true", result.rejected)
			}
			execute(runtimeCommand{kind: cmdRun}, false)
			if got := cap(harness.nextSender(t)); got != capacity {
				t.Fatalf("actual Sender capacity = %d, want %d", got, capacity)
			}
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot = %+v", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 4}, true)
			control <- runtimeCommand{kind: cmdPause}
			if got := snapshot().Run.State; got != runStatePaused {
				t.Fatalf("state after Pause = %s", got)
			}
			execute(runtimeCommand{kind: cmdSetSenderChannelCapacity, value: 4}, true)
			execute(runtimeCommand{kind: cmdReset}, false)
			if got := snapshot(); got.Run.State != runStateIdle || got.SenderChannel.Capacity != capacity {
				t.Fatalf("reset snapshot = %+v", got)
			}
			execute(runtimeCommand{kind: cmdRun}, false)
			if got := cap(harness.nextSender(t)); got != capacity {
				t.Fatalf("actual Sender capacity = %d, want %d", got, capacity)
			}
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot after Reset = %+v", got)
			}
		})
	}
}
