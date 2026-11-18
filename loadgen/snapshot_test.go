package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSnapshotWirePreservesDistinctRuntimeValues(t *testing.T) {
	want := runtimeSnapshot{
		Run: runSnapshot{State: runStateStopping, TotalTransactions: 101, ElapsedMs: 102},
		Reader: readerSnapshot{
			Workers: 11, LiveWorkers: 12, IdleWorkers: 13, ReadingWorkers: 14, BlockedWorkers: 15,
			DrainingWorkers: 16, DrainingIdleWorkers: 17, DrainingReadingWorkers: 18, DrainingBlockedWorkers: 19,
			ReadBatchSize: 20, ReadTps: 21.5, RowsRead: 22, SourceDirectory: "reader-source",
			SourceError: &readerSourceError{Category: "reader-category", Operation: "reader-operation", RelativePath: "reader-path", Message: "reader-message"},
		},
		Throttler: throttlerSnapshot{RequestedTps: 31, AdmittedTps: 32.5, Installed: false},
		Sender: senderSnapshot{
			Workers: 41, LiveWorkers: 42, IdleWorkers: 43, InFlightWorkers: 44, BackoffWorkers: 45,
			DrainingWorkers: 46, DrainingIdleWorkers: 47, DrainingInFlightWorkers: 48, DrainingBackoffWorkers: 49,
		},
		ReaderChannel: channelSnapshot{
			Capacity: 51, DepthBatches: 52, BufferedTransactions: 53, BlockedSenders: 54, OldestBlockedSenderMs: 55,
			BlockedMs: 56, SentBatchesTotal: 57, SentTransactionsTotal: 58, ReceivedBatchesTotal: 59,
			ReceivedTransactionsTotal: 60, SentBatchesPerSecond: 61.5, SentTransactionsPerSecond: 62.5,
			ReceivedBatchesPerSecond: 63.5, ReceivedTransactionsPerSecond: 64.5,
		},
		SenderChannel: channelSnapshot{
			Capacity: 71, DepthBatches: 72, BufferedTransactions: 73, BlockedSenders: 74, OldestBlockedSenderMs: 75,
			BlockedMs: 76, SentBatchesTotal: 77, SentTransactionsTotal: 78, ReceivedBatchesTotal: 79,
			ReceivedTransactionsTotal: 80, SentBatchesPerSecond: 81.5, SentTransactionsPerSecond: 82.5,
			ReceivedBatchesPerSecond: 83.5, ReceivedTransactionsPerSecond: 84.5,
		},
		Config: snapshotConfig(testConfig(t)),
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
		"run":{"state":"stopping","totalTransactions":101,"elapsedMs":102},
		"reader":{"workers":11,"liveWorkers":12,"idleWorkers":13,"readingWorkers":14,"blockedWorkers":15,
		"drainingWorkers":16,"drainingIdleWorkers":17,"drainingReadingWorkers":18,"drainingBlockedWorkers":19,
		"readBatchSize":20,"readTps":21.5,"rowsRead":22,"sourceDirectory":"reader-source",
		"sourceError":{"category":"reader-category","operation":"reader-operation","relativePath":"reader-path","message":"reader-message"}},
		"throttler":{"requestedTps":31,"admittedTps":32.5,"installed":false},
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
