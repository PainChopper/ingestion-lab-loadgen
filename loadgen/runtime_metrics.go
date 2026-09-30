package main

import "time"

type runtimeMetrics struct {
	window      time.Duration
	metrics     <-chan time.Time
	promMetrics *Metrics
	ticker      *time.Ticker
}

func newRuntimeMetrics(policy policy) runtimeMetrics {
	window := time.Duration(policy.Metrics.WindowMS.Default) * time.Millisecond
	metrics := NewMetrics()
	metrics.targetTPS.Set(float64(policy.Throttler.RequestedTPS.Default))
	ticker := time.NewTicker(window)
	return runtimeMetrics{window: window, metrics: ticker.C, promMetrics: metrics, ticker: ticker}
}

func (m runtimeMetrics) stop() {
	m.ticker.Stop()
}
