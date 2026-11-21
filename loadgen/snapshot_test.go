package main

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSnapshotWirePreservesDistinctRuntimeValues(t *testing.T) {
	want := runtimeSnapshot{
		CapturedAtMs: 103,
		Run:          runSnapshot{State: runStateStopping, TotalTransactions: 101, ElapsedMs: 102},
		Reader: readerSnapshot{
			Workers: 11, WorkerStates: []readerWorkerState{
				{Activity: readerWireReading}, {Activity: readerWireReading},
				{Activity: readerWireBlocked, Draining: true}, {Activity: readerWireIdle},
			},
			RowsRead:    22,
			SourceError: &readerSourceError{Operation: "reader-operation", Path: "reader-path", Message: "reader-message"},
		},
		Throttler: throttlerSnapshot{RequestedTps: 31, Installed: false},
		Sender: senderSnapshot{
			Workers: 41, WorkerStates: []senderWorkerState{
				{Activity: senderWireInFlight, Draining: true},
				{Activity: senderWireBackoff}, {Activity: senderWireBackoff},
				{Activity: senderWireBackoff}, {Activity: senderWireIdle, Draining: true},
			},
		},
		ReaderChannel: channelSnapshot{
			DepthBatches: 52, BlockedSenders: 54, OldestBlockedSenderMs: 55,
			BlockedMs:               56,
			InputTransactionsTotal:  62,
			OutputTransactionsTotal: 64,
		},
		SenderChannel: channelSnapshot{
			DepthBatches: 72, BlockedSenders: 74, OldestBlockedSenderMs: 75,
			BlockedMs:               76,
			InputTransactionsTotal:  82,
			OutputTransactionsTotal: 84,
		},
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
	config := testConfig(t)
	config.ReaderChannel.Capacity = 51
	config.SenderChannel.Capacity = 71
	want := map[string]any{
		"event": "runtime_summary", "state": "stopping", "captured_at_ms": int64(103),
		"elapsed_ms": int64(102), "total_transactions": int64(101),
		"reader_rows_read": int64(22),
		"reader_workers":   int64(11), "reader_live_workers": int64(4),
		"throttler_requested_tps": int64(31), "installed": false,
		"reader_channel_capacity": int64(51), "reader_channel_depth_batches": int64(52),
		"reader_channel_blocked_senders":   int64(54),
		"reader_channel_oldest_blocked_ms": int64(55), "reader_channel_blocked_ms": int64(56),
		"reader_channel_input_transactions_total": int64(62), "reader_channel_output_transactions_total": int64(64),
		"sender_channel_capacity": int64(71), "sender_channel_depth_batches": int64(72),
		"sender_channel_blocked_senders":   int64(74),
		"sender_channel_oldest_blocked_ms": int64(75), "sender_channel_blocked_ms": int64(76),
		"sender_channel_input_transactions_total": int64(82), "sender_channel_output_transactions_total": int64(84),
		"sender_workers": int64(41), "sender_live_workers": int64(5),
		"source_error_operation": "reader-operation", "source_error_path": "reader-path",
	}
	for _, name := range []string{"source error", "no source error"} {
		t.Run(name, func(t *testing.T) {
			snapshot := snapshot
			want := maps.Clone(want)
			if name == "no source error" {
				snapshot.Reader.SourceError = nil
				want["source_error_operation"] = ""
				want["source_error_path"] = ""
			}
			core, entries := observer.New(zap.InfoLevel)
			zap.New(core).Info("summary", snapshotLogFields(snapshot, config)...)
			if got := entries.All()[0].ContextMap(); !reflect.DeepEqual(got, want) {
				t.Fatalf("log fields = %#v, want %#v", got, want)
			}
		})
	}
}
