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
	*readerChannelTelemetry,
	*readerChannelTelemetry,
	int,
	throttlerSettings,
) (<-chan []Transaction, <-chan struct{}, chan<- throttlerUpdate)

func (state *controlState) eventLoop(
	requests <-chan request,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	produce func(context.Context, int, int) (<-chan []Transaction, <-chan struct{}, error),
) {
	state.eventLoopWithThrottler(requests, metrics, promMetrics, produce, startThrottler)
}

func (state *controlState) eventLoopWithThrottler(
	requests <-chan request,
	metrics <-chan time.Time,
	promMetrics *Metrics,
	produce func(context.Context, int, int) (<-chan []Transaction, <-chan struct{}, error),
	start throttlerStarter,
) {
	var consumedSinceTick atomic.Int64
	var batches <-chan []Transaction
	var senderBatches <-chan []Transaction
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
		batches = nil
		senderBatches = nil
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
					RunState:                                 state.lifecycle.currentState(),
					TotalTransactions:                        state.totalTransactions,
					ReaderWorkers:                            1,
					ReaderReadBatchSize:                      state.readBatchSize(),
					ThrottlerRequestedTPS:                    state.requestedTPS(),
					ThrottlerAdmittedTPS:                     senderChannel.inputTransactionsPerSecond,
					ThrottlerInstallationMode:                state.installationMode(),
					SenderWorkers:                            0,
					ElapsedMs:                                state.elapsedMs(time.Now()),
					StartError:                               state.startError,
					ReaderReadTPS:                            reader.readTPS,
					ReaderRowsRead:                           reader.rowsRead,
					ReaderSource:                             reader.source,
					ReaderChannelCapacity:                    readerChannel.capacity,
					ReaderChannelDepthBatches:                readerChannel.depthBatches,
					ReaderChannelBufferedTransactions:        readerChannel.bufferedTransactions,
					ReaderChannelBlockedSenders:              readerChannel.blockedSenders,
					ReaderChannelOldestBlockedSenderMs:       readerChannel.oldestBlockedSenderMs,
					ReaderChannelBlockedMs:                   readerChannel.blockedMs,
					ReaderChannelSentBatchesTotal:            readerChannel.sentBatchesTotal,
					ReaderChannelSentTransactionsTotal:       readerChannel.sentTransactionsTotal,
					ReaderChannelReceivedBatchesTotal:        readerChannel.receivedBatchesTotal,
					ReaderChannelReceivedTransactionsTotal:   readerChannel.receivedTransactionsTotal,
					ReaderChannelInputBatchesPerSecond:       readerChannel.inputBatchesPerSecond,
					ReaderChannelInputTransactionsPerSecond:  readerChannel.inputTransactionsPerSecond,
					ReaderChannelOutputBatchesPerSecond:      readerChannel.outputBatchesPerSecond,
					ReaderChannelOutputTransactionsPerSecond: readerChannel.outputTransactionsPerSecond,
					SenderChannelCapacity:                    senderChannel.capacity,
					SenderChannelDepthBatches:                senderChannel.depthBatches,
					SenderChannelBufferedTransactions:        senderChannel.bufferedTransactions,
					SenderChannelBlockedSenders:              senderChannel.blockedSenders,
					SenderChannelOldestBlockedSenderMs:       senderChannel.oldestBlockedSenderMs,
					SenderChannelBlockedMs:                   senderChannel.blockedMs,
					SenderChannelSentBatchesTotal:            senderChannel.sentBatchesTotal,
					SenderChannelSentTransactionsTotal:       senderChannel.sentTransactionsTotal,
					SenderChannelReceivedBatchesTotal:        senderChannel.receivedBatchesTotal,
					SenderChannelReceivedTransactionsTotal:   senderChannel.receivedTransactionsTotal,
					SenderChannelInputBatchesPerSecond:       senderChannel.inputBatchesPerSecond,
					SenderChannelInputTransactionsPerSecond:  senderChannel.inputTransactionsPerSecond,
					SenderChannelOutputBatchesPerSecond:      senderChannel.outputBatchesPerSecond,
					SenderChannelOutputTransactionsPerSecond: senderChannel.outputTransactionsPerSecond,
					Policy:                                   state.policy.snapshot(),
				}
				cmd.snapshotReply <- snapshot
			case cmdRun:
				resuming := state.lifecycle.currentState() == runStatePaused
				if state.lifecycle.currentState() == runStateIdle {
					state.reader.startInterval(time.Now())
					producerContext, cancel := context.WithCancel(context.Background())
					producedBatches, done, err := produce(
						producerContext,
						state.readBatchSize(),
						state.readerChannelCapacity(),
					)
					if err != nil {
						cancel()
						state.reader.reset()
						log.Printf("cannot start load generator: %v", err)
						message := err.Error()
						state.startError = &message
						if cmd.commandReply != nil {
							cmd.commandReply <- commandResult{err: err}
						}
						continue
					}
					batches = producedBatches
					producerDone = done
					cancelProducer = cancel
					throttlerContext, cancel := context.WithCancel(context.Background())
					cancelThrottler = cancel
					senderBatches, throttlerDone, throttlerUpdates = start(
						throttlerContext,
						batches,
						&state.readerChannel,
						&state.senderChannel,
						state.senderChannelCapacity(),
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
					batches = nil
					senderBatches = nil
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
	state.readerChannel.reset()
	state.senderChannel.reset()
	consumedSinceTick.Store(0)
	state.totalTransactions = 0
	state.actualTPS = 0
	state.elapsedBeforeRun = 0
	state.runStartedAt = time.Time{}
	state.startError = nil
	promMetrics.actualTPS.Set(0)
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
