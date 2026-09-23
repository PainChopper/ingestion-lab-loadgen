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

type readerWorker struct {
	ordinal  int
	ctx      context.Context
	cancel   context.CancelFunc
	draining bool
	busy     bool
	blocked  bool
	forced   bool
}

// readerPool owns file admission. A file is assigned once to exactly one worker;
// workers emit their locally completed batches directly to the reader channel.
type readerPool struct {
	mu            sync.Mutex
	available     *sync.Cond
	ctx           context.Context
	cancel        context.CancelFunc
	files         []string
	active        map[string]bool
	replayFiles   []string
	nextFile      int
	batchSize     int
	batches       chan<- []Transaction
	telemetry     *readerTelemetry
	channel       *channelTelemetry
	workers       map[int]*readerWorker
	desired       int
	stopping      bool
	forcePending  bool
	forceSequence uint64
	afterForce    func(time.Duration, func())
	wg            sync.WaitGroup
	done          chan struct{}
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
		p.forceSequence++
		p.forcePending = false
	}
	p.desired = desired
	ordinals := make([]int, 0, len(p.workers))
	for ordinal := range p.workers {
		ordinals = append(ordinals, ordinal)
	}
	sort.Slice(ordinals, func(i, j int) bool {
		left, right := p.workers[ordinals[i]], p.workers[ordinals[j]]
		if left.busy != right.busy {
			return !left.busy
		}
		return left.ordinal < right.ordinal
	})
	for index, ordinal := range ordinals {
		worker := p.workers[ordinal]
		worker.draining = worker.forced || index < len(ordinals)-desired
		lifecycle := "active"
		if worker.draining {
			lifecycle = "draining"
		}
		p.telemetry.setWorkerLifecycle(ordinal, lifecycle)
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
	p.forceSequence++
	sequence := p.forceSequence
	callback := func() { p.forceBlocked(sequence) }
	if p.afterForce != nil {
		p.afterForce(time.Second, callback)
		return
	}
	time.AfterFunc(time.Second, callback)
}

func (p *readerPool) forceBlocked(sequence uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sequence != p.forceSequence || p.stopping || p.ctx.Err() != nil {
		return
	}
	p.forcePending = false
	if len(p.workers) <= p.desired {
		return
	}
	var victim *readerWorker
	for _, worker := range p.workers {
		if worker.draining && worker.blocked && !worker.forced &&
			(victim == nil || worker.ordinal < victim.ordinal) {
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
		delete(p.workers, worker.ordinal)
		p.telemetry.finishWorker(worker.ordinal)
		if !p.stopping && p.ctx.Err() == nil && len(p.workers) < p.desired {
			p.startReplacementLocked()
		}
		p.mu.Unlock()
	}()
	for {
		filePath, ok := p.nextJob(worker)
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
		p.telemetry.setWorkerIdle(worker.ordinal)
	}
	p.available.Broadcast()
}

func (p *readerPool) startReplacementLocked() {
	ordinal := 0
	for p.workers[ordinal] != nil {
		ordinal++
	}
	workerCtx, cancel := context.WithCancel(p.ctx)
	worker := &readerWorker{ordinal: ordinal, ctx: workerCtx, cancel: cancel}
	p.workers[ordinal] = worker
	p.telemetry.startWorker(ordinal)
	p.wg.Add(1)
	go p.runWorker(worker)
}

func (p *readerPool) nextJob(worker *readerWorker) (string, bool) {
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
	p.telemetry.setWorkerReading(worker.ordinal, source)
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
		p.telemetry.setWorkerCompleted(worker.ordinal)
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
			p.telemetry.setWorkerBlocked(worker.ordinal)
		},
		func() {
			p.mu.Lock()
			worker.blocked = false
			p.mu.Unlock()
			p.telemetry.setWorkerReading(worker.ordinal, source)
		},
	)
}
