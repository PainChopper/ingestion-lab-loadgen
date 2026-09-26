package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

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
	runtime := newPipelineRuntime(state, read, start)
	var lastRuntimeSnapshot time.Time
	defer func() {
		runtime.stop()
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
				readerPool, senderPool := runtime.snapshots()
				readerChannel := state.telemetry.readerChannel.snapshot(time.Now())
				senderChannel := state.telemetry.senderChannel.snapshot(time.Now())
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
					err := runtime.start(ctx)
					if err != nil {
						sourceError := sourceErrorFromStartup(err, state.controls.policy.Source.Path)
						logger.Error("run failed to start", zap.String("event", "run_failed"), zap.String("operation", sourceError.Operation))
						state.run.sourceError = &sourceError
						state.run.lifecycle.faultStart()
						if cmd.commandReply != nil {
							cmd.commandReply <- commandResult{err: err}
						}
						continue
					}
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
						runtime.updateThrottler(state.throttlerSettings(false))
					}
					runtime.startSender()
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
				runtime.stopSender()
				state.pauseElapsed(time.Now())
				delta := runtime.terminallyCompletedTransactionsSinceTick.Swap(0)
				state.run.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				promMetrics.actualTPS.Set(0)
				state.run.lifecycle.pause()
				runtime.updateThrottler(state.throttlerSettings(true))
				logger.Info("run paused", zap.String("event", "run_paused"))
			case cmdReset:
				result := commandResult{status: commandAccepted}
				switch state.run.lifecycle.currentState() {
				case runStatePaused:
					state.run.lifecycle.reset()
					runtime.resetPaused()
					state.resetProgress(&runtime.terminallyCompletedTransactionsSinceTick, promMetrics)
					state.run.lifecycle.completeReset()
				case runStateFaulted:
					state.run.lifecycle.reset()
					runtime.stop()
					state.resetProgress(&runtime.terminallyCompletedTransactionsSinceTick, promMetrics)
					state.run.lifecycle.completeReset()
				case runStateIdle:
					state.resetProgress(&runtime.terminallyCompletedTransactionsSinceTick, promMetrics)
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
					runtime.reconcileReader(cmd.value)
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
					runtime.updateThrottler(state.throttlerSettings(state.run.lifecycle.currentState() == runStatePaused))
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
					runtime.updateThrottler(state.throttlerSettings(state.run.lifecycle.currentState() == runStatePaused))
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
				runtime.reconcileSender(cmd.value)
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			}

		case sourceError := <-runtime.sourceErrors():
			if !state.run.lifecycle.fault() {
				continue
			}
			state.pauseElapsed(time.Now())
			state.run.sourceError = &sourceError
			logger.Error("reader source failed", zap.String("event", "reader_source_failed"), zap.String("operation", sourceError.Operation), zap.String("relative_path", sourceError.RelativePath))
			runtime.stop()
			state.resetFaultedMeasurements(&runtime.terminallyCompletedTransactionsSinceTick, promMetrics)

		case <-metrics:
			state.telemetry.reader.sample(time.Now())
			state.telemetry.readerChannel.sample(state.metricsWindow)
			state.telemetry.senderChannel.sample(state.metricsWindow)
			delta := runtime.terminallyCompletedTransactionsSinceTick.Swap(0)
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
