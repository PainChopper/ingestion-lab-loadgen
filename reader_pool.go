package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
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
	// Is the configured source root used for relative diagnostics.
	sourceDirectory string
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
	// Contains live Reader workers in their start order.
	workers []*readerWorker
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
	// Delivers exactly the first fatal source error to the event loop.
	sourceErrors    chan readerSourceError
	sourceErrorOnce sync.Once
	logger          *zap.Logger
}

func startReaderPool(
	ctx context.Context,
	dataPath string,
	batchSize, workers int,
	batches chan<- []Transaction,
	telemetry *readerTelemetry,
	channel *channelTelemetry,
	loggers ...*zap.Logger,
) (*readerPool, error) {
	sourceDirectory := readerSourceDirectory(dataPath)
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return nil, readerSourceError{Category: "source", Operation: "glob", RelativePath: relativeSourcePath(sourceDirectory, dataPath), Message: err.Error()}
	}
	if len(files) == 0 {
		return nil, readerSourceError{Category: "source", Operation: "glob", RelativePath: relativeSourcePath(sourceDirectory, dataPath), Message: "no files found matching pattern"}
	}
	sort.Strings(files)
	poolCtx, cancel := context.WithCancel(ctx)
	pool := &readerPool{
		ctx: poolCtx, cancel: cancel, files: files, sourceDirectory: sourceDirectory, active: make(map[string]bool),
		replayFiles: make([]string, 0),
		batchSize:   batchSize, batches: batches, telemetry: telemetry, channel: channel,
		workers: make([]*readerWorker, 0, workers), done: make(chan struct{}),
		sourceErrors: make(chan readerSourceError, 1),
		logger:       loggerOrNop(loggers),
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
	orderedWorkers := append([]*readerWorker(nil), p.workers...)
	sort.SliceStable(orderedWorkers, func(i, j int) bool {
		left, right := orderedWorkers[i], orderedWorkers[j]
		if left.busy != right.busy {
			return !left.busy
		}
		return false
	})
	for index, worker := range orderedWorkers {
		worker.draining = worker.forced || index < len(orderedWorkers)-desired
		lifecycle := "active"
		if worker.draining {
			lifecycle = "draining"
		}
		p.telemetry.setWorkerLifecycle(worker.workerID, lifecycle)
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
		if worker.draining && worker.blocked && !worker.forced && victim == nil {
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
	p.logger.Info("reader worker started", zap.String("event", "reader_worker_started"), zap.Int("worker_id", worker.workerID))
	defer func() {
		p.logger.Info("reader worker stopped", zap.String("event", "reader_worker_stopped"), zap.Int("worker_id", worker.workerID))
		p.mu.Lock()
		p.removeWorkerLocked(worker)
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
		if err := p.readFile(worker, filePath); err != nil {
			p.reportSourceError(*err)
		}
		p.mu.Lock()
		p.releaseFileLocked(worker, filePath)
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return
		}
	}
}

func (p *readerPool) reportSourceError(sourceError readerSourceError) {
	p.sourceErrorOnce.Do(func() {
		p.sourceErrors <- sourceError
		p.cancel()
		p.mu.Lock()
		p.stopping = true
		p.available.Broadcast()
		p.mu.Unlock()
	})
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
	p.workers = append(p.workers, worker)
	p.telemetry.registerWorker(workerID)
	p.wg.Add(1)
	go p.runWorker(worker)
}

func (p *readerPool) removeWorkerLocked(worker *readerWorker) {
	for index, candidate := range p.workers {
		if candidate == worker {
			p.workers = append(p.workers[:index], p.workers[index+1:]...)
			return
		}
	}
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

func (p *readerPool) readFile(worker *readerWorker, filePath string) *readerSourceError {
	source := filepath.ToSlash(filePath)
	p.telemetry.setWorkerReading(worker.workerID, source)
	file, err := os.Open(filePath)
	if err != nil {
		return p.newReaderSourceError("open", filePath, worker.workerID, err)
	}
	p.logger.Info("reader source opened", zap.String("event", "reader_source_opened"), zap.Int("worker_id", worker.workerID))
	reader, err := openParquetReader(file)
	if err != nil {
		if closeErr := file.Close(); closeErr != nil && worker.ctx.Err() == nil && p.ctx.Err() == nil {
			return p.newReaderSourceError("close", filePath, worker.workerID, closeErr)
		}
		return p.newReaderSourceError("open", filePath, worker.workerID, err)
	}
	rows := make([]Transaction, p.batchSize)
	batch := make([]Transaction, 0, p.batchSize)
	var sourceError *readerSourceError
	batchSendStopped := false
	for {
		if worker.ctx.Err() != nil {
			break
		}
		n, err := reader.Read(rows[:p.batchSize-len(batch)])
		if n > 0 {
			p.telemetry.recordRead(n, source)
			var sent bool
			batch, sent = p.appendRowsForWorker(worker, source, batch, rows[:n])
			if !sent {
				batchSendStopped = true
				break
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			sourceError = p.newReaderSourceError("read", filePath, worker.workerID, err)
			break
		}
	}
	if sourceError == nil && !batchSendStopped && worker.ctx.Err() == nil && len(batch) > 0 {
		if !p.sendBatch(worker, source, batch) {
			batchSendStopped = true
		}
	}
	if sourceError == nil && !batchSendStopped && worker.ctx.Err() == nil {
		p.telemetry.setWorkerCompleted(worker.workerID)
		p.logger.Info("reader source exhausted", zap.String("event", "reader_source_exhausted"), zap.Int("worker_id", worker.workerID))
	}
	return p.closeResources(worker, filePath, reader, file, sourceError)
}

func openParquetReader(file *os.File) (reader *parquet.GenericReader[Transaction], err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("open parquet reader: %v", recovered)
		}
	}()
	return parquet.NewGenericReader[Transaction](file), nil
}

func (p *readerPool) closeResources(
	worker *readerWorker,
	filePath string,
	reader io.Closer,
	file io.Closer,
	sourceError *readerSourceError,
) *readerSourceError {
	cancelled := worker.ctx.Err() != nil || p.ctx.Err() != nil
	if err := reader.Close(); sourceError == nil && !cancelled && err != nil {
		sourceError = p.newReaderSourceError("reader-close", filePath, worker.workerID, err)
	}
	if err := file.Close(); sourceError == nil && !cancelled && err != nil {
		sourceError = p.newReaderSourceError("close", filePath, worker.workerID, err)
	}
	return sourceError
}

func (p *readerPool) newReaderSourceError(operation, sourcePath string, _ int, err error) *readerSourceError {
	return &readerSourceError{
		Category:     "source",
		Operation:    operation,
		RelativePath: relativeSourcePath(p.sourceDirectory, sourcePath),
		Message:      err.Error(),
	}
}

func readerSourceDirectory(sourcePath string) string {
	directory := filepath.Dir(sourcePath)
	for current := directory; current != filepath.Dir(current); current = filepath.Dir(current) {
		if strings.ContainsAny(filepath.Base(current), "*?[") {
			directory = filepath.Dir(current)
		}
	}
	return filepath.ToSlash(directory)
}

func relativeSourcePath(sourceDirectory, sourcePath string) string {
	relative, err := filepath.Rel(filepath.FromSlash(sourceDirectory), sourcePath)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filepath.Base(sourcePath))
	}
	return filepath.ToSlash(relative)
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
	if p.channel.trySend(p.batches, batch) {
		return true
	}

	pending := p.channel.beginBlockedSend(p.batches, batch)
	p.mu.Lock()
	worker.blocked = true
	p.scheduleBlockedForceLocked()
	p.mu.Unlock()
	p.telemetry.setWorkerBlocked(worker.workerID)

	sent := pending.wait(worker.ctx)
	p.mu.Lock()
	worker.blocked = false
	p.mu.Unlock()
	if sent && worker.ctx.Err() == nil {
		p.telemetry.setWorkerReading(worker.workerID, source)
	}
	return sent
}
