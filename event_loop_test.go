package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestElapsedMsUsesRunStartAndAccumulatedTime(t *testing.T) {
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state := newTestControlState(t)
	if got := state.elapsedMs(start); got != 0 {
		t.Fatalf("idle elapsed = %d, want 0", got)
	}

	state.lifecycle.run()
	state.runStartedAt = start
	if got := state.elapsedMs(start.Add(1250 * time.Millisecond)); got != 1250 {
		t.Fatalf("running elapsed = %d, want 1250", got)
	}

	state.lifecycle.pause()
	state.pauseElapsed(start.Add(1250 * time.Millisecond))
	if got := state.elapsedMs(start.Add(10 * time.Second)); got != 1250 {
		t.Fatalf("paused elapsed = %d, want 1250", got)
	}

	state.lifecycle.run()
	state.runStartedAt = start.Add(10 * time.Second)
	if got := state.elapsedMs(start.Add(10*time.Second + 750*time.Millisecond)); got != 2000 {
		t.Fatalf("resumed elapsed = %d, want 2000", got)
	}
}

func TestRunFailureRetryAndResetUpdateStartError(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	batches := make(chan []Transaction)
	producerDone := make(chan struct{})
	var starts int
	produce := func(ctx context.Context, _ int, _ int) (<-chan []Transaction, <-chan struct{}, error) {
		starts++
		switch starts {
		case 1:
			return nil, nil, errors.New("first failure")
		case 2:
			return nil, nil, errors.New("second failure")
		}
		go func() {
			defer func() {
				close(batches)
				close(producerDone)
			}()
			<-ctx.Done()
		}()
		return batches, producerDone, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, produce)
	reply := make(chan commandResult, 1)
	snapshotReply := make(chan statusSnapshot, 1)

	for index, want := range []string{"first failure", "second failure"} {
		requests <- request{kind: cmdRun, commandReply: reply}
		if result := <-reply; result.err == nil || result.err.Error() != want {
			t.Fatalf("Run error = %v, want %q", result.err, want)
		}
		requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
		snapshot := <-snapshotReply
		if snapshot.RunState != runStateIdle || snapshot.ElapsedMs != 0 || snapshot.StartError == nil || *snapshot.StartError != want {
			t.Fatalf("failed Run snapshot = %+v, want idle, zero elapsed, %q", snapshot, want)
		}
		if index == 0 {
			requests <- request{kind: cmdReset, commandReply: reply}
			if result := <-reply; result.status != commandAccepted {
				t.Fatalf("Reset status = %v, want accepted", result.status)
			}
			requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
			if snapshot := <-snapshotReply; snapshot.StartError != nil || snapshot.ElapsedMs != 0 {
				t.Fatalf("Reset snapshot = %+v, want cleared error and elapsed", snapshot)
			}
		}
	}

	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil {
		t.Fatalf("retry Run error = %v, want nil", result.err)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStateRunning || snapshot.StartError != nil {
		t.Fatalf("successful Run snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.err != nil || starts != 3 {
		t.Fatalf("repeated Run = %+v, producer starts = %d, want nil error and 3 starts", result, starts)
	}
	requests <- request{kind: cmdPause}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStatePaused || snapshot.ElapsedMs < 0 {
		t.Fatalf("Pause snapshot = %+v", snapshot)
	}
	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("paused Reset status = %v, want accepted", result.status)
	}
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	if snapshot := <-snapshotReply; snapshot.RunState != runStateIdle || snapshot.ElapsedMs != 0 {
		t.Fatalf("paused Reset snapshot = %+v", snapshot)
	}
}

func TestRunCommandStartsPipelineOnce(t *testing.T) {
	var starts int
	onProduce := func() {
		starts++
	}
	requests, _, _ := startEventLoopForTest(t, onProduce)

	reply := make(chan statusSnapshot, 1)
	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	runningSnapshot := <-reply
	if runningSnapshot.RunState != runStateRunning {
		t.Fatalf("state after first Run = %v, want %v", runningSnapshot.RunState, runStateRunning)
	}
	if starts != 1 {
		t.Fatalf("starts after first Run = %v, want 1", starts)
	}

	requests <- request{kind: cmdRun}
	requests <- request{kind: getSnapshot, snapshotReply: reply}
	repeatedRunSnapshot := <-reply
	if repeatedRunSnapshot.RunState != runStateRunning {
		t.Fatalf("state after second Run = %v, want %v", repeatedRunSnapshot.RunState, runStateRunning)
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
	if snapshot := <-reply; snapshot.TotalTransactions != 1 || snapshot.RunState != runStatePaused {
		t.Fatalf("paused snapshot = %+v, want one consumed transaction", snapshot)
	}

	requests <- request{kind: cmdRun}
	waitForTransactions(t, requests, metrics, 2)
	if starts != 1 {
		t.Fatalf("producer starts = %v, want 1", starts)
	}
}

func TestResetFromPausedStopsProducerClearsProgressAndStartsFreshRun(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	firstBatches := make(chan []Transaction, 1)
	firstProducerReady := make(chan struct{})
	allowFirstSecondBatch := make(chan struct{})
	firstProducerDone := make(chan struct{})
	allowFirstProducerDone := make(chan struct{})
	freshBatches := make(chan []Transaction)
	freshProducerDone := make(chan struct{})
	oldAccepted := make(chan struct{})
	firstBatchBuffered := make(chan struct{})
	throttlerCancelObserved := make(chan struct{})
	allowFirstThrottlerDone := make(chan struct{})
	capturedIDs := make(chan string, 2)
	var starts int
	var throttlerStarts int

	produce := func(ctx context.Context, _ int, _ int) (<-chan []Transaction, <-chan struct{}, error) {
		starts++
		if starts == 1 {
			go func() {
				defer func() {
					close(firstBatches)
					<-allowFirstProducerDone
					close(firstProducerDone)
				}()
				select {
				case <-firstProducerReady:
				case <-ctx.Done():
					return
				}
				select {
				case firstBatches <- []Transaction{{ClientID: "old"}}:
				case <-ctx.Done():
					return
				}
				select {
				case <-allowFirstSecondBatch:
				case <-ctx.Done():
					return
				}
				select {
				case firstBatches <- []Transaction{{ClientID: "old"}}:
				case <-ctx.Done():
				}
			}()
			return firstBatches, firstProducerDone, nil
		}

		go func() {
			defer func() {
				close(freshBatches)
				close(freshProducerDone)
			}()
			<-ctx.Done()
		}()
		return freshBatches, freshProducerDone, nil
	}

	start := func(
		ctx context.Context,
		batches <-chan []Transaction,
		_ *readerChannelTelemetry,
		_ *readerChannelTelemetry,
		_ int,
		_ throttlerSettings,
	) (<-chan []Transaction, <-chan struct{}, chan<- throttlerUpdate) {
		throttlerStarts++
		first := throttlerStarts == 1
		senderBatches := make(chan []Transaction)
		done := make(chan struct{})
		updates := make(chan throttlerUpdate)
		go func() {
			defer func() {
				close(senderBatches)
				close(done)
			}()
			var pending []Transaction
			delivered := 0
			for {
				if pending == nil {
					select {
					case update := <-updates:
						close(update.applied)
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
					close(update.applied)
				case senderBatches <- pending:
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
		return senderBatches, done, updates
	}

	startCustomEventLoopForTestWithThrottlerAndDeliveryObserver(t, requests, metrics, produce, start, func(batch []Transaction) {
		capturedIDs <- batch[0].ClientID
	})
	requests <- request{kind: cmdRun}
	close(firstProducerReady)
	select {
	case <-oldAccepted:
	case <-time.After(time.Second):
		t.Fatal("old producer batch was not consumed")
	}
	select {
	case id := <-capturedIDs:
		if id != "old" {
			t.Fatalf("captured old ClientID = %q, want old", id)
		}
	case <-time.After(time.Second):
		t.Fatal("old ClientID was not handed to Consumer")
	}
	waitForTransactions(t, requests, metrics, 1)

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	close(allowFirstSecondBatch)
	select {
	case <-firstBatchBuffered:
	case <-time.After(time.Second):
		t.Fatal("producer did not fill its readerChannel after Pause")
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
	close(allowFirstProducerDone)
	select {
	case <-firstProducerDone:
	case <-time.After(time.Second):
		t.Fatal("producerDone did not close")
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
	case <-firstProducerDone:
	default:
		t.Fatal("producerDone is not closed after Reset")
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
		t.Fatalf("producer starts after Reset and Run = %v, want 2", starts)
	}
	select {
	case freshBatches <- []Transaction{{ClientID: "fresh"}}:
	case <-time.After(time.Second):
		t.Fatal("fresh producer batch was not consumed")
	}
	waitForTransactions(t, requests, metrics, 1)
	select {
	case id := <-capturedIDs:
		if id != "fresh" {
			t.Fatalf("captured ClientID = %q, want fresh", id)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh ClientID was not handed to Consumer")
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
	handler := commandsHandler(requests, testPolicy(t))
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
		t.Fatalf("producer starts after rejected Reset = %v, want 1", starts)
	}

	select {
	case batches <- []Transaction{{}}:
	case <-time.After(time.Second):
		t.Fatal("consumer stopped after rejected Reset")
	}
	waitForTransactions(t, requests, metrics, 2)
}

func TestReaderMeasurementsSurvivePauseAndClearOnReset(t *testing.T) {
	requests := make(chan request, 3)
	metrics := make(chan time.Time)
	batches := make(chan []Transaction)
	producerDone := make(chan struct{})
	state := newTestControlState(t)
	produce := func(ctx context.Context, _ int, _ int) (<-chan []Transaction, <-chan struct{}, error) {
		go func() {
			defer func() {
				close(batches)
				close(producerDone)
			}()
			<-ctx.Done()
		}()
		return batches, producerDone, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(
			requests,
			metrics,
			NewMetrics(),
			adaptLegacyProducer(produce),
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
	readerChannel := make(chan []Transaction, state.policy.ReaderChannel.Capacity.Default)
	state.readerChannel.start(readerChannel, 2)
	if !state.readerChannel.send(context.Background(), readerChannel, make([]Transaction, 2)) {
		t.Fatal("readerChannel send failed")
	}
	state.reader.recordRead(2, filepath.Join("data", "first.parquet"))
	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	state.reader.recordRead(3, filepath.Join("data", "second.parquet"))
	metrics <- time.Now()

	snapshotReply := make(chan statusSnapshot, 1)
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot := <-snapshotReply
	if snapshot.ReaderRowsRead != 5 || snapshot.ReaderSource == nil ||
		*snapshot.ReaderSource != "data/second.parquet" || snapshot.ReaderChannelCapacity != 2 ||
		snapshot.ReaderChannelDepthBatches != 1 || snapshot.ReaderChannelBufferedTransactions != 2 ||
		snapshot.ReaderChannelSentBatchesTotal != 1 || snapshot.ReaderChannelSentTransactionsTotal != 2 ||
		snapshot.ReaderChannelInputBatchesPerSecond != 1 || snapshot.ReaderChannelInputTransactionsPerSecond != 2 {
		t.Fatalf("paused snapshot = %+v, want reader and readerChannel measurements", snapshot)
	}
	state.readerChannel.recordReceive(len(<-readerChannel))
	metrics <- time.Now()
	requests <- request{kind: getSnapshot, snapshotReply: snapshotReply}
	snapshot = <-snapshotReply
	if snapshot.ReaderChannelReceivedBatchesTotal != 1 || snapshot.ReaderChannelReceivedTransactionsTotal != 2 ||
		snapshot.ReaderChannelInputBatchesPerSecond != 0 || snapshot.ReaderChannelInputTransactionsPerSecond != 0 ||
		snapshot.ReaderChannelOutputBatchesPerSecond != 1 || snapshot.ReaderChannelOutputTransactionsPerSecond != 2 {
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
	if snapshot.ReaderReadTPS != 0 || snapshot.ReaderRowsRead != 0 || snapshot.ReaderSource != nil ||
		snapshot.ReaderChannelCapacity != state.policy.ReaderChannel.Capacity.Default || snapshot.ReaderChannelDepthBatches != 0 ||
		snapshot.ReaderChannelBufferedTransactions != 0 || snapshot.ReaderChannelBlockedSenders != 0 ||
		snapshot.ReaderChannelOldestBlockedSenderMs != 0 || snapshot.ReaderChannelBlockedMs != 0 ||
		snapshot.ReaderChannelSentBatchesTotal != 0 || snapshot.ReaderChannelSentTransactionsTotal != 0 ||
		snapshot.ReaderChannelReceivedBatchesTotal != 0 || snapshot.ReaderChannelReceivedTransactionsTotal != 0 ||
		snapshot.ReaderChannelInputBatchesPerSecond != 0 || snapshot.ReaderChannelInputTransactionsPerSecond != 0 ||
		snapshot.ReaderChannelOutputBatchesPerSecond != 0 || snapshot.ReaderChannelOutputTransactionsPerSecond != 0 {
		t.Fatalf("snapshot after Reset = %+v, want zero measurements", snapshot)
	}
}

func TestThrottlerControlsApplyImmediatelyAndPersistThroughReset(t *testing.T) {
	requests, batches, metrics := startEventLoopForTest(t, func() {})
	commands := commandsHandler(requests, testPolicy(t))
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
	if got := snapshot(); got.ReaderReadBatchSize != 1_000 || got.ThrottlerRequestedTPS != 2_000 ||
		got.ThrottlerInstallationMode != throttlerInstalled {
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
	if got := snapshot(); got.TotalTransactions != 0 || got.ThrottlerRequestedTPS != 0 {
		t.Fatalf("zero-TPS running snapshot = %+v", got)
	}
	post(`{"action":"set-throttler-installation-mode","value":"bypass"}`, http.StatusOK)
	waitForTransactions(t, requests, metrics, 1)
	if got := snapshot(); got.ThrottlerRequestedTPS != 0 || got.ThrottlerInstallationMode != throttlerBypass {
		t.Fatalf("bypass snapshot = %+v", got)
	}
	post(`{"action":"pause"}`, http.StatusOK)
	waitForState(t, requests, runStatePaused)
	post(`{"action":"set-requested-tps","value":4000}`, http.StatusOK)
	post(`{"action":"set-throttler-installation-mode","value":"installed"}`, http.StatusOK)
	select {
	case batches <- []Transaction{{ClientID: "paused"}}:
	case <-time.After(time.Second):
		t.Fatal("paused stage did not receive batch")
	}
	waitForReaderReceives(t, requests, 2)
	metrics <- time.Now()
	if got := snapshot(); got.TotalTransactions != 1 || got.ThrottlerRequestedTPS != 4000 ||
		got.ThrottlerInstallationMode != throttlerInstalled {
		t.Fatalf("paused throttler snapshot = %+v", got)
	}
	post(`{"action":"run"}`, http.StatusOK)
	waitForTransactions(t, requests, metrics, 2)
	post(`{"action":"set-requested-tps","value":100}`, http.StatusOK)
	if got := snapshot().ThrottlerRequestedTPS; got != 100 {
		t.Fatalf("running TPS = %d, want 100", got)
	}
	post(`{"action":"pause"}`, http.StatusOK)
	waitForState(t, requests, runStatePaused)
	post(`{"action":"reset"}`, http.StatusOK)
	if got := snapshot(); got.RunState != runStateIdle || got.TotalTransactions != 0 ||
		got.ThrottlerRequestedTPS != 100 || got.ThrottlerInstallationMode != throttlerInstalled {
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
	if got := <-snapshotReply; got.RunState != runStateIdle || got.TotalTransactions != 0 || got.ThrottlerRequestedTPS != 0 {
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
	if initial.SenderChannelCapacity != 0 || initial.SenderChannelSentBatchesTotal != 0 ||
		initial.ThrottlerAdmittedTPS != 0 {
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
	if first := snapshot(); first.ThrottlerAdmittedTPS != 0 || first.SenderChannelInputTransactionsPerSecond != 0 {
		t.Fatalf("pre-window Sender rate = %+v", first)
	}
	batches <- make([]Transaction, 2)
	waitForSenderHandoff(t, requests, 1)
	metrics <- time.Now()
	active := snapshot()
	if active.SenderChannelCapacity != 0 || active.SenderChannelDepthBatches != 0 ||
		active.SenderChannelBufferedTransactions != 0 || active.SenderChannelSentBatchesTotal != 1 ||
		active.SenderChannelSentTransactionsTotal != 2 || active.SenderChannelReceivedBatchesTotal != 1 ||
		active.SenderChannelReceivedTransactionsTotal != 2 || active.SenderChannelInputBatchesPerSecond != 1 ||
		active.SenderChannelInputTransactionsPerSecond != 2 || active.SenderChannelOutputBatchesPerSecond != 1 ||
		active.SenderChannelOutputTransactionsPerSecond != 2 || active.ThrottlerAdmittedTPS != active.SenderChannelInputTransactionsPerSecond {
		t.Fatalf("active Sender channel = %+v", active)
	}

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	batches <- []Transaction{{}}
	waitForReaderReceives(t, requests, 2)
	metrics <- time.Now()
	paused := snapshot()
	if paused.SenderChannelSentBatchesTotal != 1 || paused.SenderChannelReceivedBatchesTotal != 1 ||
		paused.SenderChannelBlockedSenders != 0 || paused.SenderChannelDepthBatches != 0 ||
		paused.ThrottlerAdmittedTPS != 0 || paused.SenderChannelInputTransactionsPerSecond != 0 ||
		paused.SenderChannelOutputTransactionsPerSecond != 0 {
		t.Fatalf("paused Sender channel = %+v", paused)
	}

	requests <- request{kind: cmdRun, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("resumed Run = %+v", result)
	}
	waitForSenderHandoff(t, requests, 2)
	metrics <- time.Now()
	resumed := snapshot()
	if resumed.SenderChannelSentBatchesTotal != 2 || resumed.SenderChannelReceivedBatchesTotal != 2 ||
		resumed.ThrottlerAdmittedTPS != 1 || resumed.SenderChannelInputTransactionsPerSecond != 1 {
		t.Fatalf("resumed Sender channel = %+v", resumed)
	}

	requests <- request{kind: cmdPause}
	waitForState(t, requests, runStatePaused)
	requests <- request{kind: cmdReset, commandReply: reply}
	if result := <-reply; result.status != commandAccepted {
		t.Fatalf("Reset = %+v", result)
	}
	reset := snapshot()
	if reset.RunState != runStateIdle || reset.SenderChannelCapacity != 0 ||
		reset.SenderChannelDepthBatches != 0 || reset.SenderChannelBufferedTransactions != 0 ||
		reset.SenderChannelBlockedSenders != 0 || reset.SenderChannelOldestBlockedSenderMs != 0 ||
		reset.SenderChannelBlockedMs != 0 || reset.SenderChannelSentBatchesTotal != 0 ||
		reset.SenderChannelSentTransactionsTotal != 0 || reset.SenderChannelReceivedBatchesTotal != 0 ||
		reset.SenderChannelReceivedTransactionsTotal != 0 || reset.SenderChannelInputBatchesPerSecond != 0 ||
		reset.SenderChannelInputTransactionsPerSecond != 0 || reset.SenderChannelOutputBatchesPerSecond != 0 ||
		reset.SenderChannelOutputTransactionsPerSecond != 0 || reset.ThrottlerAdmittedTPS != 0 {
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
		if got.SenderChannelSentBatchesTotal == want && got.SenderChannelReceivedBatchesTotal == want {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("Sender handoff did not reach %d: %+v", want, got)
		}
	}
}

func startEventLoopForTest(t *testing.T, onProduce func()) (chan<- request, chan<- []Transaction, chan<- time.Time) {
	t.Helper()

	requests := make(chan request, 3)
	batches := make(chan []Transaction)
	metrics := make(chan time.Time)
	producerDone := make(chan struct{})
	produce := func(ctx context.Context, _ int, _ int) (<-chan []Transaction, <-chan struct{}, error) {
		onProduce()
		go func() {
			defer func() {
				close(batches)
				close(producerDone)
			}()
			<-ctx.Done()
		}()
		return batches, producerDone, nil
	}
	startCustomEventLoopForTest(t, requests, metrics, produce)

	return requests, batches, metrics
}

type legacyProducerStarter func(context.Context, int, int) (<-chan []Transaction, <-chan struct{}, error)

type legacyThrottlerStarter func(
	context.Context,
	<-chan []Transaction,
	*readerChannelTelemetry,
	*readerChannelTelemetry,
	int,
	throttlerSettings,
) (<-chan []Transaction, <-chan struct{}, chan<- throttlerUpdate)

func startCustomEventLoopForTest(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	produce legacyProducerStarter,
) {
	startCustomEventLoopForTestWithThrottler(t, requests, metrics, produce, nil)
}

func startCustomEventLoopForTestWithThrottler(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	produce legacyProducerStarter,
	start legacyThrottlerStarter,
) {
	startCustomEventLoopForTestWithThrottlerAndDeliveryObserver(t, requests, metrics, produce, start, nil)
}

func startCustomEventLoopForTestWithThrottlerAndDeliveryObserver(
	t *testing.T,
	requests chan request,
	metrics <-chan time.Time,
	produce legacyProducerStarter,
	start legacyThrottlerStarter,
	onDelivered func([]Transaction),
) {
	t.Helper()

	done := make(chan struct{})
	state := newTestControlState(t)
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(
			requests,
			metrics,
			NewMetrics(),
			adaptLegacyProducer(produce),
			adaptLegacyThrottler(start, onDelivered),
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

func adaptLegacyProducer(produce legacyProducerStarter) producerStarter {
	return func(ctx context.Context, output chan<- []Transaction, batchSize int) (<-chan struct{}, error) {
		input, done, err := produce(ctx, batchSize, cap(output))
		if err != nil {
			return nil, err
		}
		return relayBatches(ctx, input, output, done, nil), nil
	}
}

func adaptLegacyThrottler(start legacyThrottlerStarter, onDelivered func([]Transaction)) throttlerStarter {
	if start == nil {
		return startThrottler
	}
	return func(
		ctx context.Context,
		input <-chan []Transaction,
		output chan<- []Transaction,
		readerChannel *readerChannelTelemetry,
		senderChannel *readerChannelTelemetry,
		settings throttlerSettings,
	) (<-chan struct{}, chan<- throttlerUpdate) {
		batches, done, updates := start(ctx, input, readerChannel, senderChannel, cap(output), settings)
		return relayBatches(ctx, batches, output, done, onDelivered), updates
	}
}

func relayBatches(
	ctx context.Context,
	input <-chan []Transaction,
	output chan<- []Transaction,
	done <-chan struct{},
	onDelivered func([]Transaction),
) <-chan struct{} {
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-input:
				if !ok {
					return
				}
				select {
				case output <- batch:
					if onDelivered != nil {
						onDelivered(batch)
					}
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	combinedDone := make(chan struct{})
	go func() {
		defer close(combinedDone)
		<-done
		<-relayDone
	}()
	return combinedDone
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
		if snapshot.RunState != want {
			t.Fatalf("state = %v, want %v", snapshot.RunState, want)
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
			if harness.state.readerChannel.batches != reader || harness.state.senderChannel.batches != sender {
				t.Fatal("Pause/Resume replaced an actual event-loop channel")
			}

			if capacity > 0 {
				harness.send(t, []Transaction{{ClientID: "old"}})
			}
			harness.command(t, cmdPause)
			harness.command(t, cmdReset)
			if harness.state.readerChannel.batches != reader || harness.state.senderChannel.batches != sender {
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
			if harness.state.readerChannel.batches != reader || harness.state.senderChannel.batches != sender {
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
	if harness.state.readerChannel.batches != reader || harness.state.senderChannel.batches != sender {
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
			if harness.state.readerChannel.batches != nil || harness.state.senderChannel.batches != nil {
				t.Fatal("event-loop teardown retained telemetry channel attachment")
			}
		})
	}
}

type actualChannelEventLoopHarness struct {
	requests              chan request
	metrics               chan time.Time
	state                 *controlState
	producerBatches       chan []Transaction
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
	producerBatches := make(chan []Transaction)
	readerHandoffs := make(chan struct{}, 4)
	readerStarts := make(chan struct{}, 4)
	senderStarts := make(chan struct{}, 4)
	var allowThrottlerForward chan struct{}
	var forwardedBatches chan []Transaction
	if holdThrottler {
		allowThrottlerForward = make(chan struct{})
		forwardedBatches = make(chan []Transaction, 1)
	}
	produce := func(ctx context.Context, output chan<- []Transaction, _ int) (<-chan struct{}, error) {
		readerStarts <- struct{}{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-ctx.Done():
					return
				case batch := <-producerBatches:
					select {
					case output <- batch:
						readerHandoffs <- struct{}{}
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		return done, nil
	}
	start := func(
		ctx context.Context,
		input <-chan []Transaction,
		output chan<- []Transaction,
		readerChannel *readerChannelTelemetry,
		senderChannel *readerChannelTelemetry,
		settings throttlerSettings,
	) (<-chan struct{}, chan<- throttlerUpdate) {
		senderStarts <- struct{}{}
		if holdThrottler {
			return startHeldThrottlerForTest(
				ctx,
				input,
				output,
				readerChannel,
				senderChannel,
				allowThrottlerForward,
				forwardedBatches,
			)
		}
		return startThrottler(ctx, input, output, readerChannel, senderChannel, settings)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.eventLoopWithThrottler(requests, metrics, NewMetrics(), produce, start)
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
		producerBatches:       producerBatches,
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
	readerChannel *readerChannelTelemetry,
	senderChannel *readerChannelTelemetry,
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
				close(update.applied)
			case <-allowForward:
				allowForward = nil
				batches = input
			case batch, ok := <-batches:
				if !ok {
					return
				}
				readerChannel.recordReceive(len(batch))
				select {
				case <-ctx.Done():
					return
				case output <- batch:
					senderChannel.recordSend(len(batch))
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
	return h.state.readerChannel.batches
}

func (h actualChannelEventLoopHarness) nextSender(t *testing.T) <-chan []Transaction {
	t.Helper()
	<-h.senderStarts
	return h.state.senderChannel.batches
}

func (h actualChannelEventLoopHarness) send(t *testing.T, batch []Transaction) {
	t.Helper()
	select {
	case h.producerBatches <- batch:
	case <-time.After(time.Second):
		t.Fatal("test producer did not accept batch")
	}
	select {
	case <-h.readerHandoffs:
	case <-time.After(time.Second):
		t.Fatal("test producer did not hand batch to actual Reader channel")
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
			if snapshot.TotalTransactions == want {
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
			if snapshot.ReaderChannelReceivedBatchesTotal == want {
				return
			}
		case <-deadline:
			t.Fatalf("Reader channel receives did not reach %d", want)
		}
	}
}
