package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type controlState struct {
	metricsWindow time.Duration
	run           controlRunState
	config        config
	controls      configuredControls
	telemetry     controlTelemetry
	logger        *zap.Logger
}

type controlRunState struct {
	totalTransactions int64
	runStartedAt      time.Time
	sourceError       *readerSourceError
	state             runState
}

type configuredControls struct {
	readerWorkers int
	requestedTPS  int
	installed     bool
	senderWorkers int
}

type controlTelemetry struct {
	reader        readerTelemetry
	readerChannel channelTelemetry
	senderChannel channelTelemetry
}

type runtimeEvents struct {
	requests   <-chan runtimeCommand
	source     <-chan sourceDiscovery
	serverDone <-chan error
	metrics    serviceMetrics
}

func newControlState(cfg config, logger *zap.Logger) controlState {
	if logger == nil {
		logger = zap.NewNop()
	}
	return controlState{
		metricsWindow: time.Duration(cfg.Metrics.WindowMS.Initial) * time.Millisecond,
		run:           controlRunState{state: runStateStarting},
		config:        cfg,
		controls: configuredControls{
			readerWorkers: cfg.Reader.Workers.Initial,
			requestedTPS:  cfg.Throttler.RequestedTPS.Initial,
			installed:     cfg.Throttler.Installed.Initial,
			senderWorkers: cfg.Sender.Workers.Initial,
		},
		logger: logger,
	}
}

func (state *controlState) runEventLoop(
	appCtx context.Context,
	events runtimeEvents,
	grace time.Duration,
) (shutdownDeadline time.Time, runErr error) {
	metrics := events.metrics
	if metrics.prometheusMetrics == nil {
		return time.Time{}, errors.New("run event loop: Prometheus metrics are required")
	}

	// App cancellation stops admission first; the deadline cancels the pipeline.
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	appDone := appCtx.Done()
	var pipeline *pipelineRuntime
	var pipelineDone <-chan struct{}
	var sourceErrors <-chan readerSourceError
	var deadline <-chan time.Time
	var timer *time.Timer
	var partial bool
	beginDrain := func() {
		appDone = nil
		if timer == nil {
			shutdownDeadline = time.Now().Add(grace)
			timer = time.NewTimer(time.Until(shutdownDeadline))
			deadline = timer.C
		}
		if pipeline != nil {
			pipeline.readerPool.stopReading()
		}
	}
	stopOnSignal := func() {
		state.run.state = runStateStopping
		beginDrain()
		state.logger.Info("service stopping", zap.String("event", "service_stopping"))
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		// A Reader can report a fault just before the pipeline closes or the deadline fires.
		select {
		case sourceError := <-sourceErrors:
			state.recordSourceFault(sourceError)
			runErr = errors.Join(runErr, sourceError)
		default:
		}
		state.finishRun(pipeline, metrics.prometheusMetrics, partial)
	}()

	nextSummary := time.Now().Add(runtimeSummaryInterval)
	for {
		select {
		case <-appDone:
			stopOnSignal()
		case err := <-events.serverDone:
			events.serverDone = nil
			if err == nil {
				err = errors.New("http server stopped unexpectedly")
			}
			runErr = errors.Join(runErr, err)
			state.run.state = runStateFaulted
			state.logger.Error("HTTP server failed", zap.String("event", "run_failed"), zap.Error(err))
			beginDrain()
		case found := <-events.source:
			events.source = nil
			if found.err != nil {
				var sourceError readerSourceError
				if errors.As(found.err, &sourceError) {
					state.recordSourceFault(sourceError)
				}
				return shutdownDeadline, errors.Join(runErr, fmt.Errorf("start run: %w", found.err))
			}
			if state.run.state != runStateStarting {
				return shutdownDeadline, runErr
			}
			if appCtx.Err() != nil {
				stopOnSignal()
				return shutdownDeadline, runErr
			}
			pipeline = startPipeline(runCtx, state, found.source)
			pipelineDone = pipeline.done
			sourceErrors = pipeline.readerPool.sourceErrors
			state.run.runStartedAt = time.Now()
			state.run.state = runStateRunning
			state.logger.Info("run started", zap.String("event", "run_started"))
		case sourceError := <-sourceErrors:
			sourceErrors = nil
			state.recordSourceFault(sourceError)
			runErr = errors.Join(runErr, sourceError)
			beginDrain()
		case <-deadline:
			cancelRun()
			// Source filesystem calls can outlive cancellation. Leave stage channels to their owners.
			select {
			case <-pipelineDone:
			default:
				partial = true
				state.logger.Warn("pipeline drain timed out", zap.String("event", "service_stopping"))
			}
			return shutdownDeadline, runErr
		case <-pipelineDone:
			return shutdownDeadline, runErr
		case command, ok := <-events.requests:
			if !ok {
				events.requests = nil
				continue
			}
			if command.kind == getRuntimeStatus {
				command.respondStatus(state.runtimeStatusAt(pipeline, time.Now()))
				continue
			}
			applied := state.run.state == runStateRunning &&
				state.applyCommand(pipeline, command, metrics.prometheusMetrics)
			command.respond(runtimeCommandReceipt{rejected: !applied})
		case now := <-metrics.ticks:
			if pipeline != nil {
				state.sample(pipeline, metrics.prometheusMetrics, now)
			}
			if !now.Before(nextSummary) {
				nextSummary = now.Add(runtimeSummaryInterval)
				state.logger.Info("runtime summary", runtimeSummaryFields(state.runtimeStatusAt(pipeline, now))...)
			}
		}
	}
}

func (state *controlState) applyCommand(pipeline *pipelineRuntime, command runtimeCommand, metrics *PrometheusMetrics) bool {
	switch command.kind {
	case cmdSetReaderWorkers:
		if !state.config.Reader.Workers.contains(command.value) {
			return false
		}
		state.controls.readerWorkers = command.value
		pipeline.readerPool.reconcile(command.value)
	case cmdSetSenderWorkers:
		if !state.config.Sender.Workers.contains(command.value) {
			return false
		}
		state.controls.senderWorkers = command.value
		pipeline.senderPool.reconcile(command.value)
	case cmdSetRequestedTPS:
		if !state.config.Throttler.RequestedTPS.contains(command.value) {
			return false
		}
		if state.controls.requestedTPS != command.value {
			state.controls.requestedTPS = command.value
			metrics.targetTPS.Set(float64(command.value))
			pipeline.throttler.update(state.throttlerSettings())
			state.logger.Info("throttler rate changed",
				zap.String("event", "throttler_rate_changed"), zap.Int("requested_tps", command.value))
		}
	case cmdSetThrottlerInstalled:
		if !state.config.Throttler.Installed.contains(command.installed) {
			return false
		}
		if state.controls.installed != command.installed {
			state.controls.installed = command.installed
			pipeline.throttler.update(state.throttlerSettings())
			state.logger.Info("throttler mode changed",
				zap.String("event", "throttler_mode_changed"), zap.Bool("installed", command.installed))
		}
	default:
		return false
	}
	return true
}

func (state *controlState) recordSourceFault(sourceError readerSourceError) {
	state.run.state = runStateFaulted
	state.run.sourceError = &sourceError
	state.logger.Error("reader source failed", zap.String("event", "reader_source_failed"),
		zap.String("operation", sourceError.Operation), zap.String("relative_path", sourceError.RelativePath), zap.Error(sourceError))
}

func (state *controlState) sample(pipeline *pipelineRuntime, metrics *PrometheusMetrics, now time.Time) {
	delta := state.collectCompleted(pipeline, metrics)
	state.telemetry.reader.sample(now)
	state.telemetry.readerChannel.sample(state.metricsWindow)
	state.telemetry.senderChannel.sample(state.metricsWindow)
	metrics.actualTPS.Set(float64(delta) / state.metricsWindow.Seconds())
}

func (state *controlState) finishRun(pipeline *pipelineRuntime, metrics *PrometheusMetrics, partial bool) {
	if pipeline != nil {
		state.collectCompleted(pipeline, metrics)
	}
	metrics.actualTPS.Set(0)
	// Sample confirmations first: all confirmed rows were already counted at the Reader.
	rowsRead := state.telemetry.reader.snapshot().rowsRead
	state.logger.Info("run stopped", zap.String("event", "run_stopped"),
		zap.String("state", string(state.run.state)),
		zap.Bool("partial", partial),
		zap.Int64("confirmed_transactions", state.run.totalTransactions),
		zap.Int64("unconfirmed_transactions", rowsRead-state.run.totalTransactions),
	)
	if pipeline != nil && !partial {
		pipeline.detachChannels(state)
	}
}

func (state *controlState) runtimeStatusAt(runtime *pipelineRuntime, now time.Time) runtimeStatus {
	reader := state.telemetry.reader.snapshot()
	readerPool, senderPool := runtime.snapshots()
	readerChannel := state.telemetry.readerChannel.snapshot(now)
	senderChannel := state.telemetry.senderChannel.snapshot(now)

	return runtimeStatus{
		Run: runtimeRunStatus{
			State:             state.run.state,
			TotalTransactions: state.run.totalTransactions,
			ElapsedMs:         state.elapsedMs(now),
		},
		Reader: runtimeReaderStatus{
			Workers: state.controls.readerWorkers, LiveWorkers: readerPool.liveWorkers,
			IdleWorkers: readerPool.idleWorkers, ReadingWorkers: readerPool.readingWorkers,
			BlockedWorkers: readerPool.blockedWorkers, DrainingWorkers: readerPool.drainingWorkers,
			DrainingIdleWorkers:    readerPool.drainingIdleWorkers,
			DrainingReadingWorkers: readerPool.drainingReadingWorkers,
			DrainingBlockedWorkers: readerPool.drainingBlockedWorkers,
			ReadBatchSize:          state.config.Reader.ReadBatchSize.Initial, ReadTps: reader.readTPS,
			RowsRead:        reader.rowsRead,
			SourceDirectory: readerSourceDirectory(state.config.Source.Path),
			SourceError:     state.run.sourceError,
		},
		Throttler: runtimeThrottlerStatus{
			RequestedTps: state.controls.requestedTPS,
			AdmittedTps:  senderChannel.sentTransactionsPerSecond,
			Installed:    state.controls.installed,
		},
		Sender: runtimeSenderStatus{
			Workers: state.controls.senderWorkers, LiveWorkers: senderPool.liveWorkers,
			IdleWorkers: senderPool.idleWorkers, InFlightWorkers: senderPool.inFlightWorkers,
			BackoffWorkers: senderPool.backoffWorkers, DrainingWorkers: senderPool.drainingWorkers,
			DrainingIdleWorkers:     senderPool.drainingIdleWorkers,
			DrainingInFlightWorkers: senderPool.drainingInFlightWorkers,
			DrainingBackoffWorkers:  senderPool.drainingBackoffWorkers,
		},
		ReaderChannel: runtimeChannelStatus{
			Capacity:                      state.config.ReaderChannel.Capacity.Initial,
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
			Capacity:                      state.config.SenderChannel.Capacity.Initial,
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
		Config: runtimeConfigStatusFromConfig(state.config),
	}
}

func (state *controlState) throttlerSettings() throttlerSettings {
	return throttlerSettings{
		requestedTPS: state.controls.requestedTPS,
		installed:    state.controls.installed,
	}
}

func (state *controlState) collectCompleted(
	pipeline *pipelineRuntime,
	promMetrics *PrometheusMetrics,
) int64 {
	delta := pipeline.terminallyCompletedTransactionsSinceTick.Swap(0)
	state.run.totalTransactions += delta
	promMetrics.transactionsTotal.Add(float64(delta))
	return delta
}

func (state *controlState) elapsedMs(now time.Time) int64 {
	if state.run.runStartedAt.IsZero() {
		return 0
	}
	return max(0, now.Sub(state.run.runStartedAt).Milliseconds())
}
