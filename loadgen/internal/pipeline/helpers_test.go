package pipeline

import (
	"maps"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
)

func testPipelineOptions() Options {
	options := testSenderOptions()
	options.Source = Source{directory: "fixtures", files: []string{"fixtures/data.parquet"}}
	options.BatchSize = 2
	options.Readers = 1
	options.Senders = 1
	options.Rate = Rate{Installed: false}
	options.Logger = zap.NewNop()
	return options
}

func assertBlockedSender(t *testing.T, telemetry *batchQueue) {
	t.Helper()
	assertBlockedSenders(t, telemetry, 1)
}

func assertBlockedSenders(t *testing.T, telemetry *batchQueue, want int) {
	t.Helper()
	synctest.Wait()
	if got := telemetry.snapshot(time.Now()).BlockedSenders; got != want {
		t.Fatalf("blocked senders = %d, want %d", got, want)
	}
}

type fixtureRows struct {
	read     func([]Transaction) (int, error)
	closeErr error
	closed   bool
}

func (r *fixtureRows) Read(rows []Transaction) (int, error) { return r.read(rows) }

func (r *fixtureRows) Close() error {
	r.closed = true
	return r.closeErr
}

type fixtureFile struct {
	rows     RowReader
	openErr  error
	onOpen   func()
	closeErr error
	closed   bool
}

func (f *fixtureFile) OpenRows() (RowReader, error) {
	if f.onOpen != nil {
		f.onOpen()
	}
	return f.rows, f.openErr
}

func (f *fixtureFile) Close() error {
	f.closed = true
	return f.closeErr
}

func testSenderOptions() Options {
	return Options{
		SenderURL:          "http://127.0.0.1:8081/internal/ingest",
		SenderTimeout:      30 * time.Second,
		SenderMaxConns:     32,
		RetryDelays:        []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second},
		RetryJitterPercent: 20,
	}
}

func readerActivity(state ReaderWorkerState) ReaderActivity {
	return state.Activity
}

func readerDraining(state ReaderWorkerState) bool {
	return state.Draining
}

func readerBlockedAndDraining(state ReaderWorkerState) bool {
	return state.Activity == ReaderBlocked && readerDraining(state)
}

func senderActivity(state SenderWorkerState) SenderActivity {
	return state.Activity
}

func senderDraining(state SenderWorkerState) bool {
	return state.Draining
}

func senderInFlight(state SenderWorkerState) bool {
	return state.Activity == SenderInFlight
}

func senderInFlightAndDraining(state SenderWorkerState) bool {
	return senderInFlight(state) && senderDraining(state)
}

func senderBackoff(state SenderWorkerState) bool {
	return state.Activity == SenderBackoff
}

func projectWorkerStates[T, U any](states []T, project func(T) U) []U {
	projected := make([]U, 0, len(states))
	for _, state := range states {
		projected = append(projected, project(state))
	}
	return projected
}

func filterWorkerStates[T any](states []T, keep func(T) bool) []T {
	filtered := make([]T, 0, len(states))
	for _, state := range states {
		if keep(state) {
			filtered = append(filtered, state)
		}
	}
	return filtered
}

func assertWorkerStateMultiset[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	actual, expected := make(map[T]int), make(map[T]int)
	for _, state := range got {
		actual[state]++
	}
	for _, state := range want {
		expected[state]++
	}
	if !maps.Equal(actual, expected) {
		t.Fatalf("states = %v, want multiset %v", actual, expected)
	}
}
