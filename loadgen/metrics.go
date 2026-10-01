package main

import "time"

type metrics struct {
	window            time.Duration
	ticks             <-chan time.Time
	prometheusMetrics *PrometheusMetrics
	ticker            *time.Ticker
}

func newMetrics(config config) metrics {
	window := time.Duration(config.Metrics.WindowMS.Default) * time.Millisecond
	prometheusMetrics := NewPrometheusMetrics()
	prometheusMetrics.targetTPS.Set(float64(config.Throttler.RequestedTPS.Default))
	ticker := time.NewTicker(window)
	return metrics{window: window, ticks: ticker.C, prometheusMetrics: prometheusMetrics, ticker: ticker}
}

func (m metrics) stop() {
	m.ticker.Stop()
}
