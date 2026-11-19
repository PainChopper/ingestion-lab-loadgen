package main

import (
	"encoding/json"
	"os"
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
			BlockedMs:                     56,
			SentTransactionsPerSecond:     62.5,
			ReceivedTransactionsPerSecond: 64.5,
		},
		SenderChannel: channelSnapshot{
			Capacity: 71, DepthBatches: 72, BufferedTransactions: 73, BlockedSenders: 74, OldestBlockedSenderMs: 75,
			BlockedMs:                     76,
			SentTransactionsPerSecond:     82.5,
			ReceivedTransactionsPerSecond: 84.5,
		},
		Config: snapshotConfig(testConfig(t)),
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var actual any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected any
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("wire snapshot = %+v, want %+v", actual, expected)
	}
}
