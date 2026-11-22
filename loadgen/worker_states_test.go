package main

import (
	"os"
	"strings"
	"testing"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

func TestWorkerCountsAndCLI(t *testing.T) {
	for _, test := range []struct {
		name   string
		reader []api.ReaderWorkerState
		sender []api.SenderWorkerState
		wantR  readerWorkerCounts
		wantS  senderWorkerCounts
		lineR  string
		lineS  string
	}{
		{
			name:   "empty",
			reader: []api.ReaderWorkerState{}, sender: []api.SenderWorkerState{},
			lineR: "configured=1 live=0 reading=0 idle=0 blocked=0 draining=0",
			lineS: "configured=2 live=0 in-flight=0 idle=0 backoff=0 draining=0",
		},
		{
			name: "two owned workers",
			reader: []api.ReaderWorkerState{
				{Activity: api.ReaderReading}, {Activity: api.ReaderBlocked, Draining: true},
			},
			sender: []api.SenderWorkerState{
				{Activity: api.SenderInFlight}, {Activity: api.SenderBackoff, Draining: true},
			},
			wantR: readerWorkerCounts{LiveWorkers: 2, ReadingWorkers: 1, BlockedWorkers: 1, DrainingWorkers: 1},
			wantS: senderWorkerCounts{LiveWorkers: 2, InFlightWorkers: 1, BackoffWorkers: 1, DrainingWorkers: 1},
			lineR: "configured=1 live=2 reading=1 idle=0 blocked=1 draining=1",
			lineS: "configured=2 live=2 in-flight=1 idle=0 backoff=1 draining=1",
		},
		{
			name: "distinct counts and draining false",
			reader: []api.ReaderWorkerState{
				{Activity: api.ReaderReading},
				{Activity: api.ReaderBlocked}, {Activity: api.ReaderBlocked},
				{Activity: api.ReaderIdle, Draining: true},
				{Activity: api.ReaderIdle, Draining: true},
				{Activity: api.ReaderIdle, Draining: true},
			},
			sender: []api.SenderWorkerState{
				{Activity: api.SenderInFlight, Draining: true},
				{Activity: api.SenderInFlight, Draining: true},
				{Activity: api.SenderInFlight},
				{Activity: api.SenderBackoff}, {Activity: api.SenderIdle},
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
			card := formatSnapshot(api.Snapshot{
				Reader: api.ReaderSnapshot{Workers: 1, WorkerStates: test.reader},
				Sender: api.SenderSnapshot{Workers: 2, WorkerStates: test.sender},
			}, api.ConfigResponse{})
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
	snapshot, err := api.DecodeSnapshot(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	card := formatSnapshot(snapshot, api.ConfigResponse{})
	for _, line := range []string{
		"configured=11 live=4 reading=2 idle=1 blocked=1 draining=1",
		"configured=41 live=5 in-flight=1 idle=1 backoff=3 draining=2",
	} {
		if !strings.Contains(card, line) {
			t.Errorf("fixture CLI missing %q: %s", line, card)
		}
	}
}
