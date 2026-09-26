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
) (<-chan struct{}, chan<- throttlerUpdate)

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
	batches                                  chan []Transaction
	senderBatches                            chan []Transaction
	pool                                     *senderPool
	cancelThrottler                          context.CancelFunc
	throttlerDone                            <-chan struct{}
	throttlerUpdates                         chan<- throttlerUpdate
	cancelReader                             context.CancelFunc
	readerDone                               <-chan struct{}
	readerReconcile                          func(int)
	readerAggregateSnapshot                  func() readerPoolSnapshot
	readerSourceErrors                       <-chan readerSourceError
}

func newPipelineRuntime(state *controlState, read readerStarter, throttle throttlerStarter) *pipelineRuntime {
	return &pipelineRuntime{state: state, read: read, throttle: throttle}
}

func (runtime *pipelineRuntime) start(ctx context.Context) error {
	runtime.state.telemetry.reader.startInterval(time.Now())
	var readerCreated bool
	runtime.batches, readerCreated = runtime.prepareReaderChannel(runtime.batches)
	var senderCreated bool
	runtime.senderBatches, senderCreated = runtime.prepareSenderChannel(runtime.senderBatches)

	readerContext, cancelReader := context.WithCancel(ctx)
	started, err := runtime.read(
		readerContext,
		runtime.batches,
		runtime.state.readBatchSize(),
		runtime.state.readerWorkers(),
	)
	if err != nil {
		cancelReader()
		if readerCreated {
			closeAndDrain(runtime.batches)
			runtime.batches = nil
			runtime.state.telemetry.readerChannel.detach()
		}
		if senderCreated {
			closeAndDrain(runtime.senderBatches)
			runtime.senderBatches = nil
			runtime.state.telemetry.senderChannel.detach()
		}
		runtime.state.telemetry.reader.reset()
		return err
	}

	runtime.readerDone = started.done
	runtime.readerReconcile = started.reconcile
	runtime.readerAggregateSnapshot = started.aggregateSnapshot
	runtime.readerSourceErrors = started.sourceErrors
	runtime.cancelReader = cancelReader
	throttlerContext, cancelThrottler := context.WithCancel(ctx)
	runtime.cancelThrottler = cancelThrottler
	runtime.throttlerDone, runtime.throttlerUpdates = runtime.throttle(
		throttlerContext,
		runtime.batches,
		runtime.senderBatches,
		&runtime.state.telemetry.readerChannel,
		&runtime.state.telemetry.senderChannel,
		runtime.state.throttlerSettings(false),
	)
	return nil
}

func (runtime *pipelineRuntime) startSender() {
	runtime.pool = startSenderPool(
		runtime.senderBatches, &runtime.state.telemetry.senderChannel, &runtime.state.telemetry.sender,
		&runtime.terminallyCompletedTransactionsSinceTick, runtime.state.senderWorkers(), runtime.state.controls.policy.Sender.API,
		runtime.state.controls.policy.Sender.Retry,
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
	if runtime.cancelReader != nil {
		runtime.cancelReader()
	}
	if runtime.cancelThrottler != nil {
		runtime.cancelThrottler()
	}
	if runtime.readerDone != nil {
		<-runtime.readerDone
	}
	if runtime.throttlerDone != nil {
		<-runtime.throttlerDone
	}
	closeAndDrain(runtime.batches)
	closeAndDrain(runtime.senderBatches)
	runtime.batches = nil
	runtime.senderBatches = nil
	runtime.state.telemetry.readerChannel.detach()
	runtime.state.telemetry.senderChannel.detach()
	runtime.clearActive()
}

func (runtime *pipelineRuntime) resetPaused() {
	if runtime.cancelReader != nil {
		runtime.cancelReader()
	}
	if runtime.cancelThrottler != nil {
		runtime.cancelThrottler()
	}
	if runtime.readerDone != nil {
		<-runtime.readerDone
	}
	if runtime.throttlerDone != nil {
		<-runtime.throttlerDone
	}
	drain(runtime.batches)
	drain(runtime.senderBatches)
	runtime.clearActive()
}

func (runtime *pipelineRuntime) clearActive() {
	runtime.cancelReader = nil
	runtime.readerDone = nil
	runtime.readerReconcile = nil
	runtime.readerAggregateSnapshot = nil
	runtime.readerSourceErrors = nil
	runtime.cancelThrottler = nil
	runtime.throttlerDone = nil
	runtime.throttlerUpdates = nil
}

func (runtime *pipelineRuntime) reconcileReader(workers int) {
	if runtime.readerReconcile != nil {
		runtime.readerReconcile(workers)
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
	update := throttlerUpdate{settings: settings, acknowledged: make(chan struct{})}
	select {
	case runtime.throttlerUpdates <- update:
		<-update.acknowledged
	case <-runtime.throttlerDone:
	}
}

func (runtime *pipelineRuntime) snapshots() (readerPoolSnapshot, senderPoolSnapshot) {
	reader := readerPoolSnapshot{}
	if runtime.readerAggregateSnapshot != nil {
		reader = runtime.readerAggregateSnapshot()
	}
	sender := senderPoolSnapshot{}
	if runtime.pool != nil {
		sender = runtime.pool.aggregateSnapshot()
	}
	return reader, sender
}

func (runtime *pipelineRuntime) sourceErrors() <-chan readerSourceError {
	return runtime.readerSourceErrors
}

func (runtime *pipelineRuntime) prepareReaderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := runtime.state.readerChannelCapacity()
	if batches != nil && cap(batches) == capacity {
		return batches, false
	}
	closeAndDrain(batches)
	runtime.state.telemetry.readerChannel.detach()
	batches = make(chan []Transaction, capacity)
	runtime.state.telemetry.readerChannel.start(batches, runtime.state.readBatchSize())
	runtime.state.telemetry.readerChannel.clearMeasurements()
	return batches, true
}

func (runtime *pipelineRuntime) prepareSenderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := runtime.state.senderChannelCapacity()
	batchSize := runtime.state.readBatchSize()
	if batches != nil && cap(batches) == capacity {
		runtime.state.telemetry.senderChannel.start(batches, batchSize)
		return batches, false
	}
	closeAndDrain(batches)
	runtime.state.telemetry.senderChannel.detach()
	batches = make(chan []Transaction, capacity)
	runtime.state.telemetry.senderChannel.start(batches, batchSize)
	runtime.state.telemetry.senderChannel.clearMeasurements()
	return batches, true
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
