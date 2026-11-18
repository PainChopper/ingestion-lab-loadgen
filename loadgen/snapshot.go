package main

import (
	"time"

	"go.uber.org/zap"
)

type runtimeSnapshot struct {
	Run           runSnapshot       `json:"run"`
	Reader        readerSnapshot    `json:"reader"`
	Throttler     throttlerSnapshot `json:"throttler"`
	Sender        senderSnapshot    `json:"sender"`
	ReaderChannel channelSnapshot   `json:"readerChannel"`
	SenderChannel channelSnapshot   `json:"senderChannel"`
	Config        configSnapshot    `json:"config"`
}

type runSnapshot struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type readerSnapshot struct {
	Workers                int                `json:"workers"`
	LiveWorkers            int                `json:"liveWorkers"`
	IdleWorkers            int                `json:"idleWorkers"`
	ReadingWorkers         int                `json:"readingWorkers"`
	BlockedWorkers         int                `json:"blockedWorkers"`
	DrainingWorkers        int                `json:"drainingWorkers"`
	DrainingIdleWorkers    int                `json:"drainingIdleWorkers"`
	DrainingReadingWorkers int                `json:"drainingReadingWorkers"`
	DrainingBlockedWorkers int                `json:"drainingBlockedWorkers"`
	ReadBatchSize          int                `json:"readBatchSize"`
	ReadTps                float64            `json:"readTps"`
	RowsRead               int64              `json:"rowsRead"`
	SourceDirectory        string             `json:"sourceDirectory"`
	SourceError            *readerSourceError `json:"sourceError"`
}

type readerSourceError struct {
	Category     string `json:"category"`
	Operation    string `json:"operation"`
	RelativePath string `json:"relativePath"`
	Message      string `json:"message"`
}

type throttlerSnapshot struct {
	RequestedTps int     `json:"requestedTps"`
	AdmittedTps  float64 `json:"admittedTps"`
	Installed    bool    `json:"installed"`
}

type senderSnapshot struct {
	Workers                 int `json:"workers"`
	LiveWorkers             int `json:"liveWorkers"`
	IdleWorkers             int `json:"idleWorkers"`
	InFlightWorkers         int `json:"inFlightWorkers"`
	BackoffWorkers          int `json:"backoffWorkers"`
	DrainingWorkers         int `json:"drainingWorkers"`
	DrainingIdleWorkers     int `json:"drainingIdleWorkers"`
	DrainingInFlightWorkers int `json:"drainingInFlightWorkers"`
	DrainingBackoffWorkers  int `json:"drainingBackoffWorkers"`
}

type channelSnapshot struct {
	Capacity                      int     `json:"capacity"`
	DepthBatches                  int     `json:"depthBatches"`
	BufferedTransactions          int     `json:"bufferedTransactions"`
	BlockedSenders                int     `json:"blockedSenders"`
	OldestBlockedSenderMs         int64   `json:"oldestBlockedSenderMs"`
	BlockedMs                     int64   `json:"blockedMs"`
	SentBatchesTotal              int64   `json:"sentBatchesTotal"`
	SentTransactionsTotal         int64   `json:"sentTransactionsTotal"`
	ReceivedBatchesTotal          int64   `json:"receivedBatchesTotal"`
	ReceivedTransactionsTotal     int64   `json:"receivedTransactionsTotal"`
	SentBatchesPerSecond          float64 `json:"inputBatchesPerSecond"`
	SentTransactionsPerSecond     float64 `json:"inputTransactionsPerSecond"`
	ReceivedBatchesPerSecond      float64 `json:"outputBatchesPerSecond"`
	ReceivedTransactionsPerSecond float64 `json:"outputTransactionsPerSecond"`
}

type configSnapshot struct {
	ReaderReadBatchSize   rangeConfigSnapshot     `json:"readerReadBatchSize"`
	ReaderWorkers         rangeConfigSnapshot     `json:"readerWorkers"`
	ReaderChannelCapacity allowedConfigSnapshot   `json:"readerChannelCapacity"`
	SenderChannelCapacity allowedConfigSnapshot   `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS rangeConfigSnapshot     `json:"throttlerRequestedTps"`
	ThrottlerInstalled    installedConfigSnapshot `json:"throttlerInstalled"`
	MetricsWindowMS       rangeConfigSnapshot     `json:"metricsWindowMs"`
	SenderWorkers         rangeConfigSnapshot     `json:"senderWorkers"`
	Logging               loggingConfigSnapshot   `json:"logging"`
}

type rangeConfigSnapshot struct {
	Initial    int    `json:"initial"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type allowedConfigSnapshot struct {
	Initial    int    `json:"initial"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type installedConfigSnapshot struct {
	Initial    bool   `json:"initial"`
	Allowed    []bool `json:"allowed"`
	Mutability string `json:"mutability"`
}

type loggingConfigSnapshot struct {
	Level      string `json:"level"`
	Mutability string `json:"mutability"`
}

func snapshotConfig(config config) configSnapshot {
	return configSnapshot{
		ReaderReadBatchSize:   snapshotRangeConfig(config.Reader.ReadBatchSize),
		ReaderWorkers:         snapshotRangeConfig(config.Reader.Workers),
		ReaderChannelCapacity: snapshotAllowedConfig(config.ReaderChannel.Capacity),
		SenderChannelCapacity: snapshotAllowedConfig(config.SenderChannel.Capacity),
		ThrottlerRequestedTPS: snapshotRangeConfig(config.Throttler.RequestedTPS),
		ThrottlerInstalled:    snapshotInstalledConfig(config.Throttler.Installed),
		MetricsWindowMS:       snapshotRangeConfig(config.Metrics.WindowMS),
		SenderWorkers:         snapshotRangeConfig(config.Sender.Workers),
		Logging:               snapshotLoggingConfig(config.Logging),
	}
}

func snapshotRangeConfig(config rangeConfig) rangeConfigSnapshot {
	return rangeConfigSnapshot{
		Initial:    config.Initial,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func snapshotAllowedConfig(config allowedConfig) allowedConfigSnapshot {
	return allowedConfigSnapshot{
		Initial:    config.Initial,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func snapshotInstalledConfig(config installedConfig) installedConfigSnapshot {
	return installedConfigSnapshot{
		Initial:    config.Initial,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func snapshotLoggingConfig(config loggingConfig) loggingConfigSnapshot {
	return loggingConfigSnapshot{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

const snapshotLogInterval = time.Minute

func snapshotLogFields(snapshot runtimeSnapshot) []zap.Field {
	sourceErrorCategory := ""
	sourceErrorOperation := ""
	sourceErrorRelativePath := ""
	if snapshot.Reader.SourceError != nil {
		sourceErrorCategory = snapshot.Reader.SourceError.Category
		sourceErrorOperation = snapshot.Reader.SourceError.Operation
		sourceErrorRelativePath = snapshot.Reader.SourceError.RelativePath
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.String("state", string(snapshot.Run.State)),
		zap.Int64("elapsed_ms", snapshot.Run.ElapsedMs),
		zap.Int64("total_transactions", snapshot.Run.TotalTransactions),
		zap.Float64("reader_read_tps", snapshot.Reader.ReadTps),
		zap.Int64("reader_rows_read", snapshot.Reader.RowsRead),
		zap.Int("reader_workers", snapshot.Reader.Workers),
		zap.Int("reader_live_workers", snapshot.Reader.LiveWorkers),
		zap.Int("reader_reading_workers", snapshot.Reader.ReadingWorkers),
		zap.Int("reader_blocked_workers", snapshot.Reader.BlockedWorkers),
		zap.Int("reader_draining_workers", snapshot.Reader.DrainingWorkers),
		zap.Int("throttler_requested_tps", snapshot.Throttler.RequestedTps),
		zap.Float64("throttler_admitted_tps", snapshot.Throttler.AdmittedTps),
		zap.Bool("installed", snapshot.Throttler.Installed),
		zap.Int("reader_channel_capacity", snapshot.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", snapshot.ReaderChannel.DepthBatches),
		zap.Int("reader_channel_buffered_transactions", snapshot.ReaderChannel.BufferedTransactions),
		zap.Float64("reader_channel_input_tps", snapshot.ReaderChannel.SentTransactionsPerSecond),
		zap.Float64("reader_channel_output_tps", snapshot.ReaderChannel.ReceivedTransactionsPerSecond),
		zap.Int("reader_channel_blocked_senders", snapshot.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", snapshot.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", snapshot.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", snapshot.SenderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", snapshot.SenderChannel.DepthBatches),
		zap.Int("sender_channel_buffered_transactions", snapshot.SenderChannel.BufferedTransactions),
		zap.Float64("sender_channel_input_tps", snapshot.SenderChannel.SentTransactionsPerSecond),
		zap.Float64("sender_channel_output_tps", snapshot.SenderChannel.ReceivedTransactionsPerSecond),
		zap.Int("sender_channel_blocked_senders", snapshot.SenderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", snapshot.SenderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", snapshot.SenderChannel.BlockedMs),
		zap.Int("sender_workers", snapshot.Sender.Workers),
		zap.Int("sender_live_workers", snapshot.Sender.LiveWorkers),
		zap.Int("sender_in_flight_workers", snapshot.Sender.InFlightWorkers),
		zap.Int("sender_backoff_workers", snapshot.Sender.BackoffWorkers),
		zap.Int("sender_draining_workers", snapshot.Sender.DrainingWorkers),
		zap.String("source_error_category", sourceErrorCategory),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_relative_path", sourceErrorRelativePath),
	}
}
