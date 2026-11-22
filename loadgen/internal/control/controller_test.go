package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewControlStateInitializesControls(t *testing.T) {
	for _, test := range []struct {
		name                           string
		readerCapacity, senderCapacity int
		requestedTPS                   int
	}{
		{name: "nonzero initial values", readerCapacity: 2, senderCapacity: 4, requestedTPS: 1_000},
		{name: "zero initial values"},
	} {
		t.Run(test.name, func(t *testing.T) {
			loaded := controllerConfig()
			loaded.Reader.ReadBatchSize = 2000
			loaded.Reader.Workers.Initial = 3
			loaded.Sender.Workers.Initial = 7
			loaded.ReaderChannel.Capacity = test.readerCapacity
			loaded.SenderChannel.Capacity = test.senderCapacity
			loaded.Throttler.RequestedTPS.Initial = test.requestedTPS
			loaded.Throttler.Installed = false
			state := newTestController(loaded, zap.NewNop())
			snapshot := state.snapshotAt(nil, time.Now())
			if got := snapshot.Run.State; got != api.RunStateStarting {
				t.Fatalf("initial run state = %q, want %q", got, api.RunStateStarting)
			}
			if snapshot.Reader.WorkerStates == nil || len(snapshot.Reader.WorkerStates) != 0 ||
				snapshot.Sender.WorkerStates == nil || len(snapshot.Sender.WorkerStates) != 0 {
				t.Fatalf("before Start worker states must be []: %+v / %+v", snapshot.Reader, snapshot.Sender)
			}
			got := []int{
				snapshot.Reader.Workers,
				snapshot.Throttler.RequestedTps, snapshot.Sender.Workers,
			}
			want := []int{
				loaded.Reader.Workers.Initial,
				loaded.Throttler.RequestedTPS.Initial, loaded.Sender.Workers.Initial,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("initial controls = %v, want %v", got, want)
			}
			if got := snapshot.Throttler.Installed; got != loaded.Throttler.Installed {
				t.Fatalf("initial installed = %t, want %t", got, loaded.Throttler.Installed)
			}
			core, entries := observer.New(zap.InfoLevel)
			zap.New(core).Info("summary", snapshotLogFields(snapshot, loaded)...)
			fields := entries.All()[0].ContextMap()
			if fields["reader_channel_capacity"] != int64(loaded.ReaderChannel.Capacity) ||
				fields["sender_channel_capacity"] != int64(loaded.SenderChannel.Capacity) {
				t.Fatalf("startup log lost configured channel capacities: %v", fields)
			}
		})
	}
}

func TestSignalKeepsZeroTPSUntilGraceExpires(t *testing.T) {
	cfg := controllerConfig()
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}}); err != nil {
		t.Fatal(err)
	}
	cfg.Throttler.RequestedTPS.Initial = 0
	core, events := observer.New(zap.InfoLevel)
	synctest.Test(t, func(t *testing.T) {
		state := newTestController(cfg, zap.New(core))
		loop := startTestEventLoop(t, state, testGrace)
		synctest.Wait()
		// Each batch has one row; rows beyond the queue and active Readers have reached the Throttler.
		before := loop.snapshotInState(t, api.RunStateRunning)
		if before.Reader.RowsRead <= int64(cfg.ReaderChannel.Capacity+cfg.Reader.Workers.Initial) {
			t.Fatalf("pipeline did not reach the zero-rate Throttler: %+v", before)
		}
		loop.cancel()
		synctest.Wait()
		stopping := loop.snapshotInState(t, api.RunStateStopping)
		throttlerChanged := !stopping.Throttler.Installed || stopping.Throttler.RequestedTps != 0
		senderHasWork := stopping.SenderChannel.DepthBatches != 0 ||
			senderActivityCount(stopping.Sender.WorkerStates, api.SenderInFlight) != 0 || senderActivityCount(stopping.Sender.WorkerStates, api.SenderBackoff) != 0
		if throttlerChanged || senderHasWork || stopping.Run.TotalTransactions != 0 {
			t.Fatalf("shutdown changed the zero-rate throttler: %+v", stopping)
		}

		assertExitAtDeadline(t, loop)
		drainLogs := events.FilterMessage("pipeline drain timed out").All()
		if len(drainLogs) != 1 || drainLogs[0].ContextMap()["event"] != "pipeline_drain_timed_out" {
			t.Fatalf("drain event=%v", drainLogs)
		}
		rowsRead := stopping.Reader.RowsRead
		if rowsRead == 0 {
			t.Fatal("fixture produced no rows")
		}
		assertFinalTransactions(t, events, 0, rowsRead)
	})
}

func TestSignalDeadlineCancelsInFlightHTTP(t *testing.T) {
	cfg := controllerConfig()
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}}); err != nil {
		t.Fatal(err)
	}
	cfg.ReaderChannel.Capacity = 0
	cfg.SenderChannel.Capacity = 0
	cfg.Sender.Workers.Initial = 1

	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		canceled := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		receiver := httptest.NewTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return
			}
			close(entered)
			select {
			case <-r.Context().Done():
				close(canceled)
			case <-release:
			}
		}))
		core, events := observer.New(zap.InfoLevel)
		loop := startTestEventLoopWithTestReceiver(t, cfg, zap.New(core), receiver)
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("Sender did not enter HTTP")
		}
		loop.cancel()
		synctest.Wait()
		stopping := loop.snapshotInState(t, api.RunStateStopping)
		if slices.ContainsFunc(stopping.Reader.WorkerStates, func(w api.ReaderWorkerState) bool { return w.Activity == api.ReaderReading }) {
			t.Fatalf("Reader is still reading after stop: %+v", stopping.Reader)
		}
		select {
		case <-canceled:
			t.Fatal("HTTP request canceled before the grace deadline")
		default:
		}
		assertExitAtDeadline(t, loop)
		select {
		case <-canceled:
		default:
			t.Fatal("forwarded HTTP request context was not canceled")
		}
		assertFinalTransactions(t, events, 0, stopping.Reader.RowsRead)
	})
}

func TestSignalDuringSourceDiscoveryPreventsReaderStart(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "discovery completes during grace"
		if held {
			name = "discovery remains blocked at deadline"
		}
		t.Run(name, func(t *testing.T) {
			cfg := controllerConfig()
			source := testDiscoveredSource(t)
			core, events := observer.New(zap.InfoLevel)
			synctest.Test(t, func(t *testing.T) {
				read := make(chan struct{}, 1)
				found := SourceResult{Source: source}
				ready := make(chan SourceResult, 1)
				state := newTestController(cfg, zap.New(core))
				state.pipelineOptions.OpenFile = func(string) (pipeline.ReaderFile, error) {
					read <- struct{}{}
					return nil, errors.New("unexpected open after stop")
				}
				loop := startTestEventLoopWithSource(t, state, testGrace, ready)
				synctest.Wait()
				starting := loop.snapshotInState(t, api.RunStateStarting)
				if starting.Reader.SourceError != nil {
					t.Fatalf("initial source error before Start: %+v", starting.Reader)
				}

				loop.cancel()
				synctest.Wait()
				stopping := loop.snapshotInState(t, api.RunStateStopping)
				if len(stopping.Reader.WorkerStates) != 0 {
					t.Fatal("Reader started during source discovery")
				}
				if held {
					assertExitAtDeadline(t, loop)
					ready <- found
				} else {
					ready <- found
					if err := loop.awaitExit(t); err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				select {
				case <-read:
					t.Fatal("Reader opened a file after stopping")
				default:
				}
				if final := assertFinalTransactions(t, events, 0, 0); final["partial"] != held {
					t.Fatalf("final log = %v, want partial=%t", final, held)
				}
			})
		})
	}
}

func TestSignalDeadlineDoesNotWaitForBlockedReader(t *testing.T) {
	cfg := controllerConfig()
	source := testDiscoveredSource(t)
	core, events := observer.New(zap.InfoLevel)
	synctest.Test(t, func(t *testing.T) {
		inRead := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()

		ready := make(chan SourceResult, 1)
		ready <- SourceResult{Source: source}
		first := true
		state := newTestController(cfg, zap.New(core))
		state.pipelineOptions.OpenFile = func(string) (pipeline.ReaderFile, error) {
			return testReaderFile{rows: &testRowReader{read: func(rows []pipeline.Transaction) (int, error) {
				if first {
					first = false
					rows[0] = pipeline.Transaction{ClientID: "read-before-stop"}
					return 1, nil
				}
				close(inRead)
				<-release
				return 0, io.EOF
			}}}, nil
		}
		loop := startTestEventLoopWithSource(t, state, testGrace, ready)
		select {
		case <-inRead:
		case <-time.After(time.Second):
			t.Fatal("Reader did not enter blocking read")
		}
		loop.snapshotInState(t, api.RunStateRunning)

		loop.cancel()
		synctest.Wait()
		loop.snapshotInState(t, api.RunStateStopping)
		assertExitAtDeadline(t, loop)
		if final := assertFinalTransactions(t, events, 0, 1); final["partial"] != true {
			t.Fatalf("blocked Reader must produce a partial final log: %v", final)
		}
		unblock()
		synctest.Wait()

		assertFinalTransactions(t, events, 0, 1)
	})
}

func assertExitAtDeadline(t *testing.T, loop testEventLoop) {
	t.Helper()
	time.Sleep(testGrace - time.Nanosecond)
	synctest.Wait()
	select {
	case <-loop.controller.done:
		t.Fatal("event loop exited before the deadline")
	default:
	}
	time.Sleep(time.Nanosecond)
	synctest.Wait()
	select {
	case <-loop.controller.done:
		if err := <-loop.result; err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("event loop did not exit at the deadline")
	}
}

func TestRunStartupSourceErrorReturnsWithoutSignal(t *testing.T) {
	cfg := controllerConfig()
	cfg.Source.Path = filepath.Join(t.TempDir(), "*.parquet")
	core, events := observer.New(zap.InfoLevel)
	state := newTestController(cfg, zap.New(core))
	loop := startTestEventLoop(t, state, testGrace)
	var sourceError *pipeline.SourceError
	if err := loop.awaitExit(t); !errors.As(err, &sourceError) || sourceError.Op != "glob" || sourceError.Path != cfg.Source.Path {
		t.Fatalf("run error = %v, want missing source diagnostic", err)
	}
	snapshot := state.snapshotAt(nil, time.Now())
	if snapshot.Run.State != api.RunStateFaulted {
		t.Fatalf("startup snapshot state = %q, want %q", snapshot.Run.State, api.RunStateFaulted)
	}
	if got := snapshot.Reader.SourceError; got == nil || got.Operation != "glob" || got.Path != cfg.Source.Path || got.Message != sourceError.Error() {
		t.Fatalf("startup source diagnostic = %+v, want original glob and cause", got)
	}
	faults := events.FilterField(zap.String("event", "reader_source_failed")).All()
	if len(faults) != 1 {
		t.Fatalf("source fault log count = %d, want 1", len(faults))
	}
	if fields := faults[0].ContextMap(); fields["operation"] != "glob" || fields["path"] != cfg.Source.Path || fields["error"] != sourceError.Error() {
		t.Fatalf("source fault log = %v", fields)
	}
	if snapshot.Reader.WorkerStates == nil || len(snapshot.Reader.WorkerStates) != 0 ||
		snapshot.Sender.WorkerStates == nil || len(snapshot.Sender.WorkerStates) != 0 ||
		snapshot.Reader.Workers != cfg.Reader.Workers.Initial || snapshot.Sender.Workers != cfg.Sender.Workers.Initial {
		t.Fatalf("startup source fault worker states/targets = %+v / %+v", snapshot.Reader, snapshot.Sender)
	}
	if final := assertFinalTransactions(t, events, 0, 0); final["state"] != string(api.RunStateFaulted) {
		t.Fatalf("startup fault final event = %v", final)
	}
}

func TestEventLoopDrainsAfterUnexpectedHTTPServerExit(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "serve error", err: io.ErrUnexpectedEOF},
		{name: "unexpected clean exit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := controllerConfig()
			cfg.Reader.Workers.Initial, cfg.Sender.Workers.Initial = 1, 1
			cfg.Reader.ReadBatchSize = 1
			cfg.ReaderChannel.Capacity, cfg.SenderChannel.Capacity = 0, 0
			cfg.Throttler.Installed = false
			directory := t.TempDir()
			for _, name := range []string{"a.parquet", "b.parquet"} {
				if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source, err := pipeline.DiscoverSource(filepath.Join(directory, "*.parquet"))
			if err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				const grace = time.Second
				core, logs := observer.New(zap.InfoLevel)
				state := newTestController(cfg, zap.New(core))
				releaseRead, releaseDelivery, releaseExtraOpen := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var readOnce, deliveryOnce sync.Once
				unblockRead := func() { readOnce.Do(func() { close(releaseRead) }) }
				unblockDelivery := func() { deliveryOnce.Do(func() { close(releaseDelivery) }) }
				defer close(releaseExtraOpen)
				defer unblockRead()
				defer unblockDelivery()
				opens, reads, delivered := 0, 0, 0
				state.pipelineOptions.OpenFile = func(string) (pipeline.ReaderFile, error) {
					opens++
					if opens > 1 {
						<-releaseExtraOpen
						return nil, io.ErrUnexpectedEOF
					}
					return testReaderFile{rows: &testRowReader{read: func(rows []pipeline.Transaction) (int, error) {
						reads++
						if reads == 1 {
							rows[0] = pipeline.Transaction{ClientID: "accepted-before-server-exit"}
							return 1, nil
						}
						<-releaseRead
						return 0, io.EOF
					}}}, nil
				}
				state.pipelineOptions.Deliver = func(ctx context.Context, rows []pipeline.Transaction, _ int) error {
					if len(rows) != 1 || rows[0].ClientID != "accepted-before-server-exit" {
						return fmt.Errorf("unexpected delivery: %+v", rows)
					}
					select {
					case <-releaseDelivery:
						delivered += len(rows)
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ready := make(chan SourceResult, 1)
				ready <- SourceResult{Source: source}
				serverDone := make(chan error)
				done := make(chan struct{})
				var deadline time.Time
				var runErr error
				go func() {
					defer close(done)
					deadline, runErr = state.Run(ctx, Events{Source: ready, ServerDone: serverDone}, grace)
				}()
				synctest.Wait()
				before, err := state.Snapshot(t.Context())
				if err != nil || before.Run.State != api.RunStateRunning || senderActivityCount(before.Sender.WorkerStates, api.SenderInFlight) != 1 || reads != 2 {
					t.Fatalf("pipeline not held in delivery/read: snapshot=%+v reads=%d error=%v", before, reads, err)
				}
				failedAt := time.Now()
				serverDone <- test.err
				synctest.Wait()
				faulted, err := state.Snapshot(t.Context())
				if err != nil || faulted.Run.State != api.RunStateFaulted || senderActivityCount(faulted.Sender.WorkerStates, api.SenderInFlight) != 1 {
					t.Fatalf("fault lost in-flight batch: snapshot=%+v error=%v", faulted, err)
				}
				unblockRead()
				synctest.Wait()
				stoppedReader, err := state.Snapshot(t.Context())
				if err != nil || len(stoppedReader.Reader.WorkerStates) != 0 || opens != 1 || reads != 2 {
					t.Fatalf("reading/admission continued after server exit: snapshot=%+v opens=%d reads=%d error=%v", stoppedReader, opens, reads, err)
				}
				time.Sleep(grace - time.Nanosecond)
				synctest.Wait()
				select {
				case <-done:
					t.Fatal("loop exited before held delivery was released")
				default:
				}
				if delivered != 0 || ctx.Err() != nil {
					t.Fatalf("delivery=%d parent error=%v before release", delivered, ctx.Err())
				}
				unblockDelivery()
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("loop did not finish after accepted delivery")
				}
				if runErr == nil || (test.err != nil && !errors.Is(runErr, test.err)) ||
					(test.err == nil && !strings.Contains(runErr.Error(), "http server stopped unexpectedly")) {
					t.Fatalf("server exit error=%v, want cause %v", runErr, test.err)
				}
				if deadline != failedAt.Add(grace) || !time.Now().Before(deadline) || delivered != 1 {
					t.Fatalf("deadline=%s now=%s delivered=%d", deadline, time.Now(), delivered)
				}
				if final := assertFinalTransactions(t, logs, 1, 0); final["state"] != string(api.RunStateFaulted) || final["partial"] != false {
					t.Fatalf("faulted run did not drain the accepted batch completely: %v", final)
				}
			})
		})
	}
}

func TestSnapshotRetainsLiveStatesAboveTargetsDuringShrink(t *testing.T) {
	cfg := controllerConfig()
	directory := t.TempDir()
	for _, name := range []string{"a.parquet", "b.parquet"} {
		if err := parquet.WriteFile(filepath.Join(directory, name), []pipeline.Transaction{{}}); err != nil {
			t.Fatal(err)
		}
	}
	source, err := pipeline.DiscoverSource(filepath.Join(directory, "*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		state := newTestController(cfg, zap.NewNop())
		options := state.pipelineOptions
		options.Source = source
		options.Readers, options.Senders, options.BatchSize = 2, 2, 1
		options.ReaderCapacity, options.SenderCapacity = 0, 0
		options.Rate.Installed = false
		options.RetryDelays, options.RetryJitterPercent = []time.Duration{time.Hour}, 0
		options.Deliver = func(context.Context, []pipeline.Transaction, int) error { return io.ErrUnexpectedEOF }
		ctx, cancel := context.WithCancel(t.Context())
		run := pipeline.Start(ctx, options)
		defer func() { cancel(); <-run.Done() }()
		synctest.Wait()
		run.SetReaders(1)
		run.SetSenders(1)
		state.controls.readerWorkers, state.controls.senderWorkers = 1, 1
		snapshot := state.snapshotAt(run, time.Now())
		readers, senders := make(map[api.ReaderWorkerState]int), make(map[api.SenderWorkerState]int)
		for _, worker := range snapshot.Reader.WorkerStates {
			readers[worker]++
		}
		for _, worker := range snapshot.Sender.WorkerStates {
			senders[worker]++
		}
		if snapshot.Reader.Workers != 1 || !maps.Equal(readers, map[api.ReaderWorkerState]int{
			{Activity: api.ReaderBlocked}: 1, {Activity: api.ReaderBlocked, Draining: true}: 1,
		}) {
			t.Errorf("Reader shrink target/states = %+v", snapshot.Reader)
		}
		if snapshot.Sender.Workers != 1 || !maps.Equal(senders, map[api.SenderWorkerState]int{
			{Activity: api.SenderBackoff}: 1, {Activity: api.SenderBackoff, Draining: true}: 1,
		}) {
			t.Errorf("Sender shrink target/states = %+v", snapshot.Sender)
		}
	})
}

func TestConfirmedTotalsAreCurrentWithoutTicks(t *testing.T) {
	cfg := controllerConfig()
	source := testDiscoveredSource(t)
	synctest.Test(t, func(t *testing.T) {
		state := newTestController(cfg, zap.NewNop())
		options := state.pipelineOptions
		options.Source = source
		options.Readers, options.Senders, options.BatchSize = 1, 1, 3
		options.Rate.Installed = false
		options.Deliver = func(context.Context, []pipeline.Transaction, int) error { return nil }
		release := make(chan struct{})
		var run *pipeline.Pipeline
		options.OpenFile = func(string) (pipeline.ReaderFile, error) {
			return testReaderFile{rows: &testRowReader{read: func([]pipeline.Transaction) (int, error) {
				<-release
				run.StopReading()
				return 3, io.EOF
			}}}, nil
		}
		state.run.runStartedAt = time.Now()
		run = pipeline.Start(t.Context(), options)
		close(release)
		<-run.Done()
		for range 2 {
			snapshot := state.snapshotAt(run, time.Now())
			if snapshot.Run.TotalTransactions != 3 || snapshot.Reader.RowsRead != 3 ||
				snapshot.ReaderChannel.InputTransactionsTotal != 3 || snapshot.ReaderChannel.OutputTransactionsTotal != 3 ||
				snapshot.SenderChannel.InputTransactionsTotal != 3 || snapshot.SenderChannel.OutputTransactionsTotal != 3 {
				t.Fatalf("totals without a tick = %+v", snapshot)
			}
		}
	})
}

type testEventLoop struct {
	controller *Controller
	cancel     context.CancelFunc
	result     <-chan error
}

func startTestEventLoop(t *testing.T, state *Controller, grace time.Duration) testEventLoop {
	t.Helper()
	source, err := pipeline.DiscoverSource(state.config.Source.Path)
	ready := make(chan SourceResult, 1)
	ready <- SourceResult{Source: source, Err: err}
	return startTestEventLoopWithSource(t, state, grace, ready)
}

func startTestEventLoopWithTestReceiver(t *testing.T, cfg config.Config, logger *zap.Logger, receiver *httptest.Server) testEventLoop {
	t.Helper()
	// Keep the Sender's request construction and cancellation path. Only the
	// dialer is replaced until the Sender has cloned the test transport.
	dialer := receiver.Client().Transport.(*http.Transport).DialContext
	cfg.Sender.API.URL = receiver.URL
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original }()

	loop := startTestEventLoop(t, newTestController(cfg, logger), testGrace)
	synctest.Wait()
	return loop
}

func startTestEventLoopWithSource(
	t *testing.T, state *Controller, grace time.Duration, source <-chan SourceResult,
) testEventLoop {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := state.Run(ctx, Events{Source: source}, grace)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-state.done:
		case <-time.After(grace + time.Second):
			t.Error("event loop failed to exit during cleanup")
		}
	})
	return testEventLoop{controller: state, cancel: cancel, result: result}
}

func (h testEventLoop) snapshotInState(t *testing.T, state api.RunState) api.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	status, err := h.controller.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Run.State != state {
		t.Fatalf("snapshot state = %q, want %q", status.Run.State, state)
	}
	return status
}

func (h testEventLoop) awaitExit(t *testing.T) error {
	t.Helper()
	select {
	case <-h.controller.done:
		return <-h.result
	case <-time.After(testGrace + time.Second):
		t.Fatal("event loop failed to exit")
		return nil
	}
}

func assertFinalTransactions(t *testing.T, events *observer.ObservedLogs, confirmed, unconfirmed int64) map[string]any {
	t.Helper()
	final := events.FilterField(zap.String("event", "run_stopped")).All()
	if len(final) != 1 {
		t.Fatalf("got %d final events, want exactly one", len(final))
	}
	fields := final[0].ContextMap()
	if fields["confirmed_transactions"] != confirmed || fields["unconfirmed_transactions"] != unconfirmed {
		t.Fatalf("final transactions = %+v, want %d confirmed and %d unconfirmed", fields, confirmed, unconfirmed)
	}
	return fields
}

type testReaderFile struct{ rows pipeline.RowReader }

func (f testReaderFile) OpenRows() (pipeline.RowReader, error) { return f.rows, nil }

func (f testReaderFile) Close() error { return nil }

type testRowReader struct {
	read func([]pipeline.Transaction) (int, error)
}

func (r *testRowReader) Read(rows []pipeline.Transaction) (int, error) { return r.read(rows) }

func (r *testRowReader) Close() error { return nil }

func testDiscoveredSource(t *testing.T) pipeline.Source {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.parquet")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := pipeline.DiscoverSource(path)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestExecuteRuntimeCommandForwardsCommandAndReceipt(t *testing.T) {
	control := make(chan runtimeRequest, 10)
	const want = false
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := (<-control).(controlRequest)
		command := request.command
		if command.Action != api.SetThrottlerInstalled || command.Value != 42 || command.Installed != false {
			t.Errorf("command = %+v", command)
		}
		request.reply <- want
	}()

	command := Command{Action: api.SetThrottlerInstalled, Value: 42, Installed: false}
	if got, err := (&Controller{queue: control}).Execute(t.Context(), command); err != nil || got != want {
		t.Errorf("receipt = %+v, err=%v, want %+v", got, err, want)
	}
	<-done
}

func TestRequestSnapshotForwardsReply(t *testing.T) {
	control := make(chan runtimeRequest, 10)
	want := api.Snapshot{Run: api.RunSnapshot{State: api.RunStateStopping}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := (<-control).(snapshotRequest)
		request.reply <- want
	}()

	if got, err := (&Controller{queue: control}).Snapshot(t.Context()); err != nil || got.Run != want.Run {
		t.Errorf("run status = %+v, err=%v, want %+v", got.Run, err, want.Run)
	}
	<-done
}

func TestRuntimeRequestsUnblockAtBothWaits(t *testing.T) {
	for _, stage := range []string{"send", "reply"} {
		for _, cause := range []string{"request canceled", "deadline exceeded", "event loop exited"} {
			t.Run(stage+"/"+cause, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()
					control := make(chan runtimeRequest)
					done := make(chan struct{})
					owner := &Controller{queue: control, done: done}
					results := make(chan error, 2)
					go func() {
						_, err := owner.Snapshot(ctx)
						results <- err
					}()
					go func() {
						_, err := owner.Execute(ctx, Command{Action: api.SetReaderWorkers, Value: 2})
						results <- err
					}()
					var pending []runtimeRequest
					if stage == "reply" {
						pending = []runtimeRequest{<-control, <-control}
					}
					synctest.Wait()
					want := errRuntimeUnavailable
					switch cause {
					case "request canceled":
						want = context.Canceled
						cancel()
					case "deadline exceeded":
						want = context.DeadlineExceeded
						time.Sleep(time.Second)
					case "event loop exited":
						close(done)
					}
					for range 2 {
						if err := <-results; !errors.Is(err, want) {
							t.Fatalf("request returned %v, want %v", err, want)
						}
					}
					for _, request := range pending {
						switch request := request.(type) {
						case snapshotRequest:
							request.reply <- api.Snapshot{}
						case controlRequest:
							request.reply <- true
						}
					}
				})
			})
		}
	}
}

func TestRuntimeRequestPreservesReadyReceiptWhenEventLoopCloses(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "applied", want: true},
		{name: "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 64 {
				done := make(chan struct{})
				reply := make(chan bool, 1)
				// Both select cases are ready before the handler waits for the reply.
				reply <- tc.want
				close(done)
				if got, err := receiveRuntimeReply(t.Context(), done, reply); err != nil || got != tc.want {
					t.Fatalf("already published receipt = %+v, error = %v; want %+v", got, err, tc.want)
				}
				if _, err := receiveRuntimeReply(t.Context(), done, reply); !errors.Is(err, errRuntimeUnavailable) {
					t.Fatalf("closed loop without a receipt = %v, want runtime unavailable", err)
				}
			}
		})
	}
}

func TestSnapshotLogCadence(t *testing.T) {
	cfg := controllerConfig()
	synctest.Test(t, func(t *testing.T) {
		core, entries := observer.New(zap.InfoLevel)
		state := newTestController(cfg, zap.New(core))
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			_, err := state.Run(ctx, Events{}, testGrace)
			if err != nil {
				t.Errorf("event loop: %v", err)
			}
		}()
		synctest.Wait()
		time.Sleep(time.Minute - time.Millisecond)
		synctest.Wait()
		if entries.Len() != 0 {
			t.Fatalf("early summary: %+v", entries.All())
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if entries.Len() != 1 {
			t.Fatalf("summary entries=%+v", entries.All())
		}
		if fields := entries.All()[0].ContextMap(); fields["event"] != "runtime_summary" {
			t.Fatalf("summary fields=%+v", fields)
		}
		time.Sleep(time.Minute / 2)
		synctest.Wait()
		if entries.Len() != 1 {
			t.Fatalf("early duplicate: %+v", entries.All())
		}
		time.Sleep(time.Minute / 2)
		synctest.Wait()
		if entries.Len() != 2 {
			t.Fatalf("next summary missing: %+v", entries.All())
		}
	})
}

const testGrace = 10 * time.Second

func controllerConfig() config.Config {
	var cfg config.Config
	cfg.Reader.ReadBatchSize = 64
	cfg.Reader.Workers.Initial, cfg.Reader.Workers.Max = 1, 4
	cfg.ReaderChannel.Capacity = 2
	cfg.Throttler.RequestedTPS.Initial, cfg.Throttler.RequestedTPS.Max = 1_000, 2_000
	cfg.Throttler.Installed = true
	cfg.Sender.Workers.Initial, cfg.Sender.Workers.Max = 2, 8
	return cfg
}

func newTestController(cfg config.Config, logger *zap.Logger) *Controller {
	return New(cfg, pipeline.Options{
		BatchSize:      cfg.Reader.ReadBatchSize,
		ReaderCapacity: cfg.ReaderChannel.Capacity,
		SenderCapacity: cfg.SenderChannel.Capacity,
		Readers:        cfg.Reader.Workers.Initial,
		Senders:        cfg.Sender.Workers.Initial,
		Rate:           pipeline.Rate{TPS: cfg.Throttler.RequestedTPS.Initial, Installed: cfg.Throttler.Installed},
		SenderURL:      cfg.Sender.API.URL,
		Logger:         logger,
	}, logger)
}

func senderActivityCount(states []api.SenderWorkerState, activity api.SenderActivity) int {
	count := 0
	for _, state := range states {
		if state.Activity == activity {
			count++
		}
	}
	return count
}
