package main

import (
	"context"
	"time"
)

type throttlerSettings struct {
	requestedTPS int
	installed    bool
}

func (s throttlerSettings) interval(batchSize int) time.Duration {
	return time.Duration(batchSize) * time.Second / time.Duration(s.requestedTPS)
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
		var anchor time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-t.updates:
				if update.installed && update.requestedTPS == 0 {
					anchor = time.Time{}
				}
				settings = update
			case batch, ok := <-t.readerBatches:
				if !ok {
					return
				}
				t.readerChannel.recordReceive(len(batch))
				nextAnchor, ok := t.forwardBatch(ctx, batch, &settings, anchor)
				if !ok {
					return
				}
				anchor = nextAnchor
			}
		}
	}()
}

func (t *throttler) forwardBatch(
	ctx context.Context,
	batch []Transaction,
	settings *throttlerSettings,
	anchor time.Time,
) (time.Time, bool) {
	senderChannel := t.senderChannel
	slotStart := time.Now()
	outputBlocked := false
	if settings.installed && settings.requestedTPS > 0 && !anchor.IsZero() {
		// Carry timer overshoot forward, capped at one interval after idle input.
		slotStart = slotStart.Add(-settings.interval(len(batch)))
		if anchor.After(slotStart) {
			slotStart = anchor
		}
	}
	for {
		if settings.installed && settings.requestedTPS == 0 {
			select {
			case <-ctx.Done():
				return time.Time{}, false
			case update := <-t.updates:
				*settings = update
				slotStart = time.Now()
			}
			continue
		}

		var deadline time.Time
		if settings.installed {
			deadline = slotStart.Add(settings.interval(len(batch)))
			if remaining := time.Until(deadline); remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-ctx.Done():
					timer.Stop()
					return time.Time{}, false
				case update := <-t.updates:
					timer.Stop()
					*settings = update
				case <-timer.C:
				}
				continue
			}
		}

		select {
		case <-ctx.Done():
			return time.Time{}, false
		case update := <-t.updates:
			*settings = update
			continue
		case t.senderBatches <- batch:
			if !settings.installed || outputBlocked {
				deadline = time.Now()
			}
			senderChannel.recordSend(len(batch))
			return deadline, true
		default:
		}

		outputBlocked = true
		blockedID := senderChannel.startBlocked(time.Now())
		select {
		case <-ctx.Done():
			senderChannel.finishBlockedWriter(blockedID, time.Now())
			return time.Time{}, false
		case update := <-t.updates:
			senderChannel.finishBlockedWriter(blockedID, time.Now())
			*settings = update
		case t.senderBatches <- batch:
			sentAt := time.Now()
			senderChannel.finishBlockedWriter(blockedID, sentAt)
			senderChannel.recordSend(len(batch))
			return sentAt, true
		}
	}
}

func (t *throttler) update(settings throttlerSettings) {
	select {
	case t.updates <- settings:
	case <-t.done:
	}
}
