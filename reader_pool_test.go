package main

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

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

func TestReaderPoolOwnsFilesAcrossConcurrentCycles(t *testing.T) {
	pool := &readerPool{files: []string{"a", "b"}, active: map[string]bool{}}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()
	first, second := &readerWorker{}, &readerWorker{}
	for cycle := range 20 {
		one, ok := pool.nextJob(first)
		if !ok {
			t.Fatalf("cycle %d: first job unavailable", cycle)
		}
		two, ok := pool.nextJob(second)
		if !ok || one == two {
			t.Fatalf("cycle %d: simultaneous jobs %q and %q", cycle, one, two)
		}
		pool.mu.Lock()
		delete(pool.active, two)
		pool.available.Broadcast()
		pool.mu.Unlock()
		next, ok := pool.nextJob(second)
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
		workers:   map[int]*readerWorker{0: {ordinal: 0}, 1: {ordinal: 1, draining: true}},
		telemetry: &readerTelemetry{}, done: make(chan struct{}),
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()
	if job, ok := pool.nextJob(pool.workers[1]); ok || job != "" {
		t.Fatalf("draining idle worker acquired %q", job)
	}
	pool.mu.Lock()
	if pool.nextFile != 0 || len(pool.active) != 0 {
		t.Fatal("draining worker acquired a file")
	}
	pool.mu.Unlock()
	pool.reconcile(2)
	if job, ok := pool.nextJob(pool.workers[1]); !ok || job != "a" {
		t.Fatalf("reactivated worker job = %q, want a", job)
	}
}

func TestReaderPoolDownscaleSelectsIdleBeforeBusyRegardlessOfOrdinal(t *testing.T) {
	var telemetry readerTelemetry
	for ordinal := range 3 {
		telemetry.startWorker(ordinal)
	}
	pool := &readerPool{
		workers: map[int]*readerWorker{
			0: {ordinal: 0, busy: true},
			1: {ordinal: 1, busy: true},
			2: {ordinal: 2},
		},
		telemetry: &telemetry,
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	pool.available = sync.NewCond(&pool.mu)
	defer pool.cancel()

	pool.reconcile(2)
	slots := telemetry.snapshot().workerSlots
	if slots[0].Lifecycle != "active" || slots[1].Lifecycle != "active" || slots[2].Lifecycle != "draining" {
		t.Fatalf("downscale lifecycle = %+v, want idle ordinal 2 draining", slots)
	}
	if _, ok := pool.nextJob(pool.workers[2]); ok {
		t.Fatal("idle victim accepted another file")
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
	slots := telemetry.snapshot().workerSlots
	if len(slots) != 2 || (slots[0].Activity != "blocked" && slots[1].Activity != "blocked") {
		t.Fatalf("before downscale slots = %+v", slots)
	}
	var blockedOrdinal int
	for _, slot := range slots {
		if slot.Activity == "blocked" {
			blockedOrdinal = slot.Ordinal
			if slot.Source == nil {
				t.Fatalf("blocked source missing: %+v", slot)
			}
		} else if slot.Activity != "idle" {
			t.Fatalf("other worker is not idle: %+v", slot)
		}
	}
	pool.reconcile(1)
	deadline := time.After(time.Second)
	for telemetry.snapshot().liveWorkers != 1 {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("idle victim did not exit")
		}
	}
	if slot := telemetry.snapshot().workerSlots[0]; slot.Ordinal != blockedOrdinal || slot.Activity != "blocked" || slot.Lifecycle != "active" {
		t.Fatalf("busy worker was selected before idle: %+v", slot)
	}
	pool.reconcile(0)
	if slot := telemetry.snapshot().workerSlots[0]; slot.Activity != "blocked" || slot.Lifecycle != "draining" {
		t.Fatalf("blocked draining slot = %+v", slot)
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
	for telemetry.snapshot().liveWorkers != 0 {
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

type readerForceEvent struct {
	due time.Duration
	callback func()
}

type readerForceClock struct {
	mu sync.Mutex
	now time.Duration
	events []readerForceEvent
}

func (c *readerForceClock) after(delay time.Duration, callback func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, readerForceEvent{due: c.now + delay, callback: callback})
}

func (c *readerForceClock) advance(delta time.Duration) {
	c.mu.Lock()
	target := c.now + delta
	c.mu.Unlock()
	for {
		c.mu.Lock()
		index := -1
		for i, event := range c.events {
			if event.due <= target && (index < 0 || event.due < c.events[index].due) {
				index = i
			}
		}
		if index < 0 {
			c.now = target
			c.mu.Unlock()
			return
		}
		event := c.events[index]
		c.events = append(c.events[:index], c.events[index+1:]...)
		c.now = event.due
		c.mu.Unlock()
		event.callback()
	}
}

func TestReaderPoolBlockedDownscaleUnblocksDuringGraceWithoutLoss(t *testing.T) {
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
	clock := &readerForceClock{}
	pool.mu.Lock()
	pool.afterForce = clock.after
	pool.mu.Unlock()
	waitForBlockedSender(t, &channel)
	pool.reconcile(0)
	clock.advance(999 * time.Millisecond)
	if got := telemetry.snapshot().liveWorkers; got != 1 {
		t.Fatalf("worker exited during grace: live=%d", got)
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
			t.Fatal("worker did not flush during grace")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("flushed rows = %v, want %v", got, want)
	}
	waitForReaderLive(t, &telemetry, 0)
	clock.advance(time.Millisecond)
	if got := channel.snapshot(time.Now()); got.sentBatchesTotal != 2 || got.blockedSenders != 0 {
		t.Fatalf("channel after grace = %+v", got)
	}
}

func TestReaderPoolBlockedDownscaleForcesOnePerSecond(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a", "b", "c"} {
		if err := parquet.WriteFile(filepath.Join(dir, id+".parquet"), []Transaction{{ClientID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := make(chan []Transaction, 1)
	batches <- []Transaction{{ClientID: "preexisting"}}
	var telemetry readerTelemetry
	var channel channelTelemetry
	channel.start(batches, 1)
	pool, err := startReaderPool(ctx, filepath.Join(dir, "*.parquet"), 1, 3, batches, &telemetry, &channel)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { <-pool.stop() }()
	clock := &readerForceClock{}
	pool.mu.Lock()
	pool.afterForce = clock.after
	pool.mu.Unlock()
	waitForBlockedSenders(t, &channel, 3)
	pool.reconcile(0)
	clock.advance(999 * time.Millisecond)
	if got := telemetry.snapshot().liveWorkers; got != 3 {
		t.Fatalf("before grace deadline live=%d, want 3", got)
	}
	for want := 2; want >= 0; want-- {
		clock.advance(time.Millisecond)
		waitForReaderLive(t, &telemetry, want)
		if got := channel.snapshot(time.Now()).blockedSenders; got != want {
			t.Fatalf("after forced exit blocked=%d, want %d", got, want)
		}
		if want > 0 {
			clock.advance(999 * time.Millisecond)
			if got := telemetry.snapshot().liveWorkers; got != want {
				t.Fatalf("before next force live=%d, want %d", got, want)
			}
		}
	}
	if got := channel.snapshot(time.Now()); got.sentBatchesTotal != 0 || got.depthBatches != 1 {
		t.Fatalf("forced exits changed full channel: %+v", got)
	}
	if got := telemetry.snapshot().rowsRead; got != 3 {
		t.Fatalf("rows read = %d, want one current batch per worker", got)
	}
}

func waitForReaderLive(t *testing.T, telemetry *readerTelemetry, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for telemetry.snapshot().liveWorkers != want {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("reader live=%d, want %d", telemetry.snapshot().liveWorkers, want)
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
	if slot := telemetry.snapshot().workerSlots[0]; slot.Lifecycle != "draining" || slot.Activity != "blocked" {
		t.Fatalf("before reactivation = %+v", slot)
	}
	pool.reconcile(1)
	if slots := telemetry.snapshot().workerSlots; len(slots) != 1 || slots[0].Lifecycle != "active" || slots[0].Activity != "blocked" {
		t.Fatalf("reactivated slots = %+v", slots)
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
	if slots := telemetry.snapshot().workerSlots; len(slots) != 1 || slots[0].ID != "reader-worker-0" || slots[0].Lifecycle != "active" {
		t.Fatalf("worker replaced or duplicated = %+v", slots)
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
	if slots := telemetry.snapshot().workerSlots; len(slots) != 0 {
		t.Fatalf("cancelled pool recreated workers: %+v", slots)
	}
}
