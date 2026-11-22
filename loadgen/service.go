package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/control"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func runServe(appCtx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, err := newApplicationLogger(cfg.Logging.Level, os.Stdout)
	if err != nil {
		return fmt.Errorf("create application logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	state := control.New(cfg, pipelineOptions(cfg, logger), logger)
	metrics := newPrometheusMetrics()
	metrics.targetTPS.Set(float64(cfg.Throttler.RequestedTPS.Initial))

	server, serverDone, err := startHTTPServer(state, metrics, cfg, logger)
	if err != nil {
		return err
	}
	events := control.Events{
		Source:              prepareSourceAsync(cfg.Source.Path),
		ServerDone:          serverDone,
		PipelineStarted:     metrics.run.Store,
		RequestedTPSChanged: func(tps int) { metrics.targetTPS.Set(float64(tps)) },
	}
	deadline, runErr := state.Run(appCtx, events, gracefulShutdownTimeout)
	if deadline.IsZero() {
		deadline = time.Now().Add(gracefulShutdownTimeout)
	}
	shutdownCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := shutdownHTTPServer(shutdownCtx, server); err != nil {
		logger.Error("graceful HTTP shutdown failed", zap.String("event", "http_shutdown_failed"), zap.Error(err))
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

func prepareSourceAsync(path string) <-chan control.SourceResult {
	ready := make(chan control.SourceResult, 1)
	go func() {
		source, err := pipeline.DiscoverSource(path)
		ready <- control.SourceResult{Source: source, Err: err}
	}()
	return ready
}

func pipelineOptions(cfg config.Config, logger *zap.Logger) pipeline.Options {
	delays := make([]time.Duration, len(cfg.Sender.Retry.DelaysMS))
	for i, delay := range cfg.Sender.Retry.DelaysMS {
		delays[i] = time.Duration(delay) * time.Millisecond
	}
	return pipeline.Options{
		BatchSize:          cfg.Reader.ReadBatchSize,
		ReaderCapacity:     cfg.ReaderChannel.Capacity,
		SenderCapacity:     cfg.SenderChannel.Capacity,
		Readers:            cfg.Reader.Workers.Initial,
		Senders:            cfg.Sender.Workers.Initial,
		Rate:               pipeline.Rate{TPS: cfg.Throttler.RequestedTPS.Initial, Installed: cfg.Throttler.Installed},
		SenderURL:          cfg.Sender.API.URL,
		SenderTimeout:      time.Duration(cfg.Sender.API.TimeoutMS) * time.Millisecond,
		SenderMaxConns:     cfg.Sender.Workers.Max,
		RetryDelays:        delays,
		RetryJitterPercent: cfg.Sender.Retry.JitterPercent,
		Logger:             logger,
	}
}

type prometheusMetrics struct {
	registry *prometheus.Registry

	targetTPS prometheus.Gauge

	run atomic.Pointer[pipeline.Pipeline]
}

func newPrometheusMetrics() *prometheusMetrics {
	reg := prometheus.NewRegistry()
	target := prometheus.NewGauge(prometheus.GaugeOpts{Name: "loadgen_target_tps"})
	metrics := &prometheusMetrics{registry: reg, targetTPS: target}
	total := prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "loadgen_transactions_total"}, func() float64 {
		run := metrics.run.Load()
		if run == nil {
			return 0
		}
		return float64(run.CompletedTotal())
	})
	reg.MustRegister(target, total)
	return metrics
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
