package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSignalDrainsPartialBatchAndKeepsHTTPSnapshotAvailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.ReaderChannel.Capacity.Initial = 0
	cfg.SenderChannel.Capacity.Initial = 0
	cfg.Sender.Workers.Initial = 1
	cfg.Throttler.Installed.Initial = false
	cfg.Source.Path = filepath.Join(t.TempDir(), "input.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, []pipeline.Transaction{{}, {}, {}}); err != nil {
		t.Fatal(err)
	}

	entered := make(chan int, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
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
	if status.ReaderChannel.DepthBatches != 0 || status.SenderChannel.DepthBatches != 0 || status.Sender.InFlightWorkers != 1 {
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
	if stopping.Sender.InFlightWorkers == 0 {
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
	assertFinalTransactions(t, events, state.run.totalTransactions, 0)
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
		loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.ReaderChannel.ReceivedBatchesTotal > 0 })
		loop.cancel()
		synctest.Wait()
		stopping := loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
		if !stopping.Throttler.Installed || stopping.Throttler.RequestedTps != 0 || stopping.SenderChannel.SentBatchesTotal != 0 {
			t.Fatalf("shutdown changed the zero-rate throttler: %+v", stopping)
		}

		assertExitAtDeadline(t, loop)
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
	cfg.ReaderChannel.Capacity.Initial = 0
	cfg.SenderChannel.Capacity.Initial = 0
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
		return s.Run.State == runStateStopping && s.Reader.ReadingWorkers == 0
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
				if stopping.Reader.LiveWorkers != 0 {
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

func TestPipelineAppliesStartupBatchSizeAndQueueCapacities(t *testing.T) {
	const batchSize = 2_000
	for _, tc := range []struct {
		name                           string
		readerCapacity, senderCapacity int
	}{
		{name: "buffered", readerCapacity: 4, senderCapacity: 2},
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
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			defer unblock()

			conf := strings.NewReplacer(
				"C:\\dataset\\*.parquet", filepath.ToSlash(parquetPath),
				testSenderURL, receiver.URL,
				"[reader.read_batch_size]\ninitial = 1000", fmt.Sprintf("[reader.read_batch_size]\ninitial = %d", batchSize),
				"[readerChannel.capacity]\ninitial = 2", fmt.Sprintf("[readerChannel.capacity]\ninitial = %d", tc.readerCapacity),
				"[senderChannel.capacity]\ninitial = 0", fmt.Sprintf("[senderChannel.capacity]\ninitial = %d", tc.senderCapacity),
				"[throttler.installed]\ninitial = true", "[throttler.installed]\ninitial = false",
			).Replace(testConfigContents())
			configPath := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(configPath, []byte(conf), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(configPath)
			if err != nil {
				t.Fatal(err)
			}
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

			loop.cancel()
			loop.awaitSnapshot(t, func(s runtimeSnapshot) bool { return s.Run.State == runStateStopping })
			unblock()
			if err := loop.awaitExit(t); err != nil {
				t.Fatal(err)
			}
			if final := assertFinalTransactions(t, events, state.run.totalTransactions, 0); final["partial"] != false {
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
	assertFinalTransactions(t, events, 0, 0)
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
	cfg.ReaderChannel.Capacity.Initial = 0
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
	if faulted.Reader.SourceError == nil || faulted.Reader.SourceError.RelativePath != "b.parquet" || faulted.Sender.InFlightWorkers != 1 {
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

func TestMetricsWindowDrivesChannelRatesAndActualTPS(t *testing.T) {
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
		time.Sleep(900 * time.Millisecond)
		metrics := newPrometheusMetrics()
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
func (f testReaderFile) Close() error                          { return nil }

type testRowReader struct {
	read func([]pipeline.Transaction) (int, error)
}

func (r *testRowReader) Read(rows []pipeline.Transaction) (int, error) { return r.read(rows) }
func (r *testRowReader) Close() error                                  { return nil }

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
