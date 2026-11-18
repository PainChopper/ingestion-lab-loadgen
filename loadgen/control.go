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
	requests   <-chan runtimeRequest
	source     <-chan sourceDiscovery
	serverDone <-chan error
	ticks      <-chan time.Time
	metrics    *prometheusMetrics
}

func newControlState(cfg config, logger *zap.Logger) controlState {
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

func (s *controlState) runEventLoop(
	appCtx context.Context,
	events runtimeEvents,
	grace time.Duration,
) (shutdownDeadline time.Time, runErr error) {
	metrics := events.metrics

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
		s.run.state = runStateStopping
		beginDrain()
		s.logger.Info("service stopping", zap.String("event", "service_stopping"))
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		// A Reader can report a fault just before the pipeline closes or the deadline fires.
		select {
		case sourceError := <-sourceErrors:
			s.recordSourceFault(sourceError)
			runErr = errors.Join(runErr, sourceError)
		default:
		}
		s.finishRun(pipeline, metrics, partial)
	}()

	nextSnapshotLog := time.Now().Add(snapshotLogInterval)
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
			s.run.state = runStateFaulted
			s.logger.Error("HTTP server failed", zap.String("event", "run_failed"), zap.Error(err))
			beginDrain()
		case found := <-events.source:
			events.source = nil
			if found.err != nil {
				var sourceError readerSourceError
				if errors.As(found.err, &sourceError) {
					s.recordSourceFault(sourceError)
				}
				return shutdownDeadline, errors.Join(runErr, fmt.Errorf("start run: %w", found.err))
			}
			if s.run.state != runStateStarting {
				return shutdownDeadline, runErr
			}
			if appCtx.Err() != nil {
				stopOnSignal()
				return shutdownDeadline, runErr
			}
			pipeline = startPipeline(runCtx, s, found.source)
			pipelineDone = pipeline.done
			sourceErrors = pipeline.readerPool.sourceErrors
			s.run.runStartedAt = time.Now()
			s.run.state = runStateRunning
			s.logger.Info("run started", zap.String("event", "run_started"))
		case sourceError := <-sourceErrors:
			sourceErrors = nil
			s.recordSourceFault(sourceError)
			runErr = errors.Join(runErr, sourceError)
			beginDrain()
		case <-deadline:
			cancelRun()
			// Source filesystem calls can outlive cancellation. Leave stage channels to their owners.
			select {
			case <-pipelineDone:
			default:
				partial = true
				s.logger.Warn("pipeline drain timed out", zap.String("event", "service_stopping"))
			}
			return shutdownDeadline, runErr
		case <-pipelineDone:
			return shutdownDeadline, runErr
		case request, ok := <-events.requests:
			if !ok {
				events.requests = nil
				continue
			}
			switch request := request.(type) {
			case snapshotRequest:
				request.reply <- s.snapshotAt(pipeline, time.Now())
			case controlRequest:
				request.reply <- s.run.state == runStateRunning &&
					s.applyCommand(pipeline, request.command, metrics)
			}
		case now := <-events.ticks:
			if pipeline != nil {
				s.sample(pipeline, metrics, now)
			}
			if !now.Before(nextSnapshotLog) {
				nextSnapshotLog = now.Add(snapshotLogInterval)
				s.logger.Info("runtime summary", snapshotLogFields(s.snapshotAt(pipeline, now))...)
			}
		}
	}
}

func (s *controlState) applyCommand(pipeline *pipelineRuntime, command runtimeCommand, metrics *prometheusMetrics) bool {
	switch command.kind {
	case setReaderWorkers:
		if !s.config.Reader.Workers.contains(command.value) {
			return false
		}
		s.controls.readerWorkers = command.value
		pipeline.readerPool.reconcile(command.value)
	case setSenderWorkers:
		if !s.config.Sender.Workers.contains(command.value) {
			return false
		}
		s.controls.senderWorkers = command.value
		pipeline.senderPool.reconcile(command.value)
	case setRequestedTPS:
		if !s.config.Throttler.RequestedTPS.contains(command.value) {
			return false
		}
		if s.controls.requestedTPS != command.value {
			s.controls.requestedTPS = command.value
			metrics.targetTPS.Set(float64(command.value))
			pipeline.throttler.update(s.throttlerSettings())
			s.logger.Info("throttler rate changed",
				zap.String("event", "throttler_rate_changed"), zap.Int("requested_tps", command.value))
		}
	case setThrottlerInstalled:
		if !s.config.Throttler.Installed.contains(command.installed) {
			return false
		}
		if s.controls.installed != command.installed {
			s.controls.installed = command.installed
			pipeline.throttler.update(s.throttlerSettings())
			s.logger.Info("throttler mode changed",
				zap.String("event", "throttler_mode_changed"), zap.Bool("installed", command.installed))
		}
	default:
		return false
	}
	return true
}

func (s *controlState) recordSourceFault(sourceError readerSourceError) {
	s.run.state = runStateFaulted
	s.run.sourceError = &sourceError
	s.logger.Error("reader source failed", zap.String("event", "reader_source_failed"),
		zap.String("operation", sourceError.Operation), zap.String("relative_path", sourceError.RelativePath), zap.Error(sourceError))
}

func (s *controlState) sample(pipeline *pipelineRuntime, metrics *prometheusMetrics, now time.Time) {
	delta := s.collectCompleted(pipeline, metrics)
	s.telemetry.reader.sample(now)
	s.telemetry.readerChannel.sample(s.metricsWindow)
	s.telemetry.senderChannel.sample(s.metricsWindow)
	metrics.actualTPS.Set(float64(delta) / s.metricsWindow.Seconds())
}

func (s *controlState) finishRun(pipeline *pipelineRuntime, metrics *prometheusMetrics, partial bool) {
	if pipeline != nil {
		s.collectCompleted(pipeline, metrics)
	}
	metrics.actualTPS.Set(0)
	// Sample confirmations first: all confirmed rows were already counted at the Reader.
	rowsRead := s.telemetry.reader.snapshot().rowsRead
	s.logger.Info("run stopped", zap.String("event", "run_stopped"),
		zap.String("state", string(s.run.state)),
		zap.Bool("partial", partial),
		zap.Int64("confirmed_transactions", s.run.totalTransactions),
		zap.Int64("unconfirmed_transactions", rowsRead-s.run.totalTransactions),
	)
}

func (s *controlState) snapshotAt(runtime *pipelineRuntime, now time.Time) runtimeSnapshot {
	reader := s.telemetry.reader.snapshot()
	readerPool, senderPool := runtime.snapshots()
	readerChannel := s.telemetry.readerChannel.snapshot(now)
	senderChannel := s.telemetry.senderChannel.snapshot(now)

	return runtimeSnapshot{
		Run: runSnapshot{
			State:             s.run.state,
			TotalTransactions: s.run.totalTransactions,
			ElapsedMs:         s.elapsedMs(now),
		},
		Reader: readerSnapshot{
			Workers: s.controls.readerWorkers, LiveWorkers: readerPool.liveWorkers,
			IdleWorkers: readerPool.idleWorkers, ReadingWorkers: readerPool.readingWorkers,
			BlockedWorkers: readerPool.blockedWorkers, DrainingWorkers: readerPool.drainingWorkers,
			DrainingIdleWorkers:    readerPool.drainingIdleWorkers,
			DrainingReadingWorkers: readerPool.drainingReadingWorkers,
			DrainingBlockedWorkers: readerPool.drainingBlockedWorkers,
			ReadBatchSize:          s.config.Reader.ReadBatchSize.Initial, ReadTps: reader.readTPS,
			RowsRead:        reader.rowsRead,
			SourceDirectory: readerSourceDirectory(s.config.Source.Path),
			SourceError:     s.run.sourceError,
		},
		Throttler: throttlerSnapshot{
			RequestedTps: s.controls.requestedTPS,
			AdmittedTps:  senderChannel.sentTransactionsPerSecond,
			Installed:    s.controls.installed,
		},
		Sender: senderSnapshot{
			Workers: s.controls.senderWorkers, LiveWorkers: senderPool.liveWorkers,
			IdleWorkers: senderPool.idleWorkers, InFlightWorkers: senderPool.inFlightWorkers,
			BackoffWorkers: senderPool.backoffWorkers, DrainingWorkers: senderPool.drainingWorkers,
			DrainingIdleWorkers:     senderPool.drainingIdleWorkers,
			DrainingInFlightWorkers: senderPool.drainingInFlightWorkers,
			DrainingBackoffWorkers:  senderPool.drainingBackoffWorkers,
		},
		ReaderChannel: channelSnapshot{
			Capacity:                      s.config.ReaderChannel.Capacity.Initial,
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
			Capacity:                      s.config.SenderChannel.Capacity.Initial,
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
		Config: snapshotConfig(s.config),
	}
}

func (s *controlState) throttlerSettings() throttlerSettings {
	return throttlerSettings{
		requestedTPS: s.controls.requestedTPS,
		installed:    s.controls.installed,
	}
}

func (s *controlState) collectCompleted(
	pipeline *pipelineRuntime,
	promMetrics *prometheusMetrics,
) int64 {
	delta := pipeline.terminallyCompletedTransactionsSinceTick.Swap(0)
	s.run.totalTransactions += delta
	promMetrics.transactionsTotal.Add(float64(delta))
	return delta
}

func (s *controlState) elapsedMs(now time.Time) int64 {
	if s.run.runStartedAt.IsZero() {
		return 0
	}
	return max(0, now.Sub(s.run.runStartedAt).Milliseconds())
}

type runState string

const (
	runStateStarting runState = "starting"
	runStateRunning  runState = "running"
	runStateStopping runState = "stopping"
	runStateFaulted  runState = "faulted"
)

type runtimeCommandKind int

const (
	setReaderWorkers runtimeCommandKind = iota + 1
	setRequestedTPS
	setThrottlerInstalled
	setSenderWorkers
)

var errRuntimeUnavailable = errors.New("runtime unavailable")

type runtimeRequests struct {
	queue chan<- runtimeRequest
	done  <-chan struct{}
}

type runtimeCommand struct {
	kind      runtimeCommandKind
	value     int
	installed bool
}

type runtimeRequest interface {
	isRuntimeRequest()
}

type snapshotRequest struct {
	reply chan<- runtimeSnapshot
}

type controlRequest struct {
	command runtimeCommand
	reply   chan<- bool
}

func (snapshotRequest) isRuntimeRequest() {}
func (controlRequest) isRuntimeRequest()  {}

func requestSnapshot(ctx context.Context, requests runtimeRequests) (runtimeSnapshot, error) {
	reply := make(chan runtimeSnapshot, 1)
	return awaitRuntimeReply(ctx, requests, snapshotRequest{reply: reply}, reply)
}

func executeRuntimeCommand(ctx context.Context, requests runtimeRequests, command runtimeCommand) (bool, error) {
	reply := make(chan bool, 1)
	return awaitRuntimeReply(ctx, requests, controlRequest{command: command, reply: reply}, reply)
}

func awaitRuntimeReply[T any](ctx context.Context, requests runtimeRequests, request runtimeRequest, reply <-chan T) (T, error) {
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-requests.done:
		return zero, errRuntimeUnavailable
	case requests.queue <- request:
	}
	return receiveRuntimeReply(ctx, requests.done, reply)
}

func receiveRuntimeReply[T any](ctx context.Context, done <-chan struct{}, reply <-chan T) (T, error) {
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-done:
		// The loop publishes replies before closing done. Prefer an already published result.
		select {
		case result := <-reply:
			return result, nil
		default:
			return zero, errRuntimeUnavailable
		}
	case result := <-reply:
		return result, nil
	}
}
