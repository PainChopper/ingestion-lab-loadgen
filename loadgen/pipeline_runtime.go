package main

import (
	"context"
	"sync/atomic"
	"time"
)

// pipelineRuntime owns the pipeline resources for the process's single run.
type pipelineRuntime struct {
	terminallyCompletedTransactionsSinceTick atomic.Int64
	readerPool                               *readerPool
	throttler                                *throttler
	senderPool                               *senderPool
	done                                     chan struct{}
}

func startPipeline(ctx context.Context, state *controlState, source readerSource) *pipelineRuntime {
	p := &pipelineRuntime{done: make(chan struct{})}
	cfg := state.config
	batchSize := cfg.Reader.ReadBatchSize.Initial
	readerBatches := make(chan []Transaction, cfg.ReaderChannel.Capacity.Initial)
	senderBatches := make(chan []Transaction, cfg.SenderChannel.Capacity.Initial)
	state.telemetry.reader.startInterval(time.Now())
	state.telemetry.readerChannel.start(readerBatches, batchSize)
	state.telemetry.senderChannel.start(senderBatches, batchSize)

	reader := startReaderPool(
		ctx,
		source,
		batchSize,
		state.controls.readerWorkers,
		readerBatches,
		&state.telemetry.reader,
		&state.telemetry.readerChannel,
		state.logger,
	)
	p.readerPool = reader
	p.throttler = &throttler{
		readerBatches: readerBatches,
		senderBatches: senderBatches,
		readerChannel: &state.telemetry.readerChannel,
		senderChannel: &state.telemetry.senderChannel,
	}
	p.throttler.start(ctx, state.throttlerSettings())
	p.senderPool = startSenderPool(
		ctx,
		senderBatches,
		&state.telemetry.senderChannel,
		&p.terminallyCompletedTransactionsSinceTick,
		state.controls.senderWorkers,
		cfg.Sender,
		state.logger,
	)
	go func() {
		// Only the pipeline closes stage outputs, after their last producer exits.
		<-reader.done
		close(readerBatches)
		<-p.throttler.done
		close(senderBatches)
		<-p.senderPool.done
		close(p.done)
	}()
	return p
}

func (p *pipelineRuntime) snapshots() (readerPoolSnapshot, senderPoolSnapshot) {
	if p == nil {
		return readerPoolSnapshot{}, senderPoolSnapshot{}
	}
	return p.readerPool.aggregateSnapshot(), p.senderPool.aggregateSnapshot()
}
