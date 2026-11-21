package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

type ReaderActivity int

const (
	ReaderIdle ReaderActivity = iota
	ReaderReading
	ReaderBlocked
)

type ReaderWorkerState struct {
	Activity ReaderActivity
	Draining bool
}

// readerWorker holds the per-worker lifecycle state.
type readerWorker struct {
	wake chan struct{}
	// Prevents the worker from accepting new work.
	draining bool
	// Reading and blocked both retain ownership of the current source file.
	activity ReaderActivity
}

type ReaderSnapshot struct {
	RowsRead     int64
	WorkerStates []ReaderWorkerState
}

type Source struct {
	files []string
}

type SourceError struct {
	Op   string
	Path string
	Err  error
}

func (e *SourceError) Error() string { return e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

// ReaderFile separates opening the file from interpreting its rows.
type ReaderFile interface {
	io.Closer
	OpenRows() (RowReader, error)
}

type RowReader interface {
	Read([]Transaction) (int, error)
	Close() error
}

type parquetFile struct{ *os.File }

func (f parquetFile) OpenRows() (RowReader, error) { return openParquetReader(f.File) }

func openReaderFile(path string) (ReaderFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return parquetFile{file}, nil
}

func DiscoverSource(dataPath string) (Source, error) {
	files, err := filepath.Glob(dataPath)
	if err != nil {
		return Source{}, &SourceError{
			Op: "glob", Path: dataPath, Err: err,
		}
	}
	if len(files) == 0 {
		return Source{}, &SourceError{
			Op: "glob", Path: dataPath,
			Err: errors.New("no files found matching pattern"),
		}
	}
	sort.Strings(files)
	return Source{files: files}, nil
}

// readerPool owns file admission. A file is assigned once to exactly one worker;
// workers emit their locally completed batches directly to the reader channel.
type readerPool struct {
	// Protects file ownership and mutable worker state.
	mu sync.Mutex
	// Is canceled when the whole pool must stop.
	ctx context.Context
	// Tracks every started worker goroutine until it exits.
	wg sync.WaitGroup
	// Closes after pool cancellation and all workers have exited.
	done chan struct{}
	// Contains the sorted, immutable source paths.
	source Source
	// Records source paths currently claimed by a worker.
	active map[string]bool
	// Is the round-robin cursor into the source paths.
	nextFile int
	// Is the maximum number of transactions in an emitted batch.
	batchSize int
	// Records Reader-stage observations.
	telemetry *readerTelemetry
	// Records observations for batches.
	channel *batchQueue
	// Contains live Reader workers in their start order.
	workers []*readerWorker
	// Prevents new work while the pool is shutting down.
	stopping bool
	// Closes at the end of admission; in-progress sends still use ctx.
	readingDone chan struct{}
	// Delivers exactly the first fatal source error to the event loop.
	sourceErrors    chan error
	sourceErrorOnce sync.Once
	logger          *zap.Logger
	openFile        func(string) (ReaderFile, error)
	beforeClaim     func(*readerWorker)
}

func startReaderPool(
	ctx context.Context,
	source Source,
	batchSize, workers int,
	queue *batchQueue,
	telemetry *readerTelemetry,
	openFile func(string) (ReaderFile, error),
	beforeClaim func(*readerWorker),
	logger *zap.Logger,
) *readerPool {
	if openFile == nil {
		openFile = openReaderFile
	}
	pool := &readerPool{
		ctx: ctx, source: source, active: make(map[string]bool),
		batchSize: batchSize, telemetry: telemetry, channel: queue,
		workers: make([]*readerWorker, 0, workers), done: make(chan struct{}),
		readingDone:  make(chan struct{}),
		sourceErrors: make(chan error, 1),
		logger:       logger,
		openFile:     openFile,
		beforeClaim:  beforeClaim,
	}
	pool.reconcile(workers)
	go func() {
		select {
		case <-ctx.Done():
			pool.stopReading()
		case <-pool.readingDone:
		}
		pool.wg.Wait()
		close(pool.done)
	}()
	return pool
}

func (p *readerPool) reconcile(desired int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || p.ctx.Err() != nil {
		return
	}
	active := 0
	for _, worker := range p.workers {
		if !worker.draining {
			active++
		}
	}
	if active > desired {
		for _, busy := range []bool{false, true} {
			for _, worker := range p.workers {
				if active == desired {
					break
				}
				if !worker.draining && (worker.activity != ReaderIdle) == busy {
					worker.draining = true
					active--
				}
			}
		}
	} else if active < desired {
		for _, worker := range p.workers {
			if active == desired {
				break
			}
			if worker.draining {
				worker.draining = false
				active++
			}
		}
	}
	for active < desired {
		p.startWorkerLocked()
		active++
	}
	for _, worker := range p.workers {
		wakeReader(worker)
	}
}

func wakeReader(worker *readerWorker) {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (p *readerPool) stopReading() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopping {
		p.stopping = true
		close(p.readingDone)
		for _, worker := range p.workers {
			wakeReader(worker)
		}
	}
}

func (p *readerPool) canRead() bool {
	select {
	case <-p.readingDone:
		return false
	default:
		return p.ctx.Err() == nil
	}
}

func (p *readerPool) runWorker(worker *readerWorker) {
	defer p.wg.Done()
	p.logger.Debug("reader worker started", zap.String("event", "reader_worker_started"))
	defer p.logger.Debug("reader worker stopped", zap.String("event", "reader_worker_stopped"))
	for {
		filePath, ok := p.claimNextFile(worker)
		if !ok {
			return
		}
		if p.canRead() {
			if err := p.readFile(worker, filePath); err != nil {
				p.reportSourceError(err)
			}
		}
		p.mu.Lock()
		p.releaseFileLocked(worker, filePath)
		p.mu.Unlock()
	}
}

func (p *readerPool) reportSourceError(sourceError *SourceError) {
	p.sourceErrorOnce.Do(func() {
		p.sourceErrors <- sourceError
		p.stopReading()
	})
}

func (p *readerPool) releaseFileLocked(worker *readerWorker, filePath string) {
	delete(p.active, filePath)
	worker.activity = ReaderIdle
	for _, candidate := range p.workers {
		if candidate.activity == ReaderIdle && !candidate.draining {
			wakeReader(candidate)
		}
	}
}

func (p *readerPool) startWorkerLocked() {
	worker := &readerWorker{wake: make(chan struct{}, 1)}
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
	for {
		if p.beforeClaim != nil {
			p.beforeClaim(worker)
		}
		p.mu.Lock()
		if p.stopping || p.ctx.Err() != nil || worker.draining {
			p.removeWorkerLocked(worker)
			p.mu.Unlock()
			return "", false
		}
		for offset := range p.source.files {
			index := (p.nextFile + offset) % len(p.source.files)
			path := p.source.files[index]
			if !p.active[path] {
				p.active[path] = true
				p.nextFile = index + 1
				worker.activity = ReaderReading
				p.mu.Unlock()
				return path, true
			}
		}
		p.mu.Unlock()
		select {
		case <-p.ctx.Done():
		case <-worker.wake:
		}
	}
}

func (p *readerPool) readFile(worker *readerWorker, filePath string) *SourceError {
	file, err := p.openFile(filePath)
	if err != nil {
		return p.sourceError("open", filePath, err)
	}
	if !p.canRead() {
		return p.closeResources(filePath, nil, file, nil)
	}
	p.logger.Debug("reader source opened", zap.String("event", "reader_source_opened"))
	reader, err := file.OpenRows()
	if err != nil {
		if closeErr := file.Close(); closeErr != nil && p.ctx.Err() == nil {
			return p.sourceError("close", filePath, closeErr)
		}
		return p.sourceError("open", filePath, err)
	}
	var sourceError *SourceError
	if err := p.readBatches(worker, reader.Read); err != nil {
		sourceError = p.sourceError("read", filePath, err)
	} else if p.canRead() {
		p.logger.Debug("reader source exhausted", zap.String("event", "reader_source_exhausted"))
	}
	return p.closeResources(filePath, reader, file, sourceError)
}

func (p *readerPool) readBatches(worker *readerWorker, read func([]Transaction) (int, error)) error {
	rows := make([]Transaction, p.batchSize)
	batch := make([]Transaction, 0, p.batchSize)
	for p.canRead() {
		n, err := read(rows[:p.batchSize-len(batch)])
		if n > 0 {
			p.telemetry.recordRead(n)
			var sent bool
			batch, sent = p.appendRows(worker, batch, rows[:n])
			if !sent {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	if len(batch) > 0 {
		p.sendBatch(worker, batch)
	}
	return nil
}

func openParquetReader(file *os.File) (reader *parquet.GenericReader[Transaction], err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if cause, ok := recovered.(error); ok {
				err = fmt.Errorf("open parquet reader: %w", cause)
			} else {
				err = fmt.Errorf("open parquet reader: %v", recovered)
			}
		}
	}()
	return parquet.NewGenericReader[Transaction](file), nil
}

func (p *readerPool) closeResources(
	filePath string,
	reader io.Closer,
	file io.Closer,
	sourceError *SourceError,
) *SourceError {
	cancelled := p.ctx.Err() != nil
	if reader != nil {
		if err := reader.Close(); sourceError == nil && !cancelled && err != nil {
			sourceError = p.sourceError("reader-close", filePath, err)
		}
	}
	if err := file.Close(); sourceError == nil && !cancelled && err != nil {
		sourceError = p.sourceError("close", filePath, err)
	}
	return sourceError
}

func (p *readerPool) sourceError(operation, sourcePath string, err error) *SourceError {
	return &SourceError{
		Op:   operation,
		Path: sourcePath,
		Err:  err,
	}
}

func (p *readerPool) appendRows(worker *readerWorker, batch, rows []Transaction) ([]Transaction, bool) {
	for len(rows) > 0 {
		if p.ctx.Err() != nil {
			return nil, false
		}
		remainingCapacity := p.batchSize - len(batch)
		rowsToAppend := min(remainingCapacity, len(rows))
		batch = append(batch, rows[:rowsToAppend]...)
		rows = rows[rowsToAppend:]

		if len(batch) < p.batchSize {
			continue
		}
		if !p.sendBatch(worker, batch) {
			return nil, false
		}
		batch = make([]Transaction, 0, p.batchSize)
	}

	return batch, true
}

func (p *readerPool) sendBatch(worker *readerWorker, batch []Transaction) bool {
	if p.ctx.Err() != nil {
		return false
	}
	if p.channel.trySend(batch) {
		return true
	}

	pending := p.channel.beginBlockedSend(batch)
	p.mu.Lock()
	worker.activity = ReaderBlocked
	p.mu.Unlock()

	sent := pending.wait(p.ctx)
	p.mu.Lock()
	worker.activity = ReaderReading
	p.mu.Unlock()
	return sent
}

func (p *readerPool) snapshot() ReaderSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := ReaderSnapshot{WorkerStates: make([]ReaderWorkerState, 0, len(p.workers))}
	for _, worker := range p.workers {
		snapshot.WorkerStates = append(snapshot.WorkerStates, ReaderWorkerState{
			Activity: worker.activity, Draining: worker.draining,
		})
	}
	return snapshot
}
