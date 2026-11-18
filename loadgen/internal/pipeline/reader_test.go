package pipeline

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

type fixtureRows struct {
	read     func([]Transaction) (int, error)
	closeErr error
	closed   bool
}

func (r *fixtureRows) Read(rows []Transaction) (int, error) { return r.read(rows) }

func (r *fixtureRows) Close() error {
	r.closed = true
	return r.closeErr
}

type fixtureFile struct {
	rows     RowReader
	openErr  error
	onOpen   func()
	closeErr error
	closed   bool
}

func (f *fixtureFile) OpenRows() (RowReader, error) {
	if f.onOpen != nil {
		f.onOpen()
	}
	return f.rows, f.openErr
}

func (f *fixtureFile) Close() error {
	f.closed = true
	return f.closeErr
}

func TestDiscoverSourcePreservesGlobCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "[")
	_, err := DiscoverSource(path)
	var sourceError *SourceError
	if !errors.Is(err, filepath.ErrBadPattern) || !errors.As(err, &sourceError) ||
		sourceError.Op != "glob" || sourceError.Path != "[" {
		t.Fatalf("glob error = %v (%+v), want wrapped bad pattern", err, sourceError)
	}
}

type errorCloser struct {
	err    error
	closed bool
}

func (closer *errorCloser) Close() error {
	closer.closed = true
	return closer.err
}

func startTestReaderPool(
	t *testing.T,
	ctx context.Context,
	path string,
	batchSize, workers int,
	batches chan []Transaction,
	telemetry *readerTelemetry,
	channel *batchQueue,
) *readerPool {
	t.Helper()
	source, err := DiscoverSource(path)
	if err != nil {
		t.Fatal(err)
	}
	channel.ch = batches
	channel.batchSize = batchSize
	return startReaderPool(ctx, source, batchSize, workers, channel, telemetry, nil, nil, zap.NewNop())
}

func TestReaderPoolOwnsUniqueFilesAndEmitsPerFileResiduals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		for _, fixture := range []struct{ name, id string }{{"a.parquet", "a"}, {"b.parquet", "b"}} {
			rows := []Transaction{{ClientID: fixture.id}, {ClientID: fixture.id}, {ClientID: fixture.id}}
			if err := parquet.WriteFile(filepath.Join(dir, fixture.name), rows); err != nil {
				t.Fatalf("write %s: %v", fixture.name, err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction)
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 2
		pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 2, 2, batches, &telemetry, &channel)
		assertBlockedSenders(t, &channel, 2)
		pool.mu.Lock()
		owned := len(pool.active)
		issued := pool.nextFile
		pool.mu.Unlock()
		if owned != 2 || issued != 2 {
			t.Fatalf("concurrent file ownership: active=%d issued=%d, want 2 each", owned, issued)
		}
		residuals := map[string]bool{}
		for len(residuals) < 2 {
			select {
			case batch := <-batches:
				if len(batch) != 1 && len(batch) != 2 {
					t.Fatalf("batch size = %d, want 1 or 2", len(batch))
				}
				for _, row := range batch {
					if row.ClientID != batch[0].ClientID {
						t.Fatalf("batch crossed file boundary: %+v", batch)
					}
				}
				if len(batch) == 1 {
					residuals[batch[0].ClientID] = true
				}
			case <-time.After(time.Second):
				t.Fatal("reader pool did not emit per-file residuals")
			}
		}
		if !residuals["a"] || !residuals["b"] {
			t.Fatalf("per-file residuals = %v, want both files", residuals)
		}
		cancel()
		<-pool.done
	})
}

func TestReaderPoolAppendRowsSplitsShortReadsAtBatchBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batches := make(chan []Transaction, 2)
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 2
	worker := &readerWorker{busy: true}
	pool := &readerPool{
		ctx:       ctx,
		batchSize: 2,

		channel: &channel,
		workers: []*readerWorker{worker},
	}

	batch := make([]Transaction, 0, pool.batchSize)
	batch, sent := pool.appendRows(worker, batch, []Transaction{{ClientID: "one"}})
	if !sent {
		t.Fatal("first short read was not sent")
	}
	batch, sent = pool.appendRows(worker, batch, []Transaction{{ClientID: "two"}, {ClientID: "three"}})
	if !sent {
		t.Fatal("second short read was not sent")
	}
	if len(batch) != 1 {
		t.Fatalf("EOF residual size = %d, want 1", len(batch))
	}
	if !pool.sendBatch(worker, batch) {
		t.Fatal("EOF residual was not sent")
	}

	first := <-batches
	second := <-batches
	if len(first) != 2 || len(second) != 1 {
		t.Fatalf("batch sizes = %d, %d, want 2, 1", len(first), len(second))
	}
	if first[0].ClientID != "one" || first[1].ClientID != "two" || second[0].ClientID != "three" {
		t.Fatalf("batches = %+v, %+v, want rows in read order", first, second)
	}
}

func TestReaderPoolStopReadingSendsPendingPartialBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := newBatchQueue(1, 2)
		allowRead := make(chan struct{})
		var pool *readerPool
		openFile := func(string) (ReaderFile, error) {
			return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
				<-allowRead
				if !pool.canRead() {
					t.Fatal("Reader fetched new rows after admission stopped")
				}
				rows[0] = Transaction{ClientID: "already read"}
				pool.stopReading()
				return 1, nil
			}}}, nil
		}
		pool = startReaderPool(t.Context(), Source{files: []string{"one"}}, 2, 3,
			queue, &readerTelemetry{}, openFile, nil, zap.NewNop())
		synctest.Wait()
		if got := pool.snapshot(); got.ReadingWorkers != 1 || got.IdleWorkers != 2 {
			t.Fatalf("before StopReading: %+v", got)
		}
		close(allowRead)
		<-pool.done
		if t.Context().Err() != nil || pool.snapshot().LiveWorkers != 0 {
			t.Fatal("StopReading did not stop all workers with live parent context")
		}
		select {
		case batch := <-queue.ch:
			if len(batch) != 1 || batch[0].ClientID != "already read" {
				t.Fatalf("partial batch = %+v", batch)
			}
		default:
			t.Fatal("partial batch was not forwarded after admission stopped")
		}
	})
}

func TestReaderPoolImmediateSendKeepsWorkerReading(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var telemetry readerTelemetry
	var channel batchQueue
	channel.ch = batches
	channel.batchSize = 1
	worker := &readerWorker{busy: true}
	pool := &readerPool{
		ctx: context.Background(),

		telemetry: &telemetry,
		channel:   &channel,
		workers:   []*readerWorker{worker},
	}

	if !pool.sendBatch(worker, []Transaction{{ClientID: "a"}}) {
		t.Fatal("immediate send failed")
	}
	if worker.blocked {
		t.Fatal("immediate send marked worker blocked")
	}
	if snapshot := pool.snapshot(); snapshot.ReadingWorkers != 1 {
		t.Fatalf("immediate send snapshot = %+v, want one reading worker", snapshot)
	}
	if measurements := channel.snapshot(time.Now()); measurements.BlockedSenders != 0 ||
		measurements.SentBatchesTotal != 1 || measurements.SentTransactionsTotal != 1 {
		t.Fatalf("immediate send channel measurements = %+v", measurements)
	}
}

func TestReaderPoolBlockedSendReturnsToReadingAfterDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 1
		worker := &readerWorker{draining: true, busy: true}
		pool := &readerPool{
			ctx:     context.Background(),
			workers: []*readerWorker{worker},

			telemetry: &telemetry,
			channel:   &channel,
		}
		sent := make(chan bool, 1)
		go func() {
			sent <- pool.sendBatch(worker, []Transaction{{ClientID: "a"}})
		}()

		assertBlockedSender(t, &channel)
		pool.mu.Lock()
		blocked := worker.blocked
		pool.mu.Unlock()
		if !blocked {
			t.Fatal("blocked send did not mark worker blocked")
		}
		if snapshot := pool.snapshot(); snapshot.BlockedWorkers != 1 || snapshot.DrainingBlockedWorkers != 1 {
			t.Fatalf("blocked draining snapshot = %+v", snapshot)
		}

		<-batches
		select {
		case ok := <-sent:
			if !ok {
				t.Fatal("blocked send was cancelled after drain")
			}
		case <-time.After(time.Second):
			t.Fatal("blocked send did not complete after drain")
		}
		pool.mu.Lock()
		blocked = worker.blocked
		pool.mu.Unlock()
		if blocked {
			t.Fatal("drained send left worker blocked")
		}
		if snapshot := pool.snapshot(); snapshot.ReadingWorkers != 1 || snapshot.DrainingReadingWorkers != 1 {
			t.Fatalf("drained snapshot = %+v", snapshot)
		}
		if measurements := channel.snapshot(time.Now()); measurements.BlockedSenders != 0 ||
			measurements.SentBatchesTotal != 1 || measurements.SentTransactionsTotal != 1 {
			t.Fatalf("drained send channel measurements = %+v", measurements)
		}
	})
}

func TestReaderPoolCanceledBlockedSendDoesNotRestoreReading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 1
		worker := &readerWorker{busy: true}
		pool := &readerPool{
			ctx: ctx,

			telemetry: &telemetry,
			channel:   &channel,
			workers:   []*readerWorker{worker},
		}
		sent := make(chan bool, 1)
		go func() {
			sent <- pool.sendBatch(worker, []Transaction{{ClientID: "a"}})
		}()

		synctest.Wait()
		if measurements := channel.snapshot(time.Now()); measurements.BlockedSenders != 1 {
			t.Fatalf("send did not block: %+v", measurements)
		}
		const blockedFor = 25 * time.Millisecond
		time.Sleep(blockedFor)
		cancel()
		select {
		case ok := <-sent:
			if ok {
				t.Fatal("cancelled blocked send succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled blocked send did not complete")
		}
		pool.mu.Lock()
		blocked := worker.blocked
		pool.mu.Unlock()
		if blocked {
			t.Fatal("cancelled send left worker blocked")
		}
		if snapshot := pool.snapshot(); snapshot.ReadingWorkers != 1 || snapshot.BlockedWorkers != 0 {
			t.Fatalf("cancelled send snapshot = %+v", snapshot)
		}
		if measurements := channel.snapshot(time.Now()); measurements.BlockedSenders != 0 || measurements.BlockedMS != blockedFor.Milliseconds() ||
			measurements.SentBatchesTotal != 0 || measurements.SentTransactionsTotal != 0 {
			t.Fatalf("cancelled send channel measurements = %+v", measurements)
		}
	})
}

func TestReaderPoolConcurrentSendsUseLastFreeSlotOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		batches := make(chan []Transaction, 1)
		var telemetry batchQueue
		telemetry.ch = batches
		telemetry.batchSize = 1
		pool := &readerPool{ctx: ctx, channel: &telemetry}
		sent := make(chan bool, 2)
		for range 2 {
			go func() { sent <- pool.sendBatch(&readerWorker{}, []Transaction{{}}) }()
		}
		assertBlockedSender(t, &telemetry)
		if len(batches) != 1 || !<-sent {
			t.Fatal("first writer did not fill the last free slot")
		}
		cancel()
		if <-sent {
			t.Fatal("second writer unexpectedly sent after cancellation")
		}
		if snapshot := telemetry.snapshot(time.Now()); snapshot.BlockedSenders != 0 || snapshot.SentBatchesTotal != 1 {
			t.Fatalf("after race = %+v", snapshot)
		}
	})
}

func TestReaderPoolOwnsFilesAcrossConcurrentCycles(t *testing.T) {
	pool := &readerPool{source: Source{files: []string{"a", "b"}}, active: map[string]bool{}}
	pool.ctx = t.Context()
	first, second := &readerWorker{}, &readerWorker{}
	for cycle := range 2 {
		one, ok := pool.claimNextFile(first)
		if !ok {
			t.Fatalf("cycle %d: first job unavailable", cycle)
		}
		two, ok := pool.claimNextFile(second)
		if !ok || one == two {
			t.Fatalf("cycle %d: simultaneous jobs %q and %q", cycle, one, two)
		}
		pool.mu.Lock()
		pool.releaseFileLocked(second, two)
		pool.mu.Unlock()
		next, ok := pool.claimNextFile(second)
		if !ok || next == one {
			t.Fatalf("cycle %d: reissued active file %q while %q is owned", cycle, next, one)
		}
		pool.mu.Lock()
		pool.releaseFileLocked(second, next)
		pool.releaseFileLocked(first, one)
		pool.mu.Unlock()
	}
}

func TestReaderPoolDrainingIdleWorkerExitsWithoutWaiting(t *testing.T) {
	pool := &readerPool{
		source: Source{files: []string{"a"}}, active: map[string]bool{},
		workers:   []*readerWorker{{}, {draining: true}},
		telemetry: &readerTelemetry{}, done: make(chan struct{}),
	}
	pool.ctx = t.Context()
	if job, ok := pool.claimNextFile(pool.workers[1]); ok || job != "" {
		t.Fatalf("draining idle worker acquired %q", job)
	}
	pool.mu.Lock()
	if pool.nextFile != 0 || len(pool.active) != 0 {
		t.Fatal("draining worker acquired a file")
	}
	pool.mu.Unlock()
	if got := pool.snapshot(); got.LiveWorkers != 1 || got.DrainingWorkers != 0 {
		t.Fatalf("retired idle worker remains live: %+v", got)
	}
}

func TestReaderPoolReleaseWakesAllIdleWorkersBeforeDownscale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		firstRead := make(chan struct{})
		releaseFirst := make(chan struct{})
		secondRead := make(chan struct{})
		releaseSecond := make(chan struct{})
		var opened atomic.Int64
		openFile := func(string) (ReaderFile, error) {
			number := opened.Add(1)
			return &fixtureFile{rows: &fixtureRows{read: func([]Transaction) (int, error) {
				switch number {
				case 1:
					close(firstRead)
					<-releaseFirst
				case 2:
					close(secondRead)
					<-releaseSecond
				default:
					t.Errorf("unexpected file claim %d", number)
				}
				return 0, io.EOF
			}}}, nil
		}
		var blockClaims atomic.Bool
		claims := make(chan int, 3)
		releaseClaims := make(chan struct{})
		beforeClaim := func(worker *readerWorker) {
			if blockClaims.Load() {
				claims <- worker.id
				<-releaseClaims
			}
		}
		queue := newBatchQueue(1, 2)
		pool := startReaderPool(ctx, Source{files: []string{"one"}}, 2, 1,
			queue, &readerTelemetry{}, openFile, beforeClaim, zap.NewNop())
		<-firstRead
		pool.reconcile(3)
		synctest.Wait()
		if got := pool.snapshot(); got.ReadingWorkers != 1 || got.IdleWorkers != 2 {
			t.Fatalf("before release: %+v", got)
		}
		blockClaims.Store(true)
		close(releaseFirst)
		seen := map[int]bool{}
		for range 3 {
			seen[<-claims] = true
		}
		if len(seen) != 3 {
			t.Fatalf("release did not wake every idle worker: %v", seen)
		}
		// Both idle waiters must be awake before we retire W0 and W1.
		pool.reconcile(1)
		blockClaims.Store(false)
		close(releaseClaims)
		<-secondRead
		synctest.Wait()
		got := pool.snapshot()
		pool.mu.Lock()
		survivor := len(pool.workers) == 1 && pool.workers[0].id == 2
		pool.mu.Unlock()
		if !survivor || got.LiveWorkers != 1 || got.ReadingWorkers != 1 || got.DrainingWorkers != 0 {
			t.Fatalf("W2 did not claim the released file without another command: %+v", got)
		}
		cancel()
		close(releaseSecond)
		<-pool.done
		pool.reconcile(3)
		if got := pool.snapshot(); got.LiveWorkers != 0 {
			t.Fatalf("workers started after cancellation: %+v", got)
		}
	})
}

func TestReaderPoolDownscaleSelectsIdleBeforeBusy(t *testing.T) {
	var telemetry readerTelemetry
	pool := &readerPool{
		workers: []*readerWorker{
			{busy: true},
			{busy: true},
			{},
		},
		telemetry: &telemetry,
	}
	pool.ctx = t.Context()

	pool.reconcile(2)
	if snapshot := pool.snapshot(); snapshot.DrainingWorkers != 1 || snapshot.DrainingIdleWorkers != 1 {
		t.Fatalf("downscale snapshot = %+v, want one draining idle worker", snapshot)
	}
	if _, ok := pool.claimNextFile(pool.workers[2]); ok {
		t.Fatal("idle victim accepted another file")
	}
}

func TestReaderPoolBusyDownscaleFinishesCurrentFileWithoutClaimingNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		queue := newBatchQueue(2, 2)
		opened := make(chan string, 2)
		releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
		var pool *readerPool
		openFile := func(path string) (ReaderFile, error) {
			opened <- path
			return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
				if path == "first" {
					<-releaseFirst
					rows[0], rows[1] = Transaction{ClientID: "first"}, Transaction{ClientID: "first"}
					return 2, io.EOF
				}
				<-releaseSecond
				pool.stopReading()
				rows[0] = Transaction{ClientID: "second"}
				return 1, io.EOF
			}}}, nil
		}
		pool = startReaderPool(ctx, Source{files: []string{"first", "second"}}, 2, 1,
			queue, &readerTelemetry{}, openFile, nil, zap.NewNop())
		if path := <-opened; path != "first" {
			t.Fatalf("first opened file = %q", path)
		}
		synctest.Wait()
		if got := pool.snapshot(); got.ReadingWorkers != 1 || got.BlockedWorkers != 0 {
			t.Fatalf("first file must be held in Read: %+v", got)
		}
		pool.reconcile(0)
		if got := pool.snapshot(); got.DrainingReadingWorkers != 1 {
			t.Fatalf("busy worker not marked: %+v", got)
		}
		close(releaseFirst)
		synctest.Wait()
		if got := pool.snapshot(); got.LiveWorkers != 0 {
			t.Fatalf("worker did not retire after first file: %+v", got)
		}
		select {
		case path := <-opened:
			t.Fatalf("draining worker opened another file %q", path)
		default:
		}
		if batch := <-queue.ch; len(batch) != 2 || batch[0].ClientID != "first" || batch[1].ClientID != "first" {
			t.Fatalf("first file was not completed: %+v", batch)
		}
		if got := queue.snapshot(time.Now()); got.BlockedSenders != 0 {
			t.Fatalf("busy downscale entered blocked send: %+v", got)
		}
		pool.reconcile(1)
		if path := <-opened; path != "second" {
			t.Fatalf("file after scale-up = %q", path)
		}
		close(releaseSecond)
		<-pool.done
		if batch := <-queue.ch; len(batch) != 1 || batch[0].ClientID != "second" {
			t.Fatalf("second file batch = %+v", batch)
		}
	})
}

func TestReaderPoolBlockedDownscaleFlushesEntireFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := parquet.WriteFile(filepath.Join(dir, "a.parquet"), []Transaction{
			{ClientID: "one"}, {ClientID: "two"}, {ClientID: "three"},
		}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 2
		pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 2, 2, batches, &telemetry, &channel)
		assertBlockedSender(t, &channel)
		if snapshot := pool.snapshot(); snapshot.LiveWorkers != 2 || snapshot.BlockedWorkers != 1 || snapshot.IdleWorkers != 1 {
			t.Fatalf("before downscale snapshot = %+v", snapshot)
		}
		pool.reconcile(1)
		assertReaderLive(t, pool, 1)
		if snapshot := pool.snapshot(); snapshot.BlockedWorkers != 1 || snapshot.DrainingWorkers != 0 {
			t.Fatalf("busy worker was selected before idle: %+v", snapshot)
		}
		pool.reconcile(0)
		if snapshot := pool.snapshot(); snapshot.BlockedWorkers != 1 || snapshot.DrainingBlockedWorkers != 1 {
			t.Fatalf("blocked draining snapshot = %+v", snapshot)
		}
		if got := <-batches; len(got) != 1 || got[0].ClientID != "preexisting" {
			t.Fatalf("prefill = %+v", got)
		}
		var emitted []Transaction
		for len(emitted) < 3 {
			select {
			case batch := <-batches:
				emitted = append(emitted, batch...)
			case <-time.After(time.Second):
				t.Fatal("blocked worker did not flush all batches")
			}
		}
		if emitted[0].ClientID != "one" || emitted[1].ClientID != "two" || emitted[2].ClientID != "three" {
			t.Fatalf("emitted rows = %+v", emitted)
		}
		assertReaderLive(t, pool, 0)
		if got := channel.snapshot(time.Now()); got.SentBatchesTotal != 2 || got.BlockedSenders != 0 {
			t.Fatalf("channel after drain = %+v", got)
		}
		cancel()
		<-pool.done
	})
}

func TestReaderPoolBlockedDownscaleRetainsFileUntilRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := parquet.WriteFile(filepath.Join(dir, "a.parquet"), []Transaction{
			{ClientID: "one"}, {ClientID: "two"}, {ClientID: "three"},
		}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 2
		pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 2, 1, batches, &telemetry, &channel)
		defer func() {
			cancel()
			<-pool.done
		}()
		assertBlockedSender(t, &channel)
		pool.reconcile(0)
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case <-pool.done:
			t.Fatal("draining worker exited while downstream remained full")
		default:
		}
		if got := pool.snapshot().LiveWorkers; got != 1 {
			t.Fatalf("worker exited before downstream recovered: live=%d", got)
		}
		<-batches
		want := []string{"one", "two", "three"}
		var got []string
		for len(got) < len(want) {
			select {
			case batch := <-batches:
				for _, row := range batch {
					got = append(got, row.ClientID)
				}
			case <-time.After(time.Second):
				t.Fatal("worker did not flush after downstream recovery")
			}
		}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("flushed rows = %v, want %v", got, want)
		}
		assertReaderLive(t, pool, 0)
		if got := channel.snapshot(time.Now()); got.SentBatchesTotal != 2 || got.BlockedSenders != 0 {
			t.Fatalf("channel after recovery = %+v", got)
		}
	})
}

func TestReaderPoolAggregateSnapshotPartitionsActivities(t *testing.T) {
	pool := &readerPool{workers: []*readerWorker{
		{}, {draining: true},
		{busy: true}, {busy: true, draining: true},
		{busy: true, blocked: true}, {busy: true, blocked: true, draining: true},
	}}

	snapshot := pool.snapshot()
	if snapshot.LiveWorkers != 6 || snapshot.IdleWorkers != 2 || snapshot.ReadingWorkers != 2 || snapshot.BlockedWorkers != 2 {
		t.Fatalf("activity partition = %+v", snapshot)
	}
	if snapshot.DrainingWorkers != 3 || snapshot.DrainingIdleWorkers != 1 || snapshot.DrainingReadingWorkers != 1 || snapshot.DrainingBlockedWorkers != 1 {
		t.Fatalf("draining intersections = %+v", snapshot)
	}
}

func assertReaderLive(t *testing.T, pool *readerPool, want int) {
	t.Helper()
	synctest.Wait()
	if got := pool.snapshot().LiveWorkers; got != want {
		t.Fatalf("reader live=%d, want %d", got, want)
	}
}

func TestReaderPoolReactivatesBlockedWorkerWithoutDuplicateSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := parquet.WriteFile(filepath.Join(dir, "a.parquet"), []Transaction{{ClientID: "a"}}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 1
		pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 1, 1, batches, &telemetry, &channel)
		assertBlockedSender(t, &channel)
		pool.reconcile(0)
		if snapshot := pool.snapshot(); snapshot.BlockedWorkers != 1 || snapshot.DrainingBlockedWorkers != 1 {
			t.Fatalf("before reactivation = %+v", snapshot)
		}
		pool.reconcile(1)
		if snapshot := pool.snapshot(); snapshot.LiveWorkers != 1 || snapshot.BlockedWorkers != 1 || snapshot.DrainingWorkers != 0 {
			t.Fatalf("reactivated snapshot = %+v", snapshot)
		}
		<-batches
		select {
		case batch := <-batches:
			if len(batch) != 1 || batch[0].ClientID != "a" {
				t.Fatalf("reactivated worker batch = %+v", batch)
			}
		case <-time.After(time.Second):
			t.Fatal("reactivated worker did not flush its blocked batch")
		}
		if snapshot := pool.snapshot(); snapshot.LiveWorkers != 1 || snapshot.DrainingWorkers != 0 {
			t.Fatalf("worker was replaced or duplicated: %+v", snapshot)
		}
		cancel()
		<-pool.done
	})
}

func TestReaderPoolCancellationUnblocksDrainingWorkers(t *testing.T) {
	dir := t.TempDir()
	if err := parquet.WriteFile(filepath.Join(dir, "a.parquet"), []Transaction{{ClientID: "a"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan []Transaction)
	var telemetry readerTelemetry
	var channel batchQueue
	pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 1, 2, batches, &telemetry, &channel)
	pool.reconcile(1)
	cancel()
	select {
	case <-pool.done:
	case <-time.After(time.Second):
		t.Fatal("cancellation left a draining or blocked worker alive")
	}
	pool.reconcile(2)
	if snapshot := pool.snapshot(); snapshot.LiveWorkers != 0 {
		t.Fatalf("cancelled pool recreated workers: %+v", snapshot)
	}
}

func TestReaderPoolClosesFileAfterCanceledResidualBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "residual.parquet")
		if err := parquet.WriteFile(path, []Transaction{{ClientID: "residual"}}); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		batches := make(chan []Transaction)
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 2
		pool := startTestReaderPool(t, ctx, filepath.Join(dir, "*.parquet"), 2, 1, batches, &telemetry, &channel)
		assertBlockedSender(t, &channel)
		cancel()
		select {
		case <-pool.done:
		case <-time.After(time.Second):
			t.Fatal("Reader pool did not finish after canceled residual batch")
		}

		if err := os.Remove(path); err != nil {
			t.Fatalf("remove closed residual source: %v", err)
		}
	})
}

func TestReaderPoolCanceledCleanupIgnoresCloseErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool := &readerPool{ctx: ctx}
	reader := &errorCloser{err: errors.New("reader close failed")}
	file := &errorCloser{err: errors.New("file close failed")}

	if sourceError := pool.closeResources("source.parquet", reader, file, nil); sourceError != nil {
		t.Fatalf("canceled cleanup source error = %+v, want nil", sourceError)
	}
	if !reader.closed || !file.closed {
		t.Fatalf("cleanup closed reader=%t file=%t, want both true", reader.closed, file.closed)
	}
}

func TestReaderPoolActiveCleanupPreservesFirstCloseError(t *testing.T) {
	ctx := context.Background()
	pool := &readerPool{ctx: ctx}
	reader := &errorCloser{err: errors.New("reader close failed")}
	file := &errorCloser{err: errors.New("file close failed")}

	sourceError := pool.closeResources("source.parquet", reader, file, nil)
	if sourceError == nil || sourceError.Op != "reader-close" || sourceError.Error() != "reader close failed" {
		t.Fatalf("active cleanup source error = %+v", sourceError)
	}
	if !reader.closed || !file.closed {
		t.Fatalf("cleanup closed reader=%t file=%t, want both true", reader.closed, file.closed)
	}
}
