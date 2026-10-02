package main

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type controlState struct {
	metricsWindow time.Duration
	run           controlRunState
	controls      configuredControls
	telemetry     controlTelemetry
	logger        *zap.Logger
}

func newControlState(loadedConfig config, logger *zap.Logger) controlState {
	metricsWindow := time.Duration(loadedConfig.Metrics.WindowMS.Initial) * time.Millisecond
	return controlState{
		metricsWindow: metricsWindow,
		run:           controlRunState{lifecycle: newLifecycle()},
		controls: configuredControls{
			readBatchSize:         loadedConfig.Reader.ReadBatchSize.Initial,
			readerWorkers:         loadedConfig.Reader.Workers.Initial,
			readerChannelCapacity: loadedConfig.ReaderChannel.Capacity.Initial,
			senderChannelCapacity: loadedConfig.SenderChannel.Capacity.Initial,
			requestedTPS:          loadedConfig.Throttler.RequestedTPS.Initial,
			installationMode:      loadedConfig.Throttler.InstallationMode.Initial,
			senderWorkers:         loadedConfig.Sender.Workers.Initial,
			config:                loadedConfig,
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
	config                config
}

type controlTelemetry struct {
	reader        readerTelemetry
	readerChannel channelTelemetry
	senderChannel channelTelemetry
	sender        senderTelemetry
}

func (state *controlState) startReaderPool(ctx context.Context, batches chan<- []Transaction, batchSize, workers int) (readerRun, error) {
	pool, err := startReaderPool(ctx, state.controls.config.Source.Path, batchSize, workers, batches, &state.telemetry.reader, &state.telemetry.readerChannel, state.logger)
	if err != nil {
		return readerRun{}, err
	}
	return readerRun{done: pool.done, reconcile: pool.reconcile, aggregateSnapshot: pool.aggregateSnapshot, sourceErrors: pool.sourceErrors}, nil
}
