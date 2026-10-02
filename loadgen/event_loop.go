package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

func (state *controlState) eventLoop(
	requests <-chan runtimeCommand,
	metrics <-chan time.Time,
	promMetrics *PrometheusMetrics,
	read readerStarter,
) {
	state.eventLoopWithThrottlerContext(context.Background(), requests, metrics, promMetrics, read, startThrottler)
}

func (state *controlState) runEventLoop(
	ctx context.Context,
	requests <-chan runtimeCommand,
	metrics <-chan time.Time,
	promMetrics *PrometheusMetrics,
) {
	state.eventLoopWithThrottlerContext(ctx, requests, metrics, promMetrics, state.startReaderPool, startThrottler)
}

func (state *controlState) eventLoopWithThrottler(
	requests <-chan runtimeCommand,
	metrics <-chan time.Time,
	promMetrics *PrometheusMetrics,
	read readerStarter,
	start throttlerStarter,
) {
	state.eventLoopWithThrottlerContext(context.Background(), requests, metrics, promMetrics, read, start)
}

func (state *controlState) eventLoopWithThrottlerContext(
	ctx context.Context,
	requests <-chan runtimeCommand,
	metrics <-chan time.Time,
	promMetrics *PrometheusMetrics,
	read readerStarter,
	start throttlerStarter,
) {
	logger := state.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	runtime := newPipelineRuntime(state, read, start)
	nextRuntimeSummaryAt := time.Now().Add(runtimeSummaryInterval)
	defer func() {
		runtime.stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case command, ok := <-requests:
			if !ok {
				return
			}
			switch command.kind {
			case getRuntimeStatus:
				command.respondStatus(state.runtimeStatusAt(runtime, time.Now()))
			case cmdRun:
				resuming := state.run.lifecycle.currentState() == runStatePaused
				if state.run.lifecycle.currentState() == runStateIdle {
					err := runtime.start(ctx)
					if err != nil {
						sourceError := sourceErrorFromStartup(err, state.controls.config.Source.Path)
						logger.Error("run failed to start", zap.String("event", "run_failed"), zap.String("operation", sourceError.Operation))
						state.run.sourceError = &sourceError
						state.run.lifecycle.faultStart()
						command.respond(runtimeCommandReceipt{err: err})
						continue
					}
				}
				if state.run.lifecycle.currentState() == runStateFaulted {
					command.respond(runtimeCommandReceipt{status: commandConflict})
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
				command.respond(runtimeCommandReceipt{})
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
				result := runtimeCommandReceipt{status: commandAccepted}
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
				command.respond(result)
			case cmdSetReadBatchSize:
				result := runtimeCommandReceipt{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle {
					result.status = commandConflict
				} else if !validReadBatchSize(state.controls.config, command.value) {
					result.status = commandConflict
				} else {
					state.controls.readBatchSize = command.value
				}
				command.respond(result)
			case cmdSetReaderWorkers:
				result := runtimeCommandReceipt{status: commandAccepted}
				if !state.controls.config.Reader.Workers.contains(command.value) {
					result.status = commandConflict
				} else {
					state.controls.readerWorkers = command.value
					runtime.reconcileReader(command.value)
				}
				command.respond(result)
			case cmdSetReaderChannelCapacity:
				result := runtimeCommandReceipt{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle ||
					!validReaderChannelCapacity(state.controls.config, command.value) {
					result.status = commandConflict
				} else {
					state.controls.readerChannelCapacity = command.value
				}
				command.respond(result)
			case cmdSetSenderChannelCapacity:
				result := runtimeCommandReceipt{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle ||
					!validSenderChannelCapacity(state.controls.config, command.value) {
					result.status = commandConflict
				} else {
					state.controls.senderChannelCapacity = command.value
				}
				command.respond(result)
			case cmdSetRequestedTPS:
				result := runtimeCommandReceipt{status: commandAccepted}
				if !state.controls.config.Throttler.RequestedTPS.contains(command.value) {
					result.status = commandConflict
				} else if state.requestedTPS() != command.value {
					state.controls.requestedTPS = command.value
					promMetrics.targetTPS.Set(float64(state.requestedTPS()))
					runtime.updateThrottler(state.throttlerSettings(state.run.lifecycle.currentState() == runStatePaused))
					logger.Info(
						"throttler rate changed",
						zap.String("event", "throttler_rate_changed"),
						zap.Int("requested_tps", command.value),
					)
				}
				command.respond(result)
			case cmdSetThrottlerInstallationMode:
				result := runtimeCommandReceipt{status: commandAccepted}
				if !state.controls.config.Throttler.InstallationMode.contains(command.textValue) {
					result.status = commandConflict
				} else if state.installationMode() != command.textValue {
					state.controls.installationMode = command.textValue
					runtime.updateThrottler(state.throttlerSettings(state.run.lifecycle.currentState() == runStatePaused))
					logger.Info(
						"throttler mode changed",
						zap.String("event", "throttler_mode_changed"),
						zap.String("mode", command.textValue),
					)
				}
				command.respond(result)
			case cmdSetSenderWorkers:
				if !state.controls.config.Sender.Workers.contains(command.value) {
					command.respond(runtimeCommandReceipt{status: commandConflict})
					continue
				}
				state.controls.senderWorkers = command.value
				runtime.reconcileSender(command.value)
				command.respond(runtimeCommandReceipt{})
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

		case now := <-metrics:
			state.telemetry.reader.sample(now)
			state.telemetry.readerChannel.sample(state.metricsWindow)
			state.telemetry.senderChannel.sample(state.metricsWindow)
			delta := runtime.terminallyCompletedTransactionsSinceTick.Swap(0)
			state.run.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(delta) / state.metricsWindow.Seconds())
			promMetrics.transactionsTotal.Add(float64(delta))
			if !now.Before(nextRuntimeSummaryAt) {
				nextRuntimeSummaryAt = now.Add(runtimeSummaryInterval)
				logger.Info(
					"runtime summary",
					runtimeSummaryFields(runtimeSummaryFromStatus(state.runtimeStatusAt(runtime, now)))...,
				)
			}
		}
	}
}

func (state *controlState) runtimeStatusAt(runtime *pipelineRuntime, now time.Time) runtimeStatus {
	runState := state.run.lifecycle.currentState()
	reader := state.telemetry.reader.snapshot()
	readerPool, senderPool := runtime.snapshots()
	readerChannel := state.telemetry.readerChannel.snapshot(now)
	senderChannel := state.telemetry.senderChannel.snapshot(now)
	if runState == runStateIdle || runState == runStateFaulted {
		readerChannel.capacity = state.readerChannelCapacity()
		senderChannel.capacity = state.senderChannelCapacity()
	}

	return runtimeStatus{
		Run: runtimeRunStatus{
			State:             runState,
			TotalTransactions: state.run.totalTransactions,
			ElapsedMs:         state.elapsedMs(now),
		},
		Reader: runtimeReaderStatus{
			Workers: state.readerWorkers(), LiveWorkers: readerPool.liveWorkers,
			IdleWorkers: readerPool.idleWorkers, ReadingWorkers: readerPool.readingWorkers,
			BlockedWorkers: readerPool.blockedWorkers, DrainingWorkers: readerPool.drainingWorkers,
			DrainingIdleWorkers:    readerPool.drainingIdleWorkers,
			DrainingReadingWorkers: readerPool.drainingReadingWorkers,
			DrainingBlockedWorkers: readerPool.drainingBlockedWorkers,
			ReadBatchSize:          state.readBatchSize(), ReadTps: reader.readTPS,
			RowsRead:        reader.rowsRead,
			SourceDirectory: readerSourceDirectory(state.controls.config.Source.Path),
			SourceError:     state.run.sourceError,
		},
		Throttler: runtimeThrottlerStatus{
			RequestedTps:     state.requestedTPS(),
			AdmittedTps:      senderChannel.sentTransactionsPerSecond,
			InstallationMode: state.installationMode(),
		},
		Sender: runtimeSenderStatus{
			Workers: state.senderWorkers(), LiveWorkers: senderPool.liveWorkers,
			IdleWorkers: senderPool.idleWorkers, InFlightWorkers: senderPool.inFlightWorkers,
			BackoffWorkers: senderPool.backoffWorkers, DrainingWorkers: senderPool.drainingWorkers,
			DrainingIdleWorkers:     senderPool.drainingIdleWorkers,
			DrainingInFlightWorkers: senderPool.drainingInFlightWorkers,
			DrainingBackoffWorkers:  senderPool.drainingBackoffWorkers,
		},
		ReaderChannel: runtimeChannelStatus{
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
		SenderChannel: runtimeChannelStatus{
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
		Config: runtimeConfigStatusFromConfig(state.controls.config),
	}
}

func (state *controlState) readerWorkers() int {
	return state.controls.readerWorkers
}

func (state *controlState) senderWorkers() int {
	return state.controls.senderWorkers
}

func (state *controlState) requestedTPS() int {
	return state.controls.requestedTPS
}

func (state *controlState) installationMode() string {
	return state.controls.installationMode
}

func (state *controlState) throttlerSettings(paused bool) throttlerSettings {
	return throttlerSettings{
		requestedTPS: state.requestedTPS(),
		mode:         state.installationMode(),
		paused:       paused,
	}
}

func (state *controlState) resetProgress(terminallyCompletedTransactionsSinceTick *atomic.Int64, promMetrics *PrometheusMetrics) {
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
	promMetrics *PrometheusMetrics,
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
