package main

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/control"
	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/pipeline"

	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
)

func TestConfirmedMetricIsCurrentWithoutTicks(t *testing.T) {
	const batchSize = 3
	cfg := testConfig()
	cfg.Reader.ReadBatchSize = batchSize
	cfg.Reader.Workers.Initial = 1
	cfg.Sender.Workers.Initial = 1
	cfg.Throttler.Installed = false
	cfg.Source.Path = filepath.Join(t.TempDir(), "data.parquet")
	if err := parquet.WriteFile(cfg.Source.Path, make([]pipeline.Transaction, 2*batchSize)); err != nil {
		t.Fatal(err)
	}
	source, err := pipeline.DiscoverSource(cfg.Source.Path)
	if err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		inSecondDelivery := make(chan struct{})
		logger := zap.NewNop()
		options := pipelineOptions(cfg, logger)
		deliveries := 0
		options.Deliver = func(ctx context.Context, batch []pipeline.Transaction, _ int) error {
			deliveries++
			if deliveries == 1 {
				if len(batch) != batchSize {
					t.Errorf("first delivered batch = %d rows, want %d", len(batch), batchSize)
				}
				return nil
			}
			if deliveries == 2 {
				close(inSecondDelivery)
			}
			// Hold the next delivery so the controller cannot finalize the run.
			<-ctx.Done()
			return ctx.Err()
		}
		owner := control.New(cfg, options, logger)
		metrics := newPrometheusMetrics()
		ready := make(chan control.SourceResult, 1)
		ready <- control.SourceResult{Source: source}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		defer func() {
			cancel()
			<-done
		}()
		go func() {
			defer close(done)
			events := control.Events{Source: ready, PipelineStarted: metrics.run.Store}
			if _, err := owner.Run(ctx, events, time.Second); err != nil {
				t.Errorf("event loop: %v", err)
			}
		}()

		select {
		case <-inSecondDelivery:
		case <-done:
			t.Fatal("event loop finished before the second delivery")
		case <-time.After(time.Second):
			t.Fatal("second delivery did not start before the first tick")
		}
		synctest.Wait()
		for range 2 {
			assertConfirmedMetric(t, metrics, batchSize)
		}
		if elapsed := time.Since(started); elapsed != 0 {
			t.Fatalf("metric collected after clock advanced by %s", elapsed)
		}
	})
}
