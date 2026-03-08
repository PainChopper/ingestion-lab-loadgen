package main

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

type throttlerParams struct {
	tps int
}
type transactionsThrottler struct {
	tps          atomic.Int32
	burstPercent atomic.Int32
	limiter      atomic.Pointer[rate.Limiter]
}

func NewTransactionsThrottler(tps, burstPercent int) *transactionsThrottler {
	t := &transactionsThrottler{}
	t.tps.Store(int32(tps))
	t.burstPercent.Store(int32(burstPercent))
	t.resetLimiter()
	return t
}

func (t *transactionsThrottler) Throttle(trans <-chan *Transaction) <-chan *Transaction {
	c := make(chan *Transaction, 1000)
	go func() {
		defer close(c)
		for tran := range trans {
			for t.tps.Load() == 0 {
				time.Sleep(100 * time.Millisecond)
			}
			t.limiter.Load().Wait(context.Background())
			c <- tran
		}
	}()
	return c
}

func (t *transactionsThrottler) GetTPS() int {
	return int(t.tps.Load())
}

func (t *transactionsThrottler) setTPS(tps int) {
	t.tps.Store(int32(tps))
	t.resetLimiter()
}

func (t *transactionsThrottler) resetLimiter() {
	newLimiter := rate.NewLimiter(rate.Limit(t.tps.Load()), int(t.tps.Load()*t.burstPercent.Load()/100))
	t.limiter.Store(newLimiter)
}
