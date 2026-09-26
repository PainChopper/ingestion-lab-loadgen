package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReaderChannelCapacityValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "missing", body: `{"action":"set-reader-channel-capacity"}`, want: http.StatusBadRequest},
		{name: "null", body: `{"action":"set-reader-channel-capacity","value":null}`, want: http.StatusBadRequest},
		{name: "fractional", body: `{"action":"set-reader-channel-capacity","value":1.5}`, want: http.StatusBadRequest},
		{name: "string", body: `{"action":"set-reader-channel-capacity","value":"1"}`, want: http.StatusBadRequest},
		{name: "non-power-of-two", body: `{"action":"set-reader-channel-capacity","value":3}`, want: http.StatusBadRequest},
		{name: "zero", body: `{"action":"set-reader-channel-capacity","value":0}`, want: http.StatusOK},
		{name: "maximum", body: `{"action":"set-reader-channel-capacity","value":8192}`, want: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := make(chan request, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(test.body))
			done := make(chan struct{})
			go func() {
				defer close(done)
				commandsHandler(testControlPlane(commands), testPolicy(t)).ServeHTTP(recorder, request)
			}()

			if test.want == http.StatusOK {
				select {
				case command := <-commands:
					if command.kind != cmdSetReaderChannelCapacity {
						t.Errorf("command kind = %v, want reader channel capacity", command.kind)
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

func TestValidReaderChannelCapacityAcceptsOnlyConfiguredSteps(t *testing.T) {
	validValues := []int{0, 1, 2, 4, 1_024, 8_192}
	for _, value := range validValues {
		if !validReaderChannelCapacity(testPolicy(t), value) {
			t.Errorf("value %d is rejected", value)
		}
	}

	for _, value := range []int{-1, 3, 8_193, 16_384} {
		if validReaderChannelCapacity(testPolicy(t), value) {
			t.Errorf("value %d is accepted", value)
		}
	}
}

func TestReaderChannelCapacityIdleOnlyAppliesToReaderAndPersistsAfterReset(t *testing.T) {
	for _, capacity := range []int{0, 1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			requests := make(chan request, 3)
			metrics := make(chan time.Time)
			startedCapacities := make(chan int, 2)
			state := newTestControlState(t)
			read := func(ctx context.Context, output chan<- []Transaction, _, _ int) (readerRun, error) {
				startedCapacities <- cap(output)
				done := make(chan struct{})
				go func() {
					defer close(done)
					<-ctx.Done()
				}()
				return readerRun{done: done, reconcile: func(int) {}}, nil
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				state.eventLoopWithThrottler(
					testControlPlane(requests),
					metrics,
					NewMetrics(),
					read,
					startThrottler,
				)
			}()
			t.Cleanup(func() {
				close(requests)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("event loop did not stop")
				}
			})
			commands := commandsHandler(testControlPlane(requests), state.controls.policy)
			snapshot := func() statusSnapshot {
				t.Helper()
				recorder := httptest.NewRecorder()
				snapshotHandler(testControlPlane(requests)).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
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

			if got := snapshot().ReaderChannel.Capacity; got != state.controls.policy.ReaderChannel.Capacity.Default {
				t.Fatalf("default capacity = %d, want %d", got, state.controls.policy.ReaderChannel.Capacity.Default)
			}
			post(`{"action":"set-reader-channel-capacity","value":`+strconv.Itoa(capacity)+`}`, http.StatusOK)
			if got := snapshot().ReaderChannel.Capacity; got != capacity {
				t.Fatalf("idle capacity = %d, want %d", got, capacity)
			}

			ownerReply := make(chan commandResult, 1)
			requests <- request{kind: cmdSetReaderChannelCapacity, value: 3, commandReply: ownerReply}
			if result := <-ownerReply; result.status != commandConflict {
				t.Fatalf("invalid direct command status = %d, want conflict", result.status)
			}
			post(`{"action":"run"}`, http.StatusOK)
			if got := <-startedCapacities; got != capacity {
				t.Fatalf("reader configured capacity = %d, want %d", got, capacity)
			}
			if got := snapshot(); got.ReaderChannel.Capacity != capacity || got.Run.State != runStateRunning {
				t.Fatalf("running snapshot = %+v", got)
			}
			post(`{"action":"set-reader-channel-capacity","value":4}`, http.StatusConflict)
			post(`{"action":"pause"}`, http.StatusOK)
			post(`{"action":"set-reader-channel-capacity","value":4}`, http.StatusConflict)
			post(`{"action":"reset"}`, http.StatusOK)
			if got := snapshot(); got.ReaderChannel.Capacity != capacity || got.Run.State != runStateIdle {
				t.Fatalf("reset snapshot = %+v", got)
			}
			post(`{"action":"run"}`, http.StatusOK)
			if got := <-startedCapacities; got != capacity {
				t.Fatalf("reader capacity after Reset = %d, want %d", got, capacity)
			}
		})
	}
}
