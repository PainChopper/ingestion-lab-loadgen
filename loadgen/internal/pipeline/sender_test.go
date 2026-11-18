package pipeline

import (
	"bytes"
	"context"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func testSenderOptions() Options {
	return Options{
		SenderURL:          "http://127.0.0.1:8081/internal/ingest",
		SenderTimeout:      30 * time.Second,
		SenderMaxConns:     32,
		RetryDelays:        []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second},
		RetryJitterPercent: 20,
	}
}

func TestRetryPolicyUsesControlledJitter(t *testing.T) {
	options := testSenderOptions()
	values := []int{0, 20, 40, 0, 20, 40}
	next := 0
	options.RandomIntN = func(n int) int {
		if n != 41 {
			t.Errorf("random upper bound=%d, want 41", n)
		}
		value := values[next]
		next++
		return value
	}
	retry := newRetryPolicy(options)
	for _, test := range []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{"first lower", 1, 200 * time.Millisecond},
		{"first midpoint", 1, 250 * time.Millisecond},
		{"first upper", 1, 300 * time.Millisecond},
		{"past list lower", 7, 4 * time.Second},
		{"past list midpoint", 7, 5 * time.Second},
		{"past list upper", 7, 6 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retry.delay(test.attempt); got != test.want {
				t.Fatalf("delay=%v, want %v", got, test.want)
			}
		})
	}
}

func TestRetryPolicySerializesInjectedRandom(t *testing.T) {
	options := testSenderOptions()
	next := 0
	options.RandomIntN = func(n int) int {
		value := next % n
		next++
		return value
	}
	retry := newRetryPolicy(options)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 32 {
				_ = retry.delay(1)
			}
		})
	}
	wg.Wait()
	if next != 512 {
		t.Fatalf("random calls=%d, want 512", next)
	}
}

func TestSenderRetryDurationSaturates(t *testing.T) {
	const maximumMS = math.MaxInt64 / (int64(time.Millisecond) * 120 / 100)
	base := time.Duration(maximumMS) * time.Millisecond
	if got := senderRetryDuration(base, 20); got <= 0 || got == time.Duration(math.MaxInt64) {
		t.Fatalf("maximum safe retry duration=%s", got)
	}
	if got := senderRetryDuration(base+time.Millisecond, 20); got != time.Duration(math.MaxInt64) {
		t.Fatalf("overflow retry duration=%s, want saturated maximum", got)
	}
}

func TestSenderPoolRetryLogWritesEventWithoutClientID(t *testing.T) {
	var output bytes.Buffer
	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(&output), zap.InfoLevel,
	))

	clientMarker := "client-id-marker"

	batches := make(chan []Transaction)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	config.RetryDelays = []time.Duration{1 * time.Millisecond}
	pool := startSenderPool(context.Background(), &channel, &consumed, 1, config, logger)
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 2 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: clientMarker}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	close(batches)
	<-pool.done

	line := output.String()
	if !strings.Contains(line, "batch_delivery_retry") {
		t.Fatalf("retry event was not written")
	}
	if strings.Contains(line, clientMarker) {
		t.Fatal("batch ClientID was written to the retry log")
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
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	config.RetryDelays = []time.Duration{1 * time.Millisecond}
	pool := startSenderPool(context.Background(), &channel, &consumed, 1, config, zap.NewNop())
	var attempts atomic.Int64
	pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
		if attempts.Add(1) == 4 {
			return senderAttemptSuccess
		}
		return senderAttemptFailure
	}

	batches <- []Transaction{{ClientID: "invalid"}}
	waitForSenderCondition(t, func() bool { return consumed.Load() == 1 })
	close(batches)
	<-pool.done
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

	snapshot := pool.snapshot()
	if snapshot.LiveWorkers != 6 || snapshot.IdleWorkers != 2 || snapshot.InFlightWorkers != 2 || snapshot.BackoffWorkers != 2 {
		t.Fatalf("activity partition = %+v", snapshot)
	}
	if snapshot.DrainingWorkers != 3 || snapshot.DrainingIdleWorkers != 1 || snapshot.DrainingInFlightWorkers != 1 || snapshot.DrainingBackoffWorkers != 1 {
		t.Fatalf("draining intersections = %+v", snapshot)
	}
}

func TestSenderPoolCancellationStopsRetry(t *testing.T) {
	batches := make(chan []Transaction)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool := startSenderPool(ctx, &channel, &consumed, 1, config, zap.NewNop())
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
	cancel()
	<-pool.done
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
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0
		var consumed atomic.Int64
		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		pool := startSenderPool(parent, &channel, &consumed, 1, config, zap.NewNop())
		pool.attempt = func(context.Context, []Transaction, int) senderAttemptOutcome {
			return senderAttemptFailure
		}

		batches <- []Transaction{{ClientID: "canceled-parent"}}
		synctest.Wait()
		if pool.snapshot().BackoffWorkers != 1 {
			t.Fatal("Sender did not enter retry backoff")
		}
		cancel()
		<-pool.done
		if consumed.Load() != 0 {
			t.Fatalf("parent-canceled batch was counted as completed: consumed=%d", consumed.Load())
		}
	})
}

func TestSenderPoolBackpressureWhenAllWorkersRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0
		var consumed atomic.Int64
		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		config.RetryJitterPercent = 0
		pool := startSenderPool(context.Background(), &channel, &consumed, 2, config, zap.NewNop())
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
		if received := channel.snapshot(time.Now()).ReceivedBatchesTotal; received != 2 || len(batches) != 1 {
			t.Fatalf("all-worker backpressure received=%d queued=%d, want 2 and 1", received, len(batches))
		}

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if consumed.Load() != 3 {
			t.Fatalf("completed=%d, want 3", consumed.Load())
		}
		close(batches)
		<-pool.done
	})
}

func TestSenderPoolReconcilesUpDownUpWithoutLosingAcceptedBatches(t *testing.T) {
	batches := make(chan []Transaction, 3)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	pool := startSenderPool(context.Background(), &channel, &consumed, 2, config, zap.NewNop())
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
	if got := pool.snapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 1 {
		t.Fatalf("after scale down = %+v, want two live and one draining", got)
	}
	pool.reconcile(2)
	if got := pool.snapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 0 {
		t.Fatalf("after reactivation = %+v, want two live and zero draining", got)
	}
	if pool.workers[1] != drainingWorker {
		t.Fatal("scale up replaced the live draining worker")
	}
	close(release)
	batches <- []Transaction{{ClientID: "C"}}
	<-entered
	close(batches)
	<-pool.done
	if got := channel.snapshot(time.Now()).ReceivedBatchesTotal; got != 3 {
		t.Fatalf("received batches = %d, want 3", got)
	}
	if got := consumed.Load(); got != 3 {
		t.Fatalf("completed transactions = %d, want 3", got)
	}
	if got := pool.snapshot().LiveWorkers; got != 0 {
		t.Fatalf("live workers after stop = %d, want 0", got)
	}
}

func TestSenderPoolDeliversBatchWhenResizedDuringWorkerExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry batchQueue
		telemetry.ch = batches
		telemetry.batchSize = 0
		var completed atomic.Int64
		config := testSenderOptions()
		exiting := make(chan struct{})
		releaseExit := make(chan struct{})
		var firstExit atomic.Bool
		logger := zap.New(zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
			zapcore.AddSync(io.Discard), zap.DebugLevel,
		), zap.Hooks(func(entry zapcore.Entry) error {
			if entry.Message == "sender worker stopped" && firstExit.CompareAndSwap(false, true) {
				close(exiting)
				<-releaseExit
			}
			return nil
		}))
		pool := startSenderPool(context.Background(), &telemetry, &completed, 2, config, logger)
		releaseA := make(chan struct{})
		releaseB := make(chan struct{})
		attempted := make(chan string, 3)
		pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
			id := batch[0].ClientID
			attempted <- id
			switch id {
			case "A":
				<-releaseA
			case "B":
				<-releaseB
			}
			return senderAttemptSuccess
		}

		batches <- []Transaction{{ClientID: "A"}}
		synctest.Wait()
		batches <- []Transaction{{ClientID: "B"}}
		synctest.Wait()
		pool.reconcile(1)
		close(releaseB)
		<-exiting
		pool.reconcile(2)

		// A still owns the other slot. Wait until intake has assigned C or gone idle.
		batches <- []Transaction{{ClientID: "C"}}
		synctest.Wait()
		close(releaseExit)
		close(releaseA)
		close(batches)
		<-pool.done

		close(attempted)
		var delivered []string
		for id := range attempted {
			delivered = append(delivered, id)
		}
		slices.Sort(delivered)
		if !slices.Equal(delivered, []string{"A", "B", "C"}) || completed.Load() != 3 {
			t.Fatalf("delivered batches = %v, completed = %d, want A, B and C once each", delivered, completed.Load())
		}
	})
}

func TestSenderPoolReactivatesOnlyNeededWorkers(t *testing.T) {
	for _, test := range []struct {
		name                string
		retryB              bool
		wantDrainingBackoff int
	}{
		{name: "all in flight"},
		{name: "prefer in flight over backoff", retryB: true, wantDrainingBackoff: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				batches := make(chan []Transaction)
				var telemetry batchQueue
				telemetry.ch = batches
				telemetry.batchSize = 0
				var completed atomic.Int64
				config := testSenderOptions()
				config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
				config.RetryJitterPercent = 0
				pool := startSenderPool(context.Background(), &telemetry, &completed, 3, config, zap.NewNop())
				release := make(chan struct{})
				pool.attempt = func(_ context.Context, batch []Transaction, attempt int) senderAttemptOutcome {
					if test.retryB && batch[0].ClientID == "B" && attempt == 1 {
						return senderAttemptFailure
					}
					<-release
					return senderAttemptSuccess
				}
				for _, id := range []string{"A", "B", "C"} {
					batches <- []Transaction{{ClientID: id}}
					synctest.Wait()
				}

				pool.reconcile(1)
				pool.reconcile(2)
				during := pool.snapshot()

				close(release)
				time.Sleep(time.Hour)
				synctest.Wait()
				after := pool.snapshot()
				close(batches)
				<-pool.done

				if during.LiveWorkers != 3 || during.DrainingWorkers != 1 ||
					during.DrainingBackoffWorkers != test.wantDrainingBackoff {
					t.Errorf("after scale up = %+v, want three live, one draining (%d in backoff)",
						during, test.wantDrainingBackoff)
				}
				if after.LiveWorkers != 2 || after.DrainingWorkers != 0 || completed.Load() != 3 {
					t.Errorf("after drain = %+v, completed = %d, want two active workers and three completed",
						after, completed.Load())
				}
			})
		})
	}
}

func TestSenderPoolScaleDownJoinsIdleWorkerBeforeNextReceive(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	pool := startSenderPool(context.Background(), &channel, &consumed, 2, config, zap.NewNop())
	entered := make(chan string, 1)
	pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		entered <- batch[0].ClientID
		return senderAttemptSuccess
	}
	pool.reconcile(1)
	if snapshot := pool.snapshot(); snapshot.LiveWorkers != 1 || snapshot.DrainingWorkers != 0 {
		t.Fatalf("after idle downscale = %+v, want one active worker", snapshot)
	}
	batches <- []Transaction{{ClientID: "after-scale-down"}}
	if clientID := <-entered; clientID != "after-scale-down" {
		t.Fatalf("attempted batch = %q, want after-scale-down", clientID)
	}
	close(batches)
	<-pool.done
	if got := consumed.Load(); got != 1 {
		t.Fatalf("completed transactions = %d, want 1", got)
	}
}

func TestSenderPoolReplacementRestoresDesiredWorkerCount(t *testing.T) {
	batches := make(chan []Transaction)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	pool := startSenderPool(context.Background(), &channel, &consumed, 2, config, zap.NewNop())

	pool.reconcile(1)
	pool.reconcile(2)
	if snapshot := pool.snapshot(); snapshot.LiveWorkers != 2 || snapshot.IdleWorkers != 2 {
		t.Fatalf("replacement workers = %+v, want two idle workers", snapshot)
	}
	close(batches)
	<-pool.done
}

func TestSenderPoolReadyBatchCannotEnterMarkedDrainingWorker(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	pool := startSenderPool(context.Background(), &channel, &consumed, 2, config, zap.NewNop())
	entered := make(chan struct {
		clientID      string
		firstReleased bool
	}, 2)
	releaseFirst := make(chan struct{})
	pool.attempt = func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
		clientID := batch[0].ClientID
		var firstReleased bool
		if clientID == "after-mark" {
			select {
			case <-releaseFirst:
				firstReleased = true
			default:
			}
		}
		entered <- struct {
			clientID      string
			firstReleased bool
		}{clientID: clientID, firstReleased: firstReleased}
		if clientID == "first" {
			<-releaseFirst
		}
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "first"}}
	if attempt := <-entered; attempt.clientID != "first" {
		t.Fatalf("first attempted batch = %q, want first", attempt.clientID)
	}

	// Hold dispatch at the assignment lock while drain wake and a batch are both ready.
	pool.mu.Lock()
	draining := pool.workers[1]
	draining.draining = true
	draining.wake <- struct{}{}
	batches <- []Transaction{{ClientID: "after-mark"}}
	pool.mu.Unlock()
	<-draining.done
	close(releaseFirst)
	attempt := <-entered
	close(batches)
	<-pool.done
	if attempt.clientID != "after-mark" || !attempt.firstReleased {
		t.Fatalf("after-mark attempt = %+v, want after-mark after first release", attempt)
	}
	if got := consumed.Load(); got != 2 {
		t.Fatalf("completed transactions = %d, want 2", got)
	}
}

func TestSenderPoolRetainsBatchBeyondConfiguredDelayList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0
		var consumed atomic.Int64
		config := testSenderOptions()
		config.RetryJitterPercent = 0
		pool := startSenderPool(context.Background(), &channel, &consumed, 1, config, zap.NewNop())
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
		if snapshot := pool.snapshot(); snapshot.IdleWorkers != 1 || snapshot.BackoffWorkers != 0 {
			t.Fatalf("successful sender snapshot = %+v", snapshot)
		}
		close(batches)
		<-pool.done
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
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0
	var consumed atomic.Int64
	config := testSenderOptions()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool := startSenderPool(ctx, &channel, &consumed, 1, config, zap.NewNop())
	entered := make(chan struct{})
	release := make(chan struct{})
	pool.attempt = func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}
	batches <- []Transaction{{ClientID: "accepted"}}
	<-entered
	cancel()
	select {
	case <-pool.done:
		t.Fatal("pool stopped before accepted batch completed")
	default:
	}
	close(release)
	<-pool.done
	if consumed.Load() != 1 || pool.snapshot().LiveWorkers != 0 {
		t.Fatalf("after join: consumed=%d live workers=%d", consumed.Load(), pool.snapshot().LiveWorkers)
	}
}

func TestSenderPoolRetryDeadlineAndReconcileWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry batchQueue
		telemetry.ch = batches
		telemetry.batchSize = 0
		var completed atomic.Int64
		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		config.RetryJitterPercent = 0
		ctx, cancel := context.WithCancel(t.Context())
		pool := startSenderPool(
			ctx, &telemetry, &completed,
			1, config, zap.NewNop(),
		)
		defer func() {
			cancel()
			<-pool.done
		}()
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
		pool.reconcile(1)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("reconcile shortened retry delay")
		}
		time.Sleep(50*time.Minute - time.Nanosecond)
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
