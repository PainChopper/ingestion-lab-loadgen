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
		RunState:          runStateRunning,
		TotalTransactions: 46,
		ReaderWorkers:     1,
		SenderWorkers:     0,
		ElapsedMs:         1234,
		StartError:        &startError,
		ReaderReadTPS:     123.5,
		ReaderRowsRead:    47,
		ReaderSource:      &source,
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
		string(body["queue1BlockedMs"]) != "0" {
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
