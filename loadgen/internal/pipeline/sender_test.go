package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestSenderPoolRetriesHTTPInputErrorsUntilSuccess(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 4 {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			batches := make(chan []Transaction)
			var channel batchQueue
			channel.ch = batches
			channel.batchSize = 0

			config := testSenderOptions()
			config.RetryDelays = []time.Duration{1 * time.Millisecond}
			config.SenderURL = server.URL
			pool := startSenderPool(context.Background(), &channel, 1, config, nil, zap.NewNop(), nil)
			batches <- []Transaction{{ClientID: "invalid"}}
			close(batches)
			<-pool.done
			if got := pool.completed.Load(); got != 1 {
				t.Fatalf("completed=%d, want 1", got)
			}
			if requests.Load() != 4 {
				t.Fatalf("HTTP requests = %d, want 4", requests.Load())
			}
		})
	}
}

func TestSenderPoolReusesConnectionsAcrossConcurrentWaves(t *testing.T) {
	for _, requestsPerWave := range []int{4, 112} {
		t.Run(fmt.Sprintf("workers=%d", requestsPerWave), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			entered := make(chan struct{}, requestsPerWave)
			release := make(chan struct{})
			var connections atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				entered <- struct{}{}
				select {
				case <-release:
					_, _ = io.WriteString(w, "retry")
				case <-r.Context().Done():
				}
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			defer close(release)

			batches := make(chan []Transaction)
			var telemetry batchQueue
			telemetry.ch = batches
			telemetry.batchSize = 0

			config := testSenderOptions()
			config.SenderURL = server.URL
			config.SenderMaxConns = requestsPerWave
			pool := startSenderPool(ctx, &telemetry, requestsPerWave, config, nil, zap.NewNop(), nil)
			defer func() {
				cancel()
				<-pool.done
			}()

			for wave := 1; wave <= 2; wave++ {
				results := make(chan senderAttemptOutcome, requestsPerWave)
				for range requestsPerWave {
					go func() {
						results <- pool.attempt(ctx, []Transaction{{ClientID: "client"}}, 1)
					}()
				}
				for range requestsPerWave {
					select {
					case <-entered:
					case <-ctx.Done():
						t.Fatalf("wave %d did not reach %d simultaneous requests", wave, requestsPerWave)
					}
				}
				for range requestsPerWave {
					select {
					case release <- struct{}{}:
					case <-ctx.Done():
						t.Fatalf("wave %d did not release all responses", wave)
					}
				}
				for range requestsPerWave {
					select {
					case got := <-results:
						if got != senderAttemptFailure {
							t.Errorf("wave %d outcome = %d, want failure for HTTP 200", wave, got)
						}
					case <-ctx.Done():
						t.Fatalf("wave %d did not finish reading all responses", wave)
					}
				}
				got := connections.Load()
				t.Logf("wave %d total TCP connections = %d", wave, got)
				if got != int32(requestsPerWave) {
					t.Fatalf("wave %d total new connections = %d, want %d", wave, got, requestsPerWave)
				}
			}
		})
	}
}

func TestSenderPoolEntersBackoffWhenReceiverTimesOut(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	batches := make(chan []Transaction)
	var telemetry batchQueue
	telemetry.ch = batches
	telemetry.batchSize = 0

	config := testSenderOptions()
	config.SenderURL = server.URL
	config.SenderTimeout = 50 * time.Millisecond
	config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
	backoff := make(chan struct{}, 1)
	config.RandomIntN = func(n int) int {
		// retry.delay runs after the worker enters backoff.
		backoff <- struct{}{}
		return n / 2
	}
	pool := startSenderPool(ctx, &telemetry, 1, config, nil, zap.NewNop(), nil)
	defer func() {
		cancel()
		<-pool.done
	}()
	batches <- []Transaction{{ClientID: "client"}}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("receiver did not get the request")
	}
	select {
	case <-backoff:
	case <-ctx.Done():
		t.Fatal("Sender did not enter backoff after the HTTP timeout")
	}
	if got := pool.snapshot(); got.BackoffWorkers != 1 {
		t.Fatalf("after HTTP timeout: %+v", got)
	}
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

func TestSenderPoolKeepsAcceptedBatchOwnedAcrossDownscale(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		name := "downsize then enqueue"
		if concurrent {
			name = "concurrent enqueue and downsize"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				batches := make(chan []Transaction, 1)
				queue := &batchQueue{ch: batches}

				attempted := make(chan string, 2)
				release := make(chan struct{})
				attempt := func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
					attempted <- batch[0].ClientID
					<-release
					return senderAttemptSuccess
				}
				pool := startSenderPool(t.Context(), queue, 2, testSenderOptions(), attempt, zap.NewNop(), nil)
				batches <- []Transaction{{ClientID: "A"}}
				if id := <-attempted; id != "A" {
					t.Fatalf("first attempt=%q,want A", id)
				}
				// At quiescence the old intake is waiting for the next batch on behalf of the idle worker.
				synctest.Wait()
				if concurrent {
					start := make(chan struct{})
					sent, resized := make(chan struct{}), make(chan struct{})
					go func() { <-start; batches <- []Transaction{{ClientID: "B"}}; close(sent) }()
					go func() { <-start; pool.reconcile(1); close(resized) }()
					close(start)
					<-sent
					<-resized
				} else {
					pool.reconcile(1)
					synctest.Wait()
					batches <- []Transaction{{ClientID: "B"}}
				}
				synctest.Wait()
				state := pool.snapshot()
				received := queue.snapshot(time.Now()).ReceivedBatchesTotal
				depth := len(batches)
				bAttempted := false
				select {
				case id := <-attempted:
					bAttempted = id == "B"
				default:
				}
				owned := received == 1 && depth == 1 && !bAttempted && state.InFlightWorkers == 1 ||
					received == 2 && depth == 0 && bAttempted && state.InFlightWorkers == 2
				if !concurrent {
					owned = owned && received == 1 && depth == 1
				}
				t.Logf("held A: received=%d queued=%d B_attempted=%t snapshot=%+v", received, depth, bAttempted, state)
				close(release)
				close(batches)
				<-pool.done
				if !owned {
					t.Error("B belongs to neither the queue nor an attempting worker")
				}
				if pool.completed.Load() != 2 {
					t.Fatalf("completed=%d,want 2", pool.completed.Load())
				}
				if !bAttempted {
					if id := <-attempted; id != "B" {
						t.Fatalf("delivered=%q,want B", id)
					}
				}
				select {
				case id := <-attempted:
					t.Fatalf("duplicate delivery of %q", id)
				default:
				}
			})
		})
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
	synctest.Test(t, func(t *testing.T) {
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

		config := testSenderOptions()
		config.RetryDelays = []time.Duration{1 * time.Millisecond}
		var attempts atomic.Int64
		attempt := func(context.Context, []Transaction, int) senderAttemptOutcome {
			if attempts.Add(1) == 2 {
				return senderAttemptSuccess
			}
			return senderAttemptFailure
		}
		pool := startSenderPool(context.Background(), &channel, 1, config, attempt, logger, nil)

		batches <- []Transaction{{ClientID: clientMarker}}
		close(batches)
		<-pool.done
		if got := pool.completed.Load(); got != 1 {
			t.Fatalf("completed=%d, want 1", got)
		}

		line := output.String()
		if !strings.Contains(line, "batch_delivery_retry") {
			t.Fatalf("retry event was not written")
		}
		if strings.Contains(line, clientMarker) {
			t.Fatal("batch ClientID was written to the retry log")
		}
	})
}

func TestSenderPoolRetriesFailureUntilSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0

		config := testSenderOptions()
		config.RetryDelays = []time.Duration{1 * time.Millisecond}
		var attempts atomic.Int64
		attempt := func(context.Context, []Transaction, int) senderAttemptOutcome {
			if attempts.Add(1) == 4 {
				return senderAttemptSuccess
			}
			return senderAttemptFailure
		}
		pool := startSenderPool(context.Background(), &channel, 1, config, attempt, zap.NewNop(), nil)

		batches <- []Transaction{{ClientID: "invalid"}}
		close(batches)
		<-pool.done
		if got := pool.completed.Load(); got != 1 {
			t.Fatalf("completed=%d, want 1", got)
		}
		if attempts.Load() != 4 {
			t.Fatalf("failure attempts=%d, want 4", attempts.Load())
		}
	})
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

	config := testSenderOptions()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	var attempts atomic.Int64
	attempt := func(ctx context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		attempts.Add(1)
		close(entered)
		<-ctx.Done()
		return senderAttemptCanceled
	}
	pool := startSenderPool(ctx, &channel, 1, config, attempt, zap.NewNop(), nil)

	batches <- []Transaction{{ClientID: "canceled"}}
	<-entered
	cancel()
	<-pool.done
	if attempts.Load() != 1 {
		t.Fatalf("canceled attempts = %d, want 1", attempts.Load())
	}
	if pool.completed.Load() != 0 {
		t.Fatalf("canceled batch was counted as completed: consumed=%d", pool.completed.Load())
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

		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		attempt := func(context.Context, []Transaction, int) senderAttemptOutcome {
			return senderAttemptFailure
		}
		pool := startSenderPool(parent, &channel, 2, config, attempt, zap.NewNop(), nil)

		batches <- []Transaction{{ClientID: "canceled-parent"}}
		synctest.Wait()
		if got := pool.snapshot(); got.BackoffWorkers != 1 || got.IdleWorkers != 1 {
			t.Fatalf("want one worker in backoff and one waiting on open input: %+v", got)
		}
		cancel()
		<-pool.done
		pool.reconcile(3)
		if got := pool.snapshot(); got.LiveWorkers != 0 || pool.nextWorkerID != 2 {
			t.Fatalf("workers started after cancellation: %+v started=%d", got, pool.nextWorkerID)
		}
		if pool.completed.Load() != 0 {
			t.Fatalf("parent-canceled batch was counted as completed: consumed=%d", pool.completed.Load())
		}
	})
}

func TestSenderPoolBackpressureWhenAllWorkersRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0

		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		config.RetryJitterPercent = 0
		attempt := func(_ context.Context, _ []Transaction, attempt int) senderAttemptOutcome {
			if attempt == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}
		pool := startSenderPool(context.Background(), &channel, 2, config, attempt, zap.NewNop(), nil)

		batches <- []Transaction{{ClientID: "first"}}
		batches <- []Transaction{{ClientID: "second"}}
		synctest.Wait()
		batches <- []Transaction{{ClientID: "third"}}
		if received := channel.snapshot(time.Now()).ReceivedBatchesTotal; received != 2 || len(batches) != 1 {
			t.Fatalf("all-worker backpressure received=%d queued=%d, want 2 and 1", received, len(batches))
		}

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if pool.completed.Load() != 3 {
			t.Fatalf("completed=%d, want 3", pool.completed.Load())
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

	config := testSenderOptions()
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	attempt := func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		entered <- struct{}{}
		<-release
		return senderAttemptSuccess
	}
	pool := startSenderPool(context.Background(), &channel, 2, config, attempt, zap.NewNop(), nil)

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
	if got := pool.completed.Load(); got != 3 {
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
		receive := []chan struct{}{make(chan struct{}), make(chan struct{})}
		releaseA := make(chan struct{})
		releaseB := make(chan struct{})
		attempted := make(chan string, 3)
		attempt := func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
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
		pool := startSenderPool(context.Background(), &telemetry, 2, config, attempt, logger, func(worker *senderWorker) {
			if worker.id < len(receive) {
				<-receive[worker.id]
			}
		})

		close(receive[0])
		batches <- []Transaction{{ClientID: "A"}}
		synctest.Wait()
		close(receive[1])
		batches <- []Transaction{{ClientID: "B"}}
		synctest.Wait()
		pool.reconcile(1)
		close(releaseB)
		<-exiting
		pool.reconcile(2)
		if got := pool.snapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 0 {
			t.Fatalf("replacement during exit = %+v, want two live active workers", got)
		}
		pool.mu.Lock()
		replaced := pool.workers[0].id == 0 && pool.workers[1].id == 2
		pool.mu.Unlock()
		if !replaced {
			t.Fatal("growth must retain A's worker and replace B's already removed worker")
		}

		// A still owns the other slot; C must reach the replacement before B exits.
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
		if !slices.Equal(delivered, []string{"A", "B", "C"}) || pool.completed.Load() != 3 {
			t.Fatalf("delivered batches = %v, completed = %d, want A, B and C once each", delivered, pool.completed.Load())
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

				config := testSenderOptions()
				config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
				config.RetryJitterPercent = 0
				receive := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
				release := make(chan struct{})
				attempt := func(_ context.Context, batch []Transaction, attempt int) senderAttemptOutcome {
					if test.retryB && batch[0].ClientID == "B" && attempt == 1 {
						return senderAttemptFailure
					}
					<-release
					return senderAttemptSuccess
				}
				pool := startSenderPool(context.Background(), &telemetry, 3, config, attempt, zap.NewNop(), func(worker *senderWorker) {
					<-receive[worker.id]
				})
				for index, id := range []string{"A", "B", "C"} {
					close(receive[index])
					batches <- []Transaction{{ClientID: id}}
					synctest.Wait()
				}

				pool.reconcile(1)
				pool.reconcile(2)
				during := pool.snapshot()
				pool.mu.Lock()
				wantDrainingID := 2
				if test.retryB {
					wantDrainingID = 1
				}
				for _, worker := range pool.workers {
					if worker.draining != (worker.id == wantDrainingID) {
						t.Errorf("worker %d draining=%v, want only worker %d draining", worker.id, worker.draining, wantDrainingID)
					}
				}
				pool.mu.Unlock()

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
				if after.LiveWorkers != 2 || after.DrainingWorkers != 0 || pool.completed.Load() != 3 {
					t.Errorf("after drain = %+v, completed = %d, want two active workers and three completed",
						after, pool.completed.Load())
				}
			})
		})
	}
}

func TestSenderPoolDownscaleReturnsBeforeIdleWorkerExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newBatchQueue(1, 1)

		exiting, releaseExit := make(chan struct{}), make(chan struct{})
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
		attempt := func(context.Context, []Transaction, int) senderAttemptOutcome { return senderAttemptSuccess }
		pool := startSenderPool(t.Context(), queue, 2, testSenderOptions(), attempt, logger, nil)
		synctest.Wait()
		returned := make(chan struct{})
		go func() { pool.reconcile(1); close(returned) }()
		<-exiting
		<-returned
		got := pool.snapshot()
		if got.LiveWorkers != 1 || got.DrainingWorkers != 0 {
			t.Fatalf("snapshot while exit held: %+v", got)
		}
		queue.ch <- []Transaction{{ClientID: "after-scale-down"}}
		synctest.Wait()
		if pool.completed.Load() != 1 {
			t.Fatal("remaining worker cannot deliver while exit held")
		}
		close(releaseExit)
		close(queue.ch)
		<-pool.done
	})
}

func TestSenderPoolReplacementRestoresDesiredWorkerCount(t *testing.T) {
	batches := make(chan []Transaction)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 0

	config := testSenderOptions()
	pool := startSenderPool(context.Background(), &channel, 2, config, nil, zap.NewNop(), nil)

	pool.reconcile(1)
	pool.reconcile(2)
	if snapshot := pool.snapshot(); snapshot.LiveWorkers != 2 || snapshot.IdleWorkers != 2 {
		t.Fatalf("replacement workers = %+v, want two idle workers", snapshot)
	}
	close(batches)
	<-pool.done
}

func TestSenderPoolEachDrainingWorkerAcceptsAtMostOneNewBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newBatchQueue(4, 1)

		receive := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		atReceive := make(chan int, 3)
		attempted := make(chan string, 4)
		release := make(chan struct{})
		attempt := func(_ context.Context, batch []Transaction, _ int) senderAttemptOutcome {
			attempted <- batch[0].ClientID
			<-release
			return senderAttemptSuccess
		}
		pool := startSenderPool(t.Context(), queue, 3, testSenderOptions(), attempt, zap.NewNop(), func(worker *senderWorker) {
			atReceive <- worker.id
			<-receive[worker.id]
			// Force the legal receive branch of the select after marking this worker.
			// Other workers remain held, so each batch is attributed to a known worker.
			select {
			case <-worker.wake:
			default:
			}
		})
		for range 3 {
			<-atReceive
		}
		pool.reconcile(1)
		for _, id := range []string{"worker-1", "worker-2", "later-C", "later-D"} {
			queue.ch <- []Transaction{{ClientID: id}}
		}
		for _, id := range []int{1, 2} {
			close(receive[id])
			if got := <-attempted; got != fmt.Sprintf("worker-%d", id) {
				t.Fatalf("worker %d accepted %q", id, got)
			}
		}
		synctest.Wait()
		if got := pool.snapshot(); got.LiveWorkers != 3 || got.DrainingInFlightWorkers != 2 {
			t.Fatalf("two marked workers must own their distinct batches: %+v", got)
		}
		close(release)
		synctest.Wait()
		if got := pool.snapshot(); got.LiveWorkers != 1 || got.DrainingWorkers != 0 || len(queue.ch) != 2 {
			t.Fatalf("draining workers consumed extra work: %+v queued=%d", got, len(queue.ch))
		}
		close(receive[0])
		close(queue.ch)
		<-pool.done
		got := []string{<-attempted, <-attempted}
		slices.Sort(got)
		if !slices.Equal(got, []string{"later-C", "later-D"}) || pool.completed.Load() != 4 {
			t.Fatalf("remaining delivery=%v completed=%d", got, pool.completed.Load())
		}
	})
}

func TestSenderPoolRetainsBatchBeyondConfiguredDelayList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 0

		config := testSenderOptions()
		config.RetryJitterPercent = 0
		var attempts atomic.Int64
		previous := time.Now()
		delays := make(chan time.Duration, 6)
		attempt := func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
			if attempts.Load() != 0 {
				delays <- time.Since(previous)
			}
			previous = time.Now()
			if attempts.Add(1) == 7 {
				return senderAttemptSuccess
			}
			return senderAttemptFailure
		}
		pool := startSenderPool(context.Background(), &channel, 1, config, attempt, zap.NewNop(), nil)
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

	config := testSenderOptions()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	attempt := func(_ context.Context, _ []Transaction, _ int) senderAttemptOutcome {
		close(entered)
		<-release
		return senderAttemptSuccess
	}
	pool := startSenderPool(ctx, &channel, 1, config, attempt, zap.NewNop(), nil)
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
	if pool.completed.Load() != 1 || pool.snapshot().LiveWorkers != 0 {
		t.Fatalf("after join: consumed=%d live workers=%d", pool.completed.Load(), pool.snapshot().LiveWorkers)
	}
}

func TestSenderPoolRetryDeadlineSurvivesReconcile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction)
		var telemetry batchQueue
		telemetry.ch = batches
		telemetry.batchSize = 0

		config := testSenderOptions()
		config.RetryDelays = []time.Duration{3_600_000 * time.Millisecond}
		config.RetryJitterPercent = 0
		ctx, cancel := context.WithCancel(t.Context())
		var attempts atomic.Int64
		attempt := func(_ context.Context, batch []Transaction, number int) senderAttemptOutcome {
			if batch[0].ClientID != "held" || int64(number) != attempts.Load()+1 {
				t.Fatal("retry changed batch or attempt number")
			}
			attempts.Add(1)
			if number == 1 {
				return senderAttemptFailure
			}
			return senderAttemptSuccess
		}
		pool := startSenderPool(
			ctx, &telemetry,
			1, config, attempt, zap.NewNop(), nil,
		)
		defer func() {
			cancel()
			<-pool.done
		}()
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
		if attempts.Load() != 2 || pool.completed.Load() != 1 {
			t.Fatalf("at deadline attempts=%d completed=%d", attempts.Load(), pool.completed.Load())
		}
	})
}
