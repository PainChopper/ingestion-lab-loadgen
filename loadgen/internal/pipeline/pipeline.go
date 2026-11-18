package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Options configures a pipeline before any worker starts.
type Options struct {
	Source             Source
	BatchSize          int
	ReaderCapacity     int
	SenderCapacity     int
	Readers            int
	Senders            int
	Rate               Rate
	SenderURL          string
	SenderTimeout      time.Duration
	SenderMaxConns     int
	RetryDelays        []time.Duration
	RetryJitterPercent int
	Logger             *zap.Logger
	OpenFile           func(string) (ReaderFile, error)
	Deliver            func(context.Context, []Transaction, int) error
	RandomIntN         func(int) int
}

type Snapshot struct {
	Reader      ReaderSnapshot
	Sender      SenderSnapshot
	ReaderQueue QueueSnapshot
	SenderQueue QueueSnapshot
}

type Pipeline struct {
	reader          *readerPool
	sender          *senderPool
	throttler       *throttler
	readerQueue     *batchQueue
	senderQueue     *batchQueue
	readerTelemetry readerTelemetry
	completed       atomic.Int64
	done            chan struct{}
	sampleMu        sync.Mutex
	sampledAt       time.Time
}

func Start(ctx context.Context, options Options) *Pipeline {
	now := time.Now()
	p := &Pipeline{
		done:        make(chan struct{}),
		readerQueue: newBatchQueue(options.ReaderCapacity, options.BatchSize),
		senderQueue: newBatchQueue(options.SenderCapacity, options.BatchSize),
		sampledAt:   now,
	}
	p.readerTelemetry.startInterval(now)
	p.reader = startReaderPool(ctx, options.Source, options.BatchSize, options.Readers,
		p.readerQueue, &p.readerTelemetry, options.OpenFile, options.Logger)
	p.throttler = startThrottler(ctx, p.readerQueue, p.senderQueue, options.Rate)
	p.sender = startSenderPool(ctx, p.senderQueue, &p.completed, options.Senders, options, options.Logger)
	go func() {
		<-p.reader.done
		close(p.readerQueue.ch)
		<-p.throttler.done
		close(p.senderQueue.ch)
		<-p.sender.done
		close(p.done)
	}()
	return p
}

func (p *Pipeline) SetReaders(readers int)     { p.reader.reconcile(readers) }
func (p *Pipeline) SetSenders(senders int)     { p.sender.reconcile(senders) }
func (p *Pipeline) SetRate(rate Rate)          { p.throttler.update(rate) }
func (p *Pipeline) StopReading()               { p.reader.stopReading() }
func (p *Pipeline) Done() <-chan struct{}      { return p.done }
func (p *Pipeline) SourceErrors() <-chan error { return p.reader.sourceErrors }
func (p *Pipeline) TakeCompleted() int64       { return p.completed.Swap(0) }

// Sample uses the same elapsed interval for every stage and returns it to the caller.
func (p *Pipeline) Sample(now time.Time) time.Duration {
	p.sampleMu.Lock()
	defer p.sampleMu.Unlock()
	elapsed := now.Sub(p.sampledAt)
	if elapsed <= 0 {
		return 0
	}
	p.readerTelemetry.sample(now)
	p.readerQueue.sample(elapsed)
	p.senderQueue.sample(elapsed)
	p.sampledAt = now
	return elapsed
}

func (p *Pipeline) Snapshot(now time.Time) Snapshot {
	reader := p.reader.snapshot()
	measurements := p.readerTelemetry.snapshot()
	reader.RowsRead = measurements.RowsRead
	reader.ReadTPS = measurements.ReadTPS
	return Snapshot{
		Reader:      reader,
		Sender:      p.sender.snapshot(),
		ReaderQueue: p.readerQueue.snapshot(now),
		SenderQueue: p.senderQueue.snapshot(now),
	}
}
