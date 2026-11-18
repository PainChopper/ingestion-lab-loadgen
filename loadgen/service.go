package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func runServe(appCtx context.Context, configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, err := newApplicationLogger(cfg.Logging.Level, os.Stdout)
	if err != nil {
		return fmt.Errorf("create application logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	state := newControlState(cfg, logger)
	commands := make(chan runtimeCommand, 10)
	loopDone := make(chan struct{})
	requests := runtimeRequests{commands: commands, done: loopDone}
	metrics := newServiceMetrics(cfg)
	defer metrics.stop()

	server, serverDone, err := startHTTPServer(requests, metrics.prometheusMetrics, cfg, logger)
	if err != nil {
		return err
	}
	logger.Info("service started", zap.String("event", "service_started"))
	events := runtimeEvents{
		requests:   commands,
		source:     prepareSourceAsync(cfg.Source.Path),
		serverDone: serverDone,
		metrics:    metrics,
	}
	deadline, runErr := state.runEventLoop(appCtx, events, gracefulShutdownTimeout)
	close(loopDone)
	if deadline.IsZero() {
		deadline = time.Now().Add(gracefulShutdownTimeout)
	}
	shutdownCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := shutdownHTTPServer(shutdownCtx, server); err != nil {
		logger.Error("graceful HTTP shutdown failed", zap.String("event", "run_failed"), zap.Error(err))
		runErr = errors.Join(runErr, fmt.Errorf("shutdown http server: %w", err))
	}
	select {
	case err := <-serverDone:
		runErr = errors.Join(runErr, err)
	case <-shutdownCtx.Done():
	}
	logger.Info("service stopped", zap.String("event", "service_stopped"))
	return runErr
}

type sourceDiscovery struct {
	source readerSource
	err    error
}

func prepareSourceAsync(path string) <-chan sourceDiscovery {
	ready := make(chan sourceDiscovery, 1)
	go func() {
		source, err := discoverReaderSource(path)
		ready <- sourceDiscovery{source: source, err: err}
	}()
	return ready
}

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

func newApplicationLogger(level string, output io.Writer) (*zap.Logger, error) {
	var atomicLevel zapcore.Level
	if err := atomicLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.LevelKey = "level"
	encoderConfig.MessageKey = "msg"
	encoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	core := zapcore.NewCore(
		zaplogfmt.NewEncoder(encoderConfig),
		zapcore.AddSync(output),
		atomicLevel,
	)
	return zap.New(core), nil
}
