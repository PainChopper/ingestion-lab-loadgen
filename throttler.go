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
type transactionThrottler struct {
	tps          atomic.Int32
	burstPercent atomic.Int32
	limiter      atomic.Pointer[rate.Limiter]
}

func NewTransactionsThrottler(tps, burstPercent int) *transactionThrottler {
	t := &transactionThrottler{}
	t.tps.Store(int32(tps))
	t.burstPercent.Store(int32(burstPercent))
	t.resetLimiter()
	return t
}

func (t *transactionThrottler) Throttle(trans <-chan *Transaction) <-chan *Transaction {
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

func (t *transactionThrottler) GetTPS() int {
	return int(t.tps.Load())
}

func (t *transactionThrottler) setTPS(tps int) {
	t.tps.Store(int32(tps))
	t.resetLimiter()
}

func (t *transactionThrottler) resetLimiter() {
	newLimiter := rate.NewLimiter(rate.Limit(t.tps.Load()), int(t.tps.Load()*t.burstPercent.Load()/100))
	t.limiter.Store(newLimiter)
}
