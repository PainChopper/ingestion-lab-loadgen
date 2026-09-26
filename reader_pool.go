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

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

// readerWorker holds the per-worker cancellation and lifecycle state.
type readerWorker struct {
	// Is canceled when the pool stops.
	ctx context.Context
	// Prevents the worker from claiming another file.
	draining bool
	// Indicates that the worker currently owns a source file.
	busy bool
	// Indicates that the worker is blocked while sending a batch.
	blocked bool
}

type readerPoolSnapshot struct {
	liveWorkers            int
	idleWorkers            int
	readingWorkers         int
	blockedWorkers         int
	drainingWorkers        int
	drainingIdleWorkers    int
	drainingReadingWorkers int
	drainingBlockedWorkers int
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
	// Is the requested number of active Reader workers.
	desired int
	// Prevents new work while the pool is shutting down.
	stopping bool
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
		batchSize: batchSize, batches: batches, telemetry: telemetry, channel: channel,
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
		worker.draining = index < len(orderedWorkers)-desired
	}
	for len(p.workers) < desired {
		p.startReplacementLocked()
	}
	p.available.Broadcast()
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
	p.logger.Info("reader worker started", zap.String("event", "reader_worker_started"))
	defer func() {
		p.logger.Info("reader worker stopped", zap.String("event", "reader_worker_stopped"))
		p.mu.Lock()
		p.removeWorkerLocked(worker)
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
	delete(p.active, filePath)
	worker.busy = false
	p.available.Broadcast()
}

func (p *readerPool) startReplacementLocked() {
	worker := &readerWorker{ctx: p.ctx}
	p.workers = append(p.workers, worker)
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
	file, err := os.Open(filePath)
	if err != nil {
		return p.newReaderSourceError("open", filePath, err)
	}
	p.logger.Info("reader source opened", zap.String("event", "reader_source_opened"))
	reader, err := openParquetReader(file)
	if err != nil {
		if closeErr := file.Close(); closeErr != nil && worker.ctx.Err() == nil && p.ctx.Err() == nil {
			return p.newReaderSourceError("close", filePath, closeErr)
		}
		return p.newReaderSourceError("open", filePath, err)
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
			p.telemetry.recordRead(n)
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
			sourceError = p.newReaderSourceError("read", filePath, err)
			break
		}
	}
	if sourceError == nil && !batchSendStopped && worker.ctx.Err() == nil && len(batch) > 0 {
		if !p.sendBatch(worker, source, batch) {
			batchSendStopped = true
		}
	}
	if sourceError == nil && !batchSendStopped && worker.ctx.Err() == nil {
		p.logger.Info("reader source exhausted", zap.String("event", "reader_source_exhausted"))
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
		sourceError = p.newReaderSourceError("reader-close", filePath, err)
	}
	if err := file.Close(); sourceError == nil && !cancelled && err != nil {
		sourceError = p.newReaderSourceError("close", filePath, err)
	}
	return sourceError
}

func (p *readerPool) newReaderSourceError(operation, sourcePath string, err error) *readerSourceError {
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
	p.mu.Unlock()

	sent := pending.wait(worker.ctx)
	p.mu.Lock()
	worker.blocked = false
	p.mu.Unlock()
	return sent
}

func (p *readerPool) aggregateSnapshot() readerPoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := readerPoolSnapshot{liveWorkers: len(p.workers)}
	for _, worker := range p.workers {
		var draining *int
		switch {
		case worker.blocked:
			snapshot.blockedWorkers++
			draining = &snapshot.drainingBlockedWorkers
		case worker.busy:
			snapshot.readingWorkers++
			draining = &snapshot.drainingReadingWorkers
		default:
			snapshot.idleWorkers++
			draining = &snapshot.drainingIdleWorkers
		}
		if worker.draining {
			snapshot.drainingWorkers++
			(*draining)++
		}
	}
	return snapshot
}
