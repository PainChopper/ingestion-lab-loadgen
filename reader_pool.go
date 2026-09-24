package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
)

const readerForcedDrainGracePeriod = time.Second

// readerWorker holds the per-worker cancellation and lifecycle state.
type readerWorker struct {
	// Identifies this worker within the pool and its telemetry.
	workerID int
	// Is canceled when the pool stops or this worker must exit.
	ctx context.Context
	// Requests cancellation of only this worker.
	cancel context.CancelFunc
	// Prevents the worker from claiming another file.
	draining bool
	// Indicates that the worker currently owns a source file.
	busy bool
	// Indicates that the worker is blocked while sending a batch.
	blocked bool
	// Indicates that the worker was canceled to finish draining.
	forced bool
}

// readerPool owns file admission. A file is assigned once to exactly one worker;
// workers emit their locally completed batches directly to the reader channel.
type readerPool struct {
	// Protects mutable pool state and coordinates condition-variable waits.
	mu sync.Mutex
	// Wakes workers when a file, configuration change, or shutdown is available.
	available *sync.Cond
	// Is canceled when the whole pool must stop.
	ctx context.Context
	// Requests cancellation of the pool context.
	cancel context.CancelFunc
	// Tracks every started worker goroutine until it exits.
	wg sync.WaitGroup
	// Closes after pool cancellation and all workers have exited.
	done chan struct{}
	// Contains the sorted, immutable set of source Parquet paths.
	files []string
	// Records source paths currently claimed by a worker.
	active map[string]bool
	// Holds released files that must be assigned again.
	replayFiles []string
	// Is the round-robin cursor into the source paths.
	nextFile int
	// Is the maximum number of transactions in an emitted batch.
	batchSize int
	// Receives completed batches from Reader workers.
	batches chan<- []Transaction
	// Records Reader-stage observations.
	telemetry *readerTelemetry
	// Records observations for batches.
	channel *channelTelemetry
	// Contains live Reader workers keyed by worker ID.
	workers map[int]*readerWorker
	// Allocates unique worker identities for this pool lifetime.
	nextWorkerID int
	// Is the requested number of active Reader workers.
	desired int
	// Prevents new work while the pool is shutting down.
	stopping bool
	// Reports whether a delayed forced-drain callback is scheduled.
	forcePending bool
	// Invalidates forced-drain callbacks from an older configuration.
	forceGeneration uint64
	// Optionally schedules forced-drain callbacks for tests.
	afterForce func(time.Duration, func())
}

func startReaderPool(
	ctx context.Context,
	dataPath string,
	batchSize, workers int,
	batches chan<- []Transaction,
	telemetry *readerTelemetry,
	channel *channelTelemetry,
) (*readerPool, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to glob path: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found matching pattern: %s", dataPath)
	}
	sort.Strings(files)
	poolCtx, cancel := context.WithCancel(ctx)
	pool := &readerPool{
		ctx: poolCtx, cancel: cancel, files: files, active: make(map[string]bool),
		replayFiles: make([]string, 0),
		batchSize:   batchSize, batches: batches, telemetry: telemetry, channel: channel,
		workers: make(map[int]*readerWorker), done: make(chan struct{}),
	}
	pool.available = sync.NewCond(&pool.mu)
	pool.reconcile(workers)
	go func() {
		<-poolCtx.Done()
		pool.mu.Lock()
		pool.stopping = true
		pool.available.Broadcast()
		pool.mu.Unlock()
		pool.wg.Wait()
		close(pool.done)
	}()
	return pool, nil
}

func (p *readerPool) reconcile(desired int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || p.ctx.Err() != nil {
		return
	}
	if desired != p.desired {
		p.forceGeneration++
		p.forcePending = false
	}
	p.desired = desired
	workerIDs := make([]int, 0, len(p.workers))
	for workerID := range p.workers {
		workerIDs = append(workerIDs, workerID)
	}
	sort.Slice(workerIDs, func(i, j int) bool {
		left, right := p.workers[workerIDs[i]], p.workers[workerIDs[j]]
		if left.busy != right.busy {
			return !left.busy
		}
		return left.workerID < right.workerID
	})
	for index, workerID := range workerIDs {
		worker := p.workers[workerID]
		worker.draining = worker.forced || index < len(workerIDs)-desired
		lifecycle := "active"
		if worker.draining {
			lifecycle = "draining"
		}
		p.telemetry.setWorkerLifecycle(workerID, lifecycle)
	}
	for len(p.workers) < desired {
		p.startReplacementLocked()
	}
	p.scheduleBlockedForceLocked()
	p.available.Broadcast()
}

func (p *readerPool) scheduleBlockedForceLocked() {
	if p.forcePending || p.stopping || p.ctx.Err() != nil || len(p.workers) <= p.desired {
		return
	}
	var blocked bool
	for _, worker := range p.workers {
		if worker.draining && worker.blocked && !worker.forced {
			blocked = true
			break
		}
	}
	if !blocked {
		return
	}
	p.forcePending = true
	p.forceGeneration++
	sequence := p.forceGeneration
	callback := func() { p.forceBlocked(sequence) }
	if p.afterForce != nil {
		p.afterForce(readerForcedDrainGracePeriod, callback)
		return
	}
	time.AfterFunc(readerForcedDrainGracePeriod, callback)
}

func (p *readerPool) forceBlocked(sequence uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sequence != p.forceGeneration || p.stopping || p.ctx.Err() != nil {
		return
	}
	p.forcePending = false
	if len(p.workers) <= p.desired {
		return
	}
	var victim *readerWorker
	for _, worker := range p.workers {
		if worker.draining && worker.blocked && !worker.forced &&
			(victim == nil || worker.workerID < victim.workerID) {
			victim = worker
		}
	}
	if victim == nil {
		return
	}
	victim.forced = true
	victim.cancel()
	p.scheduleBlockedForceLocked()
}

func (p *readerPool) stop() <-chan struct{} {
	p.cancel()
	p.mu.Lock()
	p.stopping = true
	p.available.Broadcast()
	p.mu.Unlock()
	return p.done
}

func (p *readerPool) runWorker(worker *readerWorker) {
	defer p.wg.Done()
	defer worker.cancel()
	defer func() {
		p.mu.Lock()
		delete(p.workers, worker.workerID)
		p.telemetry.unregisterWorker(worker.workerID)
		if !p.stopping && p.ctx.Err() == nil && len(p.workers) < p.desired {
			p.startReplacementLocked()
		}
		p.mu.Unlock()
	}()
	for {
		filePath, ok := p.claimNextFile(worker)
		if !ok {
			return
		}
		p.readFile(worker, filePath)
		p.mu.Lock()
		p.releaseFileLocked(worker, filePath)
		p.mu.Unlock()
	}
}

func (p *readerPool) releaseFileLocked(worker *readerWorker, filePath string) {
	if worker.forced && !p.stopping && p.ctx.Err() == nil {
		p.replayFiles = append(p.replayFiles, filePath)
	}
	delete(p.active, filePath)
	worker.busy = false
	if !worker.forced {
		p.telemetry.setWorkerIdle(worker.workerID)
	}
	p.available.Broadcast()
}

func (p *readerPool) startReplacementLocked() {
	workerID := p.nextWorkerID
	p.nextWorkerID++
	workerCtx, cancel := context.WithCancel(p.ctx)
	worker := &readerWorker{workerID: workerID, ctx: workerCtx, cancel: cancel}
	p.workers[workerID] = worker
	p.telemetry.registerWorker(workerID)
	p.wg.Add(1)
	go p.runWorker(worker)
}

func (p *readerPool) claimNextFile(worker *readerWorker) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for !p.stopping && p.ctx.Err() == nil {
		if worker.draining {
			return "", false
		}
		if len(p.replayFiles) > 0 {
			filePath := p.replayFiles[0]
			p.replayFiles[0] = ""
			p.replayFiles = p.replayFiles[1:]
			p.active[filePath] = true
			worker.busy = true
			return filePath, true
		}
		for offset := range p.files {
			index := (p.nextFile + offset) % len(p.files)
			filePath := p.files[index]
			if !p.active[filePath] {
				p.active[filePath] = true
				p.nextFile = index + 1
				worker.busy = true
				return filePath, true
			}
		}
		p.available.Wait()
	}
	return "", false
}

func (p *readerPool) readFile(worker *readerWorker, filePath string) {
	source := filepath.ToSlash(filePath)
	p.telemetry.setWorkerReading(worker.workerID, source)
	file, err := os.Open(filePath)
	if err != nil {
		panic(fmt.Sprintf("failed to open file %s: %v", filePath, err))
	}
	defer func() {
		if err := file.Close(); err != nil {
			panic(fmt.Sprintf("failed to close file %s: %v", filePath, err))
		}
	}()
	reader := parquet.NewGenericReader[Transaction](file)
	defer func() {
		if err := reader.Close(); err != nil {
			panic(fmt.Sprintf("failed to close reader for file %s: %v", filePath, err))
		}
	}()
	rows := make([]Transaction, p.batchSize)
	batch := make([]Transaction, 0, p.batchSize)
	for {
		if worker.ctx.Err() != nil {
			return
		}
		n, err := reader.Read(rows[:p.batchSize-len(batch)])
		if n > 0 {
			p.telemetry.recordRead(n, source)
			var sent bool
			batch, sent = p.appendRowsForWorker(worker, source, batch, rows[:n])
			if !sent {
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			panic(fmt.Sprintf("failed to read rows from file %s: %v", filePath, err))
		}
	}
	if len(batch) > 0 {
		if !p.sendBatch(worker, source, batch) {
			return
		}
	}
	if worker.ctx.Err() == nil {
		p.telemetry.setWorkerCompleted(worker.workerID)
	}
}

func (p *readerPool) appendRows(batch, rows []Transaction) ([]Transaction, bool) {
	return p.appendRowsForWorker(nil, "", batch, rows)
}

func (p *readerPool) appendRowsForWorker(worker *readerWorker, source string, batch, rows []Transaction) ([]Transaction, bool) {
	for len(rows) > 0 {
		if worker != nil && worker.ctx.Err() != nil {
			return nil, false
		}
		remainingCapacity := p.batchSize - len(batch)
		rowsToAppend := min(remainingCapacity, len(rows))
		batch = append(batch, rows[:rowsToAppend]...)
		rows = rows[rowsToAppend:]

		if len(batch) < p.batchSize {
			continue
		}
		if !p.sendBatch(worker, source, batch) {
			return nil, false
		}
		batch = make([]Transaction, 0, p.batchSize)
	}

	return batch, true
}

func (p *readerPool) sendBatch(worker *readerWorker, source string, batch []Transaction) bool {
	if worker == nil {
		return p.channel.send(p.ctx, p.batches, batch)
	}
	if worker.ctx.Err() != nil {
		return false
	}
	return p.channel.sendWithBlocked(worker.ctx, p.batches, batch,
		func() {
			p.mu.Lock()
			worker.blocked = true
			p.scheduleBlockedForceLocked()
			p.mu.Unlock()
			p.telemetry.setWorkerBlocked(worker.workerID)
		},
		func() {
			p.mu.Lock()
			worker.blocked = false
			p.mu.Unlock()
			p.telemetry.setWorkerReading(worker.workerID, source)
		},
	)
}
