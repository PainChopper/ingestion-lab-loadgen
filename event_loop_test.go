package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestRunEventLoopStopsWhenApplicationContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(ctx, testControlPlane(make(chan request)), make(chan time.Time), NewMetrics())
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
	read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(done)
		}()
		return readerRun{done: done, reconcile: func(int) {}}, nil
	}
	start := func(
		ctx context.Context,
		_ <-chan []Transaction,
		_ chan<- []Transaction,
		_ *channelTelemetry,
		_ *channelTelemetry,
		_ throttlerSettings,
	) (<-chan struct{}, chan<- throttlerUpdate) {
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(done)
		}()
		return done, make(chan throttlerUpdate)
	}
	runtime := newPipelineRuntime(&state, read, start)
	if err := runtime.start(applicationContext); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	runtime.startSender()
	firstPool := runtime.pool
	runtime.stopSender()
	if runtime.runContext.Err() != nil {
		t.Fatal("Pause canceled the pipeline run context")
	}

	runtime.startSender()
	if runtime.pool == firstPool {
		t.Fatal("Resume reused the stopped Sender pool")
	}
	stopped := make(chan struct{})
	runtime.pool.attempt = func(ctx context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(stopped)
		<-ctx.Done()
		return senderAttemptCanceled
	}
	runtime.senderBatches <- []Transaction{{ClientID: "application-canceled"}}
	<-stopped
	cancelApplication()
	select {
	case <-runtime.pool.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("application cancellation did not reach resumed Sender pool")
	}
	runtime.stop()
}

func TestPipelineRuntimeStopIsIdempotentAfterActiveAndSoftResetRuns(t *testing.T) {
	state := newTestControlState(t)
	read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(done)
		}()
		return readerRun{done: done, reconcile: func(int) {}}, nil
	}
	runtime := newPipelineRuntime(&state, read, startThrottler)

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("start active runtime: %v", err)
	}
	runtime.startSender()
	runtime.stop()
	runtime.stop()

	if err := runtime.start(context.Background()); err != nil {
		t.Fatalf("start reset runtime: %v", err)
	}
	runtime.resetPaused()
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
	requests := make(chan request)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	state.metricsWindow = 300 * time.Millisecond
	promMetrics := NewMetrics()
	started := make(chan struct{})
	var output chan<- []Transaction
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoop(testControlPlane(requests), metrics, promMetrics, func(ctx context.Context, batches chan<- []Transaction, _, _ int) (readerRun, error) {
			output = batches
			close(started)
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				<-ctx.Done()
			}()
			return readerRun{done: readerDone, reconcile: func(int) {}}, nil
		})
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	reply := make(chan commandResult, 1)
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("run = %+v", result)
	}
	<-started
	output <- []Transaction{{}, {}, {}}
	waitForSenderHandoff(t, requests, 1)
	deadline := time.After(time.Second)
	for state.telemetry.sender.snapshot().completedBatches < 1 {
		select {
		case <-deadline:
			t.Fatal("Sender did not complete the accepted batch")
		default:
		}
	}
	metrics <- time.Now()
	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.Run.TotalTransactions != 3 {
		t.Fatalf("total transactions = %d, want 3", snapshot.Run.TotalTransactions)
	}

	if got := gaugeValue(t, promMetrics.actualTPS); got != 10 {
		t.Fatalf("actual TPS = %v, want 10", got)
	}
	reader := state.telemetry.readerChannel.snapshot(time.Now())
	if reader.receivedTransactionsPerSecond != 10 {
		t.Fatalf("reader channel received TPS = %v, want 10", reader.receivedTransactionsPerSecond)
	}
	sender := state.telemetry.senderChannel.snapshot(time.Now())
	if sender.receivedTransactionsPerSecond != 10 {
		t.Fatalf("sender channel received TPS = %v, want 10", sender.receivedTransactionsPerSecond)
	}

	requests <- request{kind: cmdSetRequestedTPS, value: 2_400_000, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("set requested TPS = %+v", result)
	}
	if got := gaugeValue(t, promMetrics.targetTPS); got != 2_400_000 {
		t.Fatalf("target TPS = %v, want 2400000", got)
	}
}

func TestSenderSnapshotKeepsAppliedControlsAcrossLifecycle(t *testing.T) {
	requests := make(chan request)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoop(testControlPlane(requests), metrics, NewMetrics(), func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
			readerDone := make(chan struct{})
			go func() {
				<-ctx.Done()
				close(readerDone)
			}()
			return readerRun{done: readerDone, reconcile: func(int) {}}, nil
		})
	}()
	t.Cleanup(func() {
		close(requests)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("event loop did not stop")
		}
	})

	reply := make(chan commandResult, 1)
	for _, command := range []request{
		{kind: cmdSetSenderWorkers, value: 3, commandReply: reply},
	} {
		requests <- command
		if result := <-reply; result.status != commandAccepted {
			t.Fatalf("Sender setting = %+v", result)
		}
	}
	snapshotReply := make(chan statusSnapshot, 1)
	assertSender := func(wantState runState, wantLive int) {
		t.Helper()
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		snapshot := <-snapshotReply
		sender := snapshot.Sender
		if snapshot.Run.State != wantState || sender.Workers != 3 || sender.LiveWorkers != wantLive ||
			sender.DrainingWorkers != 0 {
			t.Fatalf("Sender snapshot = %+v in %s", sender, snapshot.Run.State)
		}
	}
	assertSender(runStateIdle, 0)
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Run = %+v", result)
	}
	assertSender(runStateRunning, 3)
	requests <- request{kind: cmdPause}
	assertSender(runStatePaused, 0)
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
	snapshot statusSnapshot,
	readerCapacity int,
	senderCapacity int,
) {
	t.Helper()
	if snapshot.Run.TotalTransactions != 0 || snapshot.Reader.ReadTps != 0 || snapshot.Reader.RowsRead != 0 {
		t.Fatalf("faulted flow = %+v, want zero", snapshot)
	}
	if snapshot.ReaderChannel != (channelSnapshot{Capacity: readerCapacity}) {
		t.Fatalf("faulted Reader channel = %+v, want configured capacity %d and zero flow", snapshot.ReaderChannel, readerCapacity)
	}
	if snapshot.SenderChannel != (channelSnapshot{Capacity: senderCapacity}) {
		t.Fatalf("faulted Sender channel = %+v, want configured capacity %d and zero flow", snapshot.SenderChannel, senderCapacity)
	}
}

func configureFaultedChannelCapacities(
	t *testing.T,
	requests chan<- request,
	reply chan commandResult,
) {
	t.Helper()
	for _, setting := range []struct {
		kind     requestKind
		capacity int
	}{
		{kind: cmdSetReaderChannelCapacity, capacity: 4},
		{kind: cmdSetSenderChannelCapacity, capacity: 8},
	} {
		requests <- request{kind: setting.kind, value: setting.capacity, commandReply: reply}
		if result := <-reply; result.status != commandAccepted {
			t.Fatalf("set capacity %v = %+v", setting.kind, result)
		}
	}
}

func TestRunFailureFaultsAndResetClearsSourceError(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	readerDone := make(chan struct{})
	var starts int
	read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
		starts++
		switch starts {
		case 1:
			return readerRun{}, errors.New("first failure")
		case 2:
			return readerRun{}, errors.New("second failure")
		}
		go func() {
			defer close(readerDone)
			<-ctx.Done()
		}()
		return readerRun{done: readerDone, reconcile: func(int) {}}, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, read)
	reply := make(chan commandResult, 1)
	snapshotReply := make(chan statusSnapshot, 1)
	configureFaultedChannelCapacities(t, requests, reply)

	for _, want := range []string{"first failure", "second failure"} {
		requests <- request{kind: cmdRun, commandReply: reply}
		if result := <-reply; result.err == nil || result.err.Error() != want {
			t.Fatalf("Run error = %v, want %q", result.err, want)
		}
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		snapshot := <-snapshotReply
		if snapshot.Run.State != runStateFaulted || snapshot.Run.ElapsedMs != 0 || snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.Message != want {
			t.Fatalf("failed Run snapshot = %+v, want faulted, zero elapsed, %q", snapshot, want)
		}
		assertFaultedChannelSnapshot(t, snapshot, 4, 8)
		requests <- request{kind: cmdRun, commandReply: reply}
		if result := <-reply; result.status != commandConflict {
			t.Fatalf("Run while faulted = %+v, want conflict", result)
		}
		requests <- request{kind: cmdReset, commandReply: reply}
		if result := <-reply; result.status != commandAccepted {
			t.Fatalf("Reset status = %v, want accepted", result.status)
		}
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		if snapshot := <-snapshotReply; snapshot.Run.State != runStateIdle || snapshot.Reader.SourceError != nil || snapshot.Run.ElapsedMs != 0 {
			t.Fatalf("Reset snapshot = %+v, want idle and cleared error", snapshot)
		}
	}

	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil {
		t.Fatalf("retry Run error = %v, want nil", result.err)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.Run.State != runStateRunning || snapshot.Reader.SourceError != nil {
		t.Fatalf("successful Run snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil || starts != 3 {
		t.Fatalf("repeated Run = %+v, reader starts = %d, want nil error and 3 starts", result, starts)
	}
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.Run.State != runStatePaused || snapshot.Run.ElapsedMs < 0 {
		t.Fatalf("Pause snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("paused Reset status = %v, want accepted", result.status)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.Run.State != runStateIdle || snapshot.Run.ElapsedMs != 0 {
		t.Fatalf("paused Reset snapshot = %+v", snapshot)
	}
}

func TestRuntimeSourceErrorResetClearsFaultedSnapshot(t *testing.T) {
	requests := make(chan request)
	metrics := make(chan time.Time)
	sourceErrors := make(chan readerSourceError, 1)
	var starts int
	read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
		starts++
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(done)
		}()
		return readerRun{done: done, reconcile: func(int) {}, sourceErrors: sourceErrors}, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, read)
	reply := make(chan commandResult, 1)
	configureFaultedChannelCapacities(t, requests, reply)
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Run = %+v", result)
	}
	sourceErrors <- readerSourceError{Category: "source", Operation: "read", RelativePath: "broken.parquet", Message: "corrupt"}

	var faulted statusSnapshot
	deadline := time.After(time.Second)
	for {
		snapshotReply := make(chan statusSnapshot, 1)
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		faulted = <-snapshotReply
		if faulted.Run.State == runStateFaulted {
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("runtime source error did not fault the run")
		}
	}
	if faulted.Reader.SourceError == nil || faulted.Reader.SourceError.Message != "corrupt" {
		t.Fatalf("faulted snapshot source error = %+v", faulted.Reader.SourceError)
	}
	assertFaultedChannelSnapshot(t, faulted, 4, 8)

	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Reset = %+v", result)
	}
	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	reset := <-snapshotReply
	if reset.Run.State != runStateIdle || reset.Reader.SourceError != nil {
		t.Fatalf("Reset snapshot = %+v, want clean idle", reset)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted || starts != 2 {
		t.Fatalf("Run after Reset = %+v, starts=%d", result, starts)
	}
}

func TestRunEventLoopFaultsOnCorruptParquetAndPreservesWorkerDiagnostic(t *testing.T) {
	fixtureDirectory := t.TempDir()
	fixturePath := filepath.Join(fixtureDirectory, "broken.parquet")
	if err := os.WriteFile(fixturePath, []byte("not a parquet file"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := newTestControlState(t)
	state.controls.policy.Source.Path = filepath.Join(fixtureDirectory, "*.parquet")
	requests := make(chan request)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(ctx, testControlPlane(requests), make(chan time.Time), NewMetrics())
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	reply := make(chan commandResult, 1)
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Run = %+v", result)
	}
	var snapshot statusSnapshot
	deadline := time.After(time.Second)
	for snapshot.Run.State != runStateFaulted {
		snapshotReply := make(chan statusSnapshot, 1)
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		snapshot = <-snapshotReply
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
	assertFaultedChannelSnapshot(t, snapshot, state.readerChannelCapacity(), state.senderChannelCapacity())
}

func TestRunEventLoopFaultsBeforeWorkersForUnavailableSourceDirectory(t *testing.T) {
	fixtureRoot := t.TempDir()
	missingDirectory := filepath.Join(fixtureRoot, "unavailable")
	state := newTestControlState(t)
	state.controls.policy.Source.Path = filepath.Join(missingDirectory, "*.parquet")
	requests := make(chan request)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.runEventLoop(ctx, testControlPlane(requests), make(chan time.Time), NewMetrics())
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	reply := make(chan commandResult, 1)
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err == nil {
		t.Fatal("Run error = nil, want unavailable directory error")
	}
	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot := <-snapshotReply
	if snapshot.Run.State != runStateFaulted || snapshot.Reader.SourceDirectory != filepath.ToSlash(missingDirectory) || snapshot.Reader.SourceError == nil || snapshot.Reader.SourceError.Operation != "glob" || snapshot.Reader.SourceError.RelativePath != "*.parquet" || snapshot.Reader.SourceError.Message != "no files found matching pattern" {
		t.Fatalf("unavailable source snapshot = %+v", snapshot)
	}
	if snapshot.Reader.LiveWorkers != 0 || snapshot.Sender.LiveWorkers != 0 {
		t.Fatalf("startup failure workers = reader %+v sender %+v", snapshot.Reader, snapshot.Sender)
	}
	assertFaultedChannelSnapshot(t, snapshot, state.readerChannelCapacity(), state.senderChannelCapacity())
}

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	var starts int
	onReaderStart := func() {
		starts++
	}
	requests, _, _ := startEventLoopForTest(t, onReaderStart)

	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	runningSnapshot := <-reply
	if runningSnapshot.Run.State != runStateRunning {
		t.Fatalf("state after first Run = %v, want %v", runningSnapshot.Run.State, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after first Run = %v, want 1", starts)
	}

	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	repeatedRunSnapshot := <-reply
	if repeatedRunSnapshot.Run.State != runStateRunning {
		t.Fatalf("state after second Run = %v, want %v", repeatedRunSnapshot.Run.State, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after repeated Run = %v, want 1", starts)
	}
}

func TestPauseStopsConsumptionUntilRun(t *testing.T) {
	var starts int
	requests, batches, metrics := startEventLoopForTest(t, func() { starts++ })

	requests <- request{kind: cmdRun}
	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("first batch was not received after Run")
	}
	waitForTransactions(t, requests, metrics, 1)

	// The snapshot reply confirms that Pause was handled before the next batch.
	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	select {
	case <-reply:
	case <-time.After(time.Second):
		t.Fatal("Pause was not handled")
	}

	secondBatch := []Transaction{{}}
	select {
	case batches <- secondBatch:
	case <-time.After(time.Second):
		t.Fatal("Throttler did not accept the second batch during Pause")
	}
	waitForReaderReceives(t, requests, 2)
	metrics <- time.Now()
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	if snapshot := <-reply; snapshot.Run.TotalTransactions != 1 || snapshot.Run.State != runStatePaused ||
		snapshot.SenderChannel.ReceivedBatchesTotal != 1 || snapshot.SenderChannel.ReceivedTransactionsTotal != 1 {
		t.Fatalf("paused snapshot = %+v, want one consumed transaction", snapshot)
	}

	requests <- request{kind: cmdRun}
	waitForTransactions(t, requests, metrics, 2)
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	if snapshot := <-reply; snapshot.SenderChannel.ReceivedBatchesTotal != 2 ||
		snapshot.SenderChannel.ReceivedTransactionsTotal != 2 {
		t.Fatalf("resumed snapshot = %+v, want two received batches and transactions", snapshot)
	}
	if starts != 1 {
		t.Fatalf("reader starts = %v, want 1", starts)
	}
}

func TestResetFromPausedStopsReaderClearsProgressAndStartsFreshRun(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	firstReaderReady := make(chan struct{})
	allowFirstSecondBatch := make(chan struct{})
	firstReaderDone := make(chan struct{})
	allowFirstReaderDone := make(chan struct{})
	var freshBatches chan<- []Transaction
	freshReaderDone := make(chan struct{})
	oldAccepted := make(chan struct{})
	firstBatchBuffered := make(chan struct{})
	throttlerCancelObserved := make(chan struct{})
	allowFirstThrottlerDone := make(chan struct{})
	capturedIDs := make(chan string, 2)
	var starts int
	var throttlerStarts int

	read := func(ctx context.Context, output chan<- []Transaction, _, _ int) (readerRun, error) {
		starts++
		if starts == 1 {
			go func() {
				defer func() {
					<-allowFirstReaderDone
					close(firstReaderDone)
				}()
				select {
				case <-firstReaderReady:
				case <-ctx.Done():
					return
				}
				select {
				case output <- []Transaction{{ClientID: "old"}}:
				case <-ctx.Done():
					return
				}
				select {
				case <-allowFirstSecondBatch:
				case <-ctx.Done():
					return
				}
				select {
				case output <- []Transaction{{ClientID: "old"}}:
				case <-ctx.Done():
				}
			}()
			return readerRun{done: firstReaderDone, reconcile: func(int) {}}, nil
		}

		freshBatches = output
		go func() {
			defer close(freshReaderDone)
			<-ctx.Done()
		}()
		return readerRun{done: freshReaderDone, reconcile: func(int) {}}, nil
	}

	start := func(
		ctx context.Context,
		batches <-chan []Transaction,
		output chan<- []Transaction,
		_ *channelTelemetry,
		_ *channelTelemetry,
		_ throttlerSettings,
	) (<-chan struct{}, chan<- throttlerUpdate) {
		throttlerStarts++
		first := throttlerStarts == 1
		done := make(chan struct{})
		updates := make(chan throttlerUpdate)
		go func() {
			defer close(done)
			var pending []Transaction
			delivered := 0
			for {
				if pending == nil {
					select {
					case update := <-updates:
						close(update.acknowledged)
					case batch := <-batches:
						pending = batch
						if first && delivered == 1 {
							close(firstBatchBuffered)
						}
					case <-ctx.Done():
						if first {
							close(throttlerCancelObserved)
							<-allowFirstThrottlerDone
						}
						return
					}
					continue
				}
				select {
				case update := <-updates:
					close(update.acknowledged)
				case output <- pending:
					capturedIDs <- pending[0].ClientID
					delivered++
					if first {
						select {
						case <-oldAccepted:
						default:
							close(oldAccepted)
						}
					}
					pending = nil
				case <-ctx.Done():
					if first {
						close(throttlerCancelObserved)
						<-allowFirstThrottlerDone
					}
					return
				}
			}
		}()
		return done, updates
	}

	startCustomEventLoopForTestWithThrottler(t, requests, metrics, read, start)
	requests <- request{kind: cmdRun}
	close(firstReaderReady)
	select {
	case <-oldAccepted:
	case <-time.After(time.Second):
		t.Fatal("old reader batch was not consumed")
	}
	select {
	case id := <-capturedIDs:
		if id != "old" {
			t.Fatalf("captured old ClientID = %q, want old", id)
		}
	case <-time.After(time.Second):
		t.Fatal("old ClientID was not handed to Sender")
	}
	waitForTransactions(t, requests, metrics, 1)

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	close(allowFirstSecondBatch)
	select {
	case <-firstBatchBuffered:
	case <-time.After(time.Second):
		t.Fatal("reader did not fill its readerChannel after Pause")
	}

	resetReply := make(chan commandResult, 1)
	requests <- request{kind: cmdReset, commandReply: resetReply}
	select {
	case <-throttlerCancelObserved:
	case <-time.After(time.Second):
		t.Fatal("Throttler cancel was not observed")
	}
	select {
	case result := <-resetReply:
		t.Fatalf("Reset completed before throttlerDone: %+v", result)
	default:
	}
	close(allowFirstReaderDone)
	select {
	case <-firstReaderDone:
	case <-time.After(time.Second):
		t.Fatal("readerDone did not close")
	}
	select {
	case result := <-resetReply:
		t.Fatalf("Reset completed before throttlerDone gate: %+v", result)
	default:
	}
	close(allowFirstThrottlerDone)
	select {
	case result := <-resetReply:
		if result.status != commandAccepted {
			t.Fatalf("reset status = %v, want %v", result.status, commandAccepted)
		}
	case <-time.After(time.Second):
		t.Fatal("Reset did not complete")
	}
	select {
	case <-firstReaderDone:
	default:
		t.Fatal("readerDone is not closed after Reset")
	}
	waitForState(t, requests, runStateIdle)
	waitForTransactions(t, requests, metrics, 0)
	requests <- request{kind: cmdReset, commandReply: resetReply}
	select {
	case result := <-resetReply:
		if result.status != commandAccepted {
			t.Fatalf("second reset status = %v, want %v", result.status, commandAccepted)
		}
	case <-time.After(time.Second):
		t.Fatal("second Reset did not complete")
	}
	waitForState(t, requests, runStateIdle)

	requests <- request{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	if starts != 2 {
		t.Fatalf("reader starts after Reset and Run = %v, want 2", starts)
	}
	select {
	case freshBatches <- []Transaction{{ClientID: "fresh"}}:
	case <-time.After(time.Second):
		t.Fatal("fresh reader batch was not consumed")
	}
	waitForTransactions(t, requests, metrics, 1)
	select {
	case id := <-capturedIDs:
		if id != "fresh" {
			t.Fatalf("captured ClientID = %q, want fresh", id)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh ClientID was not handed to Sender")
	}
	select {
	case id := <-capturedIDs:
		t.Fatalf("unexpected second captured ClientID = %q", id)
	default:
	}
}

func TestResetDuringRunReturnsConflictAndPreservesPipeline(t *testing.T) {
	var starts int
	requests, batches, metrics := startEventLoopForTest(t, func() { starts++ })
	handler := commandsHandler(testControlPlane(requests), testPolicy(t))
	runRequest := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"run"}`))
	handler.ServeHTTP(httptest.NewRecorder(), runRequest)
	waitForState(t, requests, runStateRunning)

	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("first batch was not consumed")
	}
	waitForTransactions(t, requests, metrics, 1)

	resetRecorder := httptest.NewRecorder()
	resetRequest := httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(`{"action":"reset"}`))
	handler.ServeHTTP(resetRecorder, resetRequest)
	if resetRecorder.Code != http.StatusConflict {
		t.Fatalf("Reset status code = %v, want %v", resetRecorder.Code, http.StatusConflict)
	}
	waitForState(t, requests, runStateRunning)
	waitForTransactions(t, requests, metrics, 1)
	if starts != 1 {
		t.Fatalf("reader starts after rejected Reset = %v, want 1", starts)
	}

	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("Sender stopped after rejected Reset")
	}
	waitForTransactions(t, requests, metrics, 2)
}

func TestReaderMeasurementsSurvivePauseAndClearOnReset(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	readerDone := make(chan struct{})
	state := newTestControlState(t)
	read := func(ctx context.Context, _ chan<- []Transaction, _, _ int) (readerRun, error) {
		go func() {
			defer close(readerDone)
			<-ctx.Done()
		}()
		return readerRun{done: readerDone, reconcile: func(int) {}}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(
			testControlPlane(requests),
			metrics,
			NewMetrics(),
			read,
			startThrottler,
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

	requests <- request{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	readerChannel := make(chan []Transaction, state.controls.policy.ReaderChannel.Capacity.Default)
	state.telemetry.readerChannel.start(readerChannel, 2)
	if !state.telemetry.readerChannel.send(context.Background(), readerChannel, make([]Transaction, 2)) {
		t.Fatal("readerChannel send failed")
	}
	state.telemetry.reader.recordRead(2)
	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	state.telemetry.reader.recordRead(3)
	metrics <- time.Now()

	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot := <-snapshotReply
	if snapshot.Reader.RowsRead != 5 || snapshot.ReaderChannel.Capacity != 2 ||
		snapshot.ReaderChannel.DepthBatches != 1 || snapshot.ReaderChannel.BufferedTransactions != 2 ||
		snapshot.ReaderChannel.SentBatchesTotal != 1 || snapshot.ReaderChannel.SentTransactionsTotal != 2 ||
		snapshot.ReaderChannel.SentBatchesPerSecond != 1 || snapshot.ReaderChannel.SentTransactionsPerSecond != 2 {
		t.Fatalf("paused snapshot = %+v, want reader and readerChannel measurements", snapshot)
	}
	state.telemetry.readerChannel.recordReceive(len(<-readerChannel))
	metrics <- time.Now()
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot = <-snapshotReply
	if snapshot.ReaderChannel.ReceivedBatchesTotal != 1 || snapshot.ReaderChannel.ReceivedTransactionsTotal != 2 ||
		snapshot.ReaderChannel.SentBatchesPerSecond != 0 || snapshot.ReaderChannel.SentTransactionsPerSecond != 0 ||
		snapshot.ReaderChannel.ReceivedBatchesPerSecond != 1 || snapshot.ReaderChannel.ReceivedTransactionsPerSecond != 2 {
		t.Fatalf("drained readerChannel snapshot = %+v", snapshot)
	}

	requests <- request{kind: cmdRun}
	waitForState(t, requests, runStateRunning)
	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	resetReply := make(chan commandResult, 1)
	requests <- request{kind: cmdReset, commandReply: resetReply}
	if result := <-resetReply; result.status != commandAccepted {
		t.Fatalf("Reset status = %v, want accepted", result.status)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot = <-snapshotReply
	if snapshot.Reader.ReadTps != 0 || snapshot.Reader.RowsRead != 0 ||
		snapshot.ReaderChannel.Capacity != state.controls.policy.ReaderChannel.Capacity.Default || snapshot.ReaderChannel.DepthBatches != 0 ||
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
	requests, batches, metrics := startEventLoopForTest(t, func() {})
	commands := commandsHandler(testControlPlane(requests), testPolicy(t))
	post := func(body string, want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		commands.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, commandsPath, strings.NewReader(body)))
		if recorder.Code != want {
			t.Fatalf("POST %s = %d, want %d", body, recorder.Code, want)
		}
	}
	snapshot := func() statusSnapshot {
		t.Helper()
		reply := make(chan statusSnapshot, 1)
		requests <- request{kind: getSnapshot, snapshotReply: reply}
		return <-reply
	}
	if got := snapshot(); got.Reader.ReadBatchSize != 1_000 || got.Throttler.RequestedTps != 2_000_000 ||
		got.Throttler.InstallationMode != throttlerInstalled {
		t.Fatalf("initial throttler snapshot = %+v", got)
	}
	post(`{"action":"set-requested-tps","value":0}`, http.StatusOK)
	post(`{"action":"run"}`, http.StatusOK)
	select {
	case batches <- []Transaction{{ClientID: "held"}}:
	case <-time.After(time.Second):
		t.Fatal("zero-TPS stage did not receive batch")
	}
	waitForReaderReceives(t, requests, 1)
	metrics <- time.Now()
	if got := snapshot(); got.Run.TotalTransactions != 0 || got.Throttler.RequestedTps != 0 {
		t.Fatalf("zero-TPS running snapshot = %+v", got)
	}
	post(`{"action":"set-throttler-installation-mode","value":"bypass"}`, http.StatusOK)
	waitForTransactions(t, requests, metrics, 1)
	if got := snapshot(); got.Throttler.RequestedTps != 0 || got.Throttler.InstallationMode != throttlerBypass {
		t.Fatalf("bypass snapshot = %+v", got)
	}
	post(`{"action":"pause"}`, http.StatusOK)
	waitForState(t, requests, runStatePaused)
	post(`{"action":"set-requested-tps","value":4000000}`, http.StatusOK)
	post(`{"action":"set-throttler-installation-mode","value":"installed"}`, http.StatusOK)
	select {
	case batches <- []Transaction{{ClientID: "paused"}}:
	case <-time.After(time.Second):
		t.Fatal("paused stage did not receive batch")
	}
	waitForReaderReceives(t, requests, 2)
	metrics <- time.Now()
	if got := snapshot(); got.Run.TotalTransactions != 1 || got.Throttler.RequestedTps != 4_000_000 ||
		got.Throttler.InstallationMode != throttlerInstalled {
		t.Fatalf("paused throttler snapshot = %+v", got)
	}
	post(`{"action":"run"}`, http.StatusOK)
	waitForTransactions(t, requests, metrics, 2)
	post(`{"action":"set-requested-tps","value":400000}`, http.StatusOK)
	if got := snapshot().Throttler.RequestedTps; got != 400_000 {
		t.Fatalf("running TPS = %d, want 100", got)
	}
	post(`{"action":"pause"}`, http.StatusOK)
	waitForState(t, requests, runStatePaused)
	post(`{"action":"reset"}`, http.StatusOK)
	if got := snapshot(); got.Run.State != runStateIdle || got.Run.TotalTransactions != 0 ||
		got.Throttler.RequestedTps != 400_000 || got.Throttler.InstallationMode != throttlerInstalled {
		t.Fatalf("reset throttler snapshot = %+v", got)
	}
}

func TestResetWhileZeroTPSHoldsBatchCompletes(t *testing.T) {
	requests, batches, _ := startEventLoopForTest(t, func() {})
	reply := make(chan commandResult, 1)
	requests <- request{kind: cmdSetRequestedTPS, value: 0, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("set zero TPS status = %v", result.status)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Run status = %v", result.status)
	}
	select {
	case batches <- []Transaction{{ClientID: "held"}}:
	case <-time.After(time.Second):
		t.Fatal("zero-TPS stage did not receive batch")
	}
	waitForReaderReceives(t, requests, 1)
	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	requests <- request{kind: cmdReset, commandReply: reply}
	select {
	case result := <-reply:
		if result.status != commandAccepted {
			t.Fatalf("Reset status = %v", result.status)
		}
	case <-time.After(time.Second):
		t.Fatal("Reset blocked while zero TPS held a batch")
	}
	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if got := <-snapshotReply; got.Run.State != runStateIdle || got.Run.TotalTransactions != 0 || got.Throttler.RequestedTps != 0 {
		t.Fatalf("snapshot after zero-TPS Reset = %+v", got)
	}
}

func TestSenderChannelTelemetryFollowsWindowPauseRunAndReset(t *testing.T) {
	requests, batches, metrics := startEventLoopForTest(t, func() {})
	snapshotReply := make(chan statusSnapshot, 1)
	snapshot := func() statusSnapshot {
		t.Helper()
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		return <-snapshotReply
	}
	initial := snapshot()
	if initial.SenderChannel.Capacity != 0 || initial.SenderChannel.SentBatchesTotal != 0 ||
		initial.Throttler.AdmittedTps != 0 {
		t.Fatalf("initial Sender channel = %+v", initial)
	}

	reply := make(chan commandResult, 1)
	requests <- request{kind: cmdSetThrottlerInstallationMode, textValue: throttlerBypass, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("set bypass = %+v", result)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Run = %+v", result)
	}
	if first := snapshot(); first.Throttler.AdmittedTps != 0 || first.SenderChannel.SentTransactionsPerSecond != 0 {
		t.Fatalf("pre-window Sender rate = %+v", first)
	}
	batches <- make([]Transaction, 2)
	waitForSenderHandoff(t, requests, 1)
	metrics <- time.Now()
	active := snapshot()
	if active.SenderChannel.Capacity != 0 || active.SenderChannel.DepthBatches != 0 ||
		active.SenderChannel.BufferedTransactions != 0 || active.SenderChannel.SentBatchesTotal != 1 ||
		active.SenderChannel.SentTransactionsTotal != 2 || active.SenderChannel.ReceivedBatchesTotal != 1 ||
		active.SenderChannel.ReceivedTransactionsTotal != 2 || active.SenderChannel.SentBatchesPerSecond != 1 ||
		active.SenderChannel.SentTransactionsPerSecond != 2 || active.SenderChannel.ReceivedBatchesPerSecond != 1 ||
		active.SenderChannel.ReceivedTransactionsPerSecond != 2 || active.Throttler.AdmittedTps != active.SenderChannel.SentTransactionsPerSecond {
		t.Fatalf("active Sender channel = %+v", active)
	}

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	batches <- []Transaction{{}}
	waitForReaderReceives(t, requests, 2)
	metrics <- time.Now()
	paused := snapshot()
	if paused.SenderChannel.SentBatchesTotal != 1 || paused.SenderChannel.ReceivedBatchesTotal != 1 ||
		paused.SenderChannel.BlockedSenders != 0 || paused.SenderChannel.DepthBatches != 0 ||
		paused.Throttler.AdmittedTps != 0 || paused.SenderChannel.SentTransactionsPerSecond != 0 ||
		paused.SenderChannel.ReceivedTransactionsPerSecond != 0 {
		t.Fatalf("paused Sender channel = %+v", paused)
	}

	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("resumed Run = %+v", result)
	}
	waitForSenderHandoff(t, requests, 2)
	metrics <- time.Now()
	resumed := snapshot()
	if resumed.SenderChannel.SentBatchesTotal != 2 || resumed.SenderChannel.ReceivedBatchesTotal != 2 ||
		resumed.Throttler.AdmittedTps != 1 || resumed.SenderChannel.SentTransactionsPerSecond != 1 {
		t.Fatalf("resumed Sender channel = %+v", resumed)
	}

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Reset = %+v", result)
	}
	reset := snapshot()
	if reset.Run.State != runStateIdle || reset.SenderChannel.Capacity != 0 ||
		reset.SenderChannel.DepthBatches != 0 || reset.SenderChannel.BufferedTransactions != 0 ||
		reset.SenderChannel.BlockedSenders != 0 || reset.SenderChannel.OldestBlockedSenderMs != 0 ||
		reset.SenderChannel.BlockedMs != 0 || reset.SenderChannel.SentBatchesTotal != 0 ||
		reset.SenderChannel.SentTransactionsTotal != 0 || reset.SenderChannel.ReceivedBatchesTotal != 0 ||
		reset.SenderChannel.ReceivedTransactionsTotal != 0 || reset.SenderChannel.SentBatchesPerSecond != 0 ||
		reset.SenderChannel.SentTransactionsPerSecond != 0 || reset.SenderChannel.ReceivedBatchesPerSecond != 0 ||
		reset.SenderChannel.ReceivedTransactionsPerSecond != 0 || reset.Throttler.AdmittedTps != 0 {
		t.Fatalf("Sender channel after Reset = %+v", reset)
	}
}

func waitForSenderHandoff(t *testing.T, requests chan<- request, want int64) {
	t.Helper()
	reply := make(chan statusSnapshot, 1)
	deadline := time.After(time.Second)
	for {
		requests <- request{kind: getSnapshot, snapshotReply: reply}
		got := <-reply
		if got.SenderChannel.SentBatchesTotal == want && got.SenderChannel.ReceivedBatchesTotal == want {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("Sender handoff did not reach %d: %+v", want, got)
		}
	}
}

func startEventLoopForTest(t *testing.T, onReaderStart func()) (chan request, chan<- []Transaction, chan<- time.Time) {
	t.Helper()

	requests := make(chan request, 3)
	batches := make(chan []Transaction)
	metrics := make(chan time.Time)
	read := func(ctx context.Context, output chan<- []Transaction, _, _ int) (readerRun, error) {
		onReaderStart()
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			for {
				select {
				case <-ctx.Done():
					return
				case batch := <-batches:
					select {
					case output <- batch:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return readerRun{done: readerDone, reconcile: func(int) {}}, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, read)

	return requests, batches, metrics
}

func TestReaderWorkersIdleUpdateAfterResetDoesNotReconcileStoppedPool(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	staleUpdates := make(chan int, 1)
	startedWorkers := make(chan int, 2)
	read := func(ctx context.Context, _ chan<- []Transaction, _, workers int) (readerRun, error) {
		startedWorkers <- workers
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			<-ctx.Done()
		}()
		return readerRun{done: readerDone, reconcile: func(value int) { staleUpdates <- value }}, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, read)
	command := func(kind requestKind, value int) {
		t.Helper()
		reply := make(chan commandResult, 1)
		requests <- request{kind: kind, value: value, commandReply: reply}
		if result := <-reply; result.status != commandAccepted || result.err != nil {
			t.Fatalf("command %d = %+v", kind, result)
		}
	}
	command(cmdRun, 0)
	if got := <-startedWorkers; got != 1 {
		t.Fatalf("initial workers = %d, want 1", got)
	}
	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	command(cmdReset, 0)
	waitForState(t, requests, runStateIdle)
	command(cmdSetReaderWorkers, 7)
	if len(staleUpdates) != 0 {
		t.Fatalf("stopped Reader pool reconciled %d times", len(staleUpdates))
	}
	command(cmdRun, 0)
	if got := <-startedWorkers; got != 7 {
		t.Fatalf("fresh run workers = %d, want 7", got)
	}
}

func startCustomEventLoopForTest(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	read readerStarter,
) {
	startCustomEventLoopForTestWithThrottler(t, requests, metrics, read, startThrottler)
}

func startCustomEventLoopForTestWithThrottler(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	read readerStarter,
	start throttlerStarter,
) {
	t.Helper()

	done := make(chan struct{})
	state := newTestControlState(t)
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(
			testControlPlane(requests),
			metrics,
			NewMetrics(),
			read,
			start,
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
}

func waitForState(t *testing.T, requests chan<- request, want runState) {
	t.Helper()

	reply := make(chan statusSnapshot, 1)
	select {
	case requests <- request{kind: getSnapshot, snapshotReply: reply}:
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

func TestEventLoopRetainsActualChannelsAcrossPauseResumeAndSameCapacityReset(t *testing.T) {
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
			harness.assertNoReplacement(t)
			if harness.state.telemetry.readerChannel.batches != reader || harness.state.telemetry.senderChannel.batches != sender {
				t.Fatal("Pause/Resume replaced an actual event-loop channel")
			}

			if capacity > 0 {
				harness.send(t, []Transaction{{ClientID: "old"}})
			}
			harness.command(t, cmdPause)
			harness.command(t, cmdReset)
			if harness.state.telemetry.readerChannel.batches != reader || harness.state.telemetry.senderChannel.batches != sender {
				t.Fatal("same-capacity Reset replaced an actual event-loop channel")
			}
			if cap(reader) != capacity || cap(sender) != capacity || len(reader) != 0 || len(sender) != 0 {
				t.Fatalf("channels after Reset = Reader(cap=%d len=%d), Sender(cap=%d len=%d)", cap(reader), len(reader), cap(sender), len(sender))
			}

			harness.command(t, cmdRun)
			if next := harness.nextReader(t); next != reader {
				t.Fatal("same-capacity Reset did not retain actual Reader channel")
			}
			if next := harness.nextSender(t); next != sender {
				t.Fatal("same-capacity Reset did not retain actual Sender channel")
			}
			harness.send(t, []Transaction{{ClientID: "fresh"}})
			waitForTransactions(t, harness.requests, harness.metrics, 1)
		})
	}
}

func TestEventLoopSenderChannelTelemetryUsesAppliedReadBatchSizeAfterReuse(t *testing.T) {
	harness := startActualChannelEventLoopWithHeldThrottlerForTest(t)
	defer harness.stop()

	const initialBatchSize = 1_000
	const updatedBatchSize = 2_000
	snapshotReply := make(chan statusSnapshot, 1)
	snapshot := func() statusSnapshot {
		t.Helper()
		harness.requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		return <-snapshotReply
	}

	harness.setCapacity(t, cmdSetSenderChannelCapacity, 1)
	harness.command(t, cmdRun)
	harness.nextReader(t)
	sender := harness.nextSender(t)
	harness.command(t, cmdPause)
	close(harness.allowThrottlerForward)
	harness.send(t, []Transaction{{ClientID: "initial"}})
	<-harness.forwardedBatches
	if got := snapshot(); got.SenderChannel.DepthBatches != 1 ||
		got.SenderChannel.BufferedTransactions != initialBatchSize {
		t.Fatalf("initial Sender channel telemetry = %+v, want depth 1 and %d buffered transactions", got, initialBatchSize)
	}

	harness.command(t, cmdReset)
	reply := make(chan commandResult, 1)
	harness.requests <- request{kind: cmdSetReadBatchSize, value: updatedBatchSize, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("set read batch size = %+v, want accepted", result)
	}

	harness.command(t, cmdRun)
	harness.nextReader(t)
	if next := harness.nextSender(t); next != sender {
		t.Fatal("same-capacity Reset did not retain actual Sender channel")
	}
	harness.command(t, cmdPause)
	harness.send(t, []Transaction{{ClientID: "updated"}})
	<-harness.forwardedBatches
	if got := snapshot(); got.SenderChannel.DepthBatches != 1 ||
		got.SenderChannel.BufferedTransactions != updatedBatchSize {
		t.Fatalf("reused Sender channel telemetry = %+v, want depth 1 and %d buffered transactions", got, updatedBatchSize)
	}
}

func TestEventLoopSameCapacityResetDrainsRetainedActualReaderBatch(t *testing.T) {
	for _, capacity := range []int{1, 8_192} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			harness := startActualChannelEventLoopWithHeldThrottlerForTest(t)
			defer harness.stop()

			harness.setCapacity(t, cmdSetReaderChannelCapacity, capacity)
			harness.setCapacity(t, cmdSetSenderChannelCapacity, capacity)
			harness.command(t, cmdRun)
			reader := harness.nextReader(t)
			sender := harness.nextSender(t)

			harness.send(t, []Transaction{{ClientID: "old"}})
			if len(reader) != 1 {
				t.Fatalf("actual Reader channel retained %d batches, want 1 old batch", len(reader))
			}
			if len(sender) != 0 {
				t.Fatalf("actual Sender channel retained %d batches before Reset, want 0", len(sender))
			}

			harness.command(t, cmdPause)
			harness.command(t, cmdReset)
			if harness.state.telemetry.readerChannel.batches != reader || harness.state.telemetry.senderChannel.batches != sender {
				t.Fatal("same-capacity Reset replaced an actual event-loop channel")
			}
			if len(reader) != 0 || len(sender) != 0 {
				t.Fatalf("actual queues after Reset = Reader %d, Sender %d, want empty", len(reader), len(sender))
			}

			harness.command(t, cmdRun)
			if next := harness.nextReader(t); next != reader {
				t.Fatal("same-capacity Reset did not retain actual Reader channel")
			}
			if next := harness.nextSender(t); next != sender {
				t.Fatal("same-capacity Reset did not retain actual Sender channel")
			}
			harness.send(t, []Transaction{{ClientID: "fresh"}})
			close(harness.allowThrottlerForward)

			select {
			case batch := <-harness.forwardedBatches:
				if len(batch) != 1 || batch[0].ClientID != "fresh" {
					t.Fatalf("batch forwarded after Reset = %+v, want fresh only", batch)
				}
			case <-time.After(time.Second):
				t.Fatal("fresh batch was not forwarded after Reset")
			}
			select {
			case batch := <-harness.forwardedBatches:
				t.Fatalf("unexpected extra forwarded batch after Reset: %+v", batch)
			default:
			}
		})
	}
}

func TestEventLoopSelectivelyReplacesActualChannelsAfterSoftReset(t *testing.T) {
	harness := startActualChannelEventLoopForTest(t)
	defer harness.stop()

	harness.setCapacity(t, cmdSetReaderChannelCapacity, 1)
	harness.setCapacity(t, cmdSetSenderChannelCapacity, 1)
	harness.command(t, cmdRun)
	reader := harness.nextReader(t)
	sender := harness.nextSender(t)

	harness.command(t, cmdPause)
	harness.command(t, cmdReset)
	harness.setCapacity(t, cmdSetReaderChannelCapacity, 8_192)
	if harness.state.telemetry.readerChannel.batches != reader || harness.state.telemetry.senderChannel.batches != sender {
		t.Fatal("idle Reader capacity setter replaced an actual channel")
	}
	harness.command(t, cmdRun)
	replacedReader := harness.nextReader(t)
	if replacedReader == reader || cap(replacedReader) != 8_192 {
		t.Fatalf("Reader replacement = %p (capacity %d), want new channel with capacity 8192", replacedReader, cap(replacedReader))
	}
	if retainedSender := harness.nextSender(t); retainedSender != sender {
		t.Fatal("Reader-only replacement changed Sender channel")
	}
	assertClosedActualChannel(t, reader)

	harness.command(t, cmdPause)
	harness.command(t, cmdReset)
	harness.setCapacity(t, cmdSetSenderChannelCapacity, 0)
	harness.command(t, cmdRun)
	if retainedReader := harness.nextReader(t); retainedReader != replacedReader {
		t.Fatal("Sender-only replacement changed Reader channel")
	}
	replacedSender := harness.nextSender(t)
	if replacedSender == sender || cap(replacedSender) != 0 {
		t.Fatalf("Sender replacement = %p (capacity %d), want new channel with capacity 0", replacedSender, cap(replacedSender))
	}
	assertClosedActualChannel(t, sender)

	harness.command(t, cmdPause)
	harness.command(t, cmdReset)
	harness.setCapacity(t, cmdSetReaderChannelCapacity, 0)
	harness.setCapacity(t, cmdSetSenderChannelCapacity, 8_192)
	harness.command(t, cmdRun)
	if next := harness.nextReader(t); next == replacedReader || cap(next) != 0 {
		t.Fatal("both-capacity replacement did not replace Reader channel")
	}
	if next := harness.nextSender(t); next == replacedSender || cap(next) != 8_192 {
		t.Fatal("both-capacity replacement did not replace Sender channel")
	}
	assertClosedActualChannel(t, replacedReader)
	assertClosedActualChannel(t, replacedSender)
}

func TestEventLoopTeardownClosesActualChannelsAndDetachesTelemetry(t *testing.T) {
	for _, afterSoftReset := range []bool{false, true} {
		t.Run(strconv.FormatBool(afterSoftReset), func(t *testing.T) {
			harness := startActualChannelEventLoopForTest(t)
			harness.command(t, cmdRun)
			reader := harness.nextReader(t)
			sender := harness.nextSender(t)
			if afterSoftReset {
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
	requests              chan request
	metrics               chan time.Time
	state                 *controlState
	readerBatches         chan []Transaction
	readerHandoffs        chan struct{}
	readerStarts          chan struct{}
	senderStarts          chan struct{}
	allowThrottlerForward chan struct{}
	forwardedBatches      chan []Transaction
	stop                  func()
}

func startActualChannelEventLoopForTest(t *testing.T) actualChannelEventLoopHarness {
	return startActualChannelEventLoopForTestWithHeldThrottler(t, false)
}

func startActualChannelEventLoopWithHeldThrottlerForTest(t *testing.T) actualChannelEventLoopHarness {
	return startActualChannelEventLoopForTestWithHeldThrottler(t, true)
}

func startActualChannelEventLoopForTestWithHeldThrottler(t *testing.T, holdThrottler bool) actualChannelEventLoopHarness {
	t.Helper()

	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	state := newTestControlState(t)
	readerBatches := make(chan []Transaction)
	readerHandoffs := make(chan struct{}, 4)
	readerStarts := make(chan struct{}, 4)
	senderStarts := make(chan struct{}, 4)
	var allowThrottlerForward chan struct{}
	var forwardedBatches chan []Transaction
	if holdThrottler {
		allowThrottlerForward = make(chan struct{})
		forwardedBatches = make(chan []Transaction, 1)
	}
	read := func(ctx context.Context, output chan<- []Transaction, _, _ int) (readerRun, error) {
		readerStarts <- struct{}{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-ctx.Done():
					return
				case batch := <-readerBatches:
					select {
					case output <- batch:
						readerHandoffs <- struct{}{}
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return readerRun{done: done, reconcile: func(int) {}}, nil
	}
	start := func(
		ctx context.Context,
		input <-chan []Transaction,
		output chan<- []Transaction,
		readerChannelTelemetry *channelTelemetry,
		senderChannelTelemetry *channelTelemetry,
		settings throttlerSettings,
	) (<-chan struct{}, chan<- throttlerUpdate) {
		senderStarts <- struct{}{}
		if holdThrottler {
			return startHeldThrottlerForTest(
				ctx,
				input,
				output,
				readerChannelTelemetry,
				senderChannelTelemetry,
				allowThrottlerForward,
				forwardedBatches,
			)
		}
		return startThrottler(ctx, input, output, readerChannelTelemetry, senderChannelTelemetry, settings)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(testControlPlane(requests), metrics, NewMetrics(), read, start)
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
	return actualChannelEventLoopHarness{
		requests:              requests,
		metrics:               metrics,
		state:                 &state,
		readerBatches:         readerBatches,
		readerHandoffs:        readerHandoffs,
		readerStarts:          readerStarts,
		senderStarts:          senderStarts,
		allowThrottlerForward: allowThrottlerForward,
		forwardedBatches:      forwardedBatches,
		stop:                  stop,
	}
}

func startHeldThrottlerForTest(
	ctx context.Context,
	input <-chan []Transaction,
	output chan<- []Transaction,
	readerChannelTelemetry *channelTelemetry,
	senderChannelTelemetry *channelTelemetry,
	allowForward <-chan struct{},
	forwarded chan<- []Transaction,
) (<-chan struct{}, chan<- throttlerUpdate) {
	done := make(chan struct{})
	updates := make(chan throttlerUpdate)
	go func() {
		defer close(done)

		var batches <-chan []Transaction
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				close(update.acknowledged)
			case <-allowForward:
				allowForward = nil
				batches = input
			case batch, ok := <-batches:
				if !ok {
					return
				}
				readerChannelTelemetry.recordReceive(len(batch))
				select {
				case <-ctx.Done():
					return
				case output <- batch:
					senderChannelTelemetry.recordSend(len(batch))
					forwarded <- batch
				}
			}
		}
	}()
	return done, updates
}

func (h actualChannelEventLoopHarness) command(t *testing.T, kind requestKind) {
	t.Helper()
	if kind == cmdPause {
		h.requests <- request{kind: kind}
		waitForState(t, h.requests, runStatePaused)
		return
	}
	reply := make(chan commandResult, 1)
	h.requests <- request{kind: kind, commandReply: reply}
	if result := <-reply; result.status != commandAccepted || result.err != nil {
		t.Fatalf("command %v = %+v, want accepted", kind, result)
	}
}

func (h actualChannelEventLoopHarness) setCapacity(t *testing.T, kind requestKind, capacity int) {
	t.Helper()
	reply := make(chan commandResult, 1)
	h.requests <- request{kind: kind, value: capacity, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("set capacity command %v = %+v, want accepted", kind, result)
	}
}

func (h actualChannelEventLoopHarness) assertNoReplacement(t *testing.T) {
	t.Helper()
	select {
	case <-h.readerStarts:
		t.Fatal("Pause/Resume started replacement Reader channel")
	default:
	}
	select {
	case <-h.senderStarts:
		t.Fatal("Pause/Resume started replacement Sender channel")
	default:
	}
}

func (h actualChannelEventLoopHarness) nextReader(t *testing.T) <-chan []Transaction {
	t.Helper()
	<-h.readerStarts
	return h.state.telemetry.readerChannel.batches
}

func (h actualChannelEventLoopHarness) nextSender(t *testing.T) <-chan []Transaction {
	t.Helper()
	<-h.senderStarts
	return h.state.telemetry.senderChannel.batches
}

func (h actualChannelEventLoopHarness) send(t *testing.T, batch []Transaction) {
	t.Helper()
	select {
	case h.readerBatches <- batch:
	case <-time.After(time.Second):
		t.Fatal("test reader did not accept batch")
	}
	select {
	case <-h.readerHandoffs:
	case <-time.After(time.Second):
		t.Fatal("test reader did not hand batch to actual Reader channel")
	}
}

func assertClosedActualChannel(t *testing.T, batches <-chan []Transaction) {
	t.Helper()
	if _, ok := <-batches; ok {
		t.Fatal("replaced or torn-down actual channel is still open")
	}
}

func waitForTransactions(t *testing.T, requests chan<- request, metrics chan<- time.Time, want int64) {
	t.Helper()

	reply := make(chan statusSnapshot, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case metrics <- time.Now():
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
		select {
		case requests <- request{kind: getSnapshot, snapshotReply: reply}:
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
		select {
		case snapshot := <-reply:
			if snapshot.Run.TotalTransactions == want {
				return
			}
		case <-deadline:
			t.Fatalf("transactions did not reach %d", want)
		}
	}
}

func waitForReaderReceives(t *testing.T, requests chan<- request, want int64) {
	t.Helper()

	reply := make(chan statusSnapshot, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case requests <- request{kind: getSnapshot, snapshotReply: reply}:
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
