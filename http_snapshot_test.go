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
		ThrottlerRequestedTPS:                    200,
		ThrottlerAdmittedTPS:                     3,
		ThrottlerInstallationMode:                throttlerInstalled,
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
		SenderChannelCapacity:                    0,
		SenderChannelDepthBatches:                0,
		SenderChannelBufferedTransactions:        0,
		SenderChannelBlockedSenders:              1,
		SenderChannelOldestBlockedSenderMs:       12,
		SenderChannelBlockedMs:                   34,
		SenderChannelSentBatchesTotal:            2,
		SenderChannelSentTransactionsTotal:       6,
		SenderChannelReceivedBatchesTotal:        1,
		SenderChannelReceivedTransactionsTotal:   3,
		SenderChannelInputBatchesPerSecond:       1,
		SenderChannelInputTransactionsPerSecond:  3,
		SenderChannelOutputBatchesPerSecond:      0.5,
		SenderChannelOutputTransactionsPerSecond: 1.5,
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
	if len(fields) != 42 {
		t.Errorf("snapshot field count = %d, want 42", len(fields))
	}
	if string(fields["throttlerRequestedTps"]) != "200" || string(fields["throttlerInstallationMode"]) != `"installed"` {
		t.Errorf("throttler applied fields = %s, %s", fields["throttlerRequestedTps"], fields["throttlerInstallationMode"])
	}
	if string(fields["policy"]) == "null" {
		t.Error("policy must be a JSON object")
	}
	for name, want := range wantReaderChannelFields {
		if got := string(fields[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	wantSenderFields := map[string]string{
		"throttlerAdmittedTps":                     "3",
		"senderChannelCapacity":                    "0",
		"senderChannelDepthBatches":                "0",
		"senderChannelBufferedTransactions":        "0",
		"senderChannelBlockedSenders":              "1",
		"senderChannelOldestBlockedSenderMs":       "12",
		"senderChannelBlockedMs":                   "34",
		"senderChannelSentBatchesTotal":            "2",
		"senderChannelSentTransactionsTotal":       "6",
		"senderChannelReceivedBatchesTotal":        "1",
		"senderChannelReceivedTransactionsTotal":   "3",
		"senderChannelInputBatchesPerSecond":       "1",
		"senderChannelInputTransactionsPerSecond":  "3",
		"senderChannelOutputBatchesPerSecond":      "0.5",
		"senderChannelOutputTransactionsPerSecond": "1.5",
	}
	for name, want := range wantSenderFields {
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
		string(body["readerChannelOutputTransactionsPerSecond"]) != "0" ||
		string(body["throttlerAdmittedTps"]) != "0" ||
		string(body["senderChannelCapacity"]) != "0" ||
		string(body["senderChannelDepthBatches"]) != "0" ||
		string(body["senderChannelBufferedTransactions"]) != "0" ||
		string(body["senderChannelBlockedSenders"]) != "0" ||
		string(body["senderChannelOldestBlockedSenderMs"]) != "0" ||
		string(body["senderChannelBlockedMs"]) != "0" ||
		string(body["senderChannelSentBatchesTotal"]) != "0" ||
		string(body["senderChannelSentTransactionsTotal"]) != "0" ||
		string(body["senderChannelReceivedBatchesTotal"]) != "0" ||
		string(body["senderChannelReceivedTransactionsTotal"]) != "0" ||
		string(body["senderChannelInputBatchesPerSecond"]) != "0" ||
		string(body["senderChannelInputTransactionsPerSecond"]) != "0" ||
		string(body["senderChannelOutputBatchesPerSecond"]) != "0" ||
		string(body["senderChannelOutputTransactionsPerSecond"]) != "0" {
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
