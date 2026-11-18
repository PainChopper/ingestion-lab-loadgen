package main

import (
	"time"

	"go.uber.org/zap"
)

type runtimeStatus struct {
	Run           runtimeRunStatus       `json:"run"`
	Reader        runtimeReaderStatus    `json:"reader"`
	Throttler     runtimeThrottlerStatus `json:"throttler"`
	Sender        runtimeSenderStatus    `json:"sender"`
	ReaderChannel runtimeChannelStatus   `json:"readerChannel"`
	SenderChannel runtimeChannelStatus   `json:"senderChannel"`
	Config        runtimeConfigStatus    `json:"config"`
}

type runtimeRunStatus struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type runtimeReaderStatus struct {
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

func (sourceError readerSourceError) Error() string {
	return sourceError.Message
}

type runtimeThrottlerStatus struct {
	RequestedTps int     `json:"requestedTps"`
	AdmittedTps  float64 `json:"admittedTps"`
	Installed    bool    `json:"installed"`
}

type runtimeSenderStatus struct {
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

type runtimeChannelStatus struct {
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

type runtimeConfigStatus struct {
	ReaderReadBatchSize   runtimeRangeConfig     `json:"readerReadBatchSize"`
	ReaderWorkers         runtimeRangeConfig     `json:"readerWorkers"`
	ReaderChannelCapacity runtimeAllowedConfig   `json:"readerChannelCapacity"`
	SenderChannelCapacity runtimeAllowedConfig   `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS runtimeRangeConfig     `json:"throttlerRequestedTps"`
	ThrottlerInstalled    runtimeInstalledConfig `json:"throttlerInstalled"`
	MetricsWindowMS       runtimeRangeConfig     `json:"metricsWindowMs"`
	SenderWorkers         runtimeRangeConfig     `json:"senderWorkers"`
	Logging               runtimeLoggingConfig   `json:"logging"`
}

type runtimeRangeConfig struct {
	Initial    int    `json:"initial"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type runtimeAllowedConfig struct {
	Initial    int    `json:"initial"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type runtimeInstalledConfig struct {
	Initial    bool   `json:"initial"`
	Allowed    []bool `json:"allowed"`
	Mutability string `json:"mutability"`
}

type runtimeLoggingConfig struct {
	Level      string `json:"level"`
	Mutability string `json:"mutability"`
}

func runtimeConfigStatusFromConfig(config config) runtimeConfigStatus {
	return runtimeConfigStatus{
		ReaderReadBatchSize:   runtimeRangeConfigFromConfig(config.Reader.ReadBatchSize),
		ReaderWorkers:         runtimeRangeConfigFromConfig(config.Reader.Workers),
		ReaderChannelCapacity: runtimeAllowedConfigFromConfig(config.ReaderChannel.Capacity),
		SenderChannelCapacity: runtimeAllowedConfigFromConfig(config.SenderChannel.Capacity),
		ThrottlerRequestedTPS: runtimeRangeConfigFromConfig(config.Throttler.RequestedTPS),
		ThrottlerInstalled:    runtimeInstalledConfigFromConfig(config.Throttler.Installed),
		MetricsWindowMS:       runtimeRangeConfigFromConfig(config.Metrics.WindowMS),
		SenderWorkers:         runtimeRangeConfigFromConfig(config.Sender.Workers),
		Logging:               runtimeLoggingConfigFromConfig(config.Logging),
	}
}

func runtimeRangeConfigFromConfig(config rangeConfig) runtimeRangeConfig {
	return runtimeRangeConfig{
		Initial:    config.Initial,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func runtimeAllowedConfigFromConfig(config allowedConfig) runtimeAllowedConfig {
	return runtimeAllowedConfig{
		Initial:    config.Initial,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func runtimeInstalledConfigFromConfig(config installedConfig) runtimeInstalledConfig {
	return runtimeInstalledConfig{
		Initial:    config.Initial,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func runtimeLoggingConfigFromConfig(config loggingConfig) runtimeLoggingConfig {
	return runtimeLoggingConfig{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

const runtimeSummaryInterval = time.Minute

func runtimeSummaryFields(status runtimeStatus) []zap.Field {
	sourceErrorCategory := ""
	sourceErrorOperation := ""
	sourceErrorRelativePath := ""
	if status.Reader.SourceError != nil {
		sourceErrorCategory = status.Reader.SourceError.Category
		sourceErrorOperation = status.Reader.SourceError.Operation
		sourceErrorRelativePath = status.Reader.SourceError.RelativePath
	}

	return []zap.Field{
		zap.String("event", "runtime_summary"),
		zap.String("state", string(status.Run.State)),
		zap.Int64("elapsed_ms", status.Run.ElapsedMs),
		zap.Int64("total_transactions", status.Run.TotalTransactions),
		zap.Float64("reader_read_tps", status.Reader.ReadTps),
		zap.Int64("reader_rows_read", status.Reader.RowsRead),
		zap.Int("reader_workers", status.Reader.Workers),
		zap.Int("reader_live_workers", status.Reader.LiveWorkers),
		zap.Int("reader_reading_workers", status.Reader.ReadingWorkers),
		zap.Int("reader_blocked_workers", status.Reader.BlockedWorkers),
		zap.Int("reader_draining_workers", status.Reader.DrainingWorkers),
		zap.Int("throttler_requested_tps", status.Throttler.RequestedTps),
		zap.Float64("throttler_admitted_tps", status.Throttler.AdmittedTps),
		zap.Bool("installed", status.Throttler.Installed),
		zap.Int("reader_channel_capacity", status.ReaderChannel.Capacity),
		zap.Int("reader_channel_depth_batches", status.ReaderChannel.DepthBatches),
		zap.Int("reader_channel_buffered_transactions", status.ReaderChannel.BufferedTransactions),
		zap.Float64("reader_channel_input_tps", status.ReaderChannel.SentTransactionsPerSecond),
		zap.Float64("reader_channel_output_tps", status.ReaderChannel.ReceivedTransactionsPerSecond),
		zap.Int("reader_channel_blocked_senders", status.ReaderChannel.BlockedSenders),
		zap.Int64("reader_channel_oldest_blocked_ms", status.ReaderChannel.OldestBlockedSenderMs),
		zap.Int64("reader_channel_blocked_ms", status.ReaderChannel.BlockedMs),
		zap.Int("sender_channel_capacity", status.SenderChannel.Capacity),
		zap.Int("sender_channel_depth_batches", status.SenderChannel.DepthBatches),
		zap.Int("sender_channel_buffered_transactions", status.SenderChannel.BufferedTransactions),
		zap.Float64("sender_channel_input_tps", status.SenderChannel.SentTransactionsPerSecond),
		zap.Float64("sender_channel_output_tps", status.SenderChannel.ReceivedTransactionsPerSecond),
		zap.Int("sender_channel_blocked_senders", status.SenderChannel.BlockedSenders),
		zap.Int64("sender_channel_oldest_blocked_ms", status.SenderChannel.OldestBlockedSenderMs),
		zap.Int64("sender_channel_blocked_ms", status.SenderChannel.BlockedMs),
		zap.Int("sender_workers", status.Sender.Workers),
		zap.Int("sender_live_workers", status.Sender.LiveWorkers),
		zap.Int("sender_in_flight_workers", status.Sender.InFlightWorkers),
		zap.Int("sender_backoff_workers", status.Sender.BackoffWorkers),
		zap.Int("sender_draining_workers", status.Sender.DrainingWorkers),
		zap.String("source_error_category", sourceErrorCategory),
		zap.String("source_error_operation", sourceErrorOperation),
		zap.String("source_error_relative_path", sourceErrorRelativePath),
	}
}
