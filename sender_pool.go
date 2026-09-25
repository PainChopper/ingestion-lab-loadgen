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

type senderAttempt func(context.Context, []Transaction, int, int) senderAttemptOutcome

type senderWorker struct {
	workerID int
	wake     chan struct{}
	work     chan []Transaction
	done     chan struct{}
	draining bool
	busy     bool
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
	workers                                  map[int]*senderWorker
	nextWorkerID                             int
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
	batches <-chan []Transaction,
	channelTelemetry *channelTelemetry,
	telemetry *senderTelemetry,
	terminallyCompletedTransactionsSinceTick *atomic.Int64,
	workers int,
	api senderAPIPolicy,
	retry senderRetryPolicy,
	loggers ...*zap.Logger,
) *senderPool {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &senderPool{
		ctx:                                      ctx,
		cancel:                                   cancel,
		intakeDone:                               make(chan struct{}),
		workers:                                  make(map[int]*senderWorker),
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
		for workerID := p.nextWorkerID - 1; activeWorkers > desired && workerID >= 0; workerID-- {
			worker, ok := p.workers[workerID]
			if !ok || worker.draining {
				continue
			}
			worker.draining = true
			activeWorkers--
		}
	} else if activeWorkers < desired {
		for workerID := 0; activeWorkers < desired && workerID < p.nextWorkerID; workerID++ {
			worker, ok := p.workers[workerID]
			if !ok || !worker.draining {
				continue
			}
			worker.draining = false
			activeWorkers++
		}
	}
	for workerID, worker := range p.workers {
		lifecycle := "active"
		if worker.draining {
			lifecycle = "draining"
			if !worker.busy {
				idleDraining = append(idleDraining, worker.done)
			}
		}
		p.telemetry.setLifecycle(workerID, lifecycle)
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
	workerID := p.nextWorkerID
	p.nextWorkerID++
	worker := &senderWorker{
		workerID: workerID,
		wake:     make(chan struct{}, 1),
		work:     make(chan []Transaction, 1),
		done:     make(chan struct{}),
	}
	p.workers[workerID] = worker
	p.telemetry.startWorker(workerID)
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
		if selected == nil || worker.workerID < selected.workerID {
			selected = worker
		}
	}
	return selected
}

func (p *senderPool) runWorker(worker *senderWorker) {
	defer p.wg.Done()
	defer close(worker.done)
	defer p.workerExited(worker)
	p.logger.Info("sender worker started", zap.String("event", "sender_worker_started"), zap.Int("worker_id", worker.workerID))
	defer p.logger.Info("sender worker stopped", zap.String("event", "sender_worker_stopped"), zap.Int("worker_id", worker.workerID))
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
	p.processBatch(worker.workerID, batch)
	p.terminallyCompletedTransactionsSinceTick.Add(int64(len(batch)))
	p.mu.Lock()
	worker.busy = false
	p.available.Broadcast()
	p.mu.Unlock()
}

func (p *senderPool) workerExited(worker *senderWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.workers[worker.workerID] != worker {
		return
	}
	delete(p.workers, worker.workerID)
	p.telemetry.finishWorker(worker.workerID)
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

func (p *senderPool) processBatch(workerID int, batch []Transaction) {
	for attempt := 1; attempt <= p.retry.MaxAttempts; attempt++ {
		p.telemetry.setActivity(workerID, "in-flight")
		switch p.attempt(p.ctx, batch, workerID, attempt) {
		case senderAttemptSuccess:
			p.telemetry.finishBatch(workerID, true)
			return
		case senderAttemptTerminalFailure, senderAttemptCanceled:
			p.telemetry.finishBatch(workerID, false)
			p.logger.Error("batch delivery failed", zap.String("event", "batch_delivery_failed"), zap.Int("worker_id", workerID), zap.Int("batch_size", len(batch)), zap.Int("attempt", attempt))
			return
		case senderAttemptRetryableFailure:
			if attempt == p.retry.MaxAttempts {
				p.telemetry.finishBatch(workerID, false)
				p.logger.Error("batch delivery failed", zap.String("event", "batch_delivery_failed"), zap.Int("worker_id", workerID), zap.Int("batch_size", len(batch)), zap.Int("attempt", attempt))
				return
			}
		}

		p.telemetry.setActivity(workerID, "backoff")
		base := time.Duration(p.retry.BackoffBaseMS) * time.Millisecond
		if attempt > 1 {
			base *= time.Duration(p.retry.BackoffMultiplier)
		}
		jitter := deterministicSenderValue(batch, workerID, attempt, 41) - 20
		if !p.wait(p.ctx, base+base*time.Duration(jitter)/100) {
			p.telemetry.finishBatch(workerID, false)
			return
		}
	}
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

func deterministicSenderValue(batch []Transaction, workerID, attempt int, modulus uint64) int {
	hash := fnv.New64a()
	var values [24]byte
	binary.LittleEndian.PutUint64(values[:8], uint64(len(batch)))
	binary.LittleEndian.PutUint64(values[8:16], uint64(workerID))
	binary.LittleEndian.PutUint64(values[16:], uint64(attempt))
	_, _ = hash.Write(values[:])
	if len(batch) > 0 {
		_, _ = hash.Write([]byte(batch[0].ClientID))
	}
	return int(hash.Sum64() % modulus)
}
