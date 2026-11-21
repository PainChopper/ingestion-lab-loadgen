package pipeline

import (
	"context"
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
	// Logger defaults to a no-op logger when nil.
	Logger     *zap.Logger
	OpenFile   func(string) (ReaderFile, error)
	Deliver    func(context.Context, []Transaction, int) error
	RandomIntN func(int) int
}

type Snapshot struct {
	CompletedTransactions int64
	Reader                ReaderSnapshot
	Sender                SenderSnapshot
	ReaderQueue           QueueSnapshot
	SenderQueue           QueueSnapshot
}

type Pipeline struct {
	reader          *readerPool
	sender          *senderPool
	throttler       *throttler
	readerQueue     *batchQueue
	senderQueue     *batchQueue
	readerTelemetry readerTelemetry
	done            chan struct{}
}

func Start(ctx context.Context, options Options) *Pipeline {
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	p := &Pipeline{
		done:        make(chan struct{}),
		readerQueue: newBatchQueue(options.ReaderCapacity, options.BatchSize),
		senderQueue: newBatchQueue(options.SenderCapacity, options.BatchSize),
	}
	p.reader = startReaderPool(ctx, options.Source, options.BatchSize, options.Readers,
		p.readerQueue, &p.readerTelemetry, options.OpenFile, nil, options.Logger)
	p.throttler = startThrottler(ctx, p.readerQueue, p.senderQueue, options.Rate)
	p.sender = startSenderPool(ctx, p.senderQueue, options, nil)
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
func (p *Pipeline) CompletedTotal() int64      { return p.sender.completedTotal() }

func (p *Pipeline) Snapshot(now time.Time) Snapshot {
	completed := p.CompletedTotal()
	reader := p.reader.snapshot()
	reader.RowsRead = p.readerTelemetry.snapshot()
	return Snapshot{
		CompletedTransactions: completed,
		Reader:                reader,
		Sender:                p.sender.snapshot(),
		ReaderQueue:           p.readerQueue.snapshot(now),
		SenderQueue:           p.senderQueue.snapshot(now),
	}
}
