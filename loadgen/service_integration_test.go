package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/control"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

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
	loop := startServiceLoop(t, cfg)
	mux := loop.mux

	select {
	case size := <-entered:
		if size != 3 {
			t.Fatalf("received batch size = %d, want partial batch of 3", size)
		}
	case <-time.After(time.Second):
		t.Fatal("autostarted Sender did not reach receiver")
	}
	status := loop.awaitSnapshot(t, func(s api.Snapshot) bool { return s.Run.State == api.RunStateRunning })
	if status.ReaderChannel.DepthBatches != 0 || status.SenderChannel.DepthBatches != 0 || inFlightSenders(status) != 1 {
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
	status = loop.awaitSnapshot(t, func(s api.Snapshot) bool { return s.Run.State == api.RunStateRunning })
	if status.Reader.Workers != 2 || status.Sender.Workers != 2 || status.Throttler.RequestedTps != 2_400_000 || !status.Throttler.Installed {
		t.Fatalf("live controls not applied: %+v", status)
	}

	loop.cancel()
	stopping := loop.awaitSnapshot(t, func(s api.Snapshot) bool { return s.Run.State == api.RunStateStopping })
	if inFlightSenders(stopping) == 0 {
		t.Fatal("stopping prematurely canceled the HTTP attempt")
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, api.SnapshotPath, nil)
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("stopping snapshot HTTP = %d", response.Code)
	}
	var snapshot api.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || snapshot.Run.State != api.RunStateStopping {
		t.Fatalf("stopping snapshot = %q, err = %v", response.Body.String(), err)
	}
	for _, body := range updates {
		assertCommandHTTP(t, mux, body, http.StatusConflict)
	}
	unblock()
	if err := loop.awaitExit(t); err != nil {
		t.Fatal(err)
	}
	assertServiceDrainLog(t, loop.logs.String(), deliveredRows.Load(), false)
	assertCommandHTTP(t, mux, updates[0], http.StatusServiceUnavailable)
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
				"../data/MBD-mini/trx/fold=*/*.parquet", filepath.ToSlash(parquetPath),
				testSenderURL, receiver.URL,
				"[reader]\nread_batch_size = 1000", fmt.Sprintf("[reader]\nread_batch_size = %d", batchSize),
				"[readerChannel]\ncapacity = 2", fmt.Sprintf("[readerChannel]\ncapacity = %d", tc.readerCapacity),
				"[senderChannel]\ncapacity = 0", fmt.Sprintf("[senderChannel]\ncapacity = %d", tc.senderCapacity),
				"[throttler]\ninstalled = true", "[throttler]\ninstalled = false",
			).Replace(testConfigContents(t))
			configFilePath := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(configFilePath, []byte(conf), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(configFilePath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Reader.Workers.Initial, cfg.Reader.Workers.Max = 1, 13
			cfg.Sender.Workers.Initial, cfg.Sender.Workers.Max = 2, 19
			cfg.Throttler.RequestedTPS.Initial, cfg.Throttler.RequestedTPS.Max = 200_001, 400_001
			loop := startServiceLoop(t, cfg)

			select {
			case got := <-received:
				if got != batchSize {
					t.Fatalf("Sender got %d rows per full batch, want %d", got, batchSize)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Sender received no full batch")
			}
			status := loop.awaitSnapshot(t, func(s api.Snapshot) bool {
				return s.ReaderChannel.BlockedSenders > 0 && s.SenderChannel.BlockedSenders > 0
			})

			for _, queue := range []struct {
				name     string
				snapshot api.ChannelSnapshot
				capacity int
			}{
				{name: "Reader", snapshot: status.ReaderChannel, capacity: tc.readerCapacity},
				{name: "Sender", snapshot: status.SenderChannel, capacity: tc.senderCapacity},
			} {
				q := queue.snapshot
				if q.DepthBatches != queue.capacity {
					t.Errorf("%s did not report its filled channel: %+v", queue.name, q)
				}
			}

			mux := loop.mux
			for _, body := range []string{
				`{"action":"set-reader-workers","value":3}`,
				`{"action":"set-sender-workers","value":4}`,
				`{"action":"set-requested-tps","value":0}`,
				`{"action":"set-throttler-installed","value":true}`,
			} {
				assertCommandHTTP(t, mux, body, http.StatusOK)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, api.SnapshotPath, nil))
			var updated api.Snapshot
			if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
				t.Fatal(err)
			}
			wantConfig := api.ConfigResponse{
				ReaderWorkersMax: 13, SenderWorkersMax: 19, RequestedTpsMax: 400_001,
				ReadBatchSize: batchSize, ReaderChannelCapacity: tc.readerCapacity, SenderChannelCapacity: tc.senderCapacity,
				SourcePath: cfg.Source.Path,
			}
			configRecorder := httptest.NewRecorder()
			mux.ServeHTTP(configRecorder, httptest.NewRequest(http.MethodGet, api.ConfigPath, nil))
			var gotConfig api.ConfigResponse
			if err := json.Unmarshal(configRecorder.Body.Bytes(), &gotConfig); err != nil {
				t.Fatal(err)
			}
			if gotConfig != wantConfig || updated.Reader.Workers != 3 ||
				updated.Sender.Workers != 4 || updated.Throttler.RequestedTps != 0 || !updated.Throttler.Installed {
				t.Fatalf("runtime commands changed immutable config or lost applied values: %+v", updated)
			}
			// The cross-client smoke reads this real handler response from the verbose run log.
			t.Logf("snapshot: %s", response.Body.String())
			assertCommandHTTP(t, mux, `{"action":"set-throttler-installed","value":false}`, http.StatusOK)

			loop.cancel()
			loop.awaitSnapshot(t, func(s api.Snapshot) bool { return s.Run.State == api.RunStateStopping })
			unblock()
			if err := loop.awaitExit(t); err != nil {
				t.Fatal(err)
			}
			assertServiceDrainLog(t, loop.logs.String(), deliveredRows.Load(), true)
			assertConfirmedMetric(t, loop.metrics, deliveredRows.Load())
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
	loop := startServiceLoop(t, cfg)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("valid source never reached receiver")
	}
	faulted := loop.awaitSnapshot(t, func(s api.Snapshot) bool { return s.Run.State == api.RunStateFaulted })
	if faulted.Reader.SourceError == nil || faulted.Reader.SourceError.Path != filepath.Join(directory, "b.parquet") || inFlightSenders(faulted) != 1 {
		t.Fatalf("runtime fault lost source or in-flight diagnostic: %+v", faulted)
	}
	unblock()
	var sourceError *pipeline.SourceError
	if err := loop.awaitExit(t); !errors.As(err, &sourceError) || sourceError.Op != "open" {
		t.Fatalf("run error = %v, want corrupt source diagnostic", err)
	}
	assertServiceDrainLog(t, loop.logs.String(), 1, false)
	assertConfirmedMetric(t, loop.metrics, 1)
}

func assertCommandHTTP(t *testing.T, mux http.Handler, body string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodPost, api.CommandsPath, strings.NewReader(body)))
	if response.Code != want || ctx.Err() != nil {
		t.Fatalf("POST %s: HTTP %d, context %v, body %q, want HTTP %d", body, response.Code, ctx.Err(), response.Body.String(), want)
	}
}

type serviceLoop struct {
	mux     http.Handler
	metrics *prometheusMetrics
	logs    *bytes.Buffer
	cancel  context.CancelFunc
	result  <-chan error
}

func startServiceLoop(t *testing.T, cfg config.Config) serviceLoop {
	t.Helper()
	logs := new(bytes.Buffer)
	logger, err := newApplicationLogger("info", zapcore.Lock(zapcore.AddSync(logs)))
	if err != nil {
		t.Fatal(err)
	}
	owner := control.New(cfg, pipelineOptions(cfg, logger), logger)
	metrics := newPrometheusMetrics()
	mux := newServeMux(owner, metrics, cfg, zap.NewNop())
	events := control.Events{
		Source:              prepareSourceAsync(cfg.Source.Path),
		PipelineStarted:     metrics.run.Store,
		RequestedTPSChanged: func(tps int) { metrics.targetTPS.Set(float64(tps)) },
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := owner.Run(ctx, events, gracefulShutdownTimeout)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(gracefulShutdownTimeout + time.Second):
			t.Error("service loop failed to exit")
		}
	})
	return serviceLoop{mux: mux, metrics: metrics, logs: logs, cancel: cancel, result: result}
}

func (h serviceLoop) awaitSnapshot(t *testing.T, ready func(api.Snapshot) bool) api.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		response := httptest.NewRecorder()
		h.mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, api.SnapshotPath, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("snapshot HTTP = %d, body %q", response.Code, response.Body.String())
		}
		value, err := api.DecodeSnapshot(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if ready(value) {
			return value
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("service snapshot did not reach expected state")
	return api.Snapshot{}
}

func (h serviceLoop) awaitExit(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.result:
		return err
	case <-time.After(gracefulShutdownTimeout + time.Second):
		t.Fatal("service loop failed to exit")
		return nil
	}
}

func inFlightSenders(snapshot api.Snapshot) int {
	count := 0
	for _, state := range snapshot.Sender.WorkerStates {
		if state.Activity == api.SenderInFlight {
			count++
		}
	}
	return count
}

func assertServiceDrainLog(t *testing.T, output string, confirmed int64, requireComplete bool) {
	t.Helper()
	want := []string{fmt.Sprintf("confirmed_transactions=%d", confirmed), "unconfirmed_transactions=0"}
	if requireComplete {
		want = append(want, "partial=false")
	}
	count := 0
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if !slices.Contains(fields, "event=run_stopped") {
			continue
		}
		count++
		for _, field := range want {
			if !slices.Contains(fields, field) {
				t.Errorf("drain log %q missing %q", line, field)
			}
		}
	}
	if count != 1 {
		t.Fatalf("run_stopped entries = %d, want 1", count)
	}
}

func assertConfirmedMetric(t *testing.T, metrics *prometheusMetrics, want int64) {
	t.Helper()
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "loadgen_transactions_total" {
			if got := family.GetMetric()[0].GetCounter().GetValue(); got != float64(want) {
				t.Fatalf("confirmed metric = %v, want %d", got, want)
			}
			return
		}
	}
	t.Fatal("confirmed metric missing")
}
