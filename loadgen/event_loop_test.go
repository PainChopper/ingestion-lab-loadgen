package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	dto "github.com/prometheus/client_model/go"
)

func TestElapsedMsUsesRunStartAndAccumulatedTime(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state := newTestControlState(t)
	if got := state.elapsedMs(start); got != 0 {
		t.Fatalf("idle elapsed = %d, want 0", got)
	}

	state.run.lifecycle.run()
	state.run.runStartedAt = start
	if got := state.elapsedMs(start.Add(1250 * time.Millisecond)); got != 1250 {
		t.Fatalf("running elapsed = %d, want 1250", got)
	}

	state.run.lifecycle.pause()
	state.pauseElapsed(start.Add(1250 * time.Millisecond))
	if got := state.elapsedMs(start.Add(10 * time.Second)); got != 1250 {
		t.Fatalf("paused elapsed = %d, want 1250", got)
	}

	state.run.lifecycle.run()
	state.run.runStartedAt = start.Add(10 * time.Second)
	if got := state.elapsedMs(start.Add(10*time.Second + 750*time.Millisecond)); got != 2000 {
		t.Fatalf("resumed elapsed = %d, want 2000", got)
	}
}

func TestPausedFaultPreservesElapsedAndCollectsLateSuccessOnce(t *testing.T) {
	state := newTestControlState(t)
	start := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	state.run.lifecycle.run()
	state.run.runStartedAt = start
	state.pauseElapsed(start.Add(time.Second))
	state.run.lifecycle.pause()
	state.run.pausedStatus = runtimeStatus{Run: runtimeRunStatus{ElapsedMs: 1_000}}
	if !state.run.lifecycle.fault() {
		t.Fatal("Pause did not allow source fault")
	}
	state.pauseElapsed(start.Add(time.Hour))
	state.run.sourceError = &readerSourceError{Category: "source", Operation: "read", Message: "corrupt"}
	var completed atomic.Int64
	completed.Add(3)
	metrics := NewPrometheusMetrics()
	state.resetFaultedMeasurements(&completed, metrics)
	state.resetFaultedMeasurements(&completed, metrics)
	metric := &dto.Metric{}
	if err := metrics.transactionsTotal.Write(metric); err != nil {
		t.Fatal(err)
	}
	status := state.runtimeStatusAt(newPipelineRuntime(&state), start.Add(2*time.Hour))
	if status.Run.State != runStateFaulted || status.Run.ElapsedMs != 1_000 ||
		status.Run.TotalTransactions != 3 || status.Reader.SourceError == nil ||
		state.run.pausedStatus.Run.ElapsedMs != 0 || metric.GetCounter().GetValue() != 3 {
		t.Fatalf("faulted status=%+v cumulative=%v", status, metric.GetCounter().GetValue())
	}
	completed.Add(2)
	state.resetProgress(&completed, metrics)
	if err := metrics.transactionsTotal.Write(metric); err != nil {
		t.Fatal(err)
	}
	if state.run.totalTransactions != 0 || state.run.elapsedBeforeRun != 0 ||
		state.run.sourceError != nil || metric.GetCounter().GetValue() != 5 {
		t.Fatalf("Reset progress=%+v cumulative=%v", state.run, metric.GetCounter().GetValue())
	}
}

func TestRunEventLoopStopsWhenApplicationContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(
			ctx,
			make(chan runtimeCommand),
			make(chan time.Time),
			NewPrometheusMetrics(),
		)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event loop did not stop after application cancellation")
	}
}

func TestPipelineRuntimeSenderInheritsRunContextAcrossPauseResume(t *testing.T) {
	applicationContext, cancelApplication := context.WithCancel(context.Background())
	defer cancelApplication()
	state := newTestControlState(t)
	runtime := newPipelineRuntime(&state)
	if err := runtime.start(applicationContext); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	runtime.startSender()
	firstPool := runtime.senderPool
	firstContext := firstPool.ctx
	firstPool.pause()
	if runtime.runContext.Err() != nil {
		t.Fatal("Pause canceled the pipeline run context")
	}

	firstPool.resumeRun()
	if runtime.senderPool != firstPool || runtime.senderPool.ctx != firstContext {
		t.Fatal("Resume replaced the Sender pool/context")
	}
	stopped := make(chan struct{})
	runtime.senderPool.attempt = func(ctx context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(stopped)
		<-ctx.Done()
		return senderAttemptCanceled
	}
	runtime.senderBatches <- []Transaction{{ClientID: "application-canceled"}}
	<-stopped
	cancelApplication()
	select {
	case <-runtime.senderPool.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("application cancellation did not reach resumed Sender pool")
	}
	runtime.stop()
}

func TestPipelineRuntimeStopIsIdempotentWithAndWithoutSender(t *testing.T) {
	state := newTestControlState(t)
	runtime := newPipelineRuntime(&state)

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("start active runtime: %v", err)
	}
	runtime.startSender()
	runtime.stop()
	runtime.stop()

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("start reset runtime: %v", err)
	}
	runtime.stop()
	runtime.stop()

	if reader := state.telemetry.readerChannel.snapshot(time.Now()); reader.capacity != 0 {
		t.Fatalf("reader channel capacity after stop = %d, want 0", reader.capacity)
	}
	if sender := state.telemetry.senderChannel.snapshot(time.Now()); sender.capacity != 0 {
		t.Fatalf("sender channel capacity after stop = %d, want 0", sender.capacity)
	}
}

func TestMetricsWindowDrivesChannelRatesAndActualTPS(t *testing.T) {
	state := newTestControlState(t)
	state.metricsWindow = 300 * time.Millisecond
	if err := parquet.WriteFile(state.config.Source.Path, []Transaction{{}, {}, {}}); err != nil {
		t.Fatal(err)
	}
	first := true
	secondAttempt := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first {
			first = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
		close(secondAttempt)
		<-release
	}))
	defer server.Close()
	defer close(release)
	state.config.Sender.API.URL = server.URL
	requests := make(chan runtimeCommand)
	metrics := make(chan time.Time)
	promMetrics := NewPrometheusMetrics()
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, promMetrics)
	}()
	defer func() { close(requests); <-done }()
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	select {
	case <-secondAttempt:
	case <-time.After(time.Second):
		t.Fatal("Sender did not complete first batch and start the second")
	}
	metrics <- time.Now()
	snapshot := requestRuntimeStatus(requests)
	if snapshot.Run.TotalTransactions != 3 || gaugeValue(t, promMetrics.actualTPS) != 10 {
		t.Fatalf("sampled progress = %+v, actual TPS = %v", snapshot.Run, gaugeValue(t, promMetrics.actualTPS))
	}
	if got := snapshot.ReaderChannel; got.ReceivedTransactionsPerSecond != float64(got.ReceivedTransactionsTotal)/state.metricsWindow.Seconds() {
		t.Fatalf("Reader channel rate = %+v", got)
	}
	if got := snapshot.SenderChannel; got.ReceivedTransactionsPerSecond != float64(got.ReceivedTransactionsTotal)/state.metricsWindow.Seconds() {
		t.Fatalf("Sender channel rate = %+v", got)
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	frozen := requestRuntimeStatus(requests)
	metrics <- time.Now().Add(time.Minute)
	paused := requestRuntimeStatus(requests)
	if paused.Run != frozen.Run || paused.Sender != frozen.Sender || paused.Reader != frozen.Reader ||
		paused.ReaderChannel != frozen.ReaderChannel || paused.SenderChannel != frozen.SenderChannel ||
		gaugeValue(t, promMetrics.actualTPS) != 10 {
		t.Fatalf("paused measurements/gauge changed: %+v TPS=%v", paused, gaugeValue(t, promMetrics.actualTPS))
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdSetRequestedTPS, value: 2_400_000}); result.rejected {
		t.Fatalf("set TPS = %+v", result)
	}
	if got := gaugeValue(t, promMetrics.targetTPS); got != 2_400_000 {
		t.Fatalf("target TPS = %v", got)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	if got := gaugeValue(t, promMetrics.actualTPS); got != 0 {
		t.Fatalf("fresh Resume actual TPS = %v, want 0", got)
	}
}

func TestSenderSnapshotKeepsAppliedControlsAcrossLifecycle(t *testing.T) {
	requests := make(chan runtimeCommand)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, NewPrometheusMetrics())
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	reply := make(chan runtimeCommandReceipt, 1)
	for _, command := range []runtimeCommand{
		{kind: cmdSetSenderWorkers, value: 3, receiptReply: reply},
	} {
		requests <- command
		if result := <-reply; result.rejected {
			t.Fatalf("Sender setting = %+v", result)
		}
	}
	statusReply := make(chan runtimeStatus, 1)
	assertSender := func(wantState runState, wantLive int) {
		t.Helper()
		requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
		snapshot := <-statusReply
		sender := snapshot.Sender
		if snapshot.Run.State != wantState || sender.Workers != 3 || sender.LiveWorkers != wantLive ||
			sender.DrainingWorkers != 0 {
			t.Fatalf("Sender snapshot = %+v in %s", sender, snapshot.Run.State)
		}
	}
	assertSender(runStateIdle, 0)
	requests <- runtimeCommand{kind: cmdRun, receiptReply: reply}
	if result := <-reply; result.rejected {
		t.Fatalf("Run = %+v", result)
	}
	assertSender(runStateRunning, 3)
	requests <- runtimeCommand{kind: cmdPause}
	assertSender(runStatePaused, 3)
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdSetSenderWorkers, value: 7}); result.rejected {
		t.Fatalf("paused Sender workers = %+v", result)
	}
	if got := requestRuntimeStatus(requests).Sender; got.Workers != 7 || got.LiveWorkers != 3 {
		t.Fatalf("paused live controls/frozen categories = %+v", got)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
		t.Fatalf("Reset = %+v", result)
	}
	if got := requestRuntimeStatus(requests).Sender; got.Workers != 7 || got.LiveWorkers != 0 {
		t.Fatalf("reset Sender = %+v", got)
	}
}

func gaugeValue(t *testing.T, gauge interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := gauge.Write(metric); err != nil {
		t.Fatalf("write gauge: %v", err)
	}
	return metric.GetGauge().GetValue()
}

func assertFaultedChannelSnapshot(
	t *testing.T,
	snapshot runtimeStatus,
	readerCapacity int,
	senderCapacity int,
) {
	t.Helper()
	if snapshot.Run.TotalTransactions != 0 || snapshot.Reader.ReadTps != 0 || snapshot.Reader.RowsRead != 0 {
		t.Fatalf("faulted flow = %+v, want zero", snapshot)
	}
	if snapshot.ReaderChannel != (runtimeChannelStatus{Capacity: readerCapacity}) {
		t.Fatalf("faulted Reader channel = %+v, want configured capacity %d and zero flow", snapshot.ReaderChannel, readerCapacity)
	}
	if snapshot.SenderChannel != (runtimeChannelStatus{Capacity: senderCapacity}) {
		t.Fatalf("faulted Sender channel = %+v, want configured capacity %d and zero flow", snapshot.SenderChannel, senderCapacity)
	}
}

func configureFaultedChannelCapacities(
	t *testing.T,
	requests chan<- runtimeCommand,
	reply chan runtimeCommandReceipt,
) {
	t.Helper()
	for _, setting := range []struct {
		kind     runtimeCommandKind
		capacity int
	}{
		{kind: cmdSetReaderChannelCapacity, capacity: 4},
		{kind: cmdSetSenderChannelCapacity, capacity: 8},
	} {
		requests <- runtimeCommand{kind: setting.kind, value: setting.capacity, receiptReply: reply}
		if result := <-reply; result.rejected {
			t.Fatalf("set capacity %v = %+v", setting.kind, result)
		}
	}
}

func TestRunFailureFaultsAndResetClearsSourceError(t *testing.T) {
	state := newTestControlState(t)
	if err := os.Remove(state.config.Source.Path); err != nil {
		t.Fatal(err)
	}
	requests := make(chan runtimeCommand)
	metrics := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, NewPrometheusMetrics())
	}()
	t.Cleanup(func() { close(requests); <-done })
	reply := make(chan runtimeCommandReceipt, 1)
	configureFaultedChannelCapacities(t, requests, reply)
	for range 2 {
		result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun})
		if result.err == nil {
			t.Fatal("missing source did not fail startup")
		}
		snapshot := requestRuntimeStatus(requests)
		if snapshot.Run.State != runStateFaulted || snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.Operation != "glob" || snapshot.Run.ElapsedMs != 0 {
			t.Fatalf("startup snapshot = %+v", snapshot)
		}
		assertFaultedChannelSnapshot(t, snapshot, 4, 8)
		if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); !result.rejected {
			t.Fatalf("Run while faulted = %+v", result)
		}
		if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
			t.Fatalf("Reset = %+v", result)
		}
		if snapshot := requestRuntimeStatus(requests); snapshot.Run.State != runStateIdle || snapshot.Reader.SourceError != nil {
			t.Fatalf("Reset snapshot = %+v", snapshot)
		}
	}
	if err := parquet.WriteFile(state.config.Source.Path, []Transaction{}); err != nil {
		t.Fatal(err)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	if snapshot := requestRuntimeStatus(requests); snapshot.Run.State != runStateRunning || snapshot.Reader.SourceError != nil {
		t.Fatalf("restored source snapshot = %+v", snapshot)
	}
}

func TestRuntimeSourceErrorResetClearsFaultedSnapshot(t *testing.T) {
	state := newTestControlState(t)
	if err := os.WriteFile(state.config.Source.Path, []byte("not a parquet file"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan runtimeCommand)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, make(chan time.Time), NewPrometheusMetrics())
	}()
	t.Cleanup(func() { close(requests); <-done })
	reply := make(chan runtimeCommandReceipt, 1)
	configureFaultedChannelCapacities(t, requests, reply)
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	deadline := time.After(time.Second)
	for {
		snapshot := requestRuntimeStatus(requests)
		if snapshot.Run.State == runStateFaulted {
			if snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.Operation != "open" {
				t.Fatalf("source error = %+v", snapshot.Reader.SourceError)
			}
			assertFaultedChannelSnapshot(t, snapshot, 4, 8)
			break
		}
		select {
		case <-deadline:
			t.Fatal("corrupt source did not fault")
		case <-time.After(time.Millisecond):
		}
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
		t.Fatalf("Reset = %+v", result)
	}
	if snapshot := requestRuntimeStatus(requests); snapshot.Run.State != runStateIdle || snapshot.Reader.SourceError != nil {
		t.Fatalf("Reset snapshot = %+v", snapshot)
	}
	if err := parquet.WriteFile(state.config.Source.Path, []Transaction{}); err != nil {
		t.Fatal(err)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	if snapshot := requestRuntimeStatus(requests); snapshot.Run.State != runStateRunning || snapshot.Reader.SourceError != nil {
		t.Fatalf("repaired source snapshot = %+v", snapshot)
	}
}

func TestRunEventLoopFaultsOnCorruptParquetAndPreservesWorkerDiagnostic(t *testing.T) {
	fixtureDirectory := t.TempDir()
	fixturePath := filepath.Join(fixtureDirectory, "broken.parquet")
	if err := os.WriteFile(fixturePath, []byte("not a parquet file"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := newTestControlState(t)
	state.config.Source.Path = filepath.Join(fixtureDirectory, "*.parquet")
	requests := make(chan runtimeCommand)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(
			ctx,
			requests,
			make(chan time.Time),
			NewPrometheusMetrics(),
		)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	reply := make(chan runtimeCommandReceipt, 1)
	requests <- runtimeCommand{kind: cmdRun, receiptReply: reply}
	if result := <-reply; result.rejected {
		t.Fatalf("Run = %+v", result)
	}
	var snapshot runtimeStatus
	deadline := time.After(time.Second)
	for snapshot.Run.State != runStateFaulted {
		statusReply := make(chan runtimeStatus, 1)
		requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
		snapshot = <-statusReply
		select {
		case <-deadline:
			t.Fatal("corrupt parquet did not fault the run")
		default:
		}
	}
	if snapshot.Reader.SourceDirectory != filepath.ToSlash(fixtureDirectory) || snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.RelativePath != "broken.parquet" || snapshot.Reader.SourceError.Message == "" {
		t.Fatalf("corrupt parquet snapshot = %+v", snapshot.Reader)
	}
	if snapshot.Reader.LiveWorkers != 0 || snapshot.Sender.LiveWorkers != 0 {
		t.Fatalf("faulted workers = reader %+v sender %+v", snapshot.Reader, snapshot.Sender)
	}
	assertFaultedChannelSnapshot(t, snapshot, state.controls.readerChannelCapacity, state.controls.senderChannelCapacity)
}

func TestRunEventLoopFaultsBeforeWorkersForUnavailableSourceDirectory(t *testing.T) {
	fixtureRoot := t.TempDir()
	missingDirectory := filepath.Join(fixtureRoot, "unavailable")
	state := newTestControlState(t)
	state.config.Source.Path = filepath.Join(missingDirectory, "*.parquet")
	requests := make(chan runtimeCommand)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(
			ctx,
			requests,
			make(chan time.Time),
			NewPrometheusMetrics(),
		)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	reply := make(chan runtimeCommandReceipt, 1)
	requests <- runtimeCommand{kind: cmdRun, receiptReply: reply}
	if result := <-reply; result.err == nil {
		t.Fatal("Run error = nil, want unavailable directory error")
	}
	statusReply := make(chan runtimeStatus, 1)
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
	snapshot := <-statusReply
	if snapshot.Run.State != runStateFaulted || snapshot.Reader.SourceDirectory != filepath.ToSlash(missingDirectory) || snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.Operation != "glob" || snapshot.Reader.SourceError.RelativePath != "*.parquet" || snapshot.Reader.SourceError.Message != "no files found matching pattern" {
		t.Fatalf("unavailable source snapshot = %+v", snapshot)
	}
	if snapshot.Reader.LiveWorkers != 0 || snapshot.Sender.LiveWorkers != 0 {
		t.Fatalf("startup failure workers = reader %+v sender %+v", snapshot.Reader, snapshot.Sender)
	}
	assertFaultedChannelSnapshot(t, snapshot, state.controls.readerChannelCapacity, state.controls.senderChannelCapacity)
}

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	harness := startActualChannelEventLoopForTest(t)
	harness.command(t, cmdRun)
	reader := harness.nextReader(t)
	sender := harness.nextSender(t)
	harness.command(t, cmdRun)
	if harness.nextReader(t) != reader || harness.nextSender(t) != sender {
		t.Fatal("repeated Run replaced pipeline channels")
	}
}

func TestPauseKeepsInFlightHTTPAndProcessesCommandsUntilRun(t *testing.T) {
	state := newTestControlState(t)
	if err := parquet.WriteFile(state.config.Source.Path, []Transaction{{ClientID: "retained"}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []Transaction
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			return
		}
		entered <- batch[0].ClientID
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	state.config.Sender.API.URL = server.URL
	requests := make(chan runtimeCommand)
	metrics := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, NewPrometheusMetrics())
	}()
	defer func() { close(requests); <-done }()
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	if id := <-entered; id != "retained" {
		t.Fatalf("in-flight ClientID = %q", id)
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	before := requestRuntimeStatus(requests)
	if before.Sender.InFlightWorkers != 1 {
		t.Fatalf("Pause snapshot = %+v", before.Sender)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdSetRequestedTPS, value: 0}); result.rejected {
		t.Fatalf("setting with pending HTTP = %+v", result)
	}
	metrics <- time.Now()
	if got := requestRuntimeStatus(requests); got.Sender != before.Sender || got.Throttler.RequestedTps != 0 {
		t.Fatalf("paused snapshot/live setting = %+v", got)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	close(release)
}

func TestPipelineRuntimeStopJoinsStagesClearsProgressAndStartsFreshRun(t *testing.T) {
	state := newTestControlState(t)
	state.controls.installed = false
	runtime := newPipelineRuntime(&state)
	t.Cleanup(runtime.stop)
	if err := runtime.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader := runtime.readerPool
	readerBatches := runtime.readerBatches
	senderBatches := runtime.senderBatches
	throttlerDone := runtime.throttler.done
	runContext := runtime.runContext
	capturedIDs := make(chan string, 2)
	runtime.startSender()
	runtime.senderPool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		capturedIDs <- batch[0].ClientID
		return senderAttemptSuccess
	}
	readerBatches <- []Transaction{{ClientID: "old"}}
	select {
	case id := <-capturedIDs:
		if id != "old" {
			t.Fatalf("old delivery = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("old batch was not delivered")
	}
	runtime.stopSender()
	if got := runtime.terminallyCompletedTransactionsSinceTick.Load(); got != 1 {
		t.Fatalf("completed transactions = %d, want 1", got)
	}
	state.controls.installed = true
	state.controls.requestedTPS = 0
	runtime.throttler.update(state.throttlerSettings())
	readerBatches <- []Transaction{{ClientID: "old-held"}}
	deadline := time.After(time.Second)
	for state.telemetry.readerChannel.snapshot(time.Now()).receivedBatchesTotal != 2 {
		select {
		case <-deadline:
			t.Fatal("Throttler did not hold the zero-TPS batch")
		default:
		}
	}
	runtime.stop()
	if runContext.Err() == nil {
		t.Fatal("stop did not cancel run context")
	}
	select {
	case <-throttlerDone:
	default:
		t.Fatal("Throttler is not joined")
	}
	select {
	case <-reader.done:
	default:
		t.Fatal("Reader done is open after stop")
	}
	assertClosedActualChannel(t, readerBatches)
	assertClosedActualChannel(t, senderBatches)
	if runtime.readerPool != nil || runtime.senderPool != nil || runtime.throttler != nil || runtime.sourceErrors() != nil {
		t.Fatal("stop retained a stage or source-error channel")
	}
	rr, sr := runtime.snapshots()
	if rr != (readerPoolSnapshot{}) || sr != (senderPoolSnapshot{}) {
		t.Fatalf("snapshots after stop = %+v / %+v", rr, sr)
	}
	runtime.reconcileReader(7)
	runtime.reconcileSender(7)
	runtime.stop()
	state.resetProgress(&runtime.terminallyCompletedTransactionsSinceTick, NewPrometheusMetrics())
	if runtime.terminallyCompletedTransactionsSinceTick.Load() != 0 || state.run.totalTransactions != 0 {
		t.Fatal("reset retained progress")
	}
	state.controls.installed = false
	if err := runtime.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.readerPool == reader || runtime.readerBatches == readerBatches ||
		runtime.senderBatches == senderBatches {
		t.Fatal("fresh run reused stopped Reader or queues")
	}
	runtime.startSender()
	runtime.senderPool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		capturedIDs <- batch[0].ClientID
		return senderAttemptSuccess
	}
	runtime.readerBatches <- []Transaction{{ClientID: "fresh"}}
	select {
	case id := <-capturedIDs:
		if id != "fresh" {
			t.Fatalf("delivery after reset = %q, want fresh", id)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh batch was not delivered")
	}
	runtime.stopSender()
	if got := runtime.terminallyCompletedTransactionsSinceTick.Load(); got != 1 {
		t.Fatalf("fresh completed transactions = %d, want 1", got)
	}
	select {
	case id := <-capturedIDs:
		t.Fatalf("unexpected delivery after reset: %q", id)
	default:
	}
}

func TestResetDuringRunReturnsConflictAndPreservesPipeline(t *testing.T) {
	harness := startActualChannelEventLoopForTest(t)
	harness.command(t, cmdRun)
	reader := harness.nextReader(t)
	sender := harness.nextSender(t)
	if result := executeRuntimeCommand(harness.requests, runtimeCommand{kind: cmdReset}); !result.rejected {
		t.Fatalf("Reset during Run = %+v", result)
	}
	waitForState(t, harness.requests, runStateRunning)
	if harness.nextReader(t) != reader || harness.nextSender(t) != sender {
		t.Fatal("rejected Reset replaced channels")
	}
}

func TestReaderMeasurementsSurvivePauseAndClearOnReset(t *testing.T) {
	requests := make(chan runtimeCommand, 3)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(
			context.Background(),
			requests,
			metrics,
			NewPrometheusMetrics(),
		)
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	requests <- runtimeCommand{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	readerChannel := make(chan []Transaction, state.config.ReaderChannel.Capacity.Initial)
	state.telemetry.readerChannel.start(readerChannel, 2)
	if !state.telemetry.readerChannel.send(context.Background(), readerChannel, make([]Transaction, 2)) {
		t.Fatal("readerChannel send failed")
	}
	state.telemetry.reader.recordRead(2)
	metrics <- time.Now()
	before := requestRuntimeStatus(requests)
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	state.telemetry.reader.recordRead(3)
	metrics <- time.Now()

	statusReply := make(chan runtimeStatus, 1)
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
	snapshot := <-statusReply
	if snapshot.Reader.RowsRead != 2 || snapshot.ReaderChannel.Capacity != 2 ||
		snapshot.ReaderChannel.DepthBatches != 1 || snapshot.ReaderChannel.BufferedTransactions != 2 ||
		snapshot.ReaderChannel.SentBatchesTotal != 1 || snapshot.ReaderChannel.SentTransactionsTotal != 2 ||
		snapshot.ReaderChannel.SentBatchesPerSecond != 1 || snapshot.ReaderChannel.SentTransactionsPerSecond != 2 {
		t.Fatalf("paused snapshot = %+v, want reader and readerChannel measurements", snapshot)
	}
	if snapshot.Reader.ReadTps != before.Reader.ReadTps {
		t.Fatalf("Pause changed last Reader rate: %v -> %v", before.Reader.ReadTps, snapshot.Reader.ReadTps)
	}
	frozen := snapshot
	state.telemetry.readerChannel.recordReceive(len(<-readerChannel))
	metrics <- time.Now()
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
	snapshot = <-statusReply
	if snapshot.ReaderChannel != frozen.ReaderChannel || snapshot.Reader != frozen.Reader || snapshot.Run != frozen.Run {
		t.Fatalf("paused measurements changed after Reader flow/tick: %+v", snapshot)
	}

	requests <- runtimeCommand{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	resumed := requestRuntimeStatus(requests)
	if resumed.Reader.RowsRead != 5 || resumed.Reader.ReadTps != 0 ||
		resumed.ReaderChannel.ReceivedTransactionsTotal != 2 || resumed.ReaderChannel.ReceivedTransactionsPerSecond != 0 {
		t.Fatalf("Resume totals/rate window = %+v", resumed)
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	resetReply := make(chan runtimeCommandReceipt, 1)
	requests <- runtimeCommand{kind: cmdReset, receiptReply: resetReply}
	if result := <-resetReply; result.rejected {
		t.Fatalf("Reset rejected = %t, want false", result.rejected)
	}
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
	snapshot = <-statusReply
	if snapshot.Reader.ReadTps != 0 || snapshot.Reader.RowsRead != 0 ||
		snapshot.ReaderChannel.Capacity != state.config.ReaderChannel.Capacity.Initial || snapshot.ReaderChannel.DepthBatches != 0 ||
		snapshot.ReaderChannel.BufferedTransactions != 0 || snapshot.ReaderChannel.BlockedSenders != 0 ||
		snapshot.ReaderChannel.OldestBlockedSenderMs != 0 || snapshot.ReaderChannel.BlockedMs != 0 ||
		snapshot.ReaderChannel.SentBatchesTotal != 0 || snapshot.ReaderChannel.SentTransactionsTotal != 0 ||
		snapshot.ReaderChannel.ReceivedBatchesTotal != 0 || snapshot.ReaderChannel.ReceivedTransactionsTotal != 0 ||
		snapshot.ReaderChannel.SentBatchesPerSecond != 0 || snapshot.ReaderChannel.SentTransactionsPerSecond != 0 ||
		snapshot.ReaderChannel.ReceivedBatchesPerSecond != 0 || snapshot.ReaderChannel.ReceivedTransactionsPerSecond != 0 {
		t.Fatalf("snapshot after Reset = %+v, want zero measurements", snapshot)
	}
}

func TestThrottlerControlsApplyImmediatelyAndPersistThroughReset(t *testing.T) {
	requests, metrics := startEventLoopForTest(t)
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdSetRequestedTPS, value: 0}); result.rejected {
		t.Fatalf("zero TPS = %+v", result)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	waitForReaderReceives(t, requests, 1)
	metrics <- time.Now()
	if got := requestRuntimeStatus(requests); got.Run.TotalTransactions != 0 || got.Throttler.RequestedTps != 0 {
		t.Fatalf("zero TPS snapshot = %+v", got)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdSetThrottlerInstalled, installed: false}); result.rejected {
		t.Fatalf("bypass = %+v", result)
	}
	deadline := time.After(time.Second)
	for requestRuntimeStatus(requests).SenderChannel.ReceivedTransactionsTotal == 0 {
		select {
		case <-deadline:
			t.Fatal("bypass did not forward real Reader batch")
		case <-time.After(time.Millisecond):
		}
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	for _, command := range []runtimeCommand{{kind: cmdSetRequestedTPS, value: 400_000}, {kind: cmdSetThrottlerInstalled, installed: true}} {
		if result := executeRuntimeCommand(requests, command); result.rejected {
			t.Fatalf("paused setting = %+v", result)
		}
	}
	before := requestRuntimeStatus(requests)
	metrics <- time.Now()
	if got := requestRuntimeStatus(requests); got.SenderChannel.ReceivedTransactionsTotal != before.SenderChannel.ReceivedTransactionsTotal {
		t.Fatal("paused snapshot changed after settings update and metrics tick")
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
		t.Fatalf("Reset = %+v", result)
	}
	if got := requestRuntimeStatus(requests); got.Run.State != runStateIdle || got.Run.TotalTransactions != 0 || got.Throttler.RequestedTps != 400_000 || got.Throttler.Installed != true || got.Config.ThrottlerRequestedTPS.Initial != 2_000_000 {
		t.Fatalf("reset settings = %+v", got)
	}
}

func TestResetWhileZeroTPSHoldsBatchCompletes(t *testing.T) {
	requests, _ := startEventLoopForTest(t)
	reply := make(chan runtimeCommandReceipt, 1)
	requests <- runtimeCommand{kind: cmdSetRequestedTPS, value: 0, receiptReply: reply}
	if result := <-reply; result.rejected {
		t.Fatalf("set zero TPS rejected = %t", result.rejected)
	}
	requests <- runtimeCommand{kind: cmdRun, receiptReply: reply}
	if result := <-reply; result.rejected {
		t.Fatalf("Run rejected = %t", result.rejected)
	}
	waitForReaderReceives(t, requests, 1)
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	requests <- runtimeCommand{kind: cmdReset, receiptReply: reply}
	select {
	case result := <-reply:
		if result.rejected {
			t.Fatalf("Reset rejected = %t", result.rejected)
		}
	case <-time.After(time.Second):
		t.Fatal("Reset blocked while zero TPS held a batch")
	}
	statusReply := make(chan runtimeStatus, 1)
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: statusReply}
	if got := <-statusReply; got.Run.State != runStateIdle || got.Run.TotalTransactions != 0 || got.Throttler.RequestedTps != 0 {
		t.Fatalf("snapshot after zero-TPS Reset = %+v", got)
	}
}

func TestSenderChannelTelemetryFollowsWindowPauseRunAndReset(t *testing.T) {
	requests, metrics := startEventLoopForTest(t)
	initial := requestRuntimeStatus(requests)
	if initial.SenderChannel != (runtimeChannelStatus{}) || initial.Throttler.AdmittedTps != 0 {
		t.Fatalf("initial snapshot = %+v", initial)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	deadline := time.After(time.Second)
	for requestRuntimeStatus(requests).SenderChannel.ReceivedTransactionsTotal == 0 {
		select {
		case <-deadline:
			t.Fatal("Sender did not receive real rows")
		case <-time.After(time.Millisecond):
		}
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	metrics <- time.Now()
	active := requestRuntimeStatus(requests)
	if active.SenderChannel.SentTransactionsTotal == 0 || active.SenderChannel.ReceivedTransactionsTotal == 0 || active.Throttler.AdmittedTps != active.SenderChannel.SentTransactionsPerSecond {
		t.Fatalf("sampled Sender telemetry = %+v", active)
	}
	metrics <- time.Now()
	paused := requestRuntimeStatus(requests)
	if paused.SenderChannel != active.SenderChannel || paused.Throttler != active.Throttler || paused.Run != active.Run || paused.Sender != active.Sender {
		t.Fatalf("paused telemetry = %+v", paused)
	}
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdRun}); result.err != nil {
		t.Fatal(result.err)
	}
	deadline = time.After(time.Second)
	for requestRuntimeStatus(requests).SenderChannel.ReceivedTransactionsTotal <= paused.SenderChannel.ReceivedTransactionsTotal {
		select {
		case <-deadline:
			t.Fatal("Resume did not receive rows")
		case <-time.After(time.Millisecond):
		}
	}
	requests <- runtimeCommand{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	if result := executeRuntimeCommand(requests, runtimeCommand{kind: cmdReset}); result.rejected {
		t.Fatalf("Reset = %+v", result)
	}
	reset := requestRuntimeStatus(requests)
	if reset.SenderChannel != (runtimeChannelStatus{}) || reset.Throttler.AdmittedTps != 0 || reset.Run.State != runStateIdle {
		t.Fatalf("reset telemetry = %+v", reset)
	}
}

func startEventLoopForTest(t *testing.T) (chan runtimeCommand, chan time.Time) {
	t.Helper()
	requests := make(chan runtimeCommand, 3)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	if err := parquet.WriteFile(state.config.Source.Path, []Transaction{{ClientID: "source"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, NewPrometheusMetrics())
	}()
	t.Cleanup(func() { close(requests); <-done })
	return requests, metrics
}

func TestReaderWorkersIdleUpdateAfterResetDoesNotReconcileStoppedPool(t *testing.T) {
	state := newTestControlState(t)
	runtime := newPipelineRuntime(&state)
	defer runtime.stop()
	if err := runtime.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldPool := runtime.readerPool
	runtime.stop()
	select {
	case <-oldPool.done:
	default:
		t.Fatal("old Reader pool not joined")
	}
	state.controls.readerWorkers = 7
	runtime.reconcileReader(7)
	if oldPool.desired != 1 {
		t.Fatalf("stopped Reader desired = %d", oldPool.desired)
	}
	if err := runtime.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.readerPool == oldPool || runtime.readerPool.desired != 7 || runtime.readerPool.aggregateSnapshot().liveWorkers != 7 {
		t.Fatal("fresh run did not create seven real Reader workers")
	}
}

func waitForState(t *testing.T, requests chan<- runtimeCommand, want runState) {
	t.Helper()

	reply := make(chan runtimeStatus, 1)
	select {
	case requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}:
	case <-time.After(time.Second):
		t.Fatalf("state did not reach %v", want)
	}
	select {
	case snapshot := <-reply:
		if snapshot.Run.State != want {
			t.Fatalf("state = %v, want %v", snapshot.Run.State, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("state did not reach %v", want)
	}
}

func TestCloseAndDrainReturnsForClosedChannel(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{ClientID: "retained"}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		closeAndDrain(batches)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closeAndDrain did not return for a closed channel")
	}
}

func TestEventLoopKeepsActualChannelsAcrossPauseResume(t *testing.T) {
	for _, capacity := range []int{0, 1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			harness := startActualChannelEventLoopForTest(t)
			defer harness.stop()

			harness.setCapacity(t, cmdSetReaderChannelCapacity, capacity)
			harness.setCapacity(t, cmdSetSenderChannelCapacity, capacity)
			harness.command(t, cmdRun)
			reader := harness.nextReader(t)
			sender := harness.nextSender(t)
			if cap(reader) != capacity || cap(sender) != capacity {
				t.Fatalf("actual channel capacities = Reader %d, Sender %d, want %d", cap(reader), cap(sender), capacity)
			}

			harness.command(t, cmdPause)
			harness.command(t, cmdRun)
			if harness.state.telemetry.readerChannel.batches != reader || harness.state.telemetry.senderChannel.batches != sender {
				t.Fatal("Pause/Resume replaced an actual event-loop channel")
			}

		})
	}
}

func TestPipelineRuntimeSenderChannelTelemetryUsesAppliedReadBatchSizeAfterReset(t *testing.T) {
	state := newTestControlState(t)
	state.controls.senderChannelCapacity = 1
	runtime := newPipelineRuntime(&state)
	defer runtime.stop()
	for _, size := range []int{1_000, 2_000} {
		state.controls.readBatchSize = size
		if err := runtime.start(context.Background()); err != nil {
			t.Fatal(err)
		}
		// A physical queued batch proves telemetry uses the applied size, not len(batch).
		runtime.senderBatches <- []Transaction{{ClientID: "queued"}}
		state.telemetry.senderChannel.recordSend(1)
		if got := state.telemetry.senderChannel.snapshot(time.Now()); got.depthBatches != 1 ||
			got.bufferedTransactions != size || got.sentTransactionsTotal != 1 {
			t.Fatalf("Sender channel telemetry = %+v, want depth 1 and %d buffered transactions", got, size)
		}
		runtime.stop()
		state.resetProgress(&runtime.terminallyCompletedTransactionsSinceTick, NewPrometheusMetrics())
		if got := state.telemetry.senderChannel.snapshot(time.Now()); got.depthBatches != 0 || got.sentTransactionsTotal != 0 {
			t.Fatalf("Sender channel after reset = %+v", got)
		}
	}
}

func TestEventLoopResetDrainsQueuedReaderBatch(t *testing.T) {
	for _, capacity := range []int{1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			harness := startActualChannelEventLoopForTest(t)
			if err := parquet.WriteFile(harness.state.config.Source.Path, []Transaction{{ClientID: "old"}}); err != nil {
				t.Fatal(err)
			}
			deliveredIDs := make(chan string, 16)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch []Transaction
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case deliveredIDs <- batch[0].ClientID:
				default:
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			harness.state.config.Sender.API.URL = server.URL
			if result := executeRuntimeCommand(harness.requests, runtimeCommand{kind: cmdSetRequestedTPS, value: 0}); result.rejected {
				t.Fatalf("zero TPS = %+v", result)
			}
			harness.setCapacity(t, cmdSetReaderChannelCapacity, capacity)
			harness.setCapacity(t, cmdSetSenderChannelCapacity, capacity)
			harness.command(t, cmdRun)
			reader := harness.nextReader(t)
			sender := harness.nextSender(t)
			waitForReaderReceives(t, harness.requests, 1)
			deadline := time.After(time.Second)
			for len(reader) == 0 {
				select {
				case <-deadline:
					t.Fatal("Reader did not queue real source batch")
				case <-time.After(time.Millisecond):
				}
			}
			if len(sender) != 0 {
				t.Fatal("zero TPS forwarded a batch")
			}
			harness.command(t, cmdPause)
			harness.command(t, cmdReset)
			assertClosedActualChannel(t, reader)
			assertClosedActualChannel(t, sender)
			if harness.state.telemetry.readerChannel.batches != nil || harness.state.telemetry.senderChannel.batches != nil {
				t.Fatal("Reset retained telemetry attachment")
			}
			if err := parquet.WriteFile(harness.state.config.Source.Path, []Transaction{{ClientID: "fresh"}}); err != nil {
				t.Fatal(err)
			}
			harness.command(t, cmdRun)
			if harness.nextReader(t) == reader || harness.nextSender(t) == sender {
				t.Fatal("Reset reused queues")
			}
			if result := executeRuntimeCommand(harness.requests, runtimeCommand{kind: cmdSetThrottlerInstalled, installed: false}); result.rejected {
				t.Fatalf("bypass = %+v", result)
			}
			select {
			case id := <-deliveredIDs:
				if id != "fresh" {
					t.Fatalf("delivery after Reset = %q", id)
				}
			case <-time.After(time.Second):
				t.Fatal("fresh batch not delivered")
			}
			harness.command(t, cmdPause)
			for len(deliveredIDs) > 0 {
				if id := <-deliveredIDs; id != "fresh" {
					t.Fatalf("old batch survived Reset: %q", id)
				}
			}
		})
	}
}

func TestEventLoopTeardownClosesActualChannelsAndDetachesTelemetry(t *testing.T) {
	for _, afterReset := range []bool{false, true} {
		t.Run(strconv.FormatBool(afterReset), func(t *testing.T) {
			harness := startActualChannelEventLoopForTest(t)
			harness.command(t, cmdRun)
			reader := harness.nextReader(t)
			sender := harness.nextSender(t)
			if afterReset {
				harness.command(t, cmdPause)
				harness.command(t, cmdReset)
			}

			harness.stop()
			assertClosedActualChannel(t, reader)
			assertClosedActualChannel(t, sender)
			if harness.state.telemetry.readerChannel.batches != nil || harness.state.telemetry.senderChannel.batches != nil {
				t.Fatal("event-loop teardown retained telemetry channel attachment")
			}
		})
	}
}

type actualChannelEventLoopHarness struct {
	requests chan runtimeCommand
	metrics  chan time.Time
	state    *controlState
	stop     func()
}

func startActualChannelEventLoopForTest(t *testing.T) actualChannelEventLoopHarness {
	t.Helper()
	requests := make(chan runtimeCommand, 3)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(context.Background(), requests, metrics, NewPrometheusMetrics())
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	}
	t.Cleanup(stop)
	return actualChannelEventLoopHarness{requests: requests, metrics: metrics, state: &state, stop: stop}
}

func (h actualChannelEventLoopHarness) command(t *testing.T, kind runtimeCommandKind) {
	t.Helper()
	if kind == cmdPause {
		h.requests <- runtimeCommand{kind: kind}
		waitForState(t, h.requests, runStatePaused)
		return
	}
	reply := make(chan runtimeCommandReceipt, 1)
	h.requests <- runtimeCommand{kind: kind, receiptReply: reply}
	if result := <-reply; result.rejected || result.err != nil {
		t.Fatalf("command %v = %+v, want accepted", kind, result)
	}
}

func (h actualChannelEventLoopHarness) setCapacity(t *testing.T, kind runtimeCommandKind, capacity int) {
	t.Helper()
	reply := make(chan runtimeCommandReceipt, 1)
	h.requests <- runtimeCommand{kind: kind, value: capacity, receiptReply: reply}
	if result := <-reply; result.rejected {
		t.Fatalf("set capacity command %v = %+v, want accepted", kind, result)
	}
}

func (h actualChannelEventLoopHarness) nextReader(t *testing.T) <-chan []Transaction {
	t.Helper()
	return h.state.telemetry.readerChannel.batches
}

func (h actualChannelEventLoopHarness) nextSender(t *testing.T) <-chan []Transaction {
	t.Helper()
	return h.state.telemetry.senderChannel.batches
}

func assertClosedActualChannel(t *testing.T, batches <-chan []Transaction) {
	t.Helper()
	if _, ok := <-batches; ok {
		t.Fatal("replaced or torn-down actual channel is still open")
	}
}

func waitForReaderReceives(t *testing.T, requests chan<- runtimeCommand, want int64) {
	t.Helper()

	reply := make(chan runtimeStatus, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}:
		case <-deadline:
			t.Fatalf("Reader channel receives did not reach %d", want)
		}
		select {
		case snapshot := <-reply:
			if snapshot.ReaderChannel.ReceivedBatchesTotal == want {
				return
			}
		case <-deadline:
			t.Fatalf("Reader channel receives did not reach %d", want)
		}
	}
}
