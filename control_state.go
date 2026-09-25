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

type controlRunState struct {
	totalTransactions int64
	elapsedBeforeRun  time.Duration
	runStartedAt      time.Time
	sourceError       *readerSourceError
	lifecycle         *lifecycle
}

type configuredControls struct {
	configuredReadBatchSize         int
	configuredReaderWorkers         int
	readerWorkersConfigured         bool
	configuredReaderChannelCapacity int
	readerChannelCapacityConfigured bool
	configuredSenderChannelCapacity int
	senderChannelCapacityConfigured bool
	configuredRequestedTPS          int
	requestedTPSConfigured          bool
	configuredInstallationMode      string
	configuredSenderWorkers         int
	senderWorkersConfigured         bool
	policy                          policy
}

type controlTelemetry struct {
	reader        readerTelemetry
	readerChannel channelTelemetry
	senderChannel channelTelemetry
	sender        senderTelemetry
}

func (state *controlState) startReaderPool(ctx context.Context, batches chan<- []Transaction, batchSize, workers int) (readerRun, error) {
	pool, err := startReaderPool(ctx, state.controls.policy.Source.Path, batchSize, workers, batches, &state.telemetry.reader, &state.telemetry.readerChannel, state.logger)
	if err != nil {
		return readerRun{}, err
	}
	return readerRun{done: pool.done, reconcile: pool.reconcile, sourceErrors: pool.sourceErrors}, nil
}
