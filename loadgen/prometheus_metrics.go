package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

type PrometheusMetrics struct {
	registry *prometheus.Registry

	targetTPS prometheus.Gauge
	actualTPS prometheus.Gauge

	transactionsTotal prometheus.Counter
}

func NewPrometheusMetrics() *PrometheusMetrics {
	reg := prometheus.NewRegistry()
	target := prometheus.NewGauge(prometheus.GaugeOpts{Name: "loadgen_target_tps"})
	actual := prometheus.NewGauge(prometheus.GaugeOpts{Name: "loadgen_actual_tps"})
	total := prometheus.NewCounter(prometheus.CounterOpts{Name: "loadgen_transactions_total"})
	reg.MustRegister(target, actual, total)
	return &PrometheusMetrics{registry: reg, targetTPS: target, actualTPS: actual, transactionsTotal: total}
}
