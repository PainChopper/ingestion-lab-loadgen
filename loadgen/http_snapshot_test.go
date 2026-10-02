package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSnapshotHandlerReturnsOwnerSnapshot(t *testing.T) {
	requests := make(chan runtimeCommand, 1)
	expected := runtimeStatus{
		Run: runtimeRunStatus{State: runStateRunning, TotalTransactions: 46, ElapsedMs: 1234},
		Reader: runtimeReaderStatus{
			Workers: 1, LiveWorkers: 2, ReadingWorkers: 2, DrainingWorkers: 1,
			DrainingReadingWorkers: 1,
			ReadBatchSize:          50000, ReadTps: 123.5, RowsRead: 47,
			SourceDirectory: "data/part", SourceError: &readerSourceError{Category: "source", Operation: "read", RelativePath: "input.parquet", Message: "corrupt parquet"},
		},
		Throttler:     runtimeThrottlerStatus{RequestedTps: 200, AdmittedTps: 3, InstallationMode: throttlerInstalled},
		Sender:        runtimeSenderStatus{Workers: 32},
		ReaderChannel: runtimeChannelStatus{Capacity: 8, DepthBatches: 6, BufferedTransactions: 300000, BlockedSenders: 1, OldestBlockedSenderMs: 12, BlockedMs: 34, SentBatchesTotal: 2, SentTransactionsTotal: 4, ReceivedBatchesTotal: 1, ReceivedTransactionsTotal: 2, SentBatchesPerSecond: 1.5, SentTransactionsPerSecond: 3, ReceivedBatchesPerSecond: 0.5, ReceivedTransactionsPerSecond: 1},
		SenderChannel: runtimeChannelStatus{Capacity: 16, DepthBatches: 4, BufferedTransactions: 100000, BlockedSenders: 2, OldestBlockedSenderMs: 13, BlockedMs: 35, SentBatchesTotal: 3, SentTransactionsTotal: 6, ReceivedBatchesTotal: 2, ReceivedTransactionsTotal: 3, SentBatchesPerSecond: 2, SentTransactionsPerSecond: 4, ReceivedBatchesPerSecond: 1.5, ReceivedTransactionsPerSecond: 2.5},
		Config:        runtimeConfigStatusFromConfig(testConfig(t)),
	}
	rec := httptest.NewRecorder()
	go func() { (<-requests).statusReply <- expected }()
	snapshotHandler(requests, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reply code = %d, want %d", rec.Code, http.StatusOK)
	}
	var actual runtimeStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if got := actual; !reflect.DeepEqual(got, expected) {
		t.Errorf("actual = %+v, want %+v", got, expected)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	assertExactJSONKeys(t, root, []string{"config", "reader", "readerChannel", "run", "sender", "senderChannel", "throttler"})
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
	if string(root["config"]) == "null" {
		t.Error("config must be a JSON object")
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(root["config"], &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	assertExactJSONKeys(t, config, []string{"logging", "metricsWindowMs", "readerChannelCapacity", "readerReadBatchSize", "readerWorkers", "senderChannelCapacity", "senderWorkers", "throttlerInstallationMode", "throttlerRequestedTps"})
	var workers rangeConfig
	if err := json.Unmarshal(config["readerWorkers"], &workers); err != nil {
		t.Fatal(err)
	}
	if want := (rangeConfig{Default: 1, Min: 1, Max: 7, Step: 1, Unit: workersUnit, Mutability: immediate}); workers != want {
		t.Errorf("Reader workers config = %+v, want %+v", workers, want)
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

func TestSnapshotWirePreservesDistinctRuntimeValues(t *testing.T) {
	want := runtimeStatus{
		Run: runtimeRunStatus{State: runStatePaused, TotalTransactions: 101, ElapsedMs: 102},
		Reader: runtimeReaderStatus{
			Workers: 11, LiveWorkers: 12, IdleWorkers: 13, ReadingWorkers: 14, BlockedWorkers: 15,
			DrainingWorkers: 16, DrainingIdleWorkers: 17, DrainingReadingWorkers: 18, DrainingBlockedWorkers: 19,
			ReadBatchSize: 20, ReadTps: 21.5, RowsRead: 22, SourceDirectory: "reader-source",
			SourceError: &readerSourceError{Category: "reader-category", Operation: "reader-operation", RelativePath: "reader-path", Message: "reader-message"},
		},
		Throttler: runtimeThrottlerStatus{RequestedTps: 31, AdmittedTps: 32.5, InstallationMode: throttlerBypass},
		Sender: runtimeSenderStatus{
			Workers: 41, LiveWorkers: 42, IdleWorkers: 43, InFlightWorkers: 44, BackoffWorkers: 45,
			DrainingWorkers: 46, DrainingIdleWorkers: 47, DrainingInFlightWorkers: 48, DrainingBackoffWorkers: 49,
		},
		ReaderChannel: runtimeChannelStatus{
			Capacity: 51, DepthBatches: 52, BufferedTransactions: 53, BlockedSenders: 54, OldestBlockedSenderMs: 55,
			BlockedMs: 56, SentBatchesTotal: 57, SentTransactionsTotal: 58, ReceivedBatchesTotal: 59,
			ReceivedTransactionsTotal: 60, SentBatchesPerSecond: 61.5, SentTransactionsPerSecond: 62.5,
			ReceivedBatchesPerSecond: 63.5, ReceivedTransactionsPerSecond: 64.5,
		},
		SenderChannel: runtimeChannelStatus{
			Capacity: 71, DepthBatches: 72, BufferedTransactions: 73, BlockedSenders: 74, OldestBlockedSenderMs: 75,
			BlockedMs: 76, SentBatchesTotal: 77, SentTransactionsTotal: 78, ReceivedBatchesTotal: 79,
			ReceivedTransactionsTotal: 80, SentBatchesPerSecond: 81.5, SentTransactionsPerSecond: 82.5,
			ReceivedBatchesPerSecond: 83.5, ReceivedTransactionsPerSecond: 84.5,
		},
		Config: runtimeConfigStatusFromConfig(testConfig(t)),
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	const expectedJSON = `{
		"run":{"state":"paused","totalTransactions":101,"elapsedMs":102},
		"reader":{"workers":11,"liveWorkers":12,"idleWorkers":13,"readingWorkers":14,"blockedWorkers":15,
		"drainingWorkers":16,"drainingIdleWorkers":17,"drainingReadingWorkers":18,"drainingBlockedWorkers":19,
		"readBatchSize":20,"readTps":21.5,"rowsRead":22,"sourceDirectory":"reader-source",
		"sourceError":{"category":"reader-category","operation":"reader-operation","relativePath":"reader-path","message":"reader-message"}},
		"throttler":{"requestedTps":31,"admittedTps":32.5,"installationMode":"bypass"},
		"sender":{"workers":41,"liveWorkers":42,"idleWorkers":43,"inFlightWorkers":44,"backoffWorkers":45,
		"drainingWorkers":46,"drainingIdleWorkers":47,"drainingInFlightWorkers":48,"drainingBackoffWorkers":49},
		"readerChannel":{"capacity":51,"depthBatches":52,"bufferedTransactions":53,"blockedSenders":54,
		"oldestBlockedSenderMs":55,"blockedMs":56,"sentBatchesTotal":57,"sentTransactionsTotal":58,
		"receivedBatchesTotal":59,"receivedTransactionsTotal":60,"inputBatchesPerSecond":61.5,
		"inputTransactionsPerSecond":62.5,"outputBatchesPerSecond":63.5,"outputTransactionsPerSecond":64.5},
		"senderChannel":{"capacity":71,"depthBatches":72,"bufferedTransactions":73,"blockedSenders":74,
		"oldestBlockedSenderMs":75,"blockedMs":76,"sentBatchesTotal":77,"sentTransactionsTotal":78,
		"receivedBatchesTotal":79,"receivedTransactionsTotal":80,"inputBatchesPerSecond":81.5,
		"inputTransactionsPerSecond":82.5,"outputBatchesPerSecond":83.5,"outputTransactionsPerSecond":84.5}
	}`
	var expected map[string]any
	if err := json.Unmarshal([]byte(expectedJSON), &expected); err != nil {
		t.Fatal(err)
	}
	for name, value := range expected {
		if !reflect.DeepEqual(actual[name], value) {
			t.Errorf("wire %s = %+v, want %+v", name, actual[name], value)
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
	requests := make(chan runtimeCommand, 1)
	go func() {
		(<-requests).statusReply <- runtimeStatus{Run: runtimeRunStatus{State: runStateIdle}}
	}()
	recorder := httptest.NewRecorder()
	snapshotHandler(requests, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
	var root map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	var actual runtimeStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if want := (runtimeStatus{Run: runtimeRunStatus{State: runStateIdle}}); !reflect.DeepEqual(actual, want) {
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
	requests := make(chan runtimeCommand, 1)
	rec := httptest.NewRecorder()
	snapshotHandler(requests, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, snapshotPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("reply code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}
