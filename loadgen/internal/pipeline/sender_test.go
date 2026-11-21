package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

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
				attempt := func(_ context.Context, batch []Transaction, _ int) error {
					attempted <- batch[0].ClientID
					<-release
					return nil
				}
				config := testSenderOptions()
				config.Senders = 2
				config.Logger = zap.NewNop()
				config.Deliver = attempt
				pool := startSenderPool(t.Context(), queue, config, nil)
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
				state := pool.testSnapshot()
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
		attempt := func(context.Context, []Transaction, int) error {
			if attempts.Add(1) == 2 {
				return nil
			}
			return io.ErrUnexpectedEOF
		}
		config.Senders = 1
		config.Logger = logger
		config.Deliver = attempt
		pool := startSenderPool(context.Background(), &channel, config, nil)

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
		attempt := func(context.Context, []Transaction, int) error {
			if attempts.Add(1) == 4 {
				return nil
			}
			return io.ErrUnexpectedEOF
		}
		config.Senders = 1
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(context.Background(), &channel, config, nil)

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

func TestSenderPoolSnapshotStatesAreIndependent(t *testing.T) {
	want := []SenderWorkerState{
		{Activity: SenderIdle}, {Activity: SenderIdle, Draining: true},
		{Activity: SenderInFlight}, {Activity: SenderInFlight, Draining: true},
		{Activity: SenderBackoff}, {Activity: SenderBackoff, Draining: true},
		{Activity: SenderInFlight},
	}
	pool := &senderPool{ctx: t.Context()}
	for _, state := range want {
		pool.workers = append(pool.workers, &senderWorker{activity: state.Activity, draining: state.Draining})
	}
	first, second := pool.snapshot(), pool.snapshot()
	assertWorkerStateMultiset(t, first.WorkerStates, want)
	first.WorkerStates[0] = SenderWorkerState{Activity: SenderBackoff, Draining: true}
	assertWorkerStateMultiset(t, second.WorkerStates, want)
	assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, want)
	pool.reconcile(0)
	pool.mu.Lock()
	pool.workers[0].activity = SenderBackoff
	pool.mu.Unlock()
	assertWorkerStateMultiset(t, second.WorkerStates, want)
	for _, state := range pool.snapshot().WorkerStates {
		if !state.Draining {
			t.Fatalf("reconcile did not drain live state: %+v", state)
		}
	}
	empty := (&senderPool{}).snapshot().WorkerStates
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty states = %#v, want non-nil []", empty)
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
	attempt := func(ctx context.Context, _ []Transaction, _ int) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	config.Senders = 1
	config.Logger = zap.NewNop()
	config.Deliver = attempt
	pool := startSenderPool(ctx, &channel, config, nil)
	if pool.client != nil {
		t.Fatal("injected delivery created an HTTP client")
	}

	batches <- []Transaction{{ClientID: "canceled"}}
	<-entered
	cancel()
	<-pool.done
	if pool.completed.Load() != 0 {
		t.Fatalf("canceled batch was counted as completed: consumed=%d", pool.completed.Load())
	}
}

func TestSenderPoolCanceledBeforeDeliveryMakesNoAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	pool := &senderPool{ctx: ctx, deliver: func(context.Context, []Transaction, int) error {
		calls++
		return nil
	}}
	if pool.processBatch(&senderWorker{}, []Transaction{{ClientID: "canceled"}}) {
		t.Fatal("canceled delivery reported success")
	}
	if calls != 0 {
		t.Fatalf("delivery calls=%d, want 0", calls)
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
		attempt := func(context.Context, []Transaction, int) error {
			return io.ErrUnexpectedEOF
		}
		config.Senders = 2
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(parent, &channel, config, nil)

		batches <- []Transaction{{ClientID: "canceled-parent"}}
		synctest.Wait()
		if got := pool.testSnapshot(); got.BackoffWorkers != 1 || got.IdleWorkers != 1 {
			t.Fatalf("want one worker in backoff and one waiting on open input: %+v", got)
		}
		cancel()
		<-pool.done
		pool.reconcile(3)
		if got := pool.testSnapshot(); got.LiveWorkers != 0 || pool.nextWorkerID != 2 {
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
		attempt := func(_ context.Context, _ []Transaction, attempt int) error {
			if attempt == 1 {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		config.Senders = 2
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(context.Background(), &channel, config, nil)

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
	attempt := func(_ context.Context, _ []Transaction, _ int) error {
		entered <- struct{}{}
		<-release
		return nil
	}
	config.Senders = 2
	config.Logger = zap.NewNop()
	config.Deliver = attempt
	pool := startSenderPool(context.Background(), &channel, config, nil)

	batches <- []Transaction{{ClientID: "A"}}
	batches <- []Transaction{{ClientID: "B"}}
	<-entered
	<-entered
	drainingWorker := pool.workers[0]
	pool.reconcile(1)
	if got := pool.testSnapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 1 {
		t.Fatalf("after scale down = %+v, want two live and one draining", got)
	}
	pool.reconcile(2)
	if got := pool.testSnapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 0 {
		t.Fatalf("after reactivation = %+v, want two live and zero draining", got)
	}
	if pool.workers[0] != drainingWorker {
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
	if got := pool.testSnapshot().LiveWorkers; got != 0 {
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
		attempt := func(_ context.Context, batch []Transaction, _ int) error {
			id := batch[0].ClientID
			attempted <- id
			switch id {
			case "A":
				<-releaseA
			case "B":
				<-releaseB
			}
			return nil
		}
		config.Senders = 2
		config.Logger = logger
		config.Deliver = attempt
		pool := startSenderPool(context.Background(), &telemetry, config, func(worker *senderWorker) {
			if worker.id < len(receive) {
				<-receive[worker.id]
			}
		})

		close(receive[0])
		batches <- []Transaction{{ClientID: "B"}}
		synctest.Wait()
		close(receive[1])
		batches <- []Transaction{{ClientID: "A"}}
		synctest.Wait()
		pool.reconcile(1)
		close(releaseB)
		<-exiting
		pool.reconcile(2)
		if got := pool.testSnapshot(); got.LiveWorkers != 2 || got.DrainingWorkers != 0 {
			t.Fatalf("replacement during exit = %+v, want two live active workers", got)
		}
		pool.mu.Lock()
		replaced := pool.workers[0].id == 1 && pool.workers[1].id == 2
		pool.mu.Unlock()
		if !replaced {
			t.Fatal("growth must retain A's worker and replace B's already removed worker")
		}

		// A still owns the other slot; C must reach the replacement before B exits.
		batches <- []Transaction{{ClientID: "C"}}
		synctest.Wait()
		if got := pool.completed.Load(); got != 2 {
			t.Fatalf("B and C must complete while A is held: completed=%d", got)
		}
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
		retryA              bool
		wantDrainingBackoff int
	}{
		{name: "all in flight"},
		{name: "prefer in flight over backoff", retryA: true, wantDrainingBackoff: 1},
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
				attempt := func(_ context.Context, batch []Transaction, attempt int) error {
					if test.retryA && batch[0].ClientID == "A" && attempt == 1 {
						return io.ErrUnexpectedEOF
					}
					<-release
					return nil
				}
				config.Senders = 3
				config.Logger = zap.NewNop()
				config.Deliver = attempt
				pool := startSenderPool(context.Background(), &telemetry, config, func(worker *senderWorker) {
					<-receive[worker.id]
				})
				for index, id := range []string{"A", "B", "C"} {
					close(receive[index])
					batches <- []Transaction{{ClientID: id}}
					synctest.Wait()
				}

				pool.reconcile(1)
				pool.reconcile(2)
				during := pool.testSnapshot()
				pool.mu.Lock()
				wantDrainingID := 1
				if test.retryA {
					wantDrainingID = 0
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
				after := pool.testSnapshot()
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
		attempt := func(context.Context, []Transaction, int) error { return nil }
		config := testSenderOptions()
		config.Senders = 2
		config.Logger = logger
		config.Deliver = attempt
		pool := startSenderPool(t.Context(), queue, config, nil)
		synctest.Wait()
		returned := make(chan struct{})
		go func() { pool.reconcile(1); close(returned) }()
		<-exiting
		<-returned
		got := pool.testSnapshot()
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

func TestSenderPoolEachDrainingWorkerAcceptsAtMostOneNewBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newBatchQueue(4, 1)

		receive := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		atReceive := make(chan int, 3)
		attempted := make(chan string, 4)
		release := make(chan struct{})
		attempt := func(_ context.Context, batch []Transaction, _ int) error {
			attempted <- batch[0].ClientID
			<-release
			return nil
		}
		config := testSenderOptions()
		config.Senders = 3
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(t.Context(), queue, config, func(worker *senderWorker) {
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
		for _, id := range []string{"worker-0", "worker-1", "later-C", "later-D"} {
			queue.ch <- []Transaction{{ClientID: id}}
		}
		for _, id := range []int{0, 1} {
			close(receive[id])
			if got := <-attempted; got != fmt.Sprintf("worker-%d", id) {
				t.Fatalf("worker %d accepted %q", id, got)
			}
		}
		synctest.Wait()
		if got := pool.testSnapshot(); got.LiveWorkers != 3 || got.DrainingInFlightWorkers != 2 {
			t.Fatalf("two marked workers must own their distinct batches: %+v", got)
		}
		close(release)
		synctest.Wait()
		if got := pool.testSnapshot(); got.LiveWorkers != 1 || got.DrainingWorkers != 0 || len(queue.ch) != 2 {
			t.Fatalf("draining workers consumed extra work: %+v queued=%d", got, len(queue.ch))
		}
		pool.mu.Lock()
		remainingID := pool.workers[0].id
		pool.mu.Unlock()
		if remainingID != 2 {
			t.Fatalf("remaining worker=%d, want 2", remainingID)
		}
		close(receive[2])
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
		attempt := func(_ context.Context, _ []Transaction, _ int) error {
			if attempts.Load() != 0 {
				delays <- time.Since(previous)
			}
			previous = time.Now()
			if attempts.Add(1) == 7 {
				return nil
			}
			return io.ErrUnexpectedEOF
		}
		config.Senders = 1
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(context.Background(), &channel, config, nil)
		batches <- []Transaction{{ClientID: "failure"}}
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if snapshot := pool.testSnapshot(); snapshot.IdleWorkers != 1 || snapshot.BackoffWorkers != 0 {
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
	attempt := func(_ context.Context, _ []Transaction, _ int) error {
		close(entered)
		<-release
		return nil
	}
	config.Senders = 1
	config.Logger = zap.NewNop()
	config.Deliver = attempt
	pool := startSenderPool(ctx, &channel, config, nil)
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
	if pool.completed.Load() != 1 || pool.testSnapshot().LiveWorkers != 0 {
		t.Fatalf("after join: consumed=%d live workers=%d", pool.completed.Load(), pool.testSnapshot().LiveWorkers)
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
		attempt := func(_ context.Context, batch []Transaction, number int) error {
			if batch[0].ClientID != "held" || int64(number) != attempts.Load()+1 {
				t.Error("retry changed batch or attempt number")
				return io.ErrNoProgress
			}
			attempts.Add(1)
			if number == 1 {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		config.Senders = 1
		config.Logger = zap.NewNop()
		config.Deliver = attempt
		pool := startSenderPool(ctx, &telemetry, config, nil)
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
