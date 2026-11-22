package main

import (
	"os"
	"testing"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/config"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pelletier/go-toml/v2"
)

const blackBoxTimeout = 10 * time.Second

func testConfig() config.Config {
	return config.Config{
		HTTP:          config.HTTP{Addr: "127.0.0.1:8080"},
		Source:        config.Source{Path: "input.parquet"},
		Reader:        config.Reader{ReadBatchSize: 1000, Workers: config.Workers{Initial: 1, Max: 7}},
		ReaderChannel: config.Channel{Capacity: 2},
		SenderChannel: config.Channel{Capacity: 0},
		Throttler:     config.Throttler{Installed: true, RequestedTPS: config.TPS{Initial: 2_000_000, Max: 4_000_000}},
		Sender: config.Sender{
			Workers: config.Workers{Initial: 1, Max: 32},
			API:     config.SenderAPI{URL: "http://127.0.0.1:8081/internal/ingest", TimeoutMS: 30_000},
			Retry:   config.SenderRetry{DelaysMS: []int{250, 500, 1000, 2000, 5000}, JitterPercent: 20},
		},
		Logging: config.Logging{Level: "info"},
	}
}

func writeTestConfig(t *testing.T, path string, cfg config.Config) {
	t.Helper()
	// The service entry point takes a TOML path. Encode the same field names it decodes.
	var values map[string]any
	if err := mapstructure.Decode(cfg, &values); err != nil {
		t.Fatal(err)
	}
	input, err := toml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
}
