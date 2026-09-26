package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
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

func (state *controlState) eventLoop(
	plane controlPlane,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	read readerStarter,
) {
	state.eventLoopWithThrottlerContext(context.Background(), plane, metrics, promMetrics, read, startThrottler)
}

func (state *controlState) runEventLoop(
	ctx context.Context,
	plane controlPlane,
	metrics <-chan time.Time,
	promMetrics *Metrics,
) {
	state.eventLoopWithThrottlerContext(ctx, plane, metrics, promMetrics, state.startReaderPool, startThrottler)
}

func (state *controlState) eventLoopWithThrottler(
	plane controlPlane,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	read readerStarter,
	start throttlerStarter,
) {
	state.eventLoopWithThrottlerContext(context.Background(), plane, metrics, promMetrics, read, start)
}

func (state *controlState) eventLoopWithThrottlerContext(
	ctx context.Context,
	plane controlPlane,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	read readerStarter,
	start throttlerStarter,
) {
	logger := state.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	var terminallyCompletedTransactionsSinceTick atomic.Int64
	var batches chan []Transaction
	var senderBatches chan []Transaction
	var pool *senderPool
	var cancelThrottler context.CancelFunc
	var throttlerDone <-chan struct{}
	var throttlerUpdates chan<- throttlerUpdate
	var cancelReader context.CancelFunc
	var readerDone <-chan struct{}
	var readerReconcile func(int)
	var readerAggregateSnapshot func() readerPoolSnapshot
	var readerSourceErrors <-chan readerSourceError
	var lastRuntimeSnapshot time.Time
	stopActiveRun := func() {
		if pool != nil {
			<-pool.stop()
			pool = nil
		}
		if cancelReader != nil {
			cancelReader()
		}
		if cancelThrottler != nil {
			cancelThrottler()
		}
		if readerDone != nil {
			<-readerDone
		}
		if throttlerDone != nil {
			<-throttlerDone
		}
		closeAndDrain(batches)
		closeAndDrain(senderBatches)
		batches = nil
		senderBatches = nil
		state.telemetry.readerChannel.detach()
		state.telemetry.senderChannel.detach()
		cancelReader = nil
		readerDone = nil
		readerReconcile = nil
		readerAggregateSnapshot = nil
		readerSourceErrors = nil
		cancelThrottler = nil
		throttlerDone = nil
		throttlerUpdates = nil
	}
	defer func() {
		stopActiveRun()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-plane.requests:
			if !ok {
				return
			}
			switch cmd.kind {
			case getSnapshot:
				runState := state.run.lifecycle.currentState()
				reader := state.telemetry.reader.snapshot()
				readerPool := readerPoolSnapshot{}
				if readerAggregateSnapshot != nil {
					readerPool = readerAggregateSnapshot()
				}
				readerChannel := state.telemetry.readerChannel.snapshot(time.Now())
				senderChannel := state.telemetry.senderChannel.snapshot(time.Now())
				senderPool := senderPoolSnapshot{}
				if pool != nil {
					senderPool = pool.aggregateSnapshot()
				}
				if runState == runStateIdle || runState == runStateFaulted {
					readerChannel.capacity = state.readerChannelCapacity()
					senderChannel.capacity = state.senderChannelCapacity()
				}
				snapshot := statusSnapshot{
					Run: runSnapshot{
						State:             runState,
						TotalTransactions: state.run.totalTransactions,
						ElapsedMs:         state.elapsedMs(time.Now()),
					},
					Reader: readerSnapshot{
						Workers: state.readerWorkers(), LiveWorkers: readerPool.liveWorkers,
						IdleWorkers: readerPool.idleWorkers, ReadingWorkers: readerPool.readingWorkers,
						BlockedWorkers: readerPool.blockedWorkers, DrainingWorkers: readerPool.drainingWorkers,
						DrainingIdleWorkers:    readerPool.drainingIdleWorkers,
						DrainingReadingWorkers: readerPool.drainingReadingWorkers,
						DrainingBlockedWorkers: readerPool.drainingBlockedWorkers,
						ReadBatchSize:          state.readBatchSize(), ReadTps: reader.readTPS,
						RowsRead:        reader.rowsRead,
						SourceDirectory: readerSourceDirectory(state.controls.policy.Source.Path),
						SourceError:     state.run.sourceError,
					},
					Throttler: throttlerSnapshot{
						RequestedTps:     state.requestedTPS(),
						AdmittedTps:      senderChannel.sentTransactionsPerSecond,
						InstallationMode: state.installationMode(),
					},
					Sender: senderSnapshot{
						Workers: state.senderWorkers(), LiveWorkers: senderPool.liveWorkers,
						IdleWorkers: senderPool.idleWorkers, InFlightWorkers: senderPool.inFlightWorkers,
						BackoffWorkers: senderPool.backoffWorkers, DrainingWorkers: senderPool.drainingWorkers,
						DrainingIdleWorkers:     senderPool.drainingIdleWorkers,
						DrainingInFlightWorkers: senderPool.drainingInFlightWorkers,
						DrainingBackoffWorkers:  senderPool.drainingBackoffWorkers,
					},
					ReaderChannel: channelSnapshot{
						Capacity:                      readerChannel.capacity,
						DepthBatches:                  readerChannel.depthBatches,
						BufferedTransactions:          readerChannel.bufferedTransactions,
						BlockedSenders:                readerChannel.blockedSenders,
						OldestBlockedSenderMs:         readerChannel.oldestBlockedSenderMs,
						BlockedMs:                     readerChannel.blockedMs,
						SentBatchesTotal:              readerChannel.sentBatchesTotal,
						SentTransactionsTotal:         readerChannel.sentTransactionsTotal,
						ReceivedBatchesTotal:          readerChannel.receivedBatchesTotal,
						ReceivedTransactionsTotal:     readerChannel.receivedTransactionsTotal,
						SentBatchesPerSecond:          readerChannel.sentBatchesPerSecond,
						SentTransactionsPerSecond:     readerChannel.sentTransactionsPerSecond,
						ReceivedBatchesPerSecond:      readerChannel.receivedBatchesPerSecond,
						ReceivedTransactionsPerSecond: readerChannel.receivedTransactionsPerSecond,
					},
					SenderChannel: channelSnapshot{
						Capacity:                      senderChannel.capacity,
						DepthBatches:                  senderChannel.depthBatches,
						BufferedTransactions:          senderChannel.bufferedTransactions,
						BlockedSenders:                senderChannel.blockedSenders,
						OldestBlockedSenderMs:         senderChannel.oldestBlockedSenderMs,
						BlockedMs:                     senderChannel.blockedMs,
						SentBatchesTotal:              senderChannel.sentBatchesTotal,
						SentTransactionsTotal:         senderChannel.sentTransactionsTotal,
						ReceivedBatchesTotal:          senderChannel.receivedBatchesTotal,
						ReceivedTransactionsTotal:     senderChannel.receivedTransactionsTotal,
						SentBatchesPerSecond:          senderChannel.sentBatchesPerSecond,
						SentTransactionsPerSecond:     senderChannel.sentTransactionsPerSecond,
						ReceivedBatchesPerSecond:      senderChannel.receivedBatchesPerSecond,
						ReceivedTransactionsPerSecond: senderChannel.receivedTransactionsPerSecond,
					},
					Policy: state.controls.policy.snapshot(),
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				resuming := state.run.lifecycle.currentState() == runStatePaused
				if state.run.lifecycle.currentState() == runStateIdle {
					state.telemetry.reader.startInterval(time.Now())
					var readerCreated bool
					batches, readerCreated = state.prepareReaderChannel(batches)
					var senderCreated bool
					senderBatches, senderCreated = state.prepareSenderChannel(senderBatches)
					readerContext, cancel := context.WithCancel(ctx)
					started, err := read(
						readerContext,
						batches,
						state.readBatchSize(),
						state.readerWorkers(),
					)
					if err != nil {
						sourceError := sourceErrorFromStartup(err, state.controls.policy.Source.Path)
						cancel()
						if readerCreated {
							closeAndDrain(batches)
							batches = nil
							state.telemetry.readerChannel.detach()
						}
						if senderCreated {
							closeAndDrain(senderBatches)
							senderBatches = nil
							state.telemetry.senderChannel.detach()
						}
						state.telemetry.reader.reset()
						logger.Error("run failed to start", zap.String("event", "run_failed"), zap.String("operation", sourceError.Operation))
						state.run.sourceError = &sourceError
						state.run.lifecycle.faultStart()
						if cmd.commandReply != nil {
							cmd.commandReply <- commandResult{err: err}
						}
						continue
					}
					readerDone = started.done
					readerReconcile = started.reconcile
					readerAggregateSnapshot = started.aggregateSnapshot
					readerSourceErrors = started.sourceErrors
					cancelReader = cancel
					throttlerContext, cancel := context.WithCancel(ctx)
					cancelThrottler = cancel
					throttlerDone, throttlerUpdates = start(
						throttlerContext,
						batches,
						senderBatches,
						&state.telemetry.readerChannel,
						&state.telemetry.senderChannel,
						state.throttlerSettings(false),
					)
				}
				if state.run.lifecycle.currentState() == runStateFaulted {
					if cmd.commandReply != nil {
						cmd.commandReply <- commandResult{status: commandConflict}
					}
					continue
				}
				if state.run.lifecycle.run() {
					state.run.runStartedAt = time.Now()
					if resuming {
						state.notifyThrottler(throttlerUpdates, throttlerDone, false)
					}
					pool = startSenderPool(
						senderBatches, &state.telemetry.senderChannel, &state.telemetry.sender,
						&terminallyCompletedTransactionsSinceTick, state.senderWorkers(), state.controls.policy.Sender.API,
						state.controls.policy.Sender.Retry,
						logger,
					)
					if resuming {
						logger.Info("run resumed", zap.String("event", "run_resumed"))
					} else {
						logger.Info("run started", zap.String("event", "run_started"))
					}
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			case cmdPause:
				if state.run.lifecycle.currentState() != runStateRunning {
					continue
				}
				<-pool.stop()
				state.pauseElapsed(time.Now())
				pool = nil
				delta := terminallyCompletedTransactionsSinceTick.Swap(0)
				state.run.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				promMetrics.actualTPS.Set(0)
				state.run.lifecycle.pause()
				state.notifyThrottler(throttlerUpdates, throttlerDone, true)
				logger.Info("run paused", zap.String("event", "run_paused"))
			case cmdReset:
				result := commandResult{status: commandAccepted}
				switch state.run.lifecycle.currentState() {
				case runStatePaused:
					state.run.lifecycle.reset()
					cancelReader()
					cancelThrottler()
					<-readerDone
					<-throttlerDone
					drain(batches)
					drain(senderBatches)
					cancelReader = nil
					readerDone = nil
					readerReconcile = nil
					readerSourceErrors = nil
					cancelThrottler = nil
					throttlerDone = nil
					throttlerUpdates = nil
					state.resetProgress(&terminallyCompletedTransactionsSinceTick, promMetrics)
					state.run.lifecycle.completeReset()
				case runStateFaulted:
					state.run.lifecycle.reset()
					stopActiveRun()
					state.resetProgress(&terminallyCompletedTransactionsSinceTick, promMetrics)
					state.run.lifecycle.completeReset()
				case runStateIdle:
					state.resetProgress(&terminallyCompletedTransactionsSinceTick, promMetrics)
				default:
					result.status = commandConflict
				}
				if result.status == commandAccepted {
					logger.Info("run stopped", zap.String("event", "run_stopped"))
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReadBatchSize:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle {
					result.status = commandConflict
				} else if !validReadBatchSize(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredReadBatchSize = cmd.value
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReaderWorkers:
				result := commandResult{status: commandAccepted}
				if !state.controls.policy.Reader.Workers.contains(cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredReaderWorkers = cmd.value
					state.controls.readerWorkersConfigured = true
					if readerReconcile != nil {
						readerReconcile(cmd.value)
					}
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReaderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle || !validReaderChannelCapacity(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredReaderChannelCapacity = cmd.value
					state.controls.readerChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetSenderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle || !validSenderChannelCapacity(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredSenderChannelCapacity = cmd.value
					state.controls.senderChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetRequestedTPS:
				result := commandResult{status: commandAccepted}
				if !state.controls.policy.Throttler.RequestedTPS.contains(cmd.value) {
					result.status = commandConflict
				} else if state.requestedTPS() != cmd.value {
					state.controls.configuredRequestedTPS = cmd.value
					state.controls.requestedTPSConfigured = true
					promMetrics.targetTPS.Set(float64(state.requestedTPS()))
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.run.lifecycle.currentState() == runStatePaused)
					logger.Info("throttler rate changed", zap.String("event", "throttler_rate_changed"), zap.Int("requested_tps", cmd.value))
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetThrottlerInstallationMode:
				result := commandResult{status: commandAccepted}
				if !state.controls.policy.Throttler.InstallationMode.contains(cmd.textValue) {
					result.status = commandConflict
				} else if state.installationMode() != cmd.textValue {
					state.controls.configuredInstallationMode = cmd.textValue
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.run.lifecycle.currentState() == runStatePaused)
					logger.Info("throttler mode changed", zap.String("event", "throttler_mode_changed"), zap.String("mode", cmd.textValue))
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetSenderWorkers:
				if !state.controls.policy.Sender.Workers.contains(cmd.value) {
					if cmd.commandReply != nil {
						cmd.commandReply <- commandResult{status: commandConflict}
					}
					continue
				}
				state.controls.configuredSenderWorkers = cmd.value
				state.controls.senderWorkersConfigured = true
				if pool != nil {
					pool.reconcile(cmd.value)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			}

		case sourceError := <-readerSourceErrors:
			if !state.run.lifecycle.fault() {
				continue
			}
			state.pauseElapsed(time.Now())
			state.run.sourceError = &sourceError
			logger.Error("reader source failed", zap.String("event", "reader_source_failed"), zap.String("operation", sourceError.Operation), zap.String("relative_path", sourceError.RelativePath))
			stopActiveRun()
			state.resetFaultedMeasurements(&terminallyCompletedTransactionsSinceTick, promMetrics)

		case <-metrics:
			state.telemetry.reader.sample(time.Now())
			state.telemetry.readerChannel.sample(state.metricsWindow)
			state.telemetry.senderChannel.sample(state.metricsWindow)
			delta := terminallyCompletedTransactionsSinceTick.Swap(0)
			state.run.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(delta) / state.metricsWindow.Seconds())
			promMetrics.transactionsTotal.Add(float64(delta))
			if time.Since(lastRuntimeSnapshot) >= time.Minute {
				lastRuntimeSnapshot = time.Now()
				logger.Info("runtime snapshot", zap.String("event", "runtime_snapshot"), zap.String("state", string(state.run.lifecycle.currentState())), zap.Int64("transactions", state.run.totalTransactions))
			}
		}
	}
}

func (state *controlState) readerWorkers() int {
	if state.controls.readerWorkersConfigured {
		return state.controls.configuredReaderWorkers
	}
	return state.controls.policy.Reader.Workers.Default
}

func (state *controlState) senderWorkers() int {
	if state.controls.senderWorkersConfigured {
		return state.controls.configuredSenderWorkers
	}
	return state.controls.policy.Sender.Workers.Default
}

func (state *controlState) requestedTPS() int {
	if state.controls.requestedTPSConfigured {
		return state.controls.configuredRequestedTPS
	}
	return state.controls.policy.Throttler.RequestedTPS.Default
}

func (state *controlState) installationMode() string {
	if state.controls.configuredInstallationMode != "" {
		return state.controls.configuredInstallationMode
	}
	return state.controls.policy.Throttler.InstallationMode.Default
}

func (state *controlState) throttlerSettings(paused bool) throttlerSettings {
	return throttlerSettings{
		requestedTPS: state.requestedTPS(),
		mode:         state.installationMode(),
		paused:       paused,
	}
}

func (state *controlState) notifyThrottler(
	updates chan<- throttlerUpdate,
	done <-chan struct{},
	paused bool,
) {
	if updates == nil {
		return
	}
	update := throttlerUpdate{
		settings:     state.throttlerSettings(paused),
		acknowledged: make(chan struct{}),
	}
	select {
	case updates <- update:
		<-update.acknowledged
	case <-done:
	}
}

func (state *controlState) resetProgress(terminallyCompletedTransactionsSinceTick *atomic.Int64, promMetrics *Metrics) {
	state.telemetry.reader.reset()
	state.telemetry.sender.reset()
	state.telemetry.readerChannel.clearMeasurements()
	state.telemetry.senderChannel.clearMeasurements()
	terminallyCompletedTransactionsSinceTick.Store(0)
	state.run.totalTransactions = 0
	state.run.elapsedBeforeRun = 0
	state.run.runStartedAt = time.Time{}
	state.run.sourceError = nil
	promMetrics.actualTPS.Set(0)
}

func (state *controlState) resetFaultedMeasurements(
	terminallyCompletedTransactionsSinceTick *atomic.Int64,
	promMetrics *Metrics,
) {
	state.telemetry.reader.reset()
	state.telemetry.sender.reset()
	state.telemetry.readerChannel.clearMeasurements()
	state.telemetry.senderChannel.clearMeasurements()
	terminallyCompletedTransactionsSinceTick.Store(0)
	promMetrics.actualTPS.Set(0)
}

func sourceErrorFromStartup(err error, sourcePath string) readerSourceError {
	var sourceError readerSourceError
	if errors.As(err, &sourceError) {
		return sourceError
	}
	return readerSourceError{
		Category:     "source",
		Operation:    "glob",
		RelativePath: relativeSourcePath(readerSourceDirectory(sourcePath), sourcePath),
		Message:      err.Error(),
	}
}

func (state *controlState) prepareReaderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.readerChannelCapacity()
	if batches != nil && cap(batches) == capacity {
		return batches, false
	}
	closeAndDrain(batches)
	state.telemetry.readerChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.telemetry.readerChannel.start(batches, state.readBatchSize())
	state.telemetry.readerChannel.clearMeasurements()
	return batches, true
}

func (state *controlState) prepareSenderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.senderChannelCapacity()
	batchSize := state.readBatchSize()
	if batches != nil && cap(batches) == capacity {
		state.telemetry.senderChannel.start(batches, batchSize)
		return batches, false
	}
	closeAndDrain(batches)
	state.telemetry.senderChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.telemetry.senderChannel.start(batches, batchSize)
	state.telemetry.senderChannel.clearMeasurements()
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

func (state *controlState) elapsedMs(now time.Time) int64 {
	elapsed := state.run.elapsedBeforeRun
	if state.run.lifecycle.currentState() == runStateRunning {
		elapsed += now.Sub(state.run.runStartedAt)
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}

func (state *controlState) pauseElapsed(now time.Time) {
	state.run.elapsedBeforeRun += now.Sub(state.run.runStartedAt)
	state.run.runStartedAt = time.Time{}
}
