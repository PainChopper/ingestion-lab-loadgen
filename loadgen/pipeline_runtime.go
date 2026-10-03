package main

import (
	"context"
	"sync/atomic"
	"time"
)

// pipelineRuntime owns resources created for one pipeline run. controlState
// retains lifecycle, controls, telemetry sampling, and public snapshots.
type pipelineRuntime struct {
	state *controlState

	terminallyCompletedTransactionsSinceTick atomic.Int64
	readerBatches                            chan []Transaction
	senderBatches                            chan []Transaction
	senderPool                               *senderPool
	runContext                               context.Context
	cancelRun                                context.CancelFunc
	throttler                                *throttler
	readerPool                               *readerPool
}

func newPipelineRuntime(state *controlState) *pipelineRuntime {
	return &pipelineRuntime{state: state}
}

func (runtime *pipelineRuntime) start(ctx context.Context) error {
	runtime.state.telemetry.reader.startInterval(time.Now())
	runtime.runContext, runtime.cancelRun = context.WithCancel(ctx)
	runtime.readerBatches = make(chan []Transaction, runtime.state.controls.readerChannelCapacity)
	runtime.state.telemetry.readerChannel.start(runtime.readerBatches, runtime.state.controls.readBatchSize)
	runtime.state.telemetry.readerChannel.clearMeasurements()
	runtime.senderBatches = make(chan []Transaction, runtime.state.controls.senderChannelCapacity)
	runtime.state.telemetry.senderChannel.start(runtime.senderBatches, runtime.state.controls.readBatchSize)
	runtime.state.telemetry.senderChannel.clearMeasurements()

	started, err := runtime.state.startReaderPool(
		runtime.runContext,
		runtime.readerBatches,
		runtime.state.controls.readBatchSize,
		runtime.state.controls.readerWorkers,
	)
	if err != nil {
		runtime.cancelRun()
		runtime.runContext = nil
		runtime.cancelRun = nil
		closeAndDrain(runtime.readerBatches)
		runtime.readerBatches = nil
		runtime.state.telemetry.readerChannel.detach()
		closeAndDrain(runtime.senderBatches)
		runtime.senderBatches = nil
		runtime.state.telemetry.senderChannel.detach()
		runtime.state.telemetry.reader.reset()
		return err
	}

	runtime.readerPool = started
	runtime.throttler = &throttler{
		readerBatches: runtime.readerBatches,
		senderBatches: runtime.senderBatches,
		readerChannel: &runtime.state.telemetry.readerChannel,
		senderChannel: &runtime.state.telemetry.senderChannel,
	}
	runtime.throttler.start(
		runtime.runContext,
		runtime.state.throttlerSettings(false),
	)
	return nil
}

func (runtime *pipelineRuntime) startSender() {
	runtime.senderPool = startSenderPool(
		runtime.runContext,
		runtime.senderBatches,
		&runtime.state.telemetry.senderChannel,
		&runtime.terminallyCompletedTransactionsSinceTick,
		runtime.state.controls.senderWorkers,
		runtime.state.config.Sender.API,
		runtime.state.config.Sender.Retry,
		runtime.state.logger,
	)
}

func (runtime *pipelineRuntime) stopSender() {
	if runtime.senderPool == nil {
		return
	}
	<-runtime.senderPool.stop()
	runtime.senderPool = nil
}

func (runtime *pipelineRuntime) stop() {
	runtime.stopSender()
	if runtime.cancelRun != nil {
		runtime.cancelRun()
	}
	if runtime.readerPool != nil {
		<-runtime.readerPool.done
	}
	if runtime.throttler != nil {
		<-runtime.throttler.done
	}
	closeAndDrain(runtime.readerBatches)
	closeAndDrain(runtime.senderBatches)
	runtime.readerBatches = nil
	runtime.senderBatches = nil
	runtime.state.telemetry.readerChannel.detach()
	runtime.state.telemetry.senderChannel.detach()
	runtime.clearActive()
}

func (runtime *pipelineRuntime) clearActive() {
	runtime.runContext = nil
	runtime.cancelRun = nil
	runtime.readerPool = nil
	runtime.throttler = nil
}

func (runtime *pipelineRuntime) reconcileReader(workers int) {
	if runtime.readerPool != nil {
		runtime.readerPool.reconcile(workers)
	}
}

func (runtime *pipelineRuntime) reconcileSender(workers int) {
	if runtime.senderPool != nil {
		runtime.senderPool.reconcile(workers)
	}
}

func (runtime *pipelineRuntime) snapshots() (readerPoolSnapshot, senderPoolSnapshot) {
	reader := readerPoolSnapshot{}
	if runtime.readerPool != nil {
		reader = runtime.readerPool.aggregateSnapshot()
	}
	sender := senderPoolSnapshot{}
	if runtime.senderPool != nil {
		sender = runtime.senderPool.aggregateSnapshot()
	}
	return reader, sender
}

func (runtime *pipelineRuntime) sourceErrors() <-chan readerSourceError {
	if runtime.readerPool == nil {
		return nil
	}
	return runtime.readerPool.sourceErrors
}

func closeAndDrain(batches chan []Transaction) {
	if batches == nil {
		return
	}
	close(batches)
	drain(batches)
}

func drain(batches chan []Transaction) {
	if batches == nil {
		return
	}
	for {
		select {
		case _, ok := <-batches:
			if !ok {
				return
			}
		default:
			return
		}
	}
}
