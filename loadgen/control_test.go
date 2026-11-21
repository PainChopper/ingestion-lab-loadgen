package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func newTestControlState(t *testing.T) controlState {
	t.Helper()
	loaded := testConfig(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	loaded.Source.Path = filepath.Join(t.TempDir(), "source.parquet")
	if err := parquet.WriteFile(loaded.Source.Path, []pipeline.Transaction{}); err != nil {
		t.Fatal(err)
	}
	loaded.Sender.API.URL = server.URL
	return newControlState(loaded, zap.NewNop())
}

func TestNewControlStateInitializesControls(t *testing.T) {
	for _, name := range []string{"nonzero initial values", "zero initial values"} {
		t.Run(name, func(t *testing.T) {
			loaded := testConfig(t)
			loaded.Reader.ReadBatchSize = 2000
			loaded.Reader.Workers.Initial = 3
			loaded.Sender.Workers.Initial = 7
			loaded.SenderChannel.Capacity = 4
			loaded.Throttler.Installed = false
			loaded.Metrics.WindowMS = 300
			if name == "zero initial values" {
				loaded.ReaderChannel.Capacity = 0
				loaded.SenderChannel.Capacity = 0
				loaded.Throttler.RequestedTPS.Initial = 0
			}
			state := newControlState(loaded, zap.NewNop())
			if got := state.run.state; got != runStateStarting {
				t.Fatalf("initial run state = %q, want %q", got, runStateStarting)
			}
			if want := time.Duration(loaded.Metrics.WindowMS) * time.Millisecond; state.metricsWindow != want {
				t.Fatalf("metrics window = %s, want %s", state.metricsWindow, want)
			}
			snapshot := state.snapshotAt(nil, time.Now())
			if snapshot.Reader.WorkerStates == nil || len(snapshot.Reader.WorkerStates) != 0 ||
				snapshot.Sender.WorkerStates == nil || len(snapshot.Sender.WorkerStates) != 0 {
				t.Fatalf("before Start worker states must be []: %+v / %+v", snapshot.Reader, snapshot.Sender)
			}
			got := []int{
				snapshot.Reader.ReadBatchSize, snapshot.Reader.Workers,
				snapshot.ReaderChannel.Capacity, snapshot.SenderChannel.Capacity,
				snapshot.Throttler.RequestedTps, snapshot.Sender.Workers,
			}
			want := []int{
				loaded.Reader.ReadBatchSize, loaded.Reader.Workers.Initial,
				loaded.ReaderChannel.Capacity, loaded.SenderChannel.Capacity,
				loaded.Throttler.RequestedTPS.Initial, loaded.Sender.Workers.Initial,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("initial controls = %v, want %v", got, want)
			}
			if snapshot.Reader.SourceDirectory != pipeline.SourceDirectory(loaded.Source.Path) {
				t.Fatalf("source directory = %q", snapshot.Reader.SourceDirectory)
			}
			if got := snapshot.Throttler.Installed; got != loaded.Throttler.Installed {
				t.Fatalf("initial installed = %t, want %t", got, loaded.Throttler.Installed)
			}
		})
	}
}

func TestSignalDrainsPartialBatchAndKeepsHTTPSnapshotAvailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.ReaderChannel.Capacity = 0
	cfg.SenderChannel.Capacity = 0
	cfg.Sender.Workers.Initial = 1
	cfg.Throttler.Installed = false
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}, {}, {}}); err != nil {
		t.Fatal(err)
	}

	entered := make(chan int, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var deliveredRows atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []pipeline.Transaction
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case entered <- len(batch):
		default:
		}
		<-release
		deliveredRows.Add(int64(len(batch)))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	defer unblock()
	cfg.Sender.API.URL = receiver.URL
	core, events := observer.New(zap.InfoLevel)
	state := newControlState(cfg, zap.New(core))
	loop := startTestEventLoop(t, &state, gracefulShutdownTimeout)
	mux := newServeMux(loop.requests, loop.metrics, cfg, zap.NewNop())

	select {
	case size := <-entered:
		if size != 3 {
			t.Fatalf("received batch size = %d, want partial batch of 3", size)
		}
	case <-time.After(time.Second):
		t.Fatal("autostarted Sender did not reach receiver")
	}
	status := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateRunning })
	if status.ReaderChannel.DepthBatches != 0 || status.SenderChannel.DepthBatches != 0 || countSenderWorkers(status.Sender.WorkerStates).InFlightWorkers != 1 {
		t.Fatalf("wanted two empty queues with an in-flight Sender: %+v", status)
	}
	updates := []string{
		`{"action":"set-reader-workers","value":2}`,
		`{"action":"set-sender-workers","value":2}`,
		`{"action":"set-requested-tps","value":2400000}`,
		`{"action":"set-throttler-installed","value":true}`,
	}
	for _, body := range updates {
		assertCommandHTTP(t, mux, body, http.StatusOK)
	}
	status = loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateRunning })
	if status.Reader.Workers != 2 || status.Sender.Workers != 2 || status.Throttler.RequestedTps != 2_400_000 || !status.Throttler.Installed {
		t.Fatalf("live controls not applied: %+v", status)
	}

	loop.cancel()
	stopping := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
	if countSenderWorkers(stopping.Sender.WorkerStates).InFlightWorkers == 0 {
		t.Fatal("stopping prematurely canceled the HTTP attempt")
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, snapshotPath, nil)
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("stopping snapshot HTTP = %d", response.Code)
	}
	var snapshot runtimeSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || snapshot.Run.State != runStateStopping {
		t.Fatalf("stopping snapshot = %q, err = %v", response.Body.String(), err)
	}
	for _, body := range updates {
		assertCommandHTTP(t, mux, body, http.StatusConflict)
	}
	unblock()
	if err := loop.awaitExit(t); err != nil {
		t.Fatal(err)
	}
	assertFinalTransactions(t, events, deliveredRows.Load(), 0)
	assertCommandHTTP(t, mux, updates[0], http.StatusServiceUnavailable)
}

func TestSignalKeepsZeroTPSUntilGraceExpires(t *testing.T) {
	cfg := testConfig(t)
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}}); err != nil {
		t.Fatal(err)
	}
	cfg.Throttler.RequestedTPS.Initial = 0
	core, events := observer.New(zap.InfoLevel)
	synctest.Test(t, func(t *testing.T) {
		state := newControlState(cfg, zap.New(core))
		loop := startTestEventLoop(t, &state, gracefulShutdownTimeout)
		// Each batch has one row; rows beyond the queue and active Readers have reached the Throttler.
		loop.awaitSnapshot(t, func(s runtimeSnapshot) bool {
			return s.Reader.RowsRead > int64(cfg.ReaderChannel.Capacity+cfg.Reader.Workers.Initial)
		})
		loop.cancel()
		synctest.Wait()
		stopping := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
		throttlerChanged := !stopping.Throttler.Installed || stopping.Throttler.RequestedTps != 0
		senderHasWork := stopping.SenderChannel.DepthBatches != 0 ||
			countSenderWorkers(stopping.Sender.WorkerStates).InFlightWorkers != 0 || countSenderWorkers(stopping.Sender.WorkerStates).BackoffWorkers != 0
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
	cfg := testConfig(t)
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}}); err != nil {
		t.Fatal(err)
	}
	cfg.ReaderChannel.Capacity = 0
	cfg.SenderChannel.Capacity = 0
	cfg.Sender.Workers.Initial = 1

	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
	defer receiver.Close()
	defer close(release)

	cfg.Sender.API.URL = receiver.URL
	core, events := observer.New(zap.InfoLevel)
	state := newControlState(cfg, zap.New(core))
	loop := startTestEventLoop(t, &state, 50*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Sender did not enter HTTP")
	}
	loop.cancel()
	stopping := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool {
		return s.Run.State == runStateStopping && countReaderWorkers(s.Reader.WorkerStates).ReadingWorkers == 0
	})
	if err := loop.awaitExit(t); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("forwarded HTTP request context was not canceled")
	}
	assertFinalTransactions(t, events, 0, stopping.Reader.RowsRead)
}

func TestSignalDuringSourceDiscoveryPreventsReaderStart(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "discovery completes during grace"
		if held {
			name = "discovery remains blocked at deadline"
		}
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			source := testDiscoveredSource(t)
			core, events := observer.New(zap.InfoLevel)
			synctest.Test(t, func(t *testing.T) {
				read := make(chan struct{}, 1)
				found := sourceDiscovery{source: source}
				ready := make(chan sourceDiscovery, 1)
				state := newControlState(cfg, zap.New(core))
				state.pipelineOptions.OpenFile = func(string) (pipeline.ReaderFile, error) {
					read <- struct{}{}
					return nil, errors.New("unexpected open after stop")
				}
				loop := startTestEventLoopWithSource(t, &state, gracefulShutdownTimeout, ready)
				starting := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStarting })
				if starting.Reader.SourceDirectory != pipeline.SourceDirectory(cfg.Source.Path) ||
					starting.Reader.SourceError != nil {
					t.Fatalf("source directory or initial source error unavailable before Start: %+v", starting.Reader)
				}

				loop.cancel()
				synctest.Wait()
				stopping := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
				if countReaderWorkers(stopping.Reader.WorkerStates).LiveWorkers != 0 {
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
	cfg := testConfig(t)
	source := testDiscoveredSource(t)
	core, events := observer.New(zap.InfoLevel)
	synctest.Test(t, func(t *testing.T) {
		inRead := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()

		ready := make(chan sourceDiscovery, 1)
		ready <- sourceDiscovery{source: source}
		first := true
		state := newControlState(cfg, zap.New(core))
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
		loop := startTestEventLoopWithSource(t, &state, gracefulShutdownTimeout, ready)
		select {
		case <-inRead:
		case <-time.After(time.Second):
			t.Fatal("Reader did not enter blocking read")
		}
		loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateRunning })

		loop.cancel()
		synctest.Wait()
		loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
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
	time.Sleep(gracefulShutdownTimeout - time.Nanosecond)
	synctest.Wait()
	select {
	case <-loop.requests.done:
		t.Fatal("event loop exited before the deadline")
	default:
	}
	time.Sleep(time.Nanosecond)
	synctest.Wait()
	select {
	case <-loop.requests.done:
		if err := <-loop.result; err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("event loop waited for filesystem I/O after the deadline")
	}
}

func TestRuntimeControlsPreserveStartupConfiguration(t *testing.T) {
	const batchSize = 1_500
	for _, tc := range []struct {
		name                           string
		readerCapacity, senderCapacity int
	}{
		{name: "buffered", readerCapacity: 3, senderCapacity: 2},
		{name: "unbuffered", readerCapacity: 0, senderCapacity: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parquetPath := filepath.Join(dir, "data.parquet")
			if err := parquet.WriteFile(parquetPath, make([]pipeline.Transaction, 2*batchSize)); err != nil {
				t.Fatal(err)
			}

			received := make(chan int, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var deliveredRows atomic.Int64
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var rows []pipeline.Transaction
				if err := json.NewDecoder(r.Body).Decode(&rows); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case received <- len(rows):
				default:
				}
				<-release
				deliveredRows.Add(int64(len(rows)))
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			defer unblock()

			conf := strings.NewReplacer(
				"C:\\dataset\\*.parquet", filepath.ToSlash(parquetPath),
				testSenderURL, receiver.URL,
				"[reader]\nread_batch_size = 1000", fmt.Sprintf("[reader]\nread_batch_size = %d", batchSize),
				"[readerChannel]\ncapacity = 2", fmt.Sprintf("[readerChannel]\ncapacity = %d", tc.readerCapacity),
				"[senderChannel]\ncapacity = 0", fmt.Sprintf("[senderChannel]\ncapacity = %d", tc.senderCapacity),
				"[throttler]\ninstalled = true", "[throttler]\ninstalled = false",
			).Replace(testConfigContents())
			configPath := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(configPath, []byte(conf), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(configPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Reader.Workers = workersConfig{Initial: 1, Max: 13}
			cfg.Sender.Workers = workersConfig{Initial: 2, Max: 19}
			cfg.Throttler.RequestedTPS = tpsConfig{Initial: 200_000, Max: 400_001, Step: 200_000}
			core, events := observer.New(zap.InfoLevel)
			state := newControlState(cfg, zap.New(core))
			loop := startTestEventLoop(t, &state, gracefulShutdownTimeout)

			select {
			case got := <-received:
				if got != batchSize {
					t.Fatalf("Sender got %d rows per full batch, want %d", got, batchSize)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Sender received no full batch")
			}
			status := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool {
				return s.ReaderChannel.BlockedSenders > 0 && s.SenderChannel.BlockedSenders > 0
			})
			if status.Reader.ReadBatchSize != batchSize {
				t.Fatalf("snapshot batch size = %d, want %d", status.Reader.ReadBatchSize, batchSize)
			}

			for _, queue := range []struct {
				name     string
				snapshot channelSnapshot
				capacity int
			}{
				{name: "Reader", snapshot: status.ReaderChannel, capacity: tc.readerCapacity},
				{name: "Sender", snapshot: status.SenderChannel, capacity: tc.senderCapacity},
			} {
				q := queue.snapshot
				if q.Capacity != queue.capacity || q.DepthBatches != queue.capacity ||
					q.BufferedTransactions != queue.capacity*batchSize {
					t.Errorf("%s did not report its filled channel: %+v", queue.name, q)
				}
			}

			mux := newServeMux(loop.requests, loop.metrics, cfg, zap.NewNop())
			for _, body := range []string{
				`{"action":"set-reader-workers","value":3}`,
				`{"action":"set-sender-workers","value":4}`,
				`{"action":"set-requested-tps","value":0}`,
				`{"action":"set-throttler-installed","value":true}`,
			} {
				assertCommandHTTP(t, mux, body, http.StatusOK)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, snapshotPath, nil))
			var updated runtimeSnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
				t.Fatal(err)
			}
			wantConfig := configSnapshot{
				ReaderWorkers:         workersConfig{Initial: 1, Max: 13},
				SenderWorkers:         workersConfig{Initial: 2, Max: 19},
				ThrottlerRequestedTPS: tpsConfig{Initial: 200_000, Max: 400_001, Step: 200_000},
			}
			if updated.Config != wantConfig || updated.Reader.Workers != 3 ||
				updated.Sender.Workers != 4 || updated.Throttler.RequestedTps != 0 || !updated.Throttler.Installed {
				t.Fatalf("runtime commands lost initial profiles or applied values: %+v", updated)
			}
			// The cross-client smoke reads this real handler response from the verbose run log.
			t.Logf("schema4 snapshot: %s", response.Body.String())
			assertCommandHTTP(t, mux, `{"action":"set-throttler-installed","value":false}`, http.StatusOK)

			loop.cancel()
			loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
			unblock()
			if err := loop.awaitExit(t); err != nil {
				t.Fatal(err)
			}
			if final := assertFinalTransactions(t, events, deliveredRows.Load(), 0); final["partial"] != false {
				t.Fatalf("clean drain must record a complete final log: %v", final)
			}

		})
	}
}

func TestRunStartupSourceErrorReturnsWithoutSignal(t *testing.T) {
	cfg := testConfig(t)
	cfg.Source.Path = filepath.Join(t.TempDir(), "*.parquet")
	core, events := observer.New(zap.InfoLevel)
	state := newControlState(cfg, zap.New(core))
	loop := startTestEventLoop(t, &state, gracefulShutdownTimeout)
	var sourceError *pipeline.SourceError
	if err := loop.awaitExit(t); !errors.As(err, &sourceError) || sourceError.Op != "glob" {
		t.Fatalf("run error = %v, want missing source diagnostic", err)
	}
	if state.run.state != runStateFaulted || state.run.sourceError == nil {
		t.Fatalf("startup fault state = %+v", state.run)
	}
	snapshot := state.snapshotAt(nil, time.Now())
	if snapshot.Reader.WorkerStates == nil || len(snapshot.Reader.WorkerStates) != 0 ||
		snapshot.Sender.WorkerStates == nil || len(snapshot.Sender.WorkerStates) != 0 ||
		snapshot.Reader.Workers != cfg.Reader.Workers.Initial || snapshot.Sender.Workers != cfg.Sender.Workers.Initial {
		t.Fatalf("startup source fault worker states/targets = %+v / %+v", snapshot.Reader, snapshot.Sender)
	}
	assertFinalTransactions(t, events, 0, 0)
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
			cfg := testConfig(t)
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
				state := newControlState(cfg, zap.New(core))
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
				ready := make(chan sourceDiscovery, 1)
				ready <- sourceDiscovery{source: source}
				queue := make(chan runtimeRequest)
				serverDone := make(chan error)
				done := make(chan struct{})
				metrics := newPrometheusMetrics()
				var deadline time.Time
				var runErr error
				go func() {
					defer close(done)
					deadline, runErr = state.runEventLoop(ctx, runtimeEvents{
						source: ready, requests: queue, serverDone: serverDone, metrics: metrics,
					}, grace)
				}()
				synctest.Wait()
				requests := runtimeRequests{queue: queue, done: done}
				before, err := requestSnapshot(t.Context(), requests)
				if err != nil || before.Run.State != runStateRunning || countSenderWorkers(before.Sender.WorkerStates).InFlightWorkers != 1 || reads != 2 {
					t.Fatalf("pipeline not held in delivery/read: snapshot=%+v reads=%d error=%v", before, reads, err)
				}
				failedAt := time.Now()
				serverDone <- test.err
				synctest.Wait()
				faulted, err := requestSnapshot(t.Context(), requests)
				if err != nil || faulted.Run.State != runStateFaulted || countSenderWorkers(faulted.Sender.WorkerStates).InFlightWorkers != 1 {
					t.Fatalf("fault lost in-flight batch: snapshot=%+v error=%v", faulted, err)
				}
				unblockRead()
				synctest.Wait()
				stoppedReader, err := requestSnapshot(t.Context(), requests)
				if err != nil || countReaderWorkers(stoppedReader.Reader.WorkerStates).LiveWorkers != 0 || opens != 1 || reads != 2 {
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
				if state.run.state != runStateFaulted || deadline != failedAt.Add(grace) || !time.Now().Before(deadline) || delivered != 1 {
					t.Fatalf("state=%s deadline=%s now=%s delivered=%d", state.run.state, deadline, time.Now(), delivered)
				}
				if final := assertFinalTransactions(t, logs, 1, 0); final["partial"] != false {
					t.Fatalf("accepted batch did not drain completely: %v", final)
				}
			})
		})
	}
}

func TestRuntimeSourceFaultCollectsLateSuccessExactlyOnce(t *testing.T) {
	cfg := testConfig(t)
	directory := t.TempDir()
	cfg.Source.Path = filepath.Join(directory, "*.parquet")
	if err := parquet.WriteFile(filepath.Join(directory, "a.parquet"), []pipeline.Transaction{{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "b.parquet"), []byte("invalid parquet"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.ReaderChannel.Capacity = 0
	cfg.Sender.Workers.Initial = 1

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	defer unblock()
	cfg.Sender.API.URL = receiver.URL
	core, events := observer.New(zap.InfoLevel)
	state := newControlState(cfg, zap.New(core))
	loop := startTestEventLoop(t, &state, gracefulShutdownTimeout)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("valid source never reached receiver")
	}
	faulted := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateFaulted })
	if faulted.Reader.SourceError == nil || faulted.Reader.SourceError.RelativePath != "b.parquet" || countSenderWorkers(faulted.Sender.WorkerStates).InFlightWorkers != 1 {
		t.Fatalf("runtime fault lost source or in-flight diagnostic: %+v", faulted)
	}
	unblock()
	var sourceError *pipeline.SourceError
	if err := loop.awaitExit(t); !errors.As(err, &sourceError) || sourceError.Op != "open" {
		t.Fatalf("run error = %v, want corrupt source diagnostic", err)
	}
	assertFinalTransactions(t, events, 1, 0)
	var total dto.Metric
	if err := loop.metrics.transactionsTotal.Write(&total); err != nil {
		t.Fatal(err)
	}
	if confirmed := total.GetCounter().GetValue(); confirmed != 1 {
		t.Fatalf("Prometheus confirmed transactions = %v, want 1", confirmed)
	}
}

func TestSnapshotRetainsLiveStatesAboveTargetsDuringShrink(t *testing.T) {
	cfg := testConfig(t)
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
		state := newControlState(cfg, zap.NewNop())
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
		readers, senders := make(map[readerWorkerState]int), make(map[senderWorkerState]int)
		for _, worker := range snapshot.Reader.WorkerStates {
			readers[worker]++
		}
		for _, worker := range snapshot.Sender.WorkerStates {
			senders[worker]++
		}
		if snapshot.Reader.Workers != 1 || !maps.Equal(readers, map[readerWorkerState]int{
			{Activity: readerWireBlocked}: 1, {Activity: readerWireBlocked, Draining: true}: 1,
		}) {
			t.Errorf("Reader shrink target/states = %+v", snapshot.Reader)
		}
		if snapshot.Sender.Workers != 1 || !maps.Equal(senders, map[senderWorkerState]int{
			{Activity: senderWireBackoff}: 1, {Activity: senderWireBackoff, Draining: true}: 1,
		}) {
			t.Errorf("Sender shrink target/states = %+v", snapshot.Sender)
		}
	})
}

func TestSamplingUsesElapsedIntervalForChannelRatesAndActualTPS(t *testing.T) {
	cfg := testConfig(t)
	source := testDiscoveredSource(t)
	synctest.Test(t, func(t *testing.T) {
		state := newControlState(cfg, zap.NewNop())
		state.metricsWindow = 300 * time.Millisecond
		options := state.pipelineOptions
		options.Source = source
		options.Readers, options.Senders, options.BatchSize = 1, 1, 3
		options.Rate.Installed = false
		options.Deliver = func(context.Context, []pipeline.Transaction, int) error { return nil }
		release := make(chan struct{})
		var run *pipeline.Pipeline
		options.OpenFile = func(string) (pipeline.ReaderFile, error) {
			return testReaderFile{rows: &testRowReader{read: func(rows []pipeline.Transaction) (int, error) {
				<-release
				run.StopReading()
				return 3, io.EOF
			}}}, nil
		}
		startedAt := time.Now()
		run = pipeline.Start(t.Context(), options)
		close(release)
		<-run.Done()
		metrics := newPrometheusMetrics()
		metrics.actualTPS.Set(17)
		for _, now := range []time.Time{startedAt, startedAt.Add(-time.Second)} {
			state.sample(run, metrics, now)
			var actual, total dto.Metric
			if err := metrics.actualTPS.Write(&actual); err != nil {
				t.Fatal(err)
			}
			if err := metrics.transactionsTotal.Write(&total); err != nil {
				t.Fatal(err)
			}
			if actual.GetGauge().GetValue() != 17 || total.GetCounter().GetValue() != 0 || state.run.totalTransactions != 0 {
				t.Fatalf("nonpositive interval changed measurements: gauge=%v counter=%v transactions=%d",
					actual.GetGauge().GetValue(), total.GetCounter().GetValue(), state.run.totalTransactions)
			}
		}
		time.Sleep(900 * time.Millisecond)
		state.sample(run, metrics, time.Now())
		snapshot := state.snapshotAt(run, time.Now())
		wantRate := 3 / time.Since(startedAt).Seconds()
		if snapshot.Run.TotalTransactions != 3 || snapshot.Reader.ReadTps != wantRate ||
			snapshot.ReaderChannel.ReceivedTransactionsPerSecond != wantRate ||
			snapshot.SenderChannel.ReceivedTransactionsPerSecond != wantRate {
			t.Fatalf("delayed tick used inconsistent intervals: %+v, want rate %v", snapshot, wantRate)
		}
		var actual dto.Metric
		if err := metrics.actualTPS.Write(&actual); err != nil {
			t.Fatal(err)
		}
		if got := actual.GetGauge().GetValue(); got != wantRate {
			t.Fatalf("actual TPS = %v, want %v", got, wantRate)
		}
	})
}

type testEventLoop struct {
	requests runtimeRequests
	metrics  *prometheusMetrics
	cancel   context.CancelFunc
	result   <-chan error
}

func startTestEventLoop(t *testing.T, state *controlState, grace time.Duration) testEventLoop {
	t.Helper()
	return startTestEventLoopWithSource(t, state, grace, prepareSourceAsync(state.config.Source.Path))
}

func startTestEventLoopWithSource(
	t *testing.T, state *controlState, grace time.Duration, source <-chan sourceDiscovery,
) testEventLoop {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	queue := make(chan runtimeRequest)
	done := make(chan struct{})
	result := make(chan error, 1)
	metrics := newPrometheusMetrics()
	events := runtimeEvents{requests: queue, source: source, metrics: metrics}
	go func() {
		defer close(done)
		_, err := state.runEventLoop(ctx, events, grace)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(grace + time.Second):
			t.Error("event loop failed to exit during cleanup")
		}
	})
	return testEventLoop{requests: runtimeRequests{queue: queue, done: done}, metrics: metrics, cancel: cancel, result: result}
}

func (h testEventLoop) awaitSnapshot(t *testing.T, ready func(runtimeSnapshot) bool) runtimeSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		status, err := requestSnapshot(ctx, h.requests)
		if err != nil {
			t.Fatal(err)
		}
		if ready(status) {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime snapshot did not reach expected state")
	return runtimeSnapshot{}
}

func (h testEventLoop) awaitExit(t *testing.T) error {
	t.Helper()
	select {
	case <-h.requests.done:
		return <-h.result
	case <-time.After(gracefulShutdownTimeout + time.Second):
		t.Fatal("event loop failed to exit")
		return nil
	}
}

func assertCommandHTTP(t *testing.T, mux http.Handler, body string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodPost, commandsPath, strings.NewReader(body)))
	if response.Code != want || ctx.Err() != nil {
		t.Fatalf("POST %s: HTTP %d, context %v, body %q, want HTTP %d", body, response.Code, ctx.Err(), response.Body.String(), want)
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
		if command.kind != setThrottlerInstalled || command.value != 42 || command.installed != false {
			t.Errorf("command = %+v", command)
		}
		request.reply <- want
	}()

	command := runtimeCommand{kind: setThrottlerInstalled, value: 42, installed: false}
	if got, err := executeRuntimeCommand(t.Context(), runtimeRequests{queue: control}, command); err != nil || got != want {
		t.Errorf("receipt = %+v, err=%v, want %+v", got, err, want)
	}
	<-done
}

func TestRequestSnapshotForwardsReply(t *testing.T) {
	control := make(chan runtimeRequest, 10)
	want := runtimeSnapshot{Run: runSnapshot{State: runStateStopping}}
	done := make(chan struct{})

	go func() {
		defer close(done)
		request := (<-control).(snapshotRequest)
		request.reply <- want
	}()

	if got, err := requestSnapshot(t.Context(), runtimeRequests{queue: control}); err != nil || got.Run != want.Run {
		t.Errorf("run status = %+v, err=%v, want %+v", got.Run, err, want.Run)
	}
	<-done
}

func TestRuntimeRequestsUnblockAtBothWaits(t *testing.T) {
	for _, stage := range []string{"send", "reply"} {
		for _, cause := range []string{"request canceled", "event loop exited"} {
			t.Run(stage+"/"+cause, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					control := make(chan runtimeRequest)
					done := make(chan struct{})
					requests := runtimeRequests{queue: control, done: done}
					results := make(chan error, 2)
					go func() {
						_, err := requestSnapshot(ctx, requests)
						results <- err
					}()
					go func() {
						_, err := executeRuntimeCommand(ctx, requests, runtimeCommand{kind: setReaderWorkers, value: 2})
						results <- err
					}()
					var pending []runtimeRequest
					if stage == "reply" {
						pending = []runtimeRequest{<-control, <-control}
					}
					synctest.Wait()
					want := errRuntimeUnavailable
					if cause == "request canceled" {
						want = context.Canceled
						cancel()
					} else {
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
							request.reply <- runtimeSnapshot{}
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
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatal(err)
	}
	state := newTestControlState(t)
	state.logger = logger
	queue := make(chan runtimeRequest)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		events := runtimeEvents{
			requests: queue,
			source:   prepareSourceAsync(state.config.Source.Path),
			ticks:    ticks,
			metrics:  newPrometheusMetrics(),
		}
		state.runEventLoop(ctx, events, gracefulShutdownTimeout)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})
	harness := testEventLoop{requests: runtimeRequests{queue: queue, done: done}}
	harness.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateRunning })
	output.Reset()
	ticks <- time.Now()
	awaitSnapshotLogTick(t, harness.requests)
	if output.Len() != 0 {
		t.Fatalf("summary emitted before one-minute deadline: %q", output.String())
	}
	afterDeadline := time.Now().Add(2 * snapshotLogInterval)
	ticks <- afterDeadline
	awaitSnapshotLogTick(t, harness.requests)
	line := output.String()
	if strings.Count(line, "\n") != 1 || strings.Contains(line, "runtime_snapshot") || strings.Contains(line, "INGESTION LAB") {
		t.Fatalf("summary line = %q", line)
	}
	if !strings.Contains(line, "event=runtime_summary") {
		t.Fatalf("summary event missing: %q", line)
	}
	ticks <- afterDeadline.Add(time.Millisecond)
	awaitSnapshotLogTick(t, harness.requests)
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("duplicate summary before next deadline: %q", output.String())
	}
}

func awaitSnapshotLogTick(t *testing.T, requests runtimeRequests) {
	t.Helper()
	if _, err := requestSnapshot(t.Context(), requests); err != nil {
		t.Fatal(err)
	}
}
