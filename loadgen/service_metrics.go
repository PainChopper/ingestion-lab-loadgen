package main

import "time"

type serviceMetrics struct {
	ticks             <-chan time.Time
	prometheusMetrics *PrometheusMetrics
	ticker            *time.Ticker
}

func newServiceMetrics(config config) serviceMetrics {
	window := time.Duration(config.Metrics.WindowMS.Initial) * time.Millisecond
	prometheusMetrics := NewPrometheusMetrics()
	prometheusMetrics.targetTPS.Set(float64(config.Throttler.RequestedTPS.Initial))
	ticker := time.NewTicker(window)
	return serviceMetrics{ticks: ticker.C, prometheusMetrics: prometheusMetrics, ticker: ticker}
}

func (m serviceMetrics) stop() {
	m.ticker.Stop()
}
