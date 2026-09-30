package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

type errorCloser struct {
	err    error
	closed bool
}

func (closer *errorCloser) Close() error {
	closer.closed = true
	return closer.err
}

func TestReaderPoolOwnsUniqueFilesAndEmitsPerFileResiduals(t *testing.T) {
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
	var channel channelTelemetry
	channel.start(batches, 2)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 2, 2, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	waitForBlockedSenders(t, &channel, 2)
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
}

func TestReaderPoolAppendRowsSplitsShortReadsAtBatchBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batches := make(chan []Transaction, 2)
	var channel channelTelemetry
	channel.start(batches, 2)
	pool := &readerPool{
		ctx:       ctx,
		batchSize: 2,
		batches:   batches,
		channel:   &channel,
	}

	batch := make([]Transaction, 0, pool.batchSize)
	batch, sent := pool.appendRows(batch, []Transaction{{ClientID: "one"}})
	if !sent {
		t.Fatal("first short read was not sent")
	}
	batch, sent = pool.appendRows(batch, []Transaction{{ClientID: "two"}, {ClientID: "three"}})
	if !sent {
		t.Fatal("second short read was not sent")
	}
	if len(batch) != 1 {
		t.Fatalf("EOF residual size = %d, want 1", len(batch))
	}
	if !channel.send(ctx, batches, batch) {
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

func TestReaderPoolImmediateSendKeepsWorkerReading(t *testing.T) {
	batches := make(chan []Transaction, 1)
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 1)
	worker := &readerWorker{ctx: context.Background(), busy: true}
	pool := &readerPool{
		ctx:       context.Background(),
		batches:   batches,
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
	if snapshot := pool.aggregateSnapshot(); snapshot.readingWorkers != 1 {
		t.Fatalf("immediate send snapshot = %+v, want one reading worker", snapshot)
	}
	if measurements := channel.snapshot(time.Now()); measurements.blockedSenders != 0 ||
		measurements.sentBatchesTotal != 1 || measurements.sentTransactionsTotal != 1 {
		t.Fatalf("immediate send channel measurements = %+v", measurements)
	}
}

func TestReaderPoolBlockedSendReturnsToReadingAfterDrain(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{ClientID: "preexisting"}}
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 1)
	worker := &readerWorker{ctx: context.Background(), draining: true, busy: true}
	pool := &readerPool{
		ctx:       context.Background(),
		desired:   0,
		workers:   []*readerWorker{worker},
		batches:   batches,
		telemetry: &telemetry,
		channel:   &channel,
	}
	sent := make(chan bool, 1)
	go func() {
		sent <- pool.sendBatch(worker, []Transaction{{ClientID: "a"}})
	}()

	waitForBlockedSender(t, &channel)
	pool.mu.Lock()
	blocked := worker.blocked
	pool.mu.Unlock()
	if !blocked {
		t.Fatal("blocked send did not mark worker blocked")
	}
	if snapshot := pool.aggregateSnapshot(); snapshot.blockedWorkers != 1 || snapshot.drainingBlockedWorkers != 1 {
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
	if snapshot := pool.aggregateSnapshot(); snapshot.readingWorkers != 1 || snapshot.drainingReadingWorkers != 1 {
		t.Fatalf("drained snapshot = %+v", snapshot)
	}
	if measurements := channel.snapshot(time.Now()); measurements.blockedSenders != 0 ||
		measurements.sentBatchesTotal != 1 || measurements.sentTransactionsTotal != 1 {
		t.Fatalf("drained send channel measurements = %+v", measurements)
	}
}

func TestReaderPoolCanceledBlockedSendDoesNotRestoreReading(t *testing.T) {
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{ClientID: "preexisting"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 1)
	worker := &readerWorker{ctx: ctx, busy: true}
	pool := &readerPool{
		ctx:       context.Background(),
		batches:   batches,
		telemetry: &telemetry,
		channel:   &channel,
		workers:   []*readerWorker{worker},
	}
	sent := make(chan bool, 1)
	go func() {
		sent <- pool.sendBatch(worker, []Transaction{{ClientID: "a"}})
	}()

	waitForBlockedSender(t, &channel)
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
	if snapshot := pool.aggregateSnapshot(); snapshot.readingWorkers != 1 || snapshot.blockedWorkers != 0 {
		t.Fatalf("cancelled send snapshot = %+v", snapshot)
	}
	if measurements := channel.snapshot(time.Now()); measurements.blockedSenders != 0 ||
		measurements.sentBatchesTotal != 0 || measurements.sentTransactionsTotal != 0 {
		t.Fatalf("cancelled send channel measurements = %+v", measurements)
	}
}

func TestReaderPoolOwnsFilesAcrossConcurrentCycles(t *testing.T) {
	pool := &readerPool{files: []string{"a", "b"}, active: map[string]bool{}}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()
	first, second := &readerWorker{}, &readerWorker{}
	for cycle := range 20 {
		one, ok := pool.claimNextFile(first)
		if !ok {
			t.Fatalf("cycle %d: first job unavailable", cycle)
		}
		two, ok := pool.claimNextFile(second)
		if !ok || one == two {
			t.Fatalf("cycle %d: simultaneous jobs %q and %q", cycle, one, two)
		}
		pool.mu.Lock()
		delete(pool.active, two)
		pool.available.Broadcast()
		pool.mu.Unlock()
		next, ok := pool.claimNextFile(second)
		if !ok || next == one {
			t.Fatalf("cycle %d: reissued active file %q while %q is owned", cycle, next, one)
		}
		pool.mu.Lock()
		delete(pool.active, next)
		delete(pool.active, one)
		pool.available.Broadcast()
		pool.mu.Unlock()
	}
}

func TestReaderPoolDrainingIdleWorkerExitsWithoutWaiting(t *testing.T) {
	pool := &readerPool{
		files: []string{"a"}, active: map[string]bool{},
		workers:   []*readerWorker{{}, {draining: true}},
		telemetry: &readerTelemetry{}, done: make(chan struct{}),
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()
	if job, ok := pool.claimNextFile(pool.workers[1]); ok || job != "" {
		t.Fatalf("draining idle worker acquired %q", job)
	}
	pool.mu.Lock()
	if pool.nextFile != 0 || len(pool.active) != 0 {
		t.Fatal("draining worker acquired a file")
	}
	pool.mu.Unlock()
	pool.reconcile(2)
	if job, ok := pool.claimNextFile(pool.workers[1]); !ok || job != "a" {
		t.Fatalf("reactivated worker job = %q, want a", job)
	}
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
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()

	pool.reconcile(2)
	if snapshot := pool.aggregateSnapshot(); snapshot.drainingWorkers != 1 || snapshot.drainingIdleWorkers != 1 {
		t.Fatalf("downscale snapshot = %+v, want one draining idle worker", snapshot)
	}
	if _, ok := pool.claimNextFile(pool.workers[2]); ok {
		t.Fatal("idle victim accepted another file")
	}
}

func TestReaderPoolBusyDownscaleFinishesCurrentFileWithoutClaimingNext(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "a-first.parquet")
	secondPath := filepath.Join(dir, "b-second.parquet")
	firstRows := make([]Transaction, 100_000)
	for index := range firstRows {
		firstRows[index] = Transaction{ClientID: "first"}
	}
	if err := parquet.WriteFile(firstPath, firstRows); err != nil {
		t.Fatalf("write first file: %v", err)
	}
	if err := parquet.WriteFile(secondPath, []Transaction{{ClientID: "second"}}); err != nil {
		t.Fatalf("write second file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan []Transaction, 2)
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, len(firstRows))
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), len(firstRows), 1, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { <-pool.stop() }()

	deadline := time.After(time.Second)
	for {
		pool.mu.Lock()
		firstActive := pool.active[firstPath]
		busy := len(pool.workers) == 1 && pool.workers[0].busy
		blocked := len(pool.workers) == 1 && pool.workers[0].blocked
		pool.mu.Unlock()
		if firstActive && busy && !blocked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker did not begin the first file as busy and non-blocked")
		case <-time.After(time.Millisecond):
		}
	}

	pool.reconcile(0)
	waitForReaderLive(t, pool, 0)
	pool.mu.Lock()
	secondActive := pool.active[secondPath]
	pool.mu.Unlock()
	if secondActive {
		t.Fatal("draining worker claimed the second file")
	}
	if measurements := channel.snapshot(time.Now()); measurements.blockedSenders != 0 {
		t.Fatalf("busy downscale entered blocked send: %+v", measurements)
	}
	if batch := <-batches; len(batch) != len(firstRows) {
		t.Fatalf("completed first batch size = %d, want %d", len(batch), len(firstRows))
	} else if batch[0].ClientID != "first" {
		t.Fatalf("completed first batch source = %q, want first", batch[0].ClientID)
	}

	pool.reconcile(1)
	select {
	case batch := <-batches:
		if len(batch) != 1 || batch[0].ClientID != "second" {
			t.Fatalf("batch after scale-up = %+v, want second file", batch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second file did not become eligible after scale-up")
	}
}

func TestReaderPoolBlockedDownscaleFlushesEntireFile(t *testing.T) {
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
	var channel channelTelemetry
	channel.start(batches, 2)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 2, 2, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	waitForBlockedSender(t, &channel)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 2 || snapshot.blockedWorkers != 1 || snapshot.idleWorkers != 1 {
		t.Fatalf("before downscale snapshot = %+v", snapshot)
	}
	pool.reconcile(1)
	deadline := time.After(time.Second)
	for pool.aggregateSnapshot().liveWorkers != 1 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("idle victim did not exit")
		}
	}
	if snapshot := pool.aggregateSnapshot(); snapshot.blockedWorkers != 1 || snapshot.drainingWorkers != 0 {
		t.Fatalf("busy worker was selected before idle: %+v", snapshot)
	}
	pool.reconcile(0)
	if snapshot := pool.aggregateSnapshot(); snapshot.blockedWorkers != 1 || snapshot.drainingBlockedWorkers != 1 {
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
	deadline = time.After(time.Second)
	for pool.aggregateSnapshot().liveWorkers != 0 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("draining worker did not exit after EOF")
		}
	}
	if got := channel.snapshot(time.Now()); got.sentBatchesTotal != 2 || got.blockedSenders != 0 {
		t.Fatalf("channel after drain = %+v", got)
	}
	cancel()
	<-pool.done
}

func TestReaderPoolBlockedDownscaleWaitsPastFormerGraceUntilRecovery(t *testing.T) {
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
	var channel channelTelemetry
	channel.start(batches, 2)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 2, 1, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { <-pool.stop() }()
	waitForBlockedSender(t, &channel)
	pool.reconcile(0)
	select {
	case <-pool.done:
		t.Fatal("draining worker exited while downstream remained full")
	case <-time.After(1100 * time.Millisecond):
	}
	if got := pool.aggregateSnapshot().liveWorkers; got != 1 {
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
	waitForReaderLive(t, pool, 0)
	if got := channel.snapshot(time.Now()); got.sentBatchesTotal != 2 || got.blockedSenders != 0 {
		t.Fatalf("channel after recovery = %+v", got)
	}
}

func TestReaderPoolAggregateSnapshotPartitionsActivities(t *testing.T) {
	pool := &readerPool{workers: []*readerWorker{
		{}, {draining: true},
		{busy: true}, {busy: true, draining: true},
		{busy: true, blocked: true}, {busy: true, blocked: true, draining: true},
	}}

	snapshot := pool.aggregateSnapshot()
	if snapshot.liveWorkers != 6 || snapshot.idleWorkers != 2 || snapshot.readingWorkers != 2 || snapshot.blockedWorkers != 2 {
		t.Fatalf("activity partition = %+v", snapshot)
	}
	if snapshot.drainingWorkers != 3 || snapshot.drainingIdleWorkers != 1 || snapshot.drainingReadingWorkers != 1 || snapshot.drainingBlockedWorkers != 1 {
		t.Fatalf("draining intersections = %+v", snapshot)
	}
}

func waitForReaderLive(t *testing.T, pool *readerPool, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for pool.aggregateSnapshot().liveWorkers != want {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("reader live=%d, want %d", pool.aggregateSnapshot().liveWorkers, want)
		}
	}
}

func TestReaderPoolReactivatesBlockedWorkerWithoutDuplicateSlot(t *testing.T) {
	dir := t.TempDir()
	if err := parquet.WriteFile(filepath.Join(dir, "a.parquet"), []Transaction{{ClientID: "a"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{ClientID: "preexisting"}}
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 1)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 1, 1, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	waitForBlockedSender(t, &channel)
	pool.reconcile(0)
	if snapshot := pool.aggregateSnapshot(); snapshot.blockedWorkers != 1 || snapshot.drainingBlockedWorkers != 1 {
		t.Fatalf("before reactivation = %+v", snapshot)
	}
	pool.reconcile(1)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 1 || snapshot.blockedWorkers != 1 || snapshot.drainingWorkers != 0 {
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
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 1 || snapshot.drainingWorkers != 0 {
		t.Fatalf("worker was replaced or duplicated: %+v", snapshot)
	}
	cancel()
	<-pool.done
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
	var channel channelTelemetry
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 1, 2, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	pool.reconcile(1)
	cancel()
	select {
	case <-pool.done:
	case <-time.After(time.Second):
		t.Fatal("cancellation left a draining or blocked worker alive")
	}
	pool.reconcile(2)
	if snapshot := pool.aggregateSnapshot(); snapshot.liveWorkers != 0 {
		t.Fatalf("cancelled pool recreated workers: %+v", snapshot)
	}
}

func TestReaderPoolClosesFileAfterCanceledResidualBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "residual.parquet")
	if err := parquet.WriteFile(path, []Transaction{{ClientID: "residual"}}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	batches := make(chan []Transaction)
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 2)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 2, 1, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	waitForBlockedSender(t, &channel)
	cancel()
	select {
	case <-pool.done:
	case <-time.After(time.Second):
		t.Fatal("Reader pool did not finish after canceled residual batch")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove closed residual source: %v", err)
	}
}

func TestReaderPoolCanceledCleanupIgnoresCloseErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool := &readerPool{ctx: ctx}
	worker := &readerWorker{ctx: ctx}
	reader := &errorCloser{err: errors.New("reader close failed")}
	file := &errorCloser{err: errors.New("file close failed")}

	if sourceError := pool.closeResources(worker, "source.parquet", reader, file, nil); sourceError != nil {
		t.Fatalf("canceled cleanup source error = %+v, want nil", sourceError)
	}
	if !reader.closed || !file.closed {
		t.Fatalf("cleanup closed reader=%t file=%t, want both true", reader.closed, file.closed)
	}
}

func TestReaderPoolActiveCleanupPreservesFirstCloseError(t *testing.T) {
	ctx := context.Background()
	pool := &readerPool{ctx: ctx}
	worker := &readerWorker{ctx: ctx}
	reader := &errorCloser{err: errors.New("reader close failed")}
	file := &errorCloser{err: errors.New("file close failed")}

	sourceError := pool.closeResources(worker, "source.parquet", reader, file, nil)
	if sourceError == nil || sourceError.Operation != "reader-close" || sourceError.Message != "reader close failed" {
		t.Fatalf("active cleanup source error = %+v", sourceError)
	}
	if !reader.closed || !file.closed {
		t.Fatalf("cleanup closed reader=%t file=%t, want both true", reader.closed, file.closed)
	}
}
