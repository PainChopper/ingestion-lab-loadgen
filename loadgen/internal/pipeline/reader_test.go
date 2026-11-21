package pipeline

import (
	"context"
	"errors"
	"fmt"
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

func TestReaderResourcesAndSourceErrors(t *testing.T) {
	for _, test := range []struct {
		name             string
		cancelDuringOpen bool
		cancelDuringRead bool
		openErr          error
		initErr          error
		readErr          error
		readerCloseErr   error
		fileCloseErr     error
		wantOp           string
		want             error
	}{
		{name: "cancel between file and parquet", cancelDuringOpen: true, fileCloseErr: io.ErrShortWrite},
		{name: "cancel closes both resources", cancelDuringRead: true, readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite},
		{name: "file open failure", openErr: io.ErrClosedPipe, wantOp: "open", want: io.ErrClosedPipe},
		{name: "initialization and file close failure", initErr: io.ErrUnexpectedEOF, fileCloseErr: io.ErrShortWrite, wantOp: "close", want: io.ErrShortWrite},
		{name: "initialization failure", initErr: io.ErrUnexpectedEOF, wantOp: "open", want: io.ErrUnexpectedEOF},
		{name: "read cause has priority", readErr: io.ErrNoProgress, readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite, wantOp: "read", want: io.ErrNoProgress},
		{name: "reader close has priority", readerCloseErr: io.ErrClosedPipe, fileCloseErr: io.ErrShortWrite, wantOp: "reader-close", want: io.ErrClosedPipe},
		{name: "file close", fileCloseErr: io.ErrShortWrite, wantOp: "close", want: io.ErrShortWrite},
		{name: "wrapped EOF flushes residual", readErr: fmt.Errorf("wrapped: %w", io.EOF)},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				options := testPipelineOptions()
				opened := make(chan struct{})
				allowRead := make(chan struct{})
				file := &fixtureFile{openErr: test.initErr, closeErr: test.fileCloseErr}
				rows := &fixtureRows{closeErr: test.readerCloseErr}
				file.rows = rows
				var run *Pipeline
				rows.read = func(target []Transaction) (int, error) {
					<-allowRead
					if test.cancelDuringRead {
						cancel()
						return 0, io.EOF
					}
					run.StopReading()
					target[0] = Transaction{ClientID: "residual"}
					return 1, test.readErr
				}
				if test.cancelDuringOpen {
					file.onOpen = func() { t.Error("OpenRows called after cancellation") }
				}
				options.OpenFile = func(string) (ReaderFile, error) {
					close(opened)
					if test.cancelDuringOpen {
						cancel()
					}
					if test.openErr != nil {
						return nil, test.openErr
					}
					return file, nil
				}
				var delivered int
				options.Deliver = func(_ context.Context, batch []Transaction, _ int) error {
					delivered += len(batch)
					return nil
				}
				run = Start(ctx, options)
				<-opened
				close(allowRead)
				<-run.Done()
				if test.openErr == nil && !file.closed {
					t.Fatal("file remained open")
				}
				if test.openErr == nil && !test.cancelDuringOpen && test.initErr == nil && !rows.closed {
					t.Fatal("row reader remained open")
				}
				select {
				case err := <-run.SourceErrors():
					var sourceError *SourceError
					if test.want == nil || !errors.As(err, &sourceError) || !errors.Is(err, test.want) ||
						sourceError.Op != test.wantOp || sourceError.Path != "data.parquet" || sourceError.Error() != test.want.Error() {
						t.Fatalf("source error=%v (%+v), want %s %v", err, sourceError, test.wantOp, test.want)
					}
				default:
					if test.want != nil {
						t.Fatalf("missing source error %v", test.want)
					}
				}
				if test.want == nil && !test.cancelDuringOpen && !test.cancelDuringRead && delivered != 1 {
					t.Fatalf("residual delivered=%d,want 1", delivered)
				}
			})
		})
	}
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

func TestReaderSourcePaths(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct{ name, pattern, want string }{
		{name: "wildcard directory", pattern: filepath.Join(root, "fold=*", "part?", "*.parquet"), want: filepath.ToSlash(root)},
		{name: "ordinary directory", pattern: filepath.Join(root, "part", "*.parquet"), want: filepath.ToSlash(filepath.Join(root, "part"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SourceDirectory(test.pattern); got != test.want {
				t.Fatalf("directory=%q, want %q", got, test.want)
			}
		})
	}
	for _, test := range []struct{ name, path, want string }{
		{name: "inside", path: filepath.Join(root, "part", "a.parquet"), want: "part/a.parquet"},
		{name: "same path", path: root, want: filepath.Base(root)},
		{name: "parent", path: filepath.Dir(root), want: filepath.Base(filepath.Dir(root))},
		{name: "outside", path: filepath.Join(filepath.Dir(root), "outside", "a.parquet"), want: "a.parquet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := relativeSourcePath(root, test.path); got != test.want {
				t.Fatalf("relative path=%q, want %q", got, test.want)
			}
		})
	}
}

func TestOpenReaderFilePreservesMissingFileCause(t *testing.T) {
	file, err := openReaderFile(filepath.Join(t.TempDir(), "missing.parquet"))
	if file != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file=%v error=%v, want missing file", file, err)
	}
}

func TestReaderPoolReportsOnlyFirstConcurrentSourceError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		files := make(map[string]*fixtureFile)
		for _, path := range []string{"a.parquet", "b.parquet"} {
			files[path] = &fixtureFile{rows: &fixtureRows{read: func([]Transaction) (int, error) {
				entered <- struct{}{}
				<-release
				return 0, io.ErrUnexpectedEOF
			}}}
		}
		pool := startReaderPool(t.Context(), Source{files: []string{"a.parquet", "b.parquet"}}, 1, 2,
			newBatchQueue(0, 1), &readerTelemetry{}, func(path string) (ReaderFile, error) { return files[path], nil }, nil, zap.NewNop())
		<-entered
		<-entered
		close(release)
		<-pool.done
		if len(pool.sourceErrors) != 1 {
			t.Fatalf("source errors=%d, want exactly one", len(pool.sourceErrors))
		}
		err := <-pool.sourceErrors
		var sourceError *SourceError
		if !errors.Is(err, io.ErrUnexpectedEOF) || !errors.As(err, &sourceError) || sourceError.Op != "read" {
			t.Fatalf("source error=%v (%+v)", err, sourceError)
		}
		if sourceError.Path != "a.parquet" && sourceError.Path != "b.parquet" {
			t.Fatalf("source error path=%q", sourceError.Path)
		}
		if pool.canRead() || len(pool.snapshot().WorkerStates) != 0 {
			t.Fatal("source error did not stop all readers")
		}
		for path, file := range files {
			if !file.closed || !file.rows.(*fixtureRows).closed {
				t.Errorf("resources for %s remained open", path)
			}
		}
	})
}

func TestReaderPoolReactivatesOnlyNeededBlockedWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		queue := newBatchQueue(0, 1)
		pool := startReaderPool(ctx, Source{files: []string{"a", "b", "c"}}, 1, 3, queue, &readerTelemetry{},
			func(path string) (ReaderFile, error) {
				return &fixtureFile{rows: &fixtureRows{read: func(rows []Transaction) (int, error) {
					rows[0].ClientID = path
					return 1, io.EOF
				}}}, nil
			}, nil, zap.NewNop())
		assertBlockedSenders(t, queue, 3)
		pool.reconcile(1)
		got := pool.snapshot().WorkerStates
		if len(got) != 3 {
			t.Fatalf("after 3 to 1: %+v", got)
		}
		drainingBlocked := []ReaderWorkerState{}
		for _, state := range got {
			if state.Activity == ReaderBlocked && state.Draining {
				drainingBlocked = append(drainingBlocked, state)
			}
		}
		assertWorkerStateMultiset(t, drainingBlocked, []ReaderWorkerState{
			{Activity: ReaderBlocked, Draining: true}, {Activity: ReaderBlocked, Draining: true},
		})
		pool.reconcile(2)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderBlocked}, {Activity: ReaderBlocked}, {Activity: ReaderBlocked, Draining: true},
		})
		pool.stopReading()
		seen := make(map[string]bool)
		for range 3 {
			batch := <-queue.ch
			if len(batch) != 1 || seen[batch[0].ClientID] {
				t.Fatalf("duplicate or invalid batch: %+v", batch)
			}
			seen[batch[0].ClientID] = true
		}
		<-pool.done
		if got := queue.snapshot(time.Now()); got.SentTransactionsTotal != 3 || len(pool.snapshot().WorkerStates) != 0 {
			t.Fatalf("after drain: %+v", got)
		}
	})
}

func TestReaderPoolClosesResourcesAfterCanceledFullBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		rows := &fixtureRows{read: func(target []Transaction) (int, error) {
			target[0].ClientID = "full"
			return 1, io.EOF
		}}
		file := &fixtureFile{rows: rows}
		queue := newBatchQueue(0, 1)
		pool := startReaderPool(ctx, Source{files: []string{"full.parquet"}}, 1, 1, queue, &readerTelemetry{},
			func(string) (ReaderFile, error) { return file, nil }, nil, zap.NewNop())
		assertBlockedSender(t, queue)
		cancel()
		<-pool.done
		if !file.closed || !rows.closed || queue.snapshot(time.Now()).SentTransactionsTotal != 0 {
			t.Fatalf("file closed=%t rows closed=%t queue=%+v", file.closed, rows.closed, queue.snapshot(time.Now()))
		}
	})
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
	worker := &readerWorker{activity: ReaderReading}
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
					t.Error("Reader fetched new rows after admission stopped")
					return 0, io.ErrUnexpectedEOF
				}
				rows[0] = Transaction{ClientID: "already read"}
				pool.stopReading()
				return 1, nil
			}}}, nil
		}
		pool = startReaderPool(t.Context(), Source{files: []string{"one"}}, 2, 3,
			queue, &readerTelemetry{}, openFile, nil, zap.NewNop())
		synctest.Wait()
		activities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			activities = append(activities, state.Activity)
		}
		assertWorkerStateMultiset(t, activities, []ReaderActivity{ReaderReading, ReaderIdle, ReaderIdle})
		close(allowRead)
		<-pool.done
		if got := len(pool.snapshot().WorkerStates); got != 0 {
			t.Fatalf("live workers after StopReading = %d, want 0", got)
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
	worker := &readerWorker{activity: ReaderReading}
	pool := &readerPool{
		ctx: context.Background(),

		telemetry: &telemetry,
		channel:   &channel,
		workers:   []*readerWorker{worker},
	}

	if !pool.sendBatch(worker, []Transaction{{ClientID: "a"}}) {
		t.Fatal("immediate send failed")
	}
	if worker.activity == ReaderBlocked {
		t.Fatal("immediate send marked worker blocked")
	}
	activities := []ReaderActivity{}
	for _, state := range pool.snapshot().WorkerStates {
		activities = append(activities, state.Activity)
	}
	assertWorkerStateMultiset(t, activities, []ReaderActivity{ReaderReading})
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
		worker := &readerWorker{draining: true, activity: ReaderReading}
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
		blocked := (worker.activity == ReaderBlocked)
		pool.mu.Unlock()
		if !blocked {
			t.Fatal("blocked send did not mark worker blocked")
		}
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderBlocked, Draining: true},
		})

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
		blocked = (worker.activity == ReaderBlocked)
		pool.mu.Unlock()
		if blocked {
			t.Fatal("drained send left worker blocked")
		}
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderReading, Draining: true},
		})
		if measurements := channel.snapshot(time.Now()); measurements.BlockedSenders != 0 ||
			measurements.SentBatchesTotal != 1 || measurements.SentTransactionsTotal != 1 {
			t.Fatalf("drained send channel measurements = %+v", measurements)
		}
	})
}

func TestReaderPoolCanceledBlockedSendClearsBlockedState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		batches := make(chan []Transaction, 1)
		batches <- []Transaction{{ClientID: "preexisting"}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var telemetry readerTelemetry
		var channel batchQueue
		channel.ch = batches
		channel.batchSize = 1
		worker := &readerWorker{activity: ReaderReading}
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
		blocked := (worker.activity == ReaderBlocked)
		pool.mu.Unlock()
		if blocked {
			t.Fatal("cancelled send left worker blocked")
		}
		activities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			activities = append(activities, state.Activity)
		}
		assertWorkerStateMultiset(t, activities, []ReaderActivity{ReaderReading})
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
			go func() { sent <- pool.sendBatch(&readerWorker{activity: ReaderIdle}, []Transaction{{}}) }()
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
	first, second := &readerWorker{activity: ReaderIdle}, &readerWorker{activity: ReaderIdle}
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
		workers:   []*readerWorker{{activity: ReaderIdle}, {activity: ReaderIdle, draining: true}},
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
	states := pool.snapshot().WorkerStates
	if len(states) != 1 || states[0].Draining {
		t.Fatalf("retired idle worker remains live: %+v", states)
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
		activities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			activities = append(activities, state.Activity)
		}
		assertWorkerStateMultiset(t, activities, []ReaderActivity{ReaderReading, ReaderIdle, ReaderIdle})
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
		got := pool.snapshot().WorkerStates
		pool.mu.Lock()
		survivor := len(pool.workers) == 1 && pool.workers[0].id == 2
		pool.mu.Unlock()
		if !survivor {
			t.Fatalf("W2 did not claim the released file without another command: %+v", got)
		}
		assertWorkerStateMultiset(t, got, []ReaderWorkerState{{Activity: ReaderReading}})
		cancel()
		close(releaseSecond)
		<-pool.done
		pool.reconcile(3)
		if got := pool.snapshot().WorkerStates; len(got) != 0 {
			t.Fatalf("workers started after cancellation: %+v", got)
		}
	})
}

func TestReaderPoolDownscaleSelectsIdleBeforeBusy(t *testing.T) {
	var telemetry readerTelemetry
	pool := &readerPool{
		workers: []*readerWorker{
			{activity: ReaderReading},
			{activity: ReaderReading},
			{activity: ReaderIdle},
		},
		telemetry: &telemetry,
	}
	pool.ctx = t.Context()

	pool.reconcile(2)
	draining := []ReaderWorkerState{}
	for _, state := range pool.snapshot().WorkerStates {
		if state.Draining {
			draining = append(draining, state)
		}
	}
	assertWorkerStateMultiset(t, draining, []ReaderWorkerState{{Activity: ReaderIdle, Draining: true}})
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
		activities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			activities = append(activities, state.Activity)
		}
		assertWorkerStateMultiset(t, activities, []ReaderActivity{ReaderReading})
		pool.reconcile(0)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderReading, Draining: true},
		})
		close(releaseFirst)
		synctest.Wait()
		if got := pool.snapshot().WorkerStates; len(got) != 0 {
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
		beforeActivities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			beforeActivities = append(beforeActivities, state.Activity)
		}
		assertWorkerStateMultiset(t, beforeActivities, []ReaderActivity{ReaderBlocked, ReaderIdle})
		pool.reconcile(1)
		assertReaderLive(t, pool, 1)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{{Activity: ReaderBlocked}})
		pool.reconcile(0)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderBlocked, Draining: true},
		})
		<-batches
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
		if got := len(pool.snapshot().WorkerStates); got != 1 {
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

func TestReaderPoolSnapshotStatesAreIndependent(t *testing.T) {
	want := []ReaderWorkerState{
		{Activity: ReaderIdle}, {Activity: ReaderIdle, Draining: true},
		{Activity: ReaderReading}, {Activity: ReaderReading, Draining: true},
		{Activity: ReaderBlocked}, {Activity: ReaderBlocked, Draining: true},
		{Activity: ReaderReading},
	}
	pool := &readerPool{ctx: t.Context()}
	for _, state := range want {
		pool.workers = append(pool.workers, &readerWorker{activity: state.Activity, draining: state.Draining})
	}
	first, second := pool.snapshot(), pool.snapshot()
	assertWorkerStateMultiset(t, first.WorkerStates, want)
	first.WorkerStates[0] = ReaderWorkerState{Activity: ReaderBlocked, Draining: true}
	assertWorkerStateMultiset(t, second.WorkerStates, want)
	assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, want)
	pool.reconcile(0)
	pool.mu.Lock()
	pool.workers[0].activity = ReaderBlocked
	pool.mu.Unlock()
	assertWorkerStateMultiset(t, second.WorkerStates, want)
	for _, state := range pool.snapshot().WorkerStates {
		if !state.Draining {
			t.Fatalf("reconcile did not drain live state: %+v", state)
		}
	}
	empty := (&readerPool{}).snapshot().WorkerStates
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty states = %#v, want non-nil []", empty)
	}
}

func assertReaderLive(t *testing.T, pool *readerPool, want int) {
	t.Helper()
	synctest.Wait()
	if got := len(pool.snapshot().WorkerStates); got != want {
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
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderBlocked, Draining: true},
		})
		pool.reconcile(1)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{{Activity: ReaderBlocked}})
		<-batches
		select {
		case batch := <-batches:
			if len(batch) != 1 || batch[0].ClientID != "a" {
				t.Fatalf("reactivated worker batch = %+v", batch)
			}
		case <-time.After(time.Second):
			t.Fatal("reactivated worker did not flush its blocked batch")
		}
		states := pool.snapshot().WorkerStates
		if len(states) != 1 {
			t.Fatalf("worker was replaced or duplicated: %+v", states)
		}
		if states[0].Draining {
			t.Fatalf("reactivated worker is draining: %+v", states)
		}
		cancel()
		<-pool.done
	})
}

func TestReaderPoolCancellationUnblocksDrainingWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		assertBlockedSender(t, &channel)
		beforeActivities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			beforeActivities = append(beforeActivities, state.Activity)
		}
		assertWorkerStateMultiset(t, beforeActivities, []ReaderActivity{ReaderBlocked, ReaderIdle})
		pool.reconcile(1)
		synctest.Wait()
		afterActivities := []ReaderActivity{}
		for _, state := range pool.snapshot().WorkerStates {
			afterActivities = append(afterActivities, state.Activity)
		}
		assertWorkerStateMultiset(t, afterActivities, []ReaderActivity{ReaderBlocked})
		pool.reconcile(0)
		assertWorkerStateMultiset(t, pool.snapshot().WorkerStates, []ReaderWorkerState{
			{Activity: ReaderBlocked, Draining: true},
		})
		cancel()
		select {
		case <-pool.done:
		case <-time.After(time.Second):
			t.Fatal("cancellation left a draining or blocked worker alive")
		}
		pool.reconcile(2)
		if snapshot := pool.snapshot().WorkerStates; len(snapshot) != 0 {
			t.Fatalf("cancelled pool recreated workers: %+v", snapshot)
		}
	})
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
