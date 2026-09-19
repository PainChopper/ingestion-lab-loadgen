package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	requests := make(chan request, 1)
	startError := "failed to start"
	source := "data/part/input.parquet"
	expected := statusSnapshot{
		RunState:                          runStateRunning,
		TotalTransactions:                 46,
		ReaderWorkers:                     1,
		SenderWorkers:                     0,
		ElapsedMs:                         1234,
		StartError:                        &startError,
		ReaderReadTPS:                     123.5,
		ReaderRowsRead:                    47,
		ReaderSource:                      &source,
		Queue1EnqueuedBatchesTotal:        2,
		Queue1EnqueuedTransactionsTotal:   4,
		Queue1DequeuedBatchesTotal:        1,
		Queue1DequeuedTransactionsTotal:   2,
		Queue1InputBatchesPerSecond:       1.5,
		Queue1InputTransactionsPerSecond:  3,
		Queue1OutputBatchesPerSecond:      0.5,
		Queue1OutputTransactionsPerSecond: 1,
	}

	req := httptest.NewRequest(http.MethodGet, snapshotPath, nil)
	rec := httptest.NewRecorder()

	fakeStateOwner := func() {
		cmd := <-requests
		if cmd.kind != getSnapshot {
			t.Errorf("command kind = %v, want %v", cmd.kind, getSnapshot)
		}
		cmd.snapshotReply <- expected
	}
	go fakeStateOwner()

	snapshotHandler(requests).ServeHTTP(rec, req)
	response := rec.Result()

	if response.StatusCode != http.StatusOK {
		t.Errorf("reply code = %v, want %v", response.StatusCode, http.StatusOK)
	}
	contentType := response.Header.Get("Content-Type")
	const contentTypeName = "application/json"
	if contentType != contentTypeName {
		t.Errorf("content type = %v, want %v", contentType, contentTypeName)
	}

	var actual statusSnapshot
	if err := json.NewDecoder(response.Body).Decode(&actual); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual = %v, want %v", actual, expected)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	wantQueueFields := map[string]string{
		"queue1EnqueuedBatchesTotal":        "2",
		"queue1EnqueuedTransactionsTotal":   "4",
		"queue1DequeuedBatchesTotal":        "1",
		"queue1DequeuedTransactionsTotal":   "2",
		"queue1InputBatchesPerSecond":       "1.5",
		"queue1InputTransactionsPerSecond":  "3",
		"queue1OutputBatchesPerSecond":      "0.5",
		"queue1OutputTransactionsPerSecond": "1",
	}
	if len(fields) != 24 {
		t.Errorf("snapshot field count = %d, want 24", len(fields))
	}
	for name, want := range wantQueueFields {
		if got := string(fields[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestSnapshotHandlerIncludesZeroElapsedAndNullStartError(t *testing.T) {
	requests := make(chan request, 1)
	request := httptest.NewRequest(http.MethodGet, snapshotPath, nil)
	recorder := httptest.NewRecorder()
	go func() {
		command := <-requests
		command.snapshotReply <- statusSnapshot{RunState: runStateIdle}
	}()

	snapshotHandler(requests).ServeHTTP(recorder, request)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["elapsedMs"]) != "0" || string(body["startError"]) != "null" ||
		string(body["readerReadTps"]) != "0" || string(body["readerRowsRead"]) != "0" ||
		string(body["readerSource"]) != "null" || string(body["queue1Capacity"]) != "0" ||
		string(body["queue1DepthBatches"]) != "0" || string(body["queue1QueuedTransactions"]) != "0" ||
		string(body["queue1BlockedSenders"]) != "0" || string(body["queue1OldestBlockedSenderMs"]) != "0" ||
		string(body["queue1BlockedMs"]) != "0" ||
		string(body["queue1EnqueuedBatchesTotal"]) != "0" ||
		string(body["queue1EnqueuedTransactionsTotal"]) != "0" ||
		string(body["queue1DequeuedBatchesTotal"]) != "0" ||
		string(body["queue1DequeuedTransactionsTotal"]) != "0" ||
		string(body["queue1InputBatchesPerSecond"]) != "0" ||
		string(body["queue1InputTransactionsPerSecond"]) != "0" ||
		string(body["queue1OutputBatchesPerSecond"]) != "0" ||
		string(body["queue1OutputTransactionsPerSecond"]) != "0" {
		t.Errorf("idle snapshot body = %v", body)
	}
}

func TestSnapshotHandlerRejectsPost(t *testing.T) {
	requests := make(chan request, 1)

	req := httptest.NewRequest(http.MethodPost, snapshotPath, nil)
	rec := httptest.NewRecorder()

	snapshotHandler(requests).ServeHTTP(rec, req)
	response := rec.Result()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %v, want %v", rec.Code, http.StatusMethodNotAllowed)
	}
	allow := response.Header.Get("Allow")
	if allow != http.MethodGet {
		t.Errorf("allow header = %q, want %q", allow, http.MethodGet)
	}
}
