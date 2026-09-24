package main

import (
	"context"
	"log"
	"os"
	"time"
)

type controlState struct {
	metricsWindow time.Duration
	run           controlRunState
	controls      configuredControls
	telemetry     controlTelemetry
}

type controlRunState struct {
	totalTransactions int64
	elapsedBeforeRun  time.Duration
	runStartedAt      time.Time
	startError        *string
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

func main() {
	configArgument := ""
	if len(os.Args) > 2 {
		log.Fatal("usage: loadgen [config-path]")
	}
	if len(os.Args) == 2 {
		configArgument = os.Args[1]
	}
	loadedPolicy, _, err := loadPolicy(configArgument)
	if err != nil {
		log.Fatal(err)
	}
	metricsWindow := time.Duration(loadedPolicy.Metrics.WindowMS.Default) * time.Millisecond
	state := controlState{
		metricsWindow: metricsWindow,
		run:           controlRunState{lifecycle: newLifecycle()},
		controls:      configuredControls{policy: loadedPolicy},
	}

	requests := make(chan request, 10)

	metricsTicker := time.NewTicker(metricsWindow)
	defer metricsTicker.Stop()
	metrics := metricsTicker.C
	promMetrics := NewMetrics()
	promMetrics.targetTPS.Set(float64(loadedPolicy.Throttler.RequestedTPS.Default))

	startHttpServer(requests, promMetrics, loadedPolicy)

	state.eventLoop(
		requests,
		metrics,
		promMetrics,
		func(ctx context.Context, batches chan<- []Transaction, batchSize, workers int) (readerRun, error) {
			pool, err := startReaderPool(ctx, loadedPolicy.Source.Path, batchSize, workers, batches, &state.telemetry.reader, &state.telemetry.readerChannel)
			if err != nil {
				return readerRun{}, err
			}
			return readerRun{done: pool.done, reconcile: pool.reconcile}, nil
		},
	)
}
