package main

import (
	"context"
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

func newControlState(cfg config, logger *zap.Logger) controlState {
	metricsWindow := time.Duration(cfg.Metrics.WindowMS.Initial) * time.Millisecond
	return controlState{
		metricsWindow: metricsWindow,
		run:           controlRunState{lifecycle: newLifecycle()},
		config:        cfg,
		controls: configuredControls{
			readBatchSize:         cfg.Reader.ReadBatchSize.Initial,
			readerWorkers:         cfg.Reader.Workers.Initial,
			readerChannelCapacity: cfg.ReaderChannel.Capacity.Initial,
			senderChannelCapacity: cfg.SenderChannel.Capacity.Initial,
			requestedTPS:          cfg.Throttler.RequestedTPS.Initial,
			installationMode:      cfg.Throttler.InstallationMode.Initial,
			senderWorkers:         cfg.Sender.Workers.Initial,
		},
		logger: logger,
	}
}

type controlRunState struct {
	totalTransactions int64
	elapsedBeforeRun  time.Duration
	runStartedAt      time.Time
	sourceError       *readerSourceError
	lifecycle         *lifecycle
}

type configuredControls struct {
	readBatchSize         int
	readerWorkers         int
	readerChannelCapacity int
	senderChannelCapacity int
	requestedTPS          int
	installationMode      string
	senderWorkers         int
}

type controlTelemetry struct {
	reader        readerTelemetry
	readerChannel channelTelemetry
	senderChannel channelTelemetry
}

func (state *controlState) startReaderPool(ctx context.Context, batches chan<- []Transaction, batchSize, workers int) (*readerPool, error) {
	return startReaderPool(
		ctx,
		state.config.Source.Path,
		batchSize,
		workers,
		batches,
		&state.telemetry.reader,
		&state.telemetry.readerChannel,
		state.logger,
	)
}
