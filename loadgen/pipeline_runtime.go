package main

import (
	"context"
	"sync/atomic"
	"time"
)

type throttlerStarter func(
	ctx context.Context,
	readerBatches <-chan []Transaction,
	senderBatches chan<- []Transaction,
	readerChannelTelemetry *channelTelemetry,
	senderChannelTelemetry *channelTelemetry,
	initial throttlerSettings,
) (<-chan struct{}, chan<- throttlerSettings)

type readerRun struct {
	done              <-chan struct{}
	reconcile         func(int)
	aggregateSnapshot func() readerPoolSnapshot
	sourceErrors      <-chan readerSourceError
}

type readerStarter func(context.Context, chan<- []Transaction, int, int) (readerRun, error)

// pipelineRuntime owns resources created for one pipeline run. controlState
// retains lifecycle, controls, telemetry sampling, and public snapshots.
type pipelineRuntime struct {
	state    *controlState
	read     readerStarter
	throttle throttlerStarter

	terminallyCompletedTransactionsSinceTick atomic.Int64
	readerBatches                            chan []Transaction
	senderBatches                            chan []Transaction
	pool                                     *senderPool
	runContext                               context.Context
	cancelRun                                context.CancelFunc
	cancelThrottler                          context.CancelFunc
	throttlerDone                            <-chan struct{}
	throttlerUpdates                         chan<- throttlerSettings
	cancelReader                             context.CancelFunc
	reader                                   readerRun
}

func newPipelineRuntime(state *controlState, read readerStarter, throttle throttlerStarter) *pipelineRuntime {
	return &pipelineRuntime{state: state, read: read, throttle: throttle}
}

func (runtime *pipelineRuntime) start(ctx context.Context) error {
	runtime.state.telemetry.reader.startInterval(time.Now())
	runtime.runContext, runtime.cancelRun = context.WithCancel(ctx)
	runtime.readerBatches = make(chan []Transaction, runtime.state.readerChannelCapacity())
	runtime.state.telemetry.readerChannel.start(runtime.readerBatches, runtime.state.readBatchSize())
	runtime.state.telemetry.readerChannel.clearMeasurements()
	runtime.senderBatches = make(chan []Transaction, runtime.state.senderChannelCapacity())
	runtime.state.telemetry.senderChannel.start(runtime.senderBatches, runtime.state.readBatchSize())
	runtime.state.telemetry.senderChannel.clearMeasurements()

	readerContext, cancelReader := context.WithCancel(runtime.runContext)
	started, err := runtime.read(
		readerContext,
		runtime.readerBatches,
		runtime.state.readBatchSize(),
		runtime.state.readerWorkers(),
	)
	if err != nil {
		cancelReader()
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

	runtime.reader = started
	runtime.cancelReader = cancelReader
	throttlerContext, cancelThrottler := context.WithCancel(runtime.runContext)
	runtime.cancelThrottler = cancelThrottler
	runtime.throttlerDone, runtime.throttlerUpdates = runtime.throttle(
		throttlerContext,
		runtime.readerBatches,
		runtime.senderBatches,
		&runtime.state.telemetry.readerChannel,
		&runtime.state.telemetry.senderChannel,
		runtime.state.throttlerSettings(false),
	)
	return nil
}

func (runtime *pipelineRuntime) startSender() {
	runtime.pool = startSenderPool(
		runtime.runContext,
		runtime.senderBatches, &runtime.state.telemetry.senderChannel, &runtime.state.telemetry.sender,
		&runtime.terminallyCompletedTransactionsSinceTick, runtime.state.senderWorkers(), runtime.state.controls.config.Sender.API,
		runtime.state.controls.config.Sender.Retry,
		runtime.state.logger,
	)
}

func (runtime *pipelineRuntime) stopSender() {
	if runtime.pool == nil {
		return
	}
	<-runtime.pool.stop()
	runtime.pool = nil
}

func (runtime *pipelineRuntime) stop() {
	runtime.stopSender()
	if runtime.cancelRun != nil {
		runtime.cancelRun()
	}
	if runtime.cancelReader != nil {
		runtime.cancelReader()
	}
	if runtime.cancelThrottler != nil {
		runtime.cancelThrottler()
	}
	if runtime.reader.done != nil {
		<-runtime.reader.done
	}
	if runtime.throttlerDone != nil {
		<-runtime.throttlerDone
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
	runtime.cancelReader = nil
	runtime.reader = readerRun{}
	runtime.cancelThrottler = nil
	runtime.throttlerDone = nil
	runtime.throttlerUpdates = nil
}

func (runtime *pipelineRuntime) reconcileReader(workers int) {
	if runtime.reader.reconcile != nil {
		runtime.reader.reconcile(workers)
	}
}

func (runtime *pipelineRuntime) reconcileSender(workers int) {
	if runtime.pool != nil {
		runtime.pool.reconcile(workers)
	}
}

func (runtime *pipelineRuntime) updateThrottler(settings throttlerSettings) {
	if runtime.throttlerUpdates == nil {
		return
	}
	select {
	case runtime.throttlerUpdates <- settings:
	case <-runtime.throttlerDone:
	}
}

func (runtime *pipelineRuntime) snapshots() (readerPoolSnapshot, senderPoolSnapshot) {
	reader := readerPoolSnapshot{}
	if runtime.reader.aggregateSnapshot != nil {
		reader = runtime.reader.aggregateSnapshot()
	}
	sender := senderPoolSnapshot{}
	if runtime.pool != nil {
		sender = runtime.pool.aggregateSnapshot()
	}
	return reader, sender
}

func (runtime *pipelineRuntime) sourceErrors() <-chan readerSourceError {
	return runtime.reader.sourceErrors
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
