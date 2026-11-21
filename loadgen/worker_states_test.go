package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"loadgen/internal/pipeline"
)

func TestSnapshotReaderWorkerMapping(t *testing.T) {
	for _, test := range []struct {
		name     string
		activity pipeline.ReaderActivity
		want     string
	}{
		{name: "zero idle", want: "idle"},
		{name: "reading", activity: pipeline.ReaderReading, want: "reading"},
		{name: "blocked", activity: pipeline.ReaderBlocked, want: "blocked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, draining := range []bool{false, true} {
				got := snapshotReaderWorkers([]pipeline.ReaderWorkerState{{Activity: test.activity, Draining: draining}})
				if len(got) != 1 || string(got[0].Activity) != test.want || got[0].Draining != draining {
					t.Fatalf("mapped reader (draining=%t) = %+v, want %s", draining, got, test.want)
				}
			}
		})
	}
	for _, source := range [][]pipeline.ReaderWorkerState{nil, {}} {
		if got := snapshotReaderWorkers(source); got == nil || len(got) != 0 {
			t.Fatalf("empty reader mapping = %#v, want []", got)
		}
	}
	for _, activity := range []pipeline.ReaderActivity{-1, pipeline.ReaderBlocked + 1} {
		t.Run("invalid "+strconv.Itoa(int(activity)), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid reader activity did not panic")
				}
			}()
			snapshotReaderWorkers([]pipeline.ReaderWorkerState{{Activity: activity}})
		})
	}
}

func TestSnapshotSenderWorkerMapping(t *testing.T) {
	for _, test := range []struct {
		name     string
		activity pipeline.SenderActivity
		want     string
	}{
		{name: "zero idle", want: "idle"},
		{name: "in flight", activity: pipeline.SenderInFlight, want: "inFlight"},
		{name: "backoff", activity: pipeline.SenderBackoff, want: "backoff"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, draining := range []bool{false, true} {
				got := snapshotSenderWorkers([]pipeline.SenderWorkerState{{Activity: test.activity, Draining: draining}})
				if len(got) != 1 || string(got[0].Activity) != test.want || got[0].Draining != draining {
					t.Fatalf("mapped sender (draining=%t) = %+v, want %s", draining, got, test.want)
				}
			}
		})
	}
	for _, source := range [][]pipeline.SenderWorkerState{nil, {}} {
		if got := snapshotSenderWorkers(source); got == nil || len(got) != 0 {
			t.Fatalf("empty sender mapping = %#v, want []", got)
		}
	}
	for _, activity := range []pipeline.SenderActivity{-1, pipeline.SenderBackoff + 1} {
		t.Run("invalid "+strconv.Itoa(int(activity)), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid sender activity did not panic")
				}
			}()
			snapshotSenderWorkers([]pipeline.SenderWorkerState{{Activity: activity}})
		})
	}
}

func TestWorkerCountsAndCLI(t *testing.T) {
	for _, test := range []struct {
		name   string
		reader []readerWorkerState
		sender []senderWorkerState
		wantR  readerWorkerCounts
		wantS  senderWorkerCounts
		lineR  string
		lineS  string
	}{
		{
			name:   "empty",
			reader: []readerWorkerState{}, sender: []senderWorkerState{},
			lineR: "configured=1 live=0 reading=0 idle=0 blocked=0 draining=0",
			lineS: "configured=2 live=0 in-flight=0 idle=0 backoff=0 draining=0",
		},
		{
			name: "two owned workers",
			reader: []readerWorkerState{
				{Activity: readerWireReading}, {Activity: readerWireBlocked, Draining: true},
			},
			sender: []senderWorkerState{
				{Activity: senderWireInFlight}, {Activity: senderWireBackoff, Draining: true},
			},
			wantR: readerWorkerCounts{LiveWorkers: 2, ReadingWorkers: 1, BlockedWorkers: 1, DrainingWorkers: 1},
			wantS: senderWorkerCounts{LiveWorkers: 2, InFlightWorkers: 1, BackoffWorkers: 1, DrainingWorkers: 1},
			lineR: "configured=1 live=2 reading=1 idle=0 blocked=1 draining=1",
			lineS: "configured=2 live=2 in-flight=1 idle=0 backoff=1 draining=1",
		},
		{
			name: "distinct counts and draining false",
			reader: []readerWorkerState{
				{Activity: readerWireReading},
				{Activity: readerWireBlocked}, {Activity: readerWireBlocked},
				{Activity: readerWireIdle, Draining: true},
				{Activity: readerWireIdle, Draining: true},
				{Activity: readerWireIdle, Draining: true},
			},
			sender: []senderWorkerState{
				{Activity: senderWireInFlight, Draining: true},
				{Activity: senderWireInFlight, Draining: true},
				{Activity: senderWireInFlight},
				{Activity: senderWireBackoff}, {Activity: senderWireIdle},
			},
			wantR: readerWorkerCounts{LiveWorkers: 6, ReadingWorkers: 1, BlockedWorkers: 2, IdleWorkers: 3, DrainingWorkers: 3},
			wantS: senderWorkerCounts{LiveWorkers: 5, InFlightWorkers: 3, BackoffWorkers: 1, IdleWorkers: 1, DrainingWorkers: 2},
			lineR: "configured=1 live=6 reading=1 idle=3 blocked=2 draining=3",
			lineS: "configured=2 live=5 in-flight=3 idle=1 backoff=1 draining=2",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := countReaderWorkers(test.reader); got != test.wantR {
				t.Errorf("Reader counts = %+v, want %+v", got, test.wantR)
			}
			if got := countSenderWorkers(test.sender); got != test.wantS {
				t.Errorf("Sender counts = %+v, want %+v", got, test.wantS)
			}
			card := formatSnapshot(runtimeSnapshot{
				Reader: readerSnapshot{Workers: 1, WorkerStates: test.reader},
				Sender: senderSnapshot{Workers: 2, WorkerStates: test.sender},
			}, configSnapshot{})
			for _, line := range []string{test.lineR, test.lineS} {
				if !strings.Contains(card, "Workers:                  "+line+"\n") {
					t.Errorf("CLI missing %q: %s", line, card)
				}
			}
		})
	}
}

func TestCLIWorkerCountsFromSharedFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := decodeStrictSnapshot(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	card := formatSnapshot(snapshot, configSnapshot{})
	for _, line := range []string{
		"configured=11 live=4 reading=2 idle=1 blocked=1 draining=1",
		"configured=41 live=5 in-flight=1 idle=1 backoff=3 draining=2",
	} {
		if !strings.Contains(card, line) {
			t.Errorf("fixture CLI missing %q: %s", line, card)
		}
	}
}

func TestDecodeStrictSnapshotWorkerStates(t *testing.T) {
	for _, pool := range []string{"reader", "sender"} {
		t.Run(pool, func(t *testing.T) {
			for _, test := range []struct {
				name, list string
				valid      bool
			}{
				{name: "empty", list: `[]`, valid: true},
				{name: "duplicates", list: `[{"activity":"idle","draining":false},{"activity":"idle","draining":false}]`, valid: true},
				{name: "null list", list: `null`},
				{name: "object list", list: `{}`},
				{name: "null item", list: `[null]`},
				{name: "unknown activity", list: `[{"activity":"unknown","draining":false}]`},
				{name: "missing activity", list: `[{"draining":false}]`},
				{name: "null activity", list: `[{"activity":null,"draining":false}]`},
				{name: "numeric activity", list: `[{"activity":1,"draining":false}]`},
				{name: "missing draining", list: `[{"activity":"idle"}]`},
				{name: "null draining", list: `[{"activity":"idle","draining":null}]`},
				{name: "string draining", list: `[{"activity":"idle","draining":"false"}]`},
				{name: "extra id", list: `[{"activity":"idle","draining":false,"id":1}]`},
			} {
				t.Run(test.name, func(t *testing.T) {
					body := `{"run":{"state":"running"},"` + pool + `":{"workerStates":` + test.list + `}}`
					_, err := decodeStrictSnapshot(strings.NewReader(body))
					if (err == nil) != test.valid {
						t.Fatalf("decode error = %v, want valid=%t", err, test.valid)
					}
				})
			}
			for _, activity := range []string{"reading", "blocked", "inFlight", "backoff"} {
				body := `{"run":{"state":"running"},"` + pool + `":{"workerStates":[{"activity":"` + activity + `","draining":true}]}}`
				_, err := decodeStrictSnapshot(strings.NewReader(body))
				valid := pool == "reader" && (activity == "reading" || activity == "blocked") ||
					pool == "sender" && (activity == "inFlight" || activity == "backoff")
				if (err == nil) != valid {
					t.Errorf("activity %s: error = %v, want valid=%t", activity, err, valid)
				}
			}
		})
	}
}

func canonicalWorkerLists(t *testing.T, value any) {
	t.Helper()
	root := value.(map[string]any)
	for _, pool := range []string{"reader", "sender"} {
		object := root[pool].(map[string]any)
		states := object["workerStates"].([]any)
		// JSON strings retain exact item shape and multiplicity, with no ordering contract.
		counts := make(map[string]int)
		for _, state := range states {
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			counts[string(encoded)]++
		}
		object["workerStates"] = counts
	}
}
