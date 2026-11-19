package pipeline

import (
	"context"
	"time"
)

type Rate struct {
	TPS       int
	Installed bool
}

func (r Rate) interval(batchSize int) time.Duration {
	return time.Duration(batchSize) * time.Second / time.Duration(r.TPS)
}

type throttler struct {
	input   *batchQueue
	output  *batchQueue
	updates chan Rate
	done    chan struct{}
}

func startThrottler(ctx context.Context, input, output *batchQueue, initial Rate) *throttler {
	t := &throttler{input: input, output: output, done: make(chan struct{}), updates: make(chan Rate)}
	go func() {
		defer close(t.done)
		settings := initial
		var anchor time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-t.updates:
				if update.Installed && update.TPS == 0 {
					anchor = time.Time{}
				}
				settings = update
			case batch, ok := <-t.input.ch:
				if !ok {
					return
				}
				t.input.recordReceive(len(batch))
				nextAnchor, ok := t.forwardBatch(ctx, batch, &settings, anchor)
				if !ok {
					return
				}
				anchor = nextAnchor
			}
		}
	}()
	return t
}

func (t *throttler) forwardBatch(
	ctx context.Context,
	batch []Transaction,
	settings *Rate,
	anchor time.Time,
) (time.Time, bool) {
	slotStart := time.Now()
	outputBlocked := false
	if settings.Installed && settings.TPS > 0 && !anchor.IsZero() {
		// Carry timer overshoot forward, capped at one interval after idle input.
		slotStart = slotStart.Add(-settings.interval(len(batch)))
		if anchor.After(slotStart) {
			slotStart = anchor
		}
	}
	for {
		if settings.Installed && settings.TPS == 0 {
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
		if settings.Installed {
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
		case t.output.ch <- batch:
			if !settings.Installed || outputBlocked {
				deadline = time.Now()
			}
			t.output.recordSend(len(batch))
			return deadline, true
		default:
		}

		outputBlocked = true
		blockedID := t.output.startBlocked(time.Now())
		select {
		case <-ctx.Done():
			t.output.finishBlockedWriter(blockedID, time.Now())
			return time.Time{}, false
		case update := <-t.updates:
			t.output.finishBlockedWriter(blockedID, time.Now())
			*settings = update
		case t.output.ch <- batch:
			sentAt := time.Now()
			t.output.finishBlockedWriter(blockedID, sentAt)
			t.output.recordSend(len(batch))
			return sentAt, true
		}
	}
}

func (t *throttler) update(settings Rate) {
	select {
	case t.updates <- settings:
	case <-t.done:
	}
}
