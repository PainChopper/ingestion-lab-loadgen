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
		RunState:                                 runStateRunning,
		TotalTransactions:                        46,
		ReaderWorkers:                            1,
		SenderWorkers:                            0,
		ElapsedMs:                                1234,
		StartError:                               &startError,
		ReaderReadTPS:                            123.5,
		ReaderRowsRead:                           47,
		ReaderSource:                             &source,
		ReaderChannelSentBatchesTotal:            2,
		ReaderChannelSentTransactionsTotal:       4,
		ReaderChannelReceivedBatchesTotal:        1,
		ReaderChannelReceivedTransactionsTotal:   2,
		ReaderChannelInputBatchesPerSecond:       1.5,
		ReaderChannelInputTransactionsPerSecond:  3,
		ReaderChannelOutputBatchesPerSecond:      0.5,
		ReaderChannelOutputTransactionsPerSecond: 1,
		Policy:                                   testPolicy(t).snapshot(),
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
	wantReaderChannelFields := map[string]string{
		"readerChannelSentBatchesTotal":            "2",
		"readerChannelSentTransactionsTotal":       "4",
		"readerChannelReceivedBatchesTotal":        "1",
		"readerChannelReceivedTransactionsTotal":   "2",
		"readerChannelInputBatchesPerSecond":       "1.5",
		"readerChannelInputTransactionsPerSecond":  "3",
		"readerChannelOutputBatchesPerSecond":      "0.5",
		"readerChannelOutputTransactionsPerSecond": "1",
	}
	if len(fields) != 25 {
		t.Errorf("snapshot field count = %d, want 25", len(fields))
	}
	if string(fields["policy"]) == "null" {
		t.Error("policy must be a JSON object")
	}
	for name, want := range wantReaderChannelFields {
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
		string(body["readerSource"]) != "null" || string(body["readerChannelCapacity"]) != "0" ||
		string(body["readerChannelDepthBatches"]) != "0" || string(body["readerChannelBufferedTransactions"]) != "0" ||
		string(body["readerChannelBlockedSenders"]) != "0" || string(body["readerChannelOldestBlockedSenderMs"]) != "0" ||
		string(body["readerChannelBlockedMs"]) != "0" ||
		string(body["readerChannelSentBatchesTotal"]) != "0" ||
		string(body["readerChannelSentTransactionsTotal"]) != "0" ||
		string(body["readerChannelReceivedBatchesTotal"]) != "0" ||
		string(body["readerChannelReceivedTransactionsTotal"]) != "0" ||
		string(body["readerChannelInputBatchesPerSecond"]) != "0" ||
		string(body["readerChannelInputTransactionsPerSecond"]) != "0" ||
		string(body["readerChannelOutputBatchesPerSecond"]) != "0" ||
		string(body["readerChannelOutputTransactionsPerSecond"]) != "0" {
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
