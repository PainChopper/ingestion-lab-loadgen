package main

import "time"

type metrics struct {
	ticks             <-chan time.Time
	prometheusMetrics *PrometheusMetrics
	ticker            *time.Ticker
}

func newMetrics(config config) metrics {
	window := time.Duration(config.Metrics.WindowMS.Initial) * time.Millisecond
	prometheusMetrics := NewPrometheusMetrics()
	prometheusMetrics.targetTPS.Set(float64(config.Throttler.RequestedTPS.Initial))
	ticker := time.NewTicker(window)
	return metrics{ticks: ticker.C, prometheusMetrics: prometheusMetrics, ticker: ticker}
}

func (m metrics) stop() {
	m.ticker.Stop()
}
