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
		state.readerChannel.detach()
		state.senderChannel.detach()
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
				reader := state.reader.snapshot()
				readerChannel := state.readerChannel.snapshot(time.Now())
				senderChannel := state.senderChannel.snapshot(time.Now())
				if state.lifecycle.currentState() == runStateIdle {
					readerChannel.capacity = state.readerChannelCapacity()
					senderChannel.capacity = state.senderChannelCapacity()
				}
				snapshot := statusSnapshot{
					Run: runSnapshot{
						State:             state.lifecycle.currentState(),
						TotalTransactions: state.totalTransactions,
						ElapsedMs:         state.elapsedMs(time.Now()),
						StartError:        state.startError,
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
					Policy: state.policy.snapshot(),
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				resuming := state.lifecycle.currentState() == runStatePaused
				if state.lifecycle.currentState() == runStateIdle {
					state.reader.startInterval(time.Now())
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
							state.readerChannel.detach()
						}
						if senderCreated {
							closeAndDrain(senderBatches)
							senderBatches = nil
							state.senderChannel.detach()
						}
						state.reader.reset()
						log.Printf("cannot start load generator: %v", err)
						message := err.Error()
						state.startError = &message
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
						&state.readerChannel,
						&state.senderChannel,
						state.throttlerSettings(false),
					)
				}
				if state.lifecycle.run() {
					state.runStartedAt = time.Now()
					state.startError = nil
					if resuming {
						state.notifyThrottler(throttlerUpdates, throttlerDone, false)
					}
					cancelConsumer, consumerDone = startConsumer(senderBatches, &state.senderChannel, &consumedSinceTick)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- commandResult{}
				}
			case cmdPause:
				if state.lifecycle.currentState() != runStateRunning {
					continue
				}
				cancelConsumer()
				<-consumerDone
				state.pauseElapsed(time.Now())
				cancelConsumer = nil
				consumerDone = nil
				delta := consumedSinceTick.Swap(0)
				state.totalTransactions += delta
				promMetrics.transactionsTotal.Add(float64(delta))
				state.actualTPS = 0
				promMetrics.actualTPS.Set(0)
				state.lifecycle.pause()
				state.notifyThrottler(throttlerUpdates, throttlerDone, true)
			case cmdReset:
				result := commandResult{status: commandAccepted}
				switch state.lifecycle.currentState() {
				case runStatePaused:
					state.lifecycle.reset()
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
					state.lifecycle.completeReset()
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
				if state.lifecycle.currentState() != runStateIdle {
					result.status = commandConflict
				} else if !validReadBatchSize(state.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.configuredReadBatchSize = cmd.value
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetReaderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.lifecycle.currentState() != runStateIdle || !validReaderChannelCapacity(state.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.configuredReaderChannelCapacity = cmd.value
					state.readerChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetSenderChannelCapacity:
				result := commandResult{status: commandAccepted}
				if state.lifecycle.currentState() != runStateIdle || !validSenderChannelCapacity(state.policy, cmd.value) {
					result.status = commandConflict
				} else {
					state.configuredSenderChannelCapacity = cmd.value
					state.senderChannelCapacityConfigured = true
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetRequestedTPS:
				result := commandResult{status: commandAccepted}
				if !state.policy.Throttler.RequestedTPS.contains(cmd.value) {
					result.status = commandConflict
				} else if state.requestedTPS() != cmd.value {
					state.configuredRequestedTPS = cmd.value
					state.requestedTPSConfigured = true
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.lifecycle.currentState() == runStatePaused)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			case cmdSetThrottlerInstallationMode:
				result := commandResult{status: commandAccepted}
				if !state.policy.Throttler.InstallationMode.contains(cmd.textValue) {
					result.status = commandConflict
				} else if state.installationMode() != cmd.textValue {
					state.configuredInstallationMode = cmd.textValue
					state.notifyThrottler(throttlerUpdates, throttlerDone, state.lifecycle.currentState() == runStatePaused)
				}
				if cmd.commandReply != nil {
					cmd.commandReply <- result
				}
			}

		case <-metrics:
			state.reader.sample(time.Now())
			state.readerChannel.sample(windowLength)
			state.senderChannel.sample(windowLength)
			delta := consumedSinceTick.Swap(0)
			state.actualTPS = delta * int64(time.Second/windowLength)
			state.totalTransactions += delta
			promMetrics.actualTPS.Set(float64(state.actualTPS))
			promMetrics.transactionsTotal.Add(float64(delta))
		}
	}
}

func (state *controlState) requestedTPS() int {
	if state.requestedTPSConfigured {
		return state.configuredRequestedTPS
	}
	return state.policy.Throttler.RequestedTPS.Default
}

func (state *controlState) installationMode() string {
	if state.configuredInstallationMode != "" {
		return state.configuredInstallationMode
	}
	return state.policy.Throttler.InstallationMode.Default
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
	state.reader.reset()
	state.readerChannel.clearMeasurements()
	state.senderChannel.clearMeasurements()
	consumedSinceTick.Store(0)
	state.totalTransactions = 0
	state.actualTPS = 0
	state.elapsedBeforeRun = 0
	state.runStartedAt = time.Time{}
	state.startError = nil
	promMetrics.actualTPS.Set(0)
}

func (state *controlState) prepareReaderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.readerChannelCapacity()
	if batches != nil && cap(batches) == capacity {
		return batches, false
	}
	closeAndDrain(batches)
	state.readerChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.readerChannel.start(batches, state.readBatchSize())
	state.readerChannel.clearMeasurements()
	return batches, true
}

func (state *controlState) prepareSenderChannel(batches chan []Transaction) (chan []Transaction, bool) {
	capacity := state.senderChannelCapacity()
	batchSize := state.readBatchSize()
	if batches != nil && cap(batches) == capacity {
		state.senderChannel.start(batches, batchSize)
		return batches, false
	}
	closeAndDrain(batches)
	state.senderChannel.detach()
	batches = make(chan []Transaction, capacity)
	state.senderChannel.start(batches, batchSize)
	state.senderChannel.clearMeasurements()
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
	elapsed := state.elapsedBeforeRun
	if state.lifecycle.currentState() == runStateRunning {
		elapsed += now.Sub(state.runStartedAt)
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}

func (state *controlState) pauseElapsed(now time.Time) {
	state.elapsedBeforeRun += now.Sub(state.runStartedAt)
	state.runStartedAt = time.Time{}
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
