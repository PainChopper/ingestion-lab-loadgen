package main

import (
	"context"
	"time"
)

type throttlerSettings struct {
	requestedTPS int
	mode         string
	paused       bool
}

type throttlerUpdate struct {
	settings throttlerSettings
	applied  chan struct{}
}

func startThrottler(
	ctx context.Context,
	readerBatches <-chan []Transaction,
	readerChannel *readerChannelTelemetry,
	senderChannel *readerChannelTelemetry,
	senderChannelCapacity int,
	initial throttlerSettings,
) (<-chan []Transaction, <-chan struct{}, chan<- throttlerUpdate) {
	senderBatches := make(chan []Transaction, senderChannelCapacity)
	senderChannel.start(senderBatches, 0)
	done := make(chan struct{})
	updates := make(chan throttlerUpdate)
	go func() {
		defer close(done)
		defer close(senderBatches)
		settings := initial
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				settings = update.settings
				close(update.applied)
			case batch, ok := <-readerBatches:
				if !ok {
					return
				}
				readerChannel.recordReceive(len(batch))
				if !forwardThrottledBatch(ctx, senderBatches, senderChannel, batch, updates, &settings) {
					return
				}
			}
		}
	}()
	return senderBatches, done, updates
}

func forwardThrottledBatch(
	ctx context.Context,
	senderBatches chan<- []Transaction,
	senderChannel *readerChannelTelemetry,
	batch []Transaction,
	updates <-chan throttlerUpdate,
	settings *throttlerSettings,
) bool {
	waitStarted := time.Now()
	for {
		if settings.paused || (settings.mode == throttlerInstalled && settings.requestedTPS == 0) {
			select {
			case <-ctx.Done():
				return false
			case update := <-updates:
				*settings = update.settings
				waitStarted = time.Now()
				close(update.applied)
			}
			continue
		}

		if settings.mode == throttlerInstalled {
			interval := time.Duration(len(batch)) * time.Second / time.Duration(settings.requestedTPS)
			if remaining := interval - time.Since(waitStarted); remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-ctx.Done():
					timer.Stop()
					return false
				case update := <-updates:
					timer.Stop()
					*settings = update.settings
					waitStarted = time.Now()
					close(update.applied)
				case <-timer.C:
				}
				continue
			}
		}

		select {
		case <-ctx.Done():
			return false
		case update := <-updates:
			*settings = update.settings
			waitStarted = time.Now()
			close(update.applied)
			continue
		case senderBatches <- batch:
			senderChannel.recordSend(len(batch))
			return true
		default:
		}

		senderChannel.startBlocked(time.Now())
		select {
		case <-ctx.Done():
			senderChannel.finishBlocked(time.Now())
			return false
		case update := <-updates:
			senderChannel.finishBlocked(time.Now())
			*settings = update.settings
			waitStarted = time.Now()
			close(update.applied)
		case senderBatches <- batch:
			senderChannel.finishBlocked(time.Now())
			senderChannel.recordSend(len(batch))
			return true
		}
	}
}
