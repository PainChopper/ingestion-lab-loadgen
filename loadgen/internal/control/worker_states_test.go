package control

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"
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
