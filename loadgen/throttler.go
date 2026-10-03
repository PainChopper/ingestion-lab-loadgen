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

func (runtime *pipelineRuntime) startThrottler(
	ctx context.Context,
	initial throttlerSettings,
) (<-chan struct{}, chan<- throttlerSettings) {
	done := make(chan struct{})
	updates := make(chan throttlerSettings)
	go func() {
		defer close(done)
		settings := initial
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				settings = update
			case batch, ok := <-runtime.readerBatches:
				if !ok {
					return
				}
				runtime.state.telemetry.readerChannel.recordReceive(len(batch))
				if !runtime.forwardThrottledBatch(
					ctx,
					batch,
					updates,
					&settings,
				) {
					return
				}
			}
		}
	}()
	return done, updates
}

func (runtime *pipelineRuntime) forwardThrottledBatch(
	ctx context.Context,
	batch []Transaction,
	updates <-chan throttlerSettings,
	settings *throttlerSettings,
) bool {
	senderChannel := &runtime.state.telemetry.senderChannel
	waitStarted := time.Now()
	for {
		if settings.paused || (settings.mode == throttlerInstalled && settings.requestedTPS == 0) {
			select {
			case <-ctx.Done():
				return false
			case update := <-updates:
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
				case update := <-updates:
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
		case update := <-updates:
			*settings = update
			waitStarted = time.Now()
			continue
		case runtime.senderBatches <- batch:
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
			*settings = update
			waitStarted = time.Now()
		case runtime.senderBatches <- batch:
			senderChannel.finishBlocked(time.Now())
			senderChannel.recordSend(len(batch))
			return true
		}
	}
}
