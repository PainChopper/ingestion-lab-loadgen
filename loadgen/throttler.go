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

type throttler struct {
	readerBatches <-chan []Transaction
	senderBatches chan<- []Transaction
	readerChannel *channelTelemetry
	senderChannel *channelTelemetry
	updates       chan throttlerSettings
	done          chan struct{}
}

func (t *throttler) start(ctx context.Context, initial throttlerSettings) {
	t.done = make(chan struct{})
	t.updates = make(chan throttlerSettings)
	go func() {
		defer close(t.done)
		settings := initial
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-t.updates:
				settings = update
			case batch, ok := <-t.readerBatches:
				if !ok {
					return
				}
				t.readerChannel.recordReceive(len(batch))
				if !t.forwardBatch(ctx, batch, &settings) {
					return
				}
			}
		}
	}()
}

func (t *throttler) forwardBatch(
	ctx context.Context,
	batch []Transaction,
	settings *throttlerSettings,
) bool {
	senderChannel := t.senderChannel
	waitStarted := time.Now()
	for {
		if settings.paused || (settings.mode == throttlerInstalled && settings.requestedTPS == 0) {
			select {
			case <-ctx.Done():
				return false
			case update := <-t.updates:
				*settings = update
				waitStarted = time.Now()
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
				case update := <-t.updates:
					timer.Stop()
					*settings = update
					waitStarted = time.Now()
				case <-timer.C:
				}
				continue
			}
		}

		select {
		case <-ctx.Done():
			return false
		case update := <-t.updates:
			*settings = update
			waitStarted = time.Now()
			continue
		case t.senderBatches <- batch:
			senderChannel.recordSend(len(batch))
			return true
		default:
		}

		senderChannel.startBlocked(time.Now())
		select {
		case <-ctx.Done():
			senderChannel.finishBlocked(time.Now())
			return false
		case update := <-t.updates:
			senderChannel.finishBlocked(time.Now())
			*settings = update
			waitStarted = time.Now()
		case t.senderBatches <- batch:
			senderChannel.finishBlocked(time.Now())
			senderChannel.recordSend(len(batch))
			return true
		}
	}
}

func (t *throttler) update(settings throttlerSettings) {
	if t == nil {
		return
	}
	select {
	case t.updates <- settings:
	case <-t.done:
	}
}
