package pipeline

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

type senderAttemptOutcome uint8

const (
	senderAttemptSuccess senderAttemptOutcome = iota
	senderAttemptFailure
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

type SenderSnapshot struct {
	LiveWorkers             int
	IdleWorkers             int
	InFlightWorkers         int
	BackoffWorkers          int
	DrainingWorkers         int
	DrainingIdleWorkers     int
	DrainingInFlightWorkers int
	DrainingBackoffWorkers  int
}

type senderPool struct {
	// mu guards worker state.
	mu        sync.Mutex
	available *sync.Cond
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
	workers   []*senderWorker
	stopping  bool
	retry     *retryPolicy
	queue     *batchQueue
	completed *atomic.Int64
	attempt   senderAttempt
	client    *http.Client
	logger    *zap.Logger
}

func startSenderPool(
	parent context.Context,
	queue *batchQueue,
	completed *atomic.Int64,
	workers int,
	cfg Options,
	logger *zap.Logger,
) *senderPool {
	ctx, cancel := context.WithCancel(parent)
	client := newSenderHTTPClient(cfg.SenderMaxConns, cfg.SenderTimeout)
	pool := &senderPool{
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		workers:   make([]*senderWorker, 0, workers),
		retry:     newRetryPolicy(cfg),
		queue:     queue,
		completed: completed,
		attempt:   newSenderClient(cfg.SenderURL, client).attempt,
		client:    client,
		logger:    logger,
	}
	if cfg.Deliver != nil {
		pool.attempt = func(ctx context.Context, batch []Transaction, number int) senderAttemptOutcome {
			if err := cfg.Deliver(ctx, batch, number); err == nil {
				return senderAttemptSuccess
			}
			if ctx.Err() != nil {
				return senderAttemptCanceled
			}
			return senderAttemptFailure
		}
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
		for _, backoff := range []bool{false, true} {
			for _, worker := range p.workers {
				if activeWorkers == desired {
					break
				}
				if worker.draining && worker.backoff == backoff {
					worker.draining = false
					activeWorkers++
				}
			}
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

func (p *senderPool) finishIntake() {
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
	p.wg.Wait()
	p.cancel()
	p.client.CloseIdleConnections()
	close(p.done)
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
	defer p.finishIntake()
	stopWakeup := context.AfterFunc(p.ctx, func() {
		p.mu.Lock()
		p.available.Broadcast()
		p.mu.Unlock()
	})
	defer stopWakeup()
	for {
		p.mu.Lock()
		for p.availableWorkerLocked() == nil && p.ctx.Err() == nil {
			p.available.Wait()
		}
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return
		}

		select {
		case <-p.ctx.Done():
			return
		case batch, ok := <-p.queue.ch:
			if !ok {
				return
			}
			p.queue.recordReceive(len(batch))
			p.mu.Lock()
			worker := p.availableWorkerLocked()
			for worker == nil && p.ctx.Err() == nil {
				p.available.Wait()
				worker = p.availableWorkerLocked()
			}
			if p.ctx.Err() != nil {
				p.mu.Unlock()
				return
			}
			worker.busy = true
			worker.work <- batch
			p.mu.Unlock()
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
	p.logger.Debug("sender worker started", zap.String("event", "sender_worker_started"))
	defer p.logger.Debug("sender worker stopped", zap.String("event", "sender_worker_stopped"))
	for {
		p.mu.Lock()
		select {
		case batch := <-worker.work:
			p.mu.Unlock()
			p.finishAcceptedBatch(worker, batch)
			continue
		default:
		}
		if p.stopping || worker.draining {
			// Remove the worker before reconcile or intake can offer it more work.
			p.removeWorkerLocked(worker)
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

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
		p.completed.Add(int64(len(batch)))
	}
	p.mu.Lock()
	worker.busy = false
	worker.backoff = false
	p.available.Broadcast()
	p.mu.Unlock()
}

func (p *senderPool) removeWorkerLocked(worker *senderWorker) {
	for index, candidate := range p.workers {
		if candidate == worker {
			p.workers = slices.Delete(p.workers, index, index+1)
			p.available.Broadcast()
			return
		}
	}
}

func (p *senderPool) processBatch(worker *senderWorker, batch []Transaction) bool {
	for attempt := 1; ; attempt++ {
		p.mu.Lock()
		worker.backoff = false
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return false
		}
		switch p.attempt(p.ctx, batch, attempt) {
		case senderAttemptSuccess:
			return true
		case senderAttemptCanceled:
			return false
		case senderAttemptFailure:
			p.logger.Warn("batch delivery will retry", zap.String("event", "batch_delivery_retry"), zap.Int("batch_size", len(batch)), zap.Int("attempt", attempt))
		}

		p.mu.Lock()
		worker.backoff = true
		p.mu.Unlock()
		delay := p.retry.delay(attempt)
		if !p.waitBackoff(worker, delay) {
			return false
		}
	}
}

func (p *senderPool) snapshot() SenderSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := SenderSnapshot{LiveWorkers: len(p.workers)}
	for _, worker := range p.workers {
		var draining *int
		switch {
		case worker.backoff:
			snapshot.BackoffWorkers++
			draining = &snapshot.DrainingBackoffWorkers
		case worker.busy:
			snapshot.InFlightWorkers++
			draining = &snapshot.DrainingInFlightWorkers
		default:
			snapshot.IdleWorkers++
			draining = &snapshot.DrainingIdleWorkers
		}
		if worker.draining {
			snapshot.DrainingWorkers++
			(*draining)++
		}
	}
	return snapshot
}

func (p *senderPool) waitBackoff(worker *senderWorker, duration time.Duration) bool {
	deadline := time.Now().Add(duration)
	for {
		if p.ctx.Err() != nil {
			return false
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true
		}
		timer := time.NewTimer(remaining)
		select {
		case <-p.ctx.Done():
			timer.Stop()
			return false
		case <-worker.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type retryPolicy struct {
	delays        []time.Duration
	jitterPercent int
	mu            sync.Mutex
	randomIntN    func(int) int
}

func newRetryPolicy(options Options) *retryPolicy {
	randomIntN := options.RandomIntN
	if randomIntN == nil {
		randomIntN = rand.IntN
	}
	return &retryPolicy{
		delays:        slices.Clone(options.RetryDelays),
		jitterPercent: options.RetryJitterPercent,
		randomIntN:    randomIntN,
	}
}

func (r *retryPolicy) delay(attempt int) time.Duration {
	base := r.delays[min(attempt-1, len(r.delays)-1)]
	r.mu.Lock()
	jitter := r.randomIntN(2*r.jitterPercent+1) - r.jitterPercent
	r.mu.Unlock()
	return senderRetryDuration(base, jitter)
}

func senderRetryDuration(base time.Duration, jitterPercent int) time.Duration {
	adjustment := base/100*time.Duration(jitterPercent) + base%100*time.Duration(jitterPercent)/100
	if adjustment > 0 && adjustment > time.Duration(math.MaxInt64)-base {
		return time.Duration(math.MaxInt64)
	}
	delay := base + adjustment
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}
