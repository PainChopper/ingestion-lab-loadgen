package main

import "time"

type runtimeMetrics struct {
	window      time.Duration
	metrics     <-chan time.Time
	promMetrics *Metrics
	ticker      *time.Ticker
}

func newRuntimeMetrics(config config) runtimeMetrics {
	window := time.Duration(config.Metrics.WindowMS.Default) * time.Millisecond
	metrics := NewMetrics()
	metrics.targetTPS.Set(float64(config.Throttler.RequestedTPS.Default))
	ticker := time.NewTicker(window)
	return runtimeMetrics{window: window, metrics: ticker.C, promMetrics: metrics, ticker: ticker}
}

func (m runtimeMetrics) stop() {
	m.ticker.Stop()
}
