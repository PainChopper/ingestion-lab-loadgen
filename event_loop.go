package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

type throttlerStarter func(
	context.Context,
	<-chan []Transaction,
	chan<- []Transaction,
	*readerChannelTelemetry,
	*readerChannelTelemetry,
	throttlerSettings,
) (<-chan struct{}, chan<- throttlerUpdate)

type producerStarter func(context.Context, chan<- []Transaction, int) (<-chan struct{}, error)

func (state *controlState) eventLoop(
	requests <-chan request,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	produce producerStarter,
) {
	state.eventLoopWithThrottler(requests, metrics, promMetrics, produce, startThrottler)
}

func (state *controlState) eventLoopWithThrottler(
	requests <-chan request,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	produce producerStarter,
	start throttlerStarter,
) {
	var consumedSinceTick atomic.Int64
	var batches chan []Transaction
	var senderBatches chan []Transaction
	var cancelConsumer context.CancelFunc
	var consumerDone <-chan struct{}
	var cancelThrottler context.CancelFunc
	var throttlerDone <-chan struct{}
	var throttlerUpdates chan<- throttlerUpdate
	var cancelProducer context.CancelFunc
	var producerDone <-chan struct{}
	defer func() {
		if cancelConsumer != nil {
			cancelConsumer()
			<-consumerDone
		}
		if cancelProducer != nil {
			cancelProducer()
		}
		if cancelThrottler != nil {
			cancelThrottler()
		}
		if producerDone != nil {
			<-producerDone
		}
		if throttlerDone != nil {
			<-throttlerDone
		}
		closeAndDrain(batches)
		closeAndDrain(senderBatches)
		batches = nil
		senderBatches = nil
		state.telemetry.readerChannel.detach()
		state.telemetry.senderChannel.detach()
		cancelConsumer = nil
		consumerDone = nil
		cancelProducer = nil
		producerDone = nil
		cancelThrottler = nil
		throttlerDone = nil
		throttlerUpdates = nil
	}()

	for {
		select {
		case <-consumerDone:
			return
		case cmd, ok := <-requests:
			if !ok {
				return
			}
			switch cmd.kind {
			case getSnapshot:
				reader := state.telemetry.reader.snapshot()
				readerChannel := state.telemetry.readerChannel.snapshot(time.Now())
				senderChannel := state.telemetry.senderChannel.snapshot(time.Now())
				if state.run.lifecycle.currentState() == runStateIdle {
					readerChannel.capacity = state.readerChannelCapacity()
					senderChannel.capacity = state.senderChannelCapacity()
				}
				snapshot := statusSnapshot{
					Run: runSnapshot{
						State:             state.run.lifecycle.currentState(),
						TotalTransactions: state.run.totalTransactions,
						ElapsedMs:         state.elapsedMs(time.Now()),
						StartError:        state.run.startError,
					},
					Reader: readerSnapshot{
						Workers:       1,
						ReadBatchSize: state.readBatchSize(),
						ReadTps:       reader.readTPS,
						RowsRead:      reader.rowsRead,
						Source:        reader.source,
					},
					Throttler: throttlerSnapshot{
						RequestedTps:     state.requestedTPS(),
						AdmittedTps:      senderChannel.inputTransactionsPerSecond,
						InstallationMode: state.installationMode(),
					},
					Sender: senderSnapshot{Workers: 0},
					ReaderChannel: channelSnapshot{
						Capacity:                    readerChannel.capacity,
						DepthBatches:                readerChannel.depthBatches,
						BufferedTransactions:        readerChannel.bufferedTransactions,
						BlockedSenders:              readerChannel.blockedSenders,
						OldestBlockedSenderMs:       readerChannel.oldestBlockedSenderMs,
						BlockedMs:                   readerChannel.blockedMs,
						SentBatchesTotal:            readerChannel.sentBatchesTotal,
						SentTransactionsTotal:       readerChannel.sentTransactionsTotal,
						ReceivedBatchesTotal:        readerChannel.receivedBatchesTotal,
						ReceivedTransactionsTotal:   readerChannel.receivedTransactionsTotal,
						InputBatchesPerSecond:       readerChannel.inputBatchesPerSecond,
						InputTransactionsPerSecond:  readerChannel.inputTransactionsPerSecond,
						OutputBatchesPerSecond:      readerChannel.outputBatchesPerSecond,
						OutputTransactionsPerSecond: readerChannel.outputTransactionsPerSecond,
					},
					SenderChannel: channelSnapshot{
						Capacity:                    senderChannel.capacity,
						DepthBatches:                senderChannel.depthBatches,
						BufferedTransactions:        senderChannel.bufferedTransactions,
						BlockedSenders:              senderChannel.blockedSenders,
						OldestBlockedSenderMs:       senderChannel.oldestBlockedSenderMs,
						BlockedMs:                   senderChannel.blockedMs,
						SentBatchesTotal:            senderChannel.sentBatchesTotal,
						SentTransactionsTotal:       senderChannel.sentTransactionsTotal,
						ReceivedBatchesTotal:        senderChannel.receivedBatchesTotal,
						ReceivedTransactionsTotal:   senderChannel.receivedTransactionsTotal,
						InputBatchesPerSecond:       senderChannel.inputBatchesPerSecond,
						InputTransactionsPerSecond:  senderChannel.inputTransactionsPerSecond,
						OutputBatchesPerSecond:      senderChannel.outputBatchesPerSecond,
						OutputTransactionsPerSecond: senderChannel.outputTransactionsPerSecond,
					},
					Policy: state.controls.policy.snapshot(),
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				resuming := state.run.lifecycle.currentState() == runStatePaused
				if state.run.lifecycle.currentState() == runStateIdle {
					state.telemetry.reader.startInterval(time.Now())
					var readerCreated bool
					batches, readerCreated = state.prepareReaderChannel(batches)
					var senderCreated bool
					senderBatches, senderCreated = state.prepareSenderChannel(senderBatches)
					producerContext, cancel := context.WithCancel(context.Background())
					done, err := produce(
						producerContext,
						batches,
						state.readBatchSize(),
					)
					if err != nil {
						cancel()
						if readerCreated {
							closeAndDrain(batches)
							batches = nil
							state.telemetry.readerChannel.detach()
						}
						if senderCreated {
							closeAndDrain(senderBatches)
							senderBatches = nil
							state.telemetry.senderChannel.detach()
						}
						state.telemetry.reader.reset()
						log.Printf("cannot start load generator: %v", err)
						message := err.Error()
						state.run.startError = &message
						if cmd.commandReply != nil {
							cmd.commandReply <- commandResult{err: err}
						}
						continue
					}
					producerDone = done
					cancelProducer = cancel
					throttlerContext, cancel := context.WithCancel(context.Background())
					cancelThrottler = cancel
					throttlerDone, throttlerUpdates = start(
						throttlerContext,
						batches,
						senderBatches,
						&state.telemetry.readerChannel,
						&state.telemetry.senderChannel,
						state.throttlerSettings(false),
					)
				}
				if state.run.lifecycle.run() {
					state.run.runStartedAt = time.Now()
					state.run.startError = nil
					if resuming {
						state.notifyThrottler(throttlerUpdates, throttlerDone, false)
					}
					cancelConsumer, consumerDone = startConsumer(senderBatches, &state.telemetry.senderChannel, &consumedSinceTick)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			case cmdPause:
				if state.run.lifecycle.currentState() != runStateRunning {
					continue
				}
				cancelConsumer()
				<-consumerDone
				state.pauseElapsed(time.Now())
				cancelConsumer = nil
				consumerDone = nil
				delta := consumedSinceTick.Swap(0)
				state.run.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				promMetrics.actualTPS.Set(0)
				state.run.lifecycle.pause()
				state.notifyThrottler(throttlerUpdates, throttlerDone, true)
			case cmdReset:
				result := commandResult{status: commandAccepted}
				switch state.run.lifecycle.currentState() {
				case runStatePaused:
					state.run.lifecycle.reset()
					cancelProducer()
					cancelThrottler()
					<-producerDone
					<-throttlerDone
					drain(batches)
					drain(senderBatches)
					cancelProducer = nil
					producerDone = nil
					cancelThrottler = nil
					throttlerDone = nil
					throttlerUpdates = nil
					state.resetProgress(&consumedSinceTick, promMetrics)
					state.run.lifecycle.completeReset()
				case runStateIdle:
					state.resetProgress(&consumedSinceTick, promMetrics)
				default:
					result.status = commandConflict
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReadBatchSize:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle {
					result.status = commandConflict
				} else if !validReadBatchSize(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredReadBatchSize = cmd.value
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReaderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle || !validReaderChannelCapacity(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredReaderChannelCapacity = cmd.value
					state.controls.readerChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetSenderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.run.lifecycle.currentState() != runStateIdle || !validSenderChannelCapacity(state.controls.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.controls.configuredSenderChannelCapacity = cmd.value
					state.controls.senderChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetRequestedTPS:
				result := commandResult{status: commandAccepted}
				if !state.controls.policy.Throttler.RequestedTPS.contains(cmd.value) {
					result.status = commandConflict
				} else if state.requestedTPS() != cmd.value {
					state.controls.configuredRequestedTPS = cmd.value
					state.controls.requestedTPSConfigured = true
					promMetrics.targetTPS.Set(float64(state.requestedTPS()))
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.run.lifecycle.currentState() == runStatePaused)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetThrottlerInstallationMode:
				result := commandResult{status: commandAccepted}
				if !state.controls.policy.Throttler.InstallationMode.contains(cmd.textValue) {
					result.status = commandConflict
				} else if state.installationMode() != cmd.textValue {
					state.controls.configuredInstallationMode = cmd.textValue
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.run.lifecycle.currentState() == runStatePaused)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			}

		case <-metrics:
			state.telemetry.reader.sample(time.Now())
			state.telemetry.readerChannel.sample(state.metricsWindow)
			state.telemetry.senderChannel.sample(state.metricsWindow)
			delta := consumedSinceTick.Swap(0)
			state.run.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(delta) / state.metricsWindow.Seconds())
			promMetrics.transactionsTotal.Add(float64(delta))
		}
	}
}

func (state *controlState) requestedTPS() int {
	if state.controls.requestedTPSConfigured {
		return state.controls.configuredRequestedTPS
	}
	return state.controls.policy.Throttler.RequestedTPS.Default
}

func (state *controlState) installationMode() string {
	if state.controls.configuredInstallationMode != "" {
		return state.controls.configuredInstallationMode
	}
	return state.controls.policy.Throttler.InstallationMode.Default
}

func (state *controlState) throttlerSettings(paused bool) throttlerSettings {
	return throttlerSettings{
		requestedTPS: state.requestedTPS(),
		mode:         state.installationMode(),
		paused:       paused,
	}
}

func (state *controlState) notifyThrottler(
	updates chan<- throttlerUpdate,
	done <-chan struct{},
	paused bool,
) {
	if updates == nil {
		return
	}
	update := throttlerUpdate{
		settings: state.throttlerSettings(paused),
		applied:  make(chan struct{}),
	}
	select {
	case updates <- update:
		<-update.applied
	case <-done:
	}
}

func (state *controlState) resetProgress(consumedSinceTick *atomic.Int64, promMetrics *Metrics) {
	state.telemetry.reader.reset()
	state.telemetry.readerChannel.clearMeasurements()
	state.telemetry.senderChannel.clearMeasurements()
	consumedSinceTick.Store(0)
	state.run.totalTransactions = 0
	state.run.elapsedBeforeRun = 0
	state.run.runStartedAt = time.Time{}
	state.run.startError = nil
	promMetrics.actualTPS.Set(0)
}

func (state *controlState) prepareReaderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.readerChannelCapacity()
	if batches != nil && cap(batches) == capacity {
		return batches, false
	}
	closeAndDrain(batches)
	state.telemetry.readerChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.telemetry.readerChannel.start(batches, state.readBatchSize())
	state.telemetry.readerChannel.clearMeasurements()
	return batches, true
}

func (state *controlState) prepareSenderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.senderChannelCapacity()
	batchSize := state.readBatchSize()
	if batches != nil && cap(batches) == capacity {
		state.telemetry.senderChannel.start(batches, batchSize)
		return batches, false
	}
	closeAndDrain(batches)
	state.telemetry.senderChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.telemetry.senderChannel.start(batches, batchSize)
	state.telemetry.senderChannel.clearMeasurements()
	return batches, true
}

func closeAndDrain(batches chan []Transaction) {
	if batches == nil {
		return
	}
	close(batches)
	drain(batches)
}

func drain(batches chan []Transaction) {
	if batches == nil {
		return
	}
	for {
		select {
		case _, ok := <-batches:
			if !ok {
				return
			}
		default:
			return
		}
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

func startConsumer(
	batches <-chan []Transaction,
	senderChannel *readerChannelTelemetry,
	consumedSinceTick *atomic.Int64,
) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeBatches(ctx, batches, senderChannel, consumedSinceTick)
	}()
	return cancel, done
}
