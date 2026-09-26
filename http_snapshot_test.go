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
	expected := statusSnapshot{
		Run: runSnapshot{State: runStateRunning, TotalTransactions: 46, ElapsedMs: 1234},
		Reader: readerSnapshot{
			Workers: 1, LiveWorkers: 2, ReadingWorkers: 2, DrainingWorkers: 1,
			DrainingReadingWorkers: 1,
			ReadBatchSize:          50000, ReadTps: 123.5, RowsRead: 47,
			SourceDirectory: "data/part", SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "input.parquet", Message: "corrupt parquet"},
		},
		Throttler:     throttlerSnapshot{RequestedTps: 200, AdmittedTps: 3, InstallationMode: throttlerInstalled},
		Sender:        senderSnapshot{Workers: 32},
		ReaderChannel: channelSnapshot{Capacity: 8, DepthBatches: 6, BufferedTransactions: 300000, BlockedSenders: 1, OldestBlockedSenderMs: 12, BlockedMs: 34, SentBatchesTotal: 2, SentTransactionsTotal: 4, ReceivedBatchesTotal: 1, ReceivedTransactionsTotal: 2, SentBatchesPerSecond: 1.5, SentTransactionsPerSecond: 3, ReceivedBatchesPerSecond: 0.5, ReceivedTransactionsPerSecond: 1},
		SenderChannel: channelSnapshot{Capacity: 16, DepthBatches: 4, BufferedTransactions: 100000, BlockedSenders: 2, OldestBlockedSenderMs: 13, BlockedMs: 35, SentBatchesTotal: 3, SentTransactionsTotal: 6, ReceivedBatchesTotal: 2, ReceivedTransactionsTotal: 3, SentBatchesPerSecond: 2, SentTransactionsPerSecond: 4, ReceivedBatchesPerSecond: 1.5, ReceivedTransactionsPerSecond: 2.5},
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
		"run":           {"elapsedMs", "state", "totalTransactions"},
		"reader":        {"blockedWorkers", "drainingBlockedWorkers", "drainingIdleWorkers", "drainingReadingWorkers", "drainingWorkers", "idleWorkers", "liveWorkers", "readBatchSize", "readTps", "readingWorkers", "rowsRead", "sourceDirectory", "sourceError", "workers"},
		"throttler":     {"admittedTps", "installationMode", "requestedTps"},
		"sender":        {"backoffWorkers", "drainingBackoffWorkers", "drainingIdleWorkers", "drainingInFlightWorkers", "drainingWorkers", "idleWorkers", "inFlightWorkers", "liveWorkers", "workers"},
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
	assertExactJSONKeys(t, policy, []string{"logging", "metricsWindowMs", "readerChannelCapacity", "readerReadBatchSize", "readerWorkers", "senderChannelCapacity", "senderWorkers", "senderRetry", "throttlerInstallationMode", "throttlerRequestedTps"})
	var workers rangePolicy
	if err := json.Unmarshal(policy["readerWorkers"], &workers); err != nil {
		t.Fatal(err)
	}
	if want := (rangePolicy{Default: 1, Min: 1, Max: 7, Step: 1, Unit: workersUnit, Mutability: immediate}); workers != want {
		t.Errorf("Reader workers policy = %+v, want %+v", workers, want)
	}
	var reader map[string]json.RawMessage
	if err := json.Unmarshal(root["reader"], &reader); err != nil {
		t.Fatal(err)
	}
	var sourceError map[string]json.RawMessage
	if err := json.Unmarshal(reader["sourceError"], &sourceError); err != nil {
		t.Fatal(err)
	}
	assertExactJSONKeys(t, sourceError, []string{"category", "message", "operation", "relativePath"})
	if string(reader["liveWorkers"]) != "2" || string(reader["drainingWorkers"]) != "1" {
		t.Errorf("Reader worker counts = %s / %s", reader["liveWorkers"], reader["drainingWorkers"])
	}
	for _, legacy := range []string{"workerSlots", "workerId", "source", "completed"} {
		if _, ok := reader[legacy]; ok {
			t.Fatalf("Reader includes legacy field %q", legacy)
		}
	}
	var sender map[string]json.RawMessage
	if err := json.Unmarshal(root["sender"], &sender); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"workerSlots", "workerId", "terminalError"} {
		if _, ok := sender[legacy]; ok {
			t.Fatalf("Sender includes legacy field %q", legacy)
		}
	}
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
	if string(run["elapsedMs"]) != "0" || string(reader["readTps"]) != "0" || string(reader["rowsRead"]) != "0" || string(reader["sourceDirectory"]) != "\"\"" || string(reader["sourceError"]) != "null" {
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
