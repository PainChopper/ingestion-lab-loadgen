package main

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

type senderAttemptOutcome uint8

const (
	senderAttemptSuccess senderAttemptOutcome = iota
	senderAttemptRetryableFailure
	senderAttemptTerminalFailure
	senderAttemptCanceled
)

type senderAttempt func(context.Context, []Transaction, int) senderAttemptOutcome

type senderWorker struct {
	wake     chan struct{}
	work     chan []Transaction
	done     chan struct{}
	draining bool
	busy     bool
	backoff  bool
}

type senderPoolSnapshot struct {
	liveWorkers             int
	idleWorkers             int
	inFlightWorkers         int
	backoffWorkers          int
	drainingWorkers         int
	drainingIdleWorkers     int
	drainingInFlightWorkers int
	drainingBackoffWorkers  int
}

type senderPool struct {
	// mu guards worker state; intakeMu keeps admission before stop's boundary.
	mu                                       sync.Mutex
	intakeMu                                 sync.Mutex
	available                                *sync.Cond
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	intakeDone                               chan struct{}
	wg                                       sync.WaitGroup
	workers                                  []*senderWorker
	desired                                  int
	stopping                                 bool
	retry                                    senderRetryPolicy
	batches                                  <-chan []Transaction
	channelTelemetry                         *channelTelemetry
	telemetry                                *senderTelemetry
	terminallyCompletedTransactionsSinceTick *atomic.Int64
	attempt                                  senderAttempt
	wait                                     func(context.Context, time.Duration) bool
	logger                                   *zap.Logger
}

func startSenderPool(
	parent context.Context,
	batches <-chan []Transaction,
	channelTelemetry *channelTelemetry,
	telemetry *senderTelemetry,
	terminallyCompletedTransactionsSinceTick *atomic.Int64,
	workers int,
	api senderAPIPolicy,
	retry senderRetryPolicy,
	loggers ...*zap.Logger,
) *senderPool {
	ctx, cancel := context.WithCancel(parent)
	pool := &senderPool{
		ctx:                                      ctx,
		cancel:                                   cancel,
		intakeDone:                               make(chan struct{}),
		workers:                                  make([]*senderWorker, 0, workers),
		retry:                                    retry,
		batches:                                  batches,
		channelTelemetry:                         channelTelemetry,
		telemetry:                                telemetry,
		terminallyCompletedTransactionsSinceTick: terminallyCompletedTransactionsSinceTick,
		attempt:                                  newSenderHTTPAttempt(api.URL, http.DefaultClient).deliver,
		wait:                                     waitSenderBackoff,
		logger:                                   loggerOrNop(loggers),
	}
	pool.available = sync.NewCond(&pool.mu)
	pool.reconcile(workers)
	go pool.runIntake()
	return pool
}

func (p *senderPool) reconcile(desired int) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return
	}
	var idleDraining []<-chan struct{}
	p.desired = desired
	activeWorkers := 0
	for _, worker := range p.workers {
		if !worker.draining {
			activeWorkers++
		}
	}
	if activeWorkers > desired {
		for index := len(p.workers) - 1; activeWorkers > desired && index >= 0; index-- {
			worker := p.workers[index]
			if worker.draining {
				continue
			}
			worker.draining = true
			activeWorkers--
		}
	} else if activeWorkers < desired {
		for _, worker := range p.workers {
			if !worker.draining {
				continue
			}
			worker.draining = false
			activeWorkers++
		}
	}
	for _, worker := range p.workers {
		if worker.draining {
			if !worker.busy {
				idleDraining = append(idleDraining, worker.done)
			}
		}
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
	for activeWorkers < desired {
		p.startWorkerLocked()
		activeWorkers++
	}
	p.available.Broadcast()
	p.mu.Unlock()
	for _, done := range idleDraining {
		<-done
	}
}

func (p *senderPool) stop() <-chan struct{} {
	p.cancel()
	p.intakeMu.Lock()
	p.mu.Lock()
	p.stopping = true
	p.available.Broadcast()
	for _, worker := range p.workers {
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
	p.mu.Unlock()
	p.intakeMu.Unlock()
	done := make(chan struct{})
	go func() {
		<-p.intakeDone
		p.wg.Wait()
		close(done)
	}()
	return done
}

func (p *senderPool) startWorkerLocked() {
	worker := &senderWorker{
		wake: make(chan struct{}, 1),
		work: make(chan []Transaction, 1),
		done: make(chan struct{}),
	}
	p.workers = append(p.workers, worker)
	p.wg.Add(1)
	go p.runWorker(worker)
}

func (p *senderPool) runIntake() {
	defer close(p.intakeDone)
	for {
		p.mu.Lock()
		for p.availableWorkerLocked() == nil && p.ctx.Err() == nil {
			p.available.Wait()
		}
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return
		}

		p.intakeMu.Lock()
		if p.ctx.Err() != nil {
			p.intakeMu.Unlock()
			return
		}
		select {
		case <-p.ctx.Done():
			p.intakeMu.Unlock()
			return
		case batch, ok := <-p.batches:
			if !ok {
				p.intakeMu.Unlock()
				return
			}
			p.channelTelemetry.recordReceive(len(batch))
			p.mu.Lock()
			worker := p.availableWorkerLocked()
			for worker == nil {
				p.available.Wait()
				worker = p.availableWorkerLocked()
			}
			worker.busy = true
			worker.work <- batch
			p.mu.Unlock()
			p.intakeMu.Unlock()
		}
	}
}

func (p *senderPool) availableWorkerLocked() *senderWorker {
	var selected *senderWorker
	for _, worker := range p.workers {
		if worker.draining || worker.busy {
			continue
		}
		if selected == nil {
			selected = worker
		}
	}
	return selected
}

func (p *senderPool) runWorker(worker *senderWorker) {
	defer p.wg.Done()
	defer close(worker.done)
	defer p.workerExited(worker)
	p.logger.Info("sender worker started", zap.String("event", "sender_worker_started"))
	defer p.logger.Info("sender worker stopped", zap.String("event", "sender_worker_stopped"))
	for {
		p.mu.Lock()
		select {
		case batch := <-worker.work:
			p.mu.Unlock()
			p.finishAcceptedBatch(worker, batch)
			continue
		default:
		}
		stop := p.stopping || worker.draining
		p.mu.Unlock()
		if stop {
			return
		}

		select {
		case <-worker.wake:
			continue
		case batch := <-worker.work:
			p.finishAcceptedBatch(worker, batch)
		}
	}
}

func (p *senderPool) finishAcceptedBatch(worker *senderWorker, batch []Transaction) {
	if p.processBatch(worker, batch) {
		p.terminallyCompletedTransactionsSinceTick.Add(int64(len(batch)))
	}
	p.mu.Lock()
	worker.busy = false
	worker.backoff = false
	p.available.Broadcast()
	p.mu.Unlock()
}

func (p *senderPool) workerExited(worker *senderWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := -1
	for candidateIndex, candidate := range p.workers {
		if candidate == worker {
			index = candidateIndex
			break
		}
	}
	if index < 0 {
		return
	}
	p.workers = append(p.workers[:index], p.workers[index+1:]...)
	if !p.stopping && !worker.draining && p.activeWorkerCountLocked() < p.desired {
		p.startWorkerLocked()
	}
	p.available.Broadcast()
}

func (p *senderPool) activeWorkerCountLocked() int {
	activeWorkers := 0
	for _, worker := range p.workers {
		if !worker.draining {
			activeWorkers++
		}
	}
	return activeWorkers
}

func (p *senderPool) processBatch(worker *senderWorker, batch []Transaction) bool {
	for attempt := 1; ; attempt++ {
		p.mu.Lock()
		worker.backoff = false
		p.mu.Unlock()
		switch p.attempt(p.ctx, batch, attempt) {
		case senderAttemptSuccess:
			p.telemetry.finishBatch()
			return true
		case senderAttemptCanceled:
			return false
		case senderAttemptRetryableFailure, senderAttemptTerminalFailure:
			p.logger.Warn("batch delivery will retry", zap.String("event", "batch_delivery_retry"), zap.Int("batch_size", len(batch)), zap.Int("attempt", attempt))
		}

		p.mu.Lock()
		worker.backoff = true
		p.mu.Unlock()
		delay := p.retry.delay(attempt, batch)
		if !p.wait(p.ctx, delay) {
			return false
		}
	}
}

func (p *senderPool) aggregateSnapshot() senderPoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := senderPoolSnapshot{liveWorkers: len(p.workers)}
	for _, worker := range p.workers {
		var draining *int
		switch {
		case worker.backoff:
			snapshot.backoffWorkers++
			draining = &snapshot.drainingBackoffWorkers
		case worker.busy:
			snapshot.inFlightWorkers++
			draining = &snapshot.drainingInFlightWorkers
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

func waitSenderBackoff(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func deterministicSenderValue(batch []Transaction, attempt int, modulus uint64) int {
	hash := fnv.New64a()
	var values [16]byte
	binary.LittleEndian.PutUint64(values[:8], uint64(len(batch)))
	binary.LittleEndian.PutUint64(values[8:], uint64(attempt))
	_, _ = hash.Write(values[:])
	if len(batch) > 0 {
		_, _ = hash.Write([]byte(batch[0].ClientID))
	}
	return int(hash.Sum64() % modulus)
}
