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

type SenderActivity int

const (
	SenderIdle SenderActivity = iota
	SenderInFlight
	SenderBackoff
)

type SenderWorkerState struct {
	Activity SenderActivity
	Draining bool
}

type senderWorker struct {
	id   int
	wake chan struct{}
	// Prevents the worker from accepting new work.
	draining bool
	// In-flight and backoff both retain ownership of the accepted batch.
	activity SenderActivity
}

type SenderSnapshot struct {
	WorkerStates []SenderWorkerState
}

type senderPool struct {
	// mu guards worker state.
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	wg            sync.WaitGroup
	workers       []*senderWorker
	stopping      bool
	stopped       chan struct{}
	nextWorkerID  int
	beforeReceive func(*senderWorker)
	retry         *retryPolicy
	queue         *batchQueue
	completed     atomic.Int64
	deliver       func(context.Context, []Transaction, int) error
	client        *http.Client
	logger        *zap.Logger
}

func startSenderPool(
	parent context.Context,
	queue *batchQueue,
	cfg Options,
	beforeReceive func(*senderWorker),
) *senderPool {
	ctx, cancel := context.WithCancel(parent)
	pool := &senderPool{
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan struct{}),
		stopped:       make(chan struct{}),
		beforeReceive: beforeReceive,
		workers:       make([]*senderWorker, 0, cfg.Senders),
		retry:         newRetryPolicy(cfg),
		queue:         queue,
		deliver:       cfg.Deliver,
		logger:        cfg.Logger,
	}
	if pool.deliver == nil {
		pool.client = newSenderHTTPClient(cfg.SenderMaxConns, cfg.SenderTimeout)
		pool.deliver = newSenderClient(cfg.SenderURL, pool.client).deliver
	}
	pool.reconcile(cfg.Senders)
	go pool.supervise()
	return pool
}

func (p *senderPool) reconcile(desired int) {
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
		// Retire idle workers before disturbing work already in progress.
		for _, busy := range []bool{false, true} {
			for _, worker := range p.workers {
				if active == desired {
					break
				}
				if !worker.draining && (worker.activity != SenderIdle) == busy {
					worker.draining = true
					active--
				}
			}
		}
	} else if active < desired {
		for _, backoff := range []bool{false, true} {
			for _, worker := range p.workers {
				if active == desired {
					break
				}
				if worker.draining && (worker.activity == SenderBackoff) == backoff {
					worker.draining = false
					active++
				}
			}
		}
	}
	for active < desired {
		p.startWorkerLocked()
		active++
	}
	for _, worker := range p.workers {
		wakeSender(worker)
	}
}

func wakeSender(worker *senderWorker) {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (p *senderPool) beginStopLocked() {
	if p.stopping {
		return
	}
	p.stopping = true
	close(p.stopped)
	for _, worker := range p.workers {
		wakeSender(worker)
	}
}

func (p *senderPool) supervise() {
	select {
	case <-p.ctx.Done():
	case <-p.stopped:
	}
	p.mu.Lock()
	p.beginStopLocked()
	p.mu.Unlock()
	p.wg.Wait()
	p.cancel()
	if p.client != nil {
		p.client.CloseIdleConnections()
	}
	close(p.done)
}

func (p *senderPool) startWorkerLocked() {
	worker := &senderWorker{
		id:   p.nextWorkerID,
		wake: make(chan struct{}, 1),
	}
	p.nextWorkerID++
	p.workers = append(p.workers, worker)
	p.wg.Add(1)
	go p.runWorker(worker)
}

func (p *senderPool) runWorker(worker *senderWorker) {
	defer p.wg.Done()
	p.logger.Debug("sender worker started", zap.String("event", "sender_worker_started"))
	defer p.logger.Debug("sender worker stopped", zap.String("event", "sender_worker_stopped"))
	for {
		p.mu.Lock()
		if p.stopping || worker.draining || p.ctx.Err() != nil {
			p.removeWorkerLocked(worker)
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		if p.beforeReceive != nil {
			p.beforeReceive(worker)
		}

		select {
		case <-p.ctx.Done():
			continue
		case <-worker.wake:
			continue
		case batch, ok := <-p.queue.ch:
			p.mu.Lock()
			if !ok {
				p.beginStopLocked()
				p.removeWorkerLocked(worker)
				p.mu.Unlock()
				return
			}
			// A batch received during downscale belongs to this worker until completion.
			worker.activity = SenderInFlight
			p.queue.recordReceive(len(batch))
			p.mu.Unlock()
			p.finishAcceptedBatch(worker, batch)
		}
	}
}

func (p *senderPool) finishAcceptedBatch(worker *senderWorker, batch []Transaction) {
	if p.processBatch(worker, batch) {
		p.completed.Add(int64(len(batch)))
	}
	p.mu.Lock()
	worker.activity = SenderIdle
	p.mu.Unlock()
}

func (p *senderPool) completedTotal() int64 { return p.completed.Load() }

func (p *senderPool) removeWorkerLocked(worker *senderWorker) {
	for index, candidate := range p.workers {
		if candidate == worker {
			p.workers = slices.Delete(p.workers, index, index+1)
			return
		}
	}
}

func (p *senderPool) processBatch(worker *senderWorker, batch []Transaction) bool {
	for attempt := 1; ; attempt++ {
		p.mu.Lock()
		worker.activity = SenderInFlight
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return false
		}
		if err := p.deliver(p.ctx, batch, attempt); err == nil {
			return true
		}
		if p.ctx.Err() != nil {
			return false
		}
		p.logger.Warn("batch delivery will retry", zap.String("event", "batch_delivery_retry"), zap.Int("batch_size", len(batch)), zap.Int("attempt", attempt))

		p.mu.Lock()
		worker.activity = SenderBackoff
		p.mu.Unlock()
		delay := p.retry.delay(attempt)
		if !p.waitBackoff(delay) {
			return false
		}
	}
}

func (p *senderPool) snapshot() SenderSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := SenderSnapshot{WorkerStates: make([]SenderWorkerState, 0, len(p.workers))}
	for _, worker := range p.workers {
		snapshot.WorkerStates = append(snapshot.WorkerStates, SenderWorkerState{
			Activity: worker.activity, Draining: worker.draining,
		})
	}
	return snapshot
}

func (p *senderPool) waitBackoff(duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-p.ctx.Done():
		return false
	case <-timer.C:
		return true
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
