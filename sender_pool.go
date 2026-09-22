package main

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

type senderAttempt func(batch []Transaction, ordinal, attempt, delayMS, errorRate int) bool

type senderWorker struct {
	ordinal  int
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
	desired                                  int
	stopping                                 bool
	delayMS                                  int
	errorRate                                int
	retry                                    senderRetryPolicy
	batches                                  <-chan []Transaction
	channelTelemetry                         *channelTelemetry
	telemetry                                *senderTelemetry
	terminallyCompletedTransactionsSinceTick *atomic.Int64
	attempt                                  senderAttempt
	wait                                     func(time.Duration)
}

func startSenderPool(
	batches <-chan []Transaction,
	channelTelemetry *channelTelemetry,
	telemetry *senderTelemetry,
	terminallyCompletedTransactionsSinceTick *atomic.Int64,
	workers, delayMS, errorRate int,
	retry senderRetryPolicy,
) *senderPool {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &senderPool{
		ctx:                                      ctx,
		cancel:                                   cancel,
		intakeDone:                               make(chan struct{}),
		workers:                                  make(map[int]*senderWorker),
		delayMS:                                  delayMS,
		errorRate:                                errorRate,
		retry:                                    retry,
		batches:                                  batches,
		channelTelemetry:                         channelTelemetry,
		telemetry:                                telemetry,
		terminallyCompletedTransactionsSinceTick: terminallyCompletedTransactionsSinceTick,
		attempt:                                  simulatedSenderAttempt,
		wait:                                     time.Sleep,
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
	for ordinal, worker := range p.workers {
		draining := ordinal >= desired
		if worker.draining == draining {
			continue
		}
		worker.draining = draining
		if draining && !worker.busy {
			idleDraining = append(idleDraining, worker.done)
		}
		lifecycle := "active"
		if draining {
			lifecycle = "draining"
		}
		p.telemetry.setLifecycle(ordinal, lifecycle)
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
	for ordinal := range desired {
		if _, ok := p.workers[ordinal]; !ok {
			p.startWorkerLocked(ordinal)
		}
	}
	p.available.Broadcast()
	p.mu.Unlock()
	for _, done := range idleDraining {
		<-done
	}
}

func (p *senderPool) updateSimulation(delayMS, errorRate int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delayMS = delayMS
	p.errorRate = errorRate
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

func (p *senderPool) startWorkerLocked(ordinal int) {
	worker := &senderWorker{
		ordinal: ordinal,
		wake:    make(chan struct{}, 1),
		work:    make(chan []Transaction, 1),
		done:    make(chan struct{}),
	}
	p.workers[ordinal] = worker
	p.telemetry.startWorker(ordinal)
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
		if worker.draining || worker.busy || worker.ordinal >= p.desired {
			continue
		}
		if selected == nil || worker.ordinal < selected.ordinal {
			selected = worker
		}
	}
	return selected
}

func (p *senderPool) runWorker(worker *senderWorker) {
	defer p.wg.Done()
	defer close(worker.done)
	defer p.workerExited(worker)
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
	p.processBatch(worker.ordinal, batch)
	p.terminallyCompletedTransactionsSinceTick.Add(int64(len(batch)))
	p.mu.Lock()
	worker.busy = false
	p.available.Broadcast()
	p.mu.Unlock()
}

func (p *senderPool) workerExited(worker *senderWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.workers[worker.ordinal] != worker {
		return
	}
	delete(p.workers, worker.ordinal)
	p.telemetry.finishWorker(worker.ordinal)
	if !p.stopping && worker.ordinal < p.desired {
		p.startWorkerLocked(worker.ordinal)
	}
	p.available.Broadcast()
}

func (p *senderPool) processBatch(ordinal int, batch []Transaction) {
	for attempt := 1; attempt <= p.retry.MaxAttempts; attempt++ {
		p.mu.Lock()
		delayMS, errorRate := p.delayMS, p.errorRate
		p.mu.Unlock()
		p.telemetry.setActivity(ordinal, "in-flight")
		if p.attempt(batch, ordinal, attempt, delayMS, errorRate) {
			p.telemetry.finishBatch(ordinal, true)
			return
		}
		if attempt == p.retry.MaxAttempts {
			p.telemetry.finishBatch(ordinal, false)
			return
		}
		p.telemetry.setActivity(ordinal, "backoff")
		base := time.Duration(p.retry.BackoffBaseMS) * time.Millisecond
		if attempt > 1 {
			base *= time.Duration(p.retry.BackoffMultiplier)
		}
		jitter := deterministicSenderValue(batch, ordinal, attempt, 41) - 20
		p.wait(base + base*time.Duration(jitter)/100)
	}
}

func simulatedSenderAttempt(batch []Transaction, ordinal, attempt, delayMS, errorRate int) bool {
	time.Sleep(time.Duration(delayMS) * time.Millisecond)
	return deterministicSenderValue(batch, ordinal, attempt, 100) >= errorRate
}

func deterministicSenderValue(batch []Transaction, ordinal, attempt int, modulus uint64) int {
	hash := fnv.New64a()
	var values [24]byte
	binary.LittleEndian.PutUint64(values[:8], uint64(len(batch)))
	binary.LittleEndian.PutUint64(values[8:16], uint64(ordinal))
	binary.LittleEndian.PutUint64(values[16:], uint64(attempt))
	_, _ = hash.Write(values[:])
	if len(batch) > 0 {
		_, _ = hash.Write([]byte(batch[0].ClientID))
	}
	return int(hash.Sum64() % modulus)
}
