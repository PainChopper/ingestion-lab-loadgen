package main

import (
	"context"
	"encoding/json"
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
					if command.kind != cmdSetSenderChannelCapacity {
						t.Errorf("command kind = %v, want sender channel capacity", command.kind)
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
			requests := make(chan request, 3)
			metrics := make(chan time.Time)
			state := newTestControlState(t)
			produce := func(ctx context.Context, _ int, _ int) (<-chan []Transaction, <-chan struct{}, error) {
				batches := make(chan []Transaction)
				done := make(chan struct{})
				go func() {
					defer func() {
						close(batches)
						close(done)
					}()
					<-ctx.Done()
				}()
				return batches, done, nil
			}
			startCustomEventLoopForTest(t, requests, metrics, produce)
			commands := commandsHandler(requests, state.policy)
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

			if got := snapshot().SenderChannel.Capacity; got != 0 {
				t.Fatalf("default capacity = %d, want 0", got)
			}
			post(`{"action":"set-sender-channel-capacity","value":`+strconv.Itoa(capacity)+`}`, http.StatusOK)
			if got := snapshot().SenderChannel.Capacity; got != capacity {
				t.Fatalf("idle capacity = %d, want %d", got, capacity)
			}

			ownerReply := make(chan commandResult, 1)
			requests <- request{kind: cmdSetSenderChannelCapacity, value: 3, commandReply: ownerReply}
			if result := <-ownerReply; result.status != commandConflict {
				t.Fatalf("invalid direct command status = %d, want conflict", result.status)
			}
			post(`{"action":"run"}`, http.StatusOK)
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot = %+v", got)
			}
			post(`{"action":"set-sender-channel-capacity","value":4}`, http.StatusConflict)
			post(`{"action":"pause"}`, http.StatusOK)
			post(`{"action":"set-sender-channel-capacity","value":4}`, http.StatusConflict)
			post(`{"action":"reset"}`, http.StatusOK)
			if got := snapshot(); got.Run.State != runStateIdle || got.SenderChannel.Capacity != capacity {
				t.Fatalf("reset snapshot = %+v", got)
			}
			post(`{"action":"run"}`, http.StatusOK)
			if got := snapshot(); got.Run.State != runStateRunning || got.SenderChannel.Capacity != capacity {
				t.Fatalf("running snapshot after Reset = %+v", got)
			}
		})
	}
}

func TestValidSenderChannelCapacityAcceptsOnlyConfiguredSteps(t *testing.T) {
	for _, value := range []int{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1_024, 2_048, 4_096, 8_192} {
		if !validSenderChannelCapacity(testPolicy(t), value) {
			t.Errorf("value %d is rejected", value)
		}
	}
	for _, value := range []int{math.MinInt, -1, 3, 8_193, 16_384} {
		if validSenderChannelCapacity(testPolicy(t), value) {
			t.Errorf("value %d is accepted", value)
		}
	}
}
