// Package control serializes LoadGen run state, live commands and snapshots.
package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"go.uber.org/zap"
)

// Controller owns run state and accepts requests while its event loop is active.
type Controller struct {
	startedAt       time.Time
	run             controlRunState
	config          config.Config
	controls        configuredControls
	pipelineOptions pipeline.Options
	logger          *zap.Logger
	queue           chan runtimeRequest
	done            chan struct{}
}

type controlRunState struct {
	runStartedAt time.Time
	sourceError  *api.SourceError
	state        api.RunState
}

type configuredControls struct {
	readerWorkers int
	requestedTPS  int
	installed     bool
	senderWorkers int
}

// SourceResult carries the outcome of startup Parquet source discovery.
type SourceResult struct {
	// Source is the discovered input.
	Source pipeline.Source
	// Err is the discovery failure, when one occurred.
	Err error
}

// Events provides external inputs and callbacks for the controller's run.
type Events struct {
	// Source delivers the result of source discovery.
	Source <-chan SourceResult
	// ServerDone reports that the HTTP listener stopped.
	ServerDone <-chan error
	// PipelineStarted receives the newly started pipeline, when provided.
	PipelineStarted func(*pipeline.Pipeline)
	// RequestedTPSChanged receives each newly applied target TPS, when provided.
	RequestedTPSChanged func(int)
}

// New prepares the controller with the validated configuration and pipeline options.
func New(cfg config.Config, options pipeline.Options, logger *zap.Logger) *Controller {
	return &Controller{
		startedAt: time.Now(),
		run:       controlRunState{state: api.RunStateStarting},
		config:    cfg,
		controls: configuredControls{
			readerWorkers: cfg.Reader.Workers.Initial,
			requestedTPS:  cfg.Throttler.RequestedTPS.Initial,
			installed:     cfg.Throttler.Installed,
			senderWorkers: cfg.Sender.Workers.Initial,
		},
		logger:          logger,
		pipelineOptions: options,
		queue:           make(chan runtimeRequest, 10),
		done:            make(chan struct{}),
	}
}

// Run processes controller events once and returns the HTTP shutdown deadline, if set, and run error.
func (s *Controller) Run(
	appCtx context.Context,
	events Events,
	grace time.Duration,
) (shutdownDeadline time.Time, runErr error) {
	defer close(s.done)
	ticker := time.NewTicker(snapshotLogInterval)
	defer ticker.Stop()

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
		s.run.state = api.RunStateStopping
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
		s.finishRun(run, partial)
	}()

	for {
		select {
		case <-appDone:
			stopOnSignal()
		case err := <-events.ServerDone:
			events.ServerDone = nil
			if err == nil {
				err = errors.New("http server stopped unexpectedly")
			}
			runErr = errors.Join(runErr, err)
			s.run.state = api.RunStateFaulted
			s.logger.Error("HTTP server failed", zap.String("event", "http_server_failed"), zap.Error(err))
			beginDrain()
		case found := <-events.Source:
			events.Source = nil
			if found.Err != nil {
				var sourceError *pipeline.SourceError
				if errors.As(found.Err, &sourceError) {
					s.recordSourceFault(sourceError)
				}
				return shutdownDeadline, errors.Join(runErr, fmt.Errorf("start run: %w", found.Err))
			}
			if s.run.state != api.RunStateStarting {
				return shutdownDeadline, runErr
			}
			if appCtx.Err() != nil {
				stopOnSignal()
				return shutdownDeadline, runErr
			}
			options := s.pipelineOptions
			options.Source = found.Source
			s.run.runStartedAt = time.Now()
			run = pipeline.Start(runCtx, options)
			if events.PipelineStarted != nil {
				events.PipelineStarted(run)
			}
			pipelineDone = run.Done()
			sourceErrors = run.SourceErrors()
			s.run.state = api.RunStateRunning
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
				s.logger.Warn("pipeline drain timed out", zap.String("event", "pipeline_drain_timed_out"))
			}
			return shutdownDeadline, runErr
		case <-pipelineDone:
			return shutdownDeadline, runErr
		case request := <-s.queue:
			switch request := request.(type) {
			case snapshotRequest:
				request.reply <- s.snapshotAt(run, time.Now())
			case controlRequest:
				request.reply <- s.run.state == api.RunStateRunning &&
					s.applyCommand(run, request.command, events.RequestedTPSChanged)
			}
		case <-ticker.C:
			s.logger.Info("runtime summary", snapshotLogFields(s.snapshotAt(run, time.Now()), s.config)...)
		}
	}
}

// ValidCommand reports whether a command is within the immutable startup limits.
func (s *Controller) ValidCommand(command Command) bool {
	switch command.Action {
	case api.SetReaderWorkers:
		return s.config.Reader.Workers.Contains(command.Value)
	case api.SetSenderWorkers:
		return s.config.Sender.Workers.Contains(command.Value)
	case api.SetRequestedTPS:
		return s.config.Throttler.RequestedTPS.Contains(command.Value)
	case api.SetThrottlerInstalled:
		return true
	default:
		return false
	}
}

func (s *Controller) applyCommand(run *pipeline.Pipeline, command Command, requestedTPSChanged func(int)) bool {
	if !s.ValidCommand(command) {
		return false
	}
	switch command.Action {
	case api.SetReaderWorkers:
		s.controls.readerWorkers = command.Value
		run.SetReaders(command.Value)
	case api.SetSenderWorkers:
		s.controls.senderWorkers = command.Value
		run.SetSenders(command.Value)
	case api.SetRequestedTPS:
		if s.controls.requestedTPS != command.Value {
			s.controls.requestedTPS = command.Value
			if requestedTPSChanged != nil {
				requestedTPSChanged(command.Value)
			}
			run.SetRate(s.rate())
			s.logger.Info("throttler rate changed",
				zap.String("event", "throttler_rate_changed"), zap.Int("requested_tps", command.Value))
		}
	case api.SetThrottlerInstalled:
		if s.controls.installed != command.Installed {
			s.controls.installed = command.Installed
			run.SetRate(s.rate())
			s.logger.Info("throttler mode changed",
				zap.String("event", "throttler_mode_changed"), zap.Bool("installed", command.Installed))
		}
	default:
		return false
	}
	return true
}

func (s *Controller) recordSourceFault(err error) {
	var sourceError *pipeline.SourceError
	if !errors.As(err, &sourceError) {
		return
	}
	s.run.state = api.RunStateFaulted
	s.run.sourceError = &api.SourceError{Operation: sourceError.Op,
		Path: sourceError.Path, Message: sourceError.Error()}
	s.logger.Error("reader source failed", zap.String("event", "reader_source_failed"),
		zap.String("operation", sourceError.Op), zap.String("path", sourceError.Path), zap.Error(sourceError))
}

func (s *Controller) finishRun(run *pipeline.Pipeline, partial bool) {
	var confirmed, rowsRead int64
	if run != nil {
		confirmed = run.CompletedTotal()
		rowsRead = run.RowsRead()
	}
	s.logger.Info("run stopped", zap.String("event", "run_stopped"),
		zap.String("state", string(s.run.state)),
		zap.Bool("partial", partial),
		zap.Int64("confirmed_transactions", confirmed),
		zap.Int64("unconfirmed_transactions", rowsRead-confirmed),
	)
}

func (s *Controller) snapshotAt(run *pipeline.Pipeline, now time.Time) api.Snapshot {
	var snapshot pipeline.Snapshot
	if run != nil {
		snapshot = run.Snapshot(now)
	}
	readerPool, senderPool := snapshot.Reader, snapshot.Sender
	readerChannel, senderChannel := snapshot.ReaderQueue, snapshot.SenderQueue

	return api.Snapshot{
		CapturedAtMs: max(0, now.Sub(s.startedAt).Milliseconds()),
		Run: api.RunSnapshot{
			State:             s.run.state,
			TotalTransactions: snapshot.CompletedTransactions,
			ElapsedMs:         s.elapsedMs(now),
		},
		Reader: api.ReaderSnapshot{
			Workers: s.controls.readerWorkers, WorkerStates: snapshotReaderWorkers(readerPool.WorkerStates),
			RowsRead:    readerPool.RowsRead,
			SourceError: s.run.sourceError,
		},
		Throttler: api.ThrottlerSnapshot{
			RequestedTps: s.controls.requestedTPS,
			Installed:    s.controls.installed,
		},
		Sender: api.SenderSnapshot{
			Workers: s.controls.senderWorkers, WorkerStates: snapshotSenderWorkers(senderPool.WorkerStates),
		},
		ReaderChannel: snapshotChannel(readerChannel),
		SenderChannel: snapshotChannel(senderChannel),
	}
}

func (s *Controller) rate() pipeline.Rate {
	return pipeline.Rate{TPS: s.controls.requestedTPS, Installed: s.controls.installed}
}

func (s *Controller) elapsedMs(now time.Time) int64 {
	if s.run.runStartedAt.IsZero() {
		return 0
	}
	return max(0, now.Sub(s.run.runStartedAt).Milliseconds())
}

var errRuntimeUnavailable = errors.New("runtime unavailable")

// Command is a validated live setting update submitted to the controller.
type Command struct {
	// Action identifies the setting to update.
	Action string
	// Value carries a worker count or requested TPS.
	Value int
	// Installed carries the requested pacing mode.
	Installed bool
}

type runtimeRequest interface {
	isRuntimeRequest()
}

type snapshotRequest struct {
	reply chan<- api.Snapshot
}

type controlRequest struct {
	command Command
	reply   chan<- bool
}

func (snapshotRequest) isRuntimeRequest() {}
func (controlRequest) isRuntimeRequest()  {}

// Snapshot asks the event loop for a point-in-time view of the run.
func (s *Controller) Snapshot(ctx context.Context) (api.Snapshot, error) {
	reply := make(chan api.Snapshot, 1)
	return awaitRuntimeReply(ctx, s, snapshotRequest{reply: reply}, reply)
}

// Execute asks the event loop to apply a command and returns its receipt.
func (s *Controller) Execute(ctx context.Context, command Command) (bool, error) {
	reply := make(chan bool, 1)
	return awaitRuntimeReply(ctx, s, controlRequest{command: command, reply: reply}, reply)
}

func awaitRuntimeReply[T any](ctx context.Context, controller *Controller, request runtimeRequest, reply <-chan T) (T, error) {
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-controller.done:
		return zero, errRuntimeUnavailable
	case controller.queue <- request:
	}
	return receiveRuntimeReply(ctx, controller.done, reply)
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
