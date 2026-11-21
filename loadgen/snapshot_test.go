package main

import (
	"encoding/json"
	"loadgen/internal/pipeline"
	"maps"
	"os"
	"reflect"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSnapshotWirePreservesDistinctRuntimeValues(t *testing.T) {
	want := runtimeSnapshot{
		Run: runSnapshot{State: runStateStopping, TotalTransactions: 101, ElapsedMs: 102},
		Reader: readerSnapshot{
			Workers: 11, WorkerStates: []pipeline.ReaderWorkerState{
				{Activity: pipeline.ReaderReading}, {Activity: pipeline.ReaderReading},
				{Activity: pipeline.ReaderBlocked, Draining: true}, {Activity: pipeline.ReaderIdle},
			},
			ReadBatchSize: 20, ReadTps: 21.5, RowsRead: 22, SourceDirectory: "reader-source",
			SourceError: &readerSourceError{Category: "reader-category", Operation: "reader-operation", RelativePath: "reader-path", Message: "reader-message"},
		},
		Throttler: throttlerSnapshot{RequestedTps: 31, AdmittedTps: 32.5, Installed: false},
		Sender: senderSnapshot{
			Workers: 41, WorkerStates: []pipeline.SenderWorkerState{
				{Activity: pipeline.SenderInFlight, Draining: true},
				{Activity: pipeline.SenderBackoff}, {Activity: pipeline.SenderBackoff},
				{Activity: pipeline.SenderBackoff}, {Activity: pipeline.SenderIdle, Draining: true},
			},
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
	canonicalWorkerLists(t, actual)
	canonicalWorkerLists(t, expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("wire snapshot = %+v, want %+v", actual, expected)
	}
}

func TestSnapshotLogFieldsPreserveDistinctRuntimeValues(t *testing.T) {
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot runtimeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"event": "runtime_summary", "state": "stopping",
		"elapsed_ms": int64(102), "total_transactions": int64(101),
		"reader_read_tps": 21.5, "reader_rows_read": int64(22),
		"reader_workers": int64(11), "reader_live_workers": int64(4),
		"throttler_requested_tps": int64(31), "throttler_admitted_tps": 32.5, "installed": false,
		"reader_channel_capacity": int64(51), "reader_channel_depth_batches": int64(52),
		"reader_channel_buffered_transactions": int64(53), "reader_channel_blocked_senders": int64(54),
		"reader_channel_oldest_blocked_ms": int64(55), "reader_channel_blocked_ms": int64(56),
		"reader_channel_input_tps": 62.5, "reader_channel_output_tps": 64.5,
		"sender_channel_capacity": int64(71), "sender_channel_depth_batches": int64(72),
		"sender_channel_buffered_transactions": int64(73), "sender_channel_blocked_senders": int64(74),
		"sender_channel_oldest_blocked_ms": int64(75), "sender_channel_blocked_ms": int64(76),
		"sender_channel_input_tps": 82.5, "sender_channel_output_tps": 84.5,
		"sender_workers": int64(41), "sender_live_workers": int64(5),
		"source_error_category": "reader-category", "source_error_operation": "reader-operation", "source_error_relative_path": "reader-path",
	}
	for _, name := range []string{"source error", "no source error"} {
		t.Run(name, func(t *testing.T) {
			snapshot := snapshot
			want := maps.Clone(want)
			if name == "no source error" {
				snapshot.Reader.SourceError = nil
				want["source_error_category"] = ""
				want["source_error_operation"] = ""
				want["source_error_relative_path"] = ""
			}
			core, entries := observer.New(zap.InfoLevel)
			zap.New(core).Info("summary", snapshotLogFields(snapshot)...)
			if got := entries.All()[0].ContextMap(); !reflect.DeepEqual(got, want) {
				t.Fatalf("log fields = %#v, want %#v", got, want)
			}
		})
	}
}
