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
		Run:           runSnapshot{State: runStateRunning, TotalTransactions: 46, ElapsedMs: 1234, StartError: &startError},
		Reader:        readerSnapshot{Workers: 1, ReadBatchSize: 50000, ReadTps: 123.5, RowsRead: 47, Source: &source},
		Throttler:     throttlerSnapshot{RequestedTps: 200, AdmittedTps: 3, InstallationMode: throttlerInstalled},
		Sender:        senderSnapshot{Workers: 0},
		ReaderChannel: channelSnapshot{Capacity: 8, DepthBatches: 6, BufferedTransactions: 300000, BlockedSenders: 1, OldestBlockedSenderMs: 12, BlockedMs: 34, SentBatchesTotal: 2, SentTransactionsTotal: 4, ReceivedBatchesTotal: 1, ReceivedTransactionsTotal: 2, InputBatchesPerSecond: 1.5, InputTransactionsPerSecond: 3, OutputBatchesPerSecond: 0.5, OutputTransactionsPerSecond: 1},
		SenderChannel: channelSnapshot{Capacity: 16, DepthBatches: 4, BufferedTransactions: 100000, BlockedSenders: 2, OldestBlockedSenderMs: 13, BlockedMs: 35, SentBatchesTotal: 3, SentTransactionsTotal: 6, ReceivedBatchesTotal: 2, ReceivedTransactionsTotal: 3, InputBatchesPerSecond: 2, InputTransactionsPerSecond: 4, OutputBatchesPerSecond: 1.5, OutputTransactionsPerSecond: 2.5},
		Policy:        testPolicy(t).snapshot(),
	}
	rec := httptest.NewRecorder()
	go func() { (<-requests).snapshotReply <- expected }()
	snapshotHandler(requests).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reply code = %d, want %d", rec.Code, http.StatusOK)
	}
	var actual statusSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual = %+v, want %+v", actual, expected)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	assertExactJSONKeys(t, root, []string{"policy", "reader", "readerChannel", "run", "sender", "senderChannel", "throttler"})
	for name, want := range map[string][]string{
		"run":           {"elapsedMs", "startError", "state", "totalTransactions"},
		"reader":        {"readBatchSize", "readTps", "rowsRead", "source", "workers"},
		"throttler":     {"admittedTps", "installationMode", "requestedTps"},
		"sender":        {"workers"},
		"readerChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
		"senderChannel": {"blockedMs", "blockedSenders", "bufferedTransactions", "capacity", "depthBatches", "inputBatchesPerSecond", "inputTransactionsPerSecond", "oldestBlockedSenderMs", "outputBatchesPerSecond", "outputTransactionsPerSecond", "receivedBatchesTotal", "receivedTransactionsTotal", "sentBatchesTotal", "sentTransactionsTotal"},
	} {
		var section map[string]json.RawMessage
		if err := json.Unmarshal(root[name], &section); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		assertExactJSONKeys(t, section, want)
	}
	if string(root["policy"]) == "null" {
		t.Error("policy must be a JSON object")
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(root["policy"], &policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	assertExactJSONKeys(t, policy, []string{"metricsWindowMs", "readerChannelCapacity", "readerReadBatchSize", "senderChannelCapacity", "throttlerInstallationMode", "throttlerRequestedTps"})
}

func assertExactJSONKeys(t *testing.T, object map[string]json.RawMessage, want []string) {
	t.Helper()
	actual := make([]string, 0, len(object))
	for key := range object {
		actual = append(actual, key)
	}
	if !reflect.DeepEqual(stringSet(actual), stringSet(want)) {
		t.Errorf("JSON keys = %v, want %v", actual, want)
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func TestSnapshotHandlerIncludesZeroAndNullValues(t *testing.T) {
	requests := make(chan request, 1)
	go func() { (<-requests).snapshotReply <- statusSnapshot{Run: runSnapshot{State: runStateIdle}} }()
	recorder := httptest.NewRecorder()
	snapshotHandler(requests).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	var root map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	var actual statusSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if want := (statusSnapshot{Run: runSnapshot{State: runStateIdle}}); !reflect.DeepEqual(actual, want) {
		t.Errorf("zero snapshot = %+v, want %+v", actual, want)
	}
	var run, reader map[string]json.RawMessage
	if err := json.Unmarshal(root["run"], &run); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		t.Fatal(err)
	}
	if string(run["elapsedMs"]) != "0" || string(run["startError"]) != "null" || string(reader["readTps"]) != "0" || string(reader["rowsRead"]) != "0" || string(reader["source"]) != "null" {
		t.Errorf("idle snapshot body = %v", root)
	}
}

func TestSnapshotHandlerRejectsPost(t *testing.T) {
	requests := make(chan request, 1)
	rec := httptest.NewRecorder()
	snapshotHandler(requests).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, snapshotPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}
