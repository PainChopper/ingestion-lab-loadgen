package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"loadgen/internal/pipeline"

	"go.uber.org/zap"
)

type controlState struct {
	metricsWindow   time.Duration
	run             controlRunState
	config          config
	controls        configuredControls
	pipelineOptions pipeline.Options
	logger          *zap.Logger
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
		logger:          logger,
		pipelineOptions: pipelineOptions(cfg, logger),
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
	var run *pipeline.Pipeline
	var pipelineDone <-chan struct{}
	var sourceErrors <-chan error
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
		if run != nil {
			run.StopReading()
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
		// A Reader can report a fault just before the run closes or the deadline fires.
		select {
		case sourceError := <-sourceErrors:
			s.recordSourceFault(sourceError)
			runErr = errors.Join(runErr, sourceError)
		default:
		}
		s.finishRun(run, metrics, partial)
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
				var sourceError *pipeline.SourceError
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
			options := s.pipelineOptions
			options.Source = found.source
			run = pipeline.Start(runCtx, options)
			pipelineDone = run.Done()
			sourceErrors = run.SourceErrors()
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
				request.reply <- s.snapshotAt(run, time.Now())
			case controlRequest:
				request.reply <- s.run.state == runStateRunning &&
					s.applyCommand(run, request.command, metrics)
			}
		case now := <-events.ticks:
			if run != nil {
				s.sample(run, metrics, now)
			}
			if !now.Before(nextSnapshotLog) {
				nextSnapshotLog = now.Add(snapshotLogInterval)
				s.logger.Info("runtime summary", snapshotLogFields(s.snapshotAt(run, now))...)
			}
		}
	}
}

func (s *controlState) applyCommand(run *pipeline.Pipeline, command runtimeCommand, metrics *prometheusMetrics) bool {
	switch command.kind {
	case setReaderWorkers:
		if !s.config.Reader.Workers.contains(command.value) {
			return false
		}
		s.controls.readerWorkers = command.value
		run.SetReaders(command.value)
	case setSenderWorkers:
		if !s.config.Sender.Workers.contains(command.value) {
			return false
		}
		s.controls.senderWorkers = command.value
		run.SetSenders(command.value)
	case setRequestedTPS:
		if !s.config.Throttler.RequestedTPS.contains(command.value) {
			return false
		}
		if s.controls.requestedTPS != command.value {
			s.controls.requestedTPS = command.value
			metrics.targetTPS.Set(float64(command.value))
			run.SetRate(s.rate())
			s.logger.Info("throttler rate changed",
				zap.String("event", "throttler_rate_changed"), zap.Int("requested_tps", command.value))
		}
	case setThrottlerInstalled:
		if !s.config.Throttler.Installed.contains(command.installed) {
			return false
		}
		if s.controls.installed != command.installed {
			s.controls.installed = command.installed
			run.SetRate(s.rate())
			s.logger.Info("throttler mode changed",
				zap.String("event", "throttler_mode_changed"), zap.Bool("installed", command.installed))
		}
	default:
		return false
	}
	return true
}

func (s *controlState) recordSourceFault(err error) {
	var sourceError *pipeline.SourceError
	if !errors.As(err, &sourceError) {
		return
	}
	s.run.state = runStateFaulted
	s.run.sourceError = &readerSourceError{Category: "source", Operation: sourceError.Op,
		RelativePath: sourceError.Path, Message: sourceError.Error()}
	s.logger.Error("reader source failed", zap.String("event", "reader_source_failed"),
		zap.String("operation", sourceError.Op), zap.String("relative_path", sourceError.Path), zap.Error(sourceError))
}

func (s *controlState) sample(run *pipeline.Pipeline, metrics *prometheusMetrics, now time.Time) {
	elapsed := run.Sample(now)
	if elapsed <= 0 {
		return
	}
	delta := s.collectCompleted(run, metrics)
	metrics.actualTPS.Set(float64(delta) / elapsed.Seconds())
}

func (s *controlState) finishRun(run *pipeline.Pipeline, metrics *prometheusMetrics, partial bool) {
	if run != nil {
		s.collectCompleted(run, metrics)
	}
	metrics.actualTPS.Set(0)
	// Sample confirmations first: all confirmed rows were already counted at the Reader.
	var rowsRead int64
	if run != nil {
		rowsRead = run.Snapshot(time.Now()).Reader.RowsRead
	}
	s.logger.Info("run stopped", zap.String("event", "run_stopped"),
		zap.String("state", string(s.run.state)),
		zap.Bool("partial", partial),
		zap.Int64("confirmed_transactions", s.run.totalTransactions),
		zap.Int64("unconfirmed_transactions", rowsRead-s.run.totalTransactions),
	)
}

func (s *controlState) snapshotAt(run *pipeline.Pipeline, now time.Time) runtimeSnapshot {
	var snapshot pipeline.Snapshot
	if run != nil {
		snapshot = run.Snapshot(now)
	}
	readerPool, senderPool := snapshot.Reader, snapshot.Sender
	readerChannel, senderChannel := snapshot.ReaderQueue, snapshot.SenderQueue

	return runtimeSnapshot{
		Run: runSnapshot{
			State:             s.run.state,
			TotalTransactions: s.run.totalTransactions,
			ElapsedMs:         s.elapsedMs(now),
		},
		Reader: readerSnapshot{
			Workers: s.controls.readerWorkers, LiveWorkers: readerPool.LiveWorkers,
			IdleWorkers: readerPool.IdleWorkers, ReadingWorkers: readerPool.ReadingWorkers,
			BlockedWorkers: readerPool.BlockedWorkers, DrainingWorkers: readerPool.DrainingWorkers,
			DrainingIdleWorkers:    readerPool.DrainingIdleWorkers,
			DrainingReadingWorkers: readerPool.DrainingReadingWorkers,
			DrainingBlockedWorkers: readerPool.DrainingBlockedWorkers,
			ReadBatchSize:          s.config.Reader.ReadBatchSize.Initial, ReadTps: readerPool.ReadTPS,
			RowsRead:        readerPool.RowsRead,
			SourceDirectory: pipeline.SourceDirectory(s.config.Source.Path),
			SourceError:     s.run.sourceError,
		},
		Throttler: throttlerSnapshot{
			RequestedTps: s.controls.requestedTPS,
			AdmittedTps:  senderChannel.SentTransactionsPerSecond,
			Installed:    s.controls.installed,
		},
		Sender: senderSnapshot{
			Workers: s.controls.senderWorkers, LiveWorkers: senderPool.LiveWorkers,
			IdleWorkers: senderPool.IdleWorkers, InFlightWorkers: senderPool.InFlightWorkers,
			BackoffWorkers: senderPool.BackoffWorkers, DrainingWorkers: senderPool.DrainingWorkers,
			DrainingIdleWorkers:     senderPool.DrainingIdleWorkers,
			DrainingInFlightWorkers: senderPool.DrainingInFlightWorkers,
			DrainingBackoffWorkers:  senderPool.DrainingBackoffWorkers,
		},
		ReaderChannel: snapshotChannel(readerChannel, s.config.ReaderChannel.Capacity.Initial),
		SenderChannel: snapshotChannel(senderChannel, s.config.SenderChannel.Capacity.Initial),
		Config:        snapshotConfig(s.config),
	}
}

func (s *controlState) rate() pipeline.Rate {
	return pipeline.Rate{TPS: s.controls.requestedTPS, Installed: s.controls.installed}
}

func (s *controlState) collectCompleted(
	run *pipeline.Pipeline,
	promMetrics *prometheusMetrics,
) int64 {
	delta := run.TakeCompleted()
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
