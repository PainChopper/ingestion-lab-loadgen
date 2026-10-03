package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestSenderPoolRetryLogExcludesRequestMarkers(t *testing.T) {
	var output bytes.Buffer
	logger, err := newApplicationLogger("info", &output)
	if err != nil {
		t.Fatalf("newApplicationLogger() error = %v", err)
	}

	urlMarker := "url-credential-and-query-marker"
	headerMarker := "header-value-marker"
	bodyMarker := "body-payload-marker"
	clientMarker := "client-id-marker"
	requestURL := "https://" + urlMarker + "@example.test/ingest?token=" + urlMarker
	headers := http.Header{"Authorization": {headerMarker}}
	body := []byte(bodyMarker)
	if requestURL == "" || len(headers) == 0 || len(body) == 0 {
		t.Fatal("test request markers were not initialized")
	}

	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	config.Retry.DelaysMS = []int{1}
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, logger)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 2 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: clientMarker}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()

	line := output.String()
	if !strings.Contains(line, "event=batch_delivery_retry") {
		t.Fatalf("retry event was not written")
	}
	for _, marker := range []string{urlMarker, headerMarker, bodyMarker, clientMarker} {
		if strings.Contains(line, marker) {
			t.Fatal("request marker was written to the log")
		}
	}
}

func waitForSenderCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("Sender condition was not reached")
		default:
		}
	}
}

func TestSenderPoolRetriesFailureUntilSuccess(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	config.Retry.DelaysMS = []int{1}
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 4 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: "invalid"}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	<-pool.stop()
	if attempts.Load() != 4 {
		t.Fatalf("failure attempts=%d, want 4", attempts.Load())
	}
}

func TestSenderPoolAggregateSnapshotPartitionsActivities(t *testing.T) {
	pool := &senderPool{workers: []*senderWorker{
		{}, {draining: true},
		{busy: true}, {busy: true, draining: true},
		{busy: true, backoff: true}, {busy: true, backoff: true, draining: true},
	}}

	snapshot := pool.aggregateSnapshot()
	if snapshot.liveWorkers != 6 || snapshot.idleWorkers != 2 || snapshot.inFlightWorkers != 2 || snapshot.backoffWorkers != 2 {
		t.Fatalf("activity partition = %+v", snapshot)
	}
	if snapshot.drainingWorkers != 3 || snapshot.drainingIdleWorkers != 1 || snapshot.drainingInFlightWorkers != 1 || snapshot.drainingBackoffWorkers != 1 {
		t.Fatalf("draining intersections = %+v", snapshot)
	}
}

func TestSenderPoolCancellationStopsRetry(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	entered := make(chan struct{})
	var attempts atomic.Int64
	pool.attempt = func(ctx context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		attempts.Add(1)
		close(entered)
		<-ctx.Done()
		return senderAttemptCanceled
	}

	batches <- []Transaction{{ClientID: "canceled"}}
	<-entered
	<-pool.stop()
	if attempts.Load() != 1 {
		t.Fatalf("canceled attempts = %d, want 1", attempts.Load())
	}
	if consumed.Load() != 0 {
		t.Fatalf("canceled batch was counted as completed: consumed=%d", consumed.Load())
	}
}

func TestSenderPoolParentCancellationStopsRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		pool := startSenderPool(parent, batches, &channel, &consumed, 1, config.API, config.Retry, nil)
		pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
			return senderAttemptFailure
		}

		batches <- []Transaction{{ClientID: "canceled-parent"}}
		synctest.Wait()
		if pool.aggregateSnapshot().backoffWorkers != 1 {
			t.Fatal("Sender did not enter retry backoff")
		}
		cancel()
		<-pool.stop()
		if consumed.Load() != 0 {
			t.Fatalf("parent-canceled batch was counted as completed: consumed=%d", consumed.Load())
		}
	})
}

func TestSenderPoolBackpressureWhenAllWorkersRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		config.Retry.JitterPercent = 0
		pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
		pool.attempt = func(_ context.Context, _ []Transaction, attempt int) senderAttemptOutcome {
			if attempt == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}

		batches <- []Transaction{{ClientID: "first"}}
		batches <- []Transaction{{ClientID: "second"}}
		synctest.Wait()
		batches <- []Transaction{{ClientID: "third"}}
		if received := channel.snapshot(time.Now()).receivedBatchesTotal; received != 2 || len(batches) != 1 {
			t.Fatalf("all-worker backpressure received=%d queued=%d, want 2 and 1", received, len(batches))
		}

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if consumed.Load() != 3 {
			t.Fatalf("completed=%d, want 3", consumed.Load())
		}
		<-pool.stop()
	})
}

func TestSenderPoolReconcilesUpDownUpWithoutLosingAcceptedBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		entered <- struct{}{}
		<-release
		return senderAttemptSuccess
	}

	batches <- []Transaction{{ClientID: "A"}}
	batches <- []Transaction{{ClientID: "B"}}
	<-entered
	<-entered
	drainingWorker := pool.workers[1]
	pool.reconcile(1)
	if got := pool.aggregateSnapshot(); got.liveWorkers != 2 || got.drainingWorkers != 1 {
		t.Fatalf("after scale down = %+v, want two live and one draining", got)
	}
	pool.reconcile(2)
	if got := pool.aggregateSnapshot(); got.liveWorkers != 2 || got.drainingWorkers != 0 {
		t.Fatalf("after reactivation = %+v, want two live and zero draining", got)
	}
	if pool.workers[1] != drainingWorker {
		t.Fatal("scale up replaced the live draining worker")
	}
	close(release)
	batches <- []Transaction{{ClientID: "C"}}
	<-entered
	<-pool.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 3 {
		t.Fatalf("received batches = %d, want 3", got)
	}
	if got := consumed.Load(); got != 3 {
		t.Fatalf("completed transactions = %d, want 3", got)
	}
	if got := pool.aggregateSnapshot().liveWorkers; got != 0 {
		t.Fatalf("live workers after stop = %d, want 0", got)
	}
}

func TestSenderPoolScaleDownJoinsIdleWorkerBeforeNextReceive(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan int, 1)
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		entered <- 0
		return senderAttemptSuccess
	}
	pool.reconcile(1)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 1 || snapshot.drainingWorkers != 0 {
		t.Fatalf("after idle downscale = %+v, want one active worker", snapshot)
	}
	batches <- []Transaction{{ClientID: "after-scale-down"}}
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("batch entered worker %d after scale-down, want worker 0", ordinal)
	}
	<-pool.stop()
}

func TestSenderPoolReplacementRestoresDesiredWorkerCount(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)

	pool.reconcile(1)
	pool.reconcile(2)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 2 || snapshot.idleWorkers != 2 {
		t.Fatalf("replacement workers = %+v, want two idle workers", snapshot)
	}
	<-pool.stop()
}

func TestSenderPoolReadyBatchCannotEnterMarkedDrainingWorker(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 2, config.API, config.Retry, nil)
	entered := make(chan int, 2)
	releaseFirst := make(chan struct{})
	pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		entered <- 0
		if batch[0].ClientID == "first" {
			<-releaseFirst
		}
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "first"}}
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("first batch entered worker %d, want worker 0", ordinal)
	}

	// Hold dispatch at the assignment lock while drain wake and a batch are both ready.
	pool.mu.Lock()
	draining := pool.workers[1]
	pool.desired = 1
	draining.draining = true
	draining.wake <- struct{}{}
	batches <- []Transaction{{ClientID: "after-mark"}}
	pool.mu.Unlock()
	<-draining.done
	close(releaseFirst)
	if ordinal := <-entered; ordinal != 0 {
		t.Fatalf("batch after drain mark entered worker %d, want worker 0", ordinal)
	}
	<-pool.stop()
	if got := consumed.Load(); got != 2 {
		t.Fatalf("completed transactions = %d, want 2", got)
	}
}

func TestSenderPoolRetainsBatchBeyondConfiguredDelayList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var channel channelTelemetry
		var consumed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.JitterPercent = 0
		pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
		var attempts atomic.Int64
		previous := time.Now()
		delays := make(chan time.Duration, 6)
		pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
			if attempts.Load() != 0 {
				delays <- time.Since(previous)
			}
			previous = time.Now()
			if attempts.Add(1) == 7 {
				return senderAttemptSuccess
			}
			return senderAttemptFailure
		}
		batches <- []Transaction{{ClientID: "failure"}}
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if snapshot := pool.aggregateSnapshot(); snapshot.idleWorkers != 1 || snapshot.backoffWorkers != 0 {
			t.Fatalf("successful sender snapshot = %+v", snapshot)
		}
		<-pool.stop()
		if got := attempts.Load(); got != 7 {
			t.Fatalf("attempts = %d, want 7", got)
		}
		gotDelays := make([]time.Duration, 0, 6)
		for range 6 {
			gotDelays = append(gotDelays, <-delays)
		}
		wantDelays := []time.Duration{250, 500, 1_000, 2_000, 5_000, 5_000}
		for index := range wantDelays {
			wantDelays[index] *= time.Millisecond
		}
		if !slices.Equal(gotDelays, wantDelays) {
			t.Fatalf("retry delays = %v, want %v", gotDelays, wantDelays)
		}
	})
}

func TestSenderPoolStopWaitsForAcceptedBatch(t *testing.T) {
	batches := make(chan []Transaction)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "accepted"}}
	<-entered
	done := pool.stop()
	select {
	case <-done:
		t.Fatal("pool stopped before accepted batch completed")
	default:
	}
	close(release)
	<-done
	if consumed.Load() != 1 || pool.aggregateSnapshot().liveWorkers != 0 {
		t.Fatalf("after join: consumed=%d live workers=%d", consumed.Load(), pool.aggregateSnapshot().liveWorkers)
	}
}

func TestSenderPoolStopLeavesReadyBatchForResume(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel channelTelemetry
	var consumed atomic.Int64
	config := testConfig(t).Sender
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config.API.URL = server.URL
	pool := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}

	batches <- []Transaction{{ClientID: "accepted"}}
	<-entered
	pool.intakeMu.Lock()
	// Leave one worker idle with a ready batch while intake is held.
	pool.reconcile(2)
	batches <- []Transaction{{ClientID: "ready"}}
	stopped := make(chan (<-chan struct{}), 1)
	go func() { stopped <- pool.stop() }()
	<-pool.ctx.Done()
	select {
	case <-stopped:
		t.Fatal("stop crossed the intake boundary while intake was held")
	default:
	}
	pool.intakeMu.Unlock()
	done := <-stopped
	received := channel.snapshot(time.Now()).receivedBatchesTotal
	if len(batches) != 1 || received != 1 {
		t.Fatalf("after stop boundary: queued=%d received=%d, want one of each", len(batches), received)
	}
	select {
	case <-done:
		t.Fatal("stop completed before the accepted batch")
	default:
	}
	close(release)
	<-done
	if consumed.Load() != 1 || len(batches) != 1 {
		t.Fatalf("after stop: completed=%d queued=%d, want one of each", consumed.Load(), len(batches))
	}

	resumed := startSenderPool(context.Background(), batches, &channel, &consumed, 1, config.API, config.Retry, nil)
	waitForSenderCondition(t, func() bool { return consumed.Load() == 2 })
	<-resumed.stop()
	if got := channel.snapshot(time.Now()).receivedBatchesTotal; got != 2 {
		t.Fatalf("received batches after resume = %d, want 2", got)
	}
}

func TestSenderPoolPauseRetainsLateHTTPOutcomeAndBatch(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				batches := make(chan []Transaction, 1)
				var telemetry channelTelemetry
				var completed atomic.Int64
				config := testConfig(t).Sender
				config.Retry.DelaysMS = []int{1_000}
				config.Retry.JitterPercent = 0
				pool := startSenderPool(
					context.Background(), batches, &telemetry, &completed,
					1, config.API, config.Retry, nil,
				)
				defer func() { <-pool.stop() }()
				entered := make(chan string, 3)
				release := make(chan struct{})
				requests := 0
				client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					entered <- string(body)
					requests++
					responseStatus := http.StatusNoContent
					if requests == 1 {
						select {
						case <-release:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						responseStatus = status
					}
					return &http.Response{
						StatusCode: responseStatus,
						Body:       io.NopCloser(strings.NewReader("")),
						Header:     make(http.Header),
					}, nil
				})}
				pool.attempt = newSenderHTTPAttempt("http://example.test/ingest", client).deliver
				batches <- []Transaction{{ClientID: "retained"}}
				first := <-entered
				pool.pause()
				batches <- []Transaction{{ClientID: "next"}}
				close(release)
				synctest.Wait()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if requests != 1 {
					t.Fatalf("HTTP requests during Pause = %d, want 1", requests)
				}
				wantCompleted := int64(0)
				if status == http.StatusNoContent {
					wantCompleted = 1
				}
				if completed.Load() != wantCompleted || pool.aggregateSnapshot().inFlightWorkers+
					pool.aggregateSnapshot().backoffWorkers != 1 {
					t.Fatalf("paused completion=%d workers=%+v", completed.Load(), pool.aggregateSnapshot())
				}
				pool.resumeRun()
				synctest.Wait()
				if status != http.StatusNoContent && <-entered != first {
					t.Fatal("retry changed the retained JSON batch/ClientID")
				}
				if next := <-entered; !strings.Contains(next, "next") || completed.Load() != 2 {
					t.Fatalf("after Resume next=%s completed=%d", next, completed.Load())
				}
			})
		})
	}
}

func TestSenderPoolPauseRetryDeadlineAndReconcileWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry channelTelemetry
		var completed atomic.Int64
		config := testConfig(t).Sender
		config.Retry.DelaysMS = []int{3_600_000}
		config.Retry.JitterPercent = 0
		pool := startSenderPool(
			context.Background(), batches, &telemetry, &completed,
			1, config.API, config.Retry, nil,
		)
		defer func() { <-pool.stop() }()
		var attempts atomic.Int64
		pool.attempt = func(_ context.Context, batch []Transaction, number int) senderAttemptOutcome {
			if batch[0].ClientID != "held" || int64(number) != attempts.Load()+1 {
				t.Fatal("retry changed batch or attempt number")
			}
			attempts.Add(1)
			if number == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}
		batches <- []Transaction{{ClientID: "held"}}
		synctest.Wait()
		time.Sleep(10 * time.Minute)
		pool.pause()
		synctest.Wait()
		if len(pool.workers[0].wake) != 0 {
			t.Fatal("long backoff did not consume the Pause wake before its deadline")
		}
		time.Sleep(10 * time.Minute)
		pool.resumeRun()
		pool.reconcile(1)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("Resume/reconcile shortened retry delay")
		}
		time.Sleep(40*time.Minute - time.Nanosecond)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("retry started before its original deadline")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if attempts.Load() != 2 || completed.Load() != 1 {
			t.Fatalf("at deadline attempts=%d completed=%d", attempts.Load(), completed.Load())
		}
	})
}

func TestSenderPoolPauseWaitersRecheckGateAndKeepBusySlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 3)
		var telemetry channelTelemetry
		var completed atomic.Int64
		config := testConfig(t).Sender
		pool := startSenderPool(
			context.Background(), batches, &telemetry, &completed,
			2, config.API, config.Retry, nil,
		)
		pool.pause()
		ids := make(chan string, 3)
		pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
			ids <- batch[0].ClientID
			return senderAttemptSuccess
		}
		batches <- []Transaction{{ClientID: "A"}}
		batches <- []Transaction{{ClientID: "B"}}
		batches <- []Transaction{{ClientID: "C"}}
		synctest.Wait()
		pool.reconcile(1)
		if got := pool.aggregateSnapshot(); got.inFlightWorkers != 2 || got.drainingWorkers != 1 || len(batches) != 1 {
			t.Fatalf("busy downscale = %+v queued=%d", got, len(batches))
		}
		pool.reconcile(2)
		// Close the old gate and install a new Pause before any waiter can recheck it.
		pool.mu.Lock()
		close(pool.resume)
		pool.resume = make(chan struct{})
		pool.mu.Unlock()
		synctest.Wait()
		if len(ids) != 0 || completed.Load() != 0 {
			t.Fatal("an old Resume wake bypassed the new gate")
		}
		pool.resumeRun()
		synctest.Wait()
		got := []string{<-ids, <-ids, <-ids}
		slices.Sort(got)
		if !slices.Equal(got, []string{"A", "B", "C"}) || completed.Load() != 3 {
			t.Fatalf("resumed batches=%v completed=%d", got, completed.Load())
		}
		pool.pause()
		batches <- []Transaction{{ClientID: "canceled"}}
		synctest.Wait()
		<-pool.stop()
		if completed.Load() != 3 || pool.aggregateSnapshot().liveWorkers != 0 {
			t.Fatal("cancel did not join gate waiters without delivery")
		}
	})
}
