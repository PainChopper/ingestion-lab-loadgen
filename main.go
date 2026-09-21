package main

import (
	"context"
	"log"
	"os"
	"runtime"
	"time"
)

var blackHole uint64

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
	configuredReaderChannelCapacity int
	readerChannelCapacityConfigured bool
	configuredSenderChannelCapacity int
	senderChannelCapacityConfigured bool
	configuredRequestedTPS          int
	requestedTPSConfigured          bool
	configuredInstallationMode      string
	policy                          policy
}

type controlTelemetry struct {
	reader        readerTelemetry
	readerChannel channelTelemetry
	senderChannel channelTelemetry
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
		func(ctx context.Context, batches chan<- []Transaction, batchSize int) (<-chan struct{}, error) {
			return produceBatches(ctx, loadedPolicy.Source.Path, batchSize, batches, &state.telemetry.reader, &state.telemetry.readerChannel)
		},
	)
}

func consumeTransaction(tran *Transaction) {
	blackHole = uint64(tran.Fold)
	blackHole ^= uint64(tran.EventType) * 1099511628211
	blackHole ^= uint64(tran.EventSubtype) * 1469598103934665603
	blackHole ^= uint64(tran.Currency) * 7809847782465536322
	blackHole ^= uint64(len(tran.ClientID)) << 32
	blackHole ^= uint64(tran.Amount)
	runtime.KeepAlive(blackHole)
}
