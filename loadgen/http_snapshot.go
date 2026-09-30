package main

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

type httpV1Status struct {
	Run           httpV1RunStatus       `json:"run"`
	Reader        httpV1ReaderStatus    `json:"reader"`
	Throttler     httpV1ThrottlerStatus `json:"throttler"`
	Sender        httpV1SenderStatus    `json:"sender"`
	ReaderChannel httpV1ChannelStatus   `json:"readerChannel"`
	SenderChannel httpV1ChannelStatus   `json:"senderChannel"`
	Config        httpV1ConfigStatus    `json:"config"`
}

type httpV1RunStatus struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type httpV1ReaderStatus struct {
	Workers                int                      `json:"workers"`
	LiveWorkers            int                      `json:"liveWorkers"`
	IdleWorkers            int                      `json:"idleWorkers"`
	ReadingWorkers         int                      `json:"readingWorkers"`
	BlockedWorkers         int                      `json:"blockedWorkers"`
	DrainingWorkers        int                      `json:"drainingWorkers"`
	DrainingIdleWorkers    int                      `json:"drainingIdleWorkers"`
	DrainingReadingWorkers int                      `json:"drainingReadingWorkers"`
	DrainingBlockedWorkers int                      `json:"drainingBlockedWorkers"`
	ReadBatchSize          int                      `json:"readBatchSize"`
	ReadTps                float64                  `json:"readTps"`
	RowsRead               int64                    `json:"rowsRead"`
	SourceDirectory        string                   `json:"sourceDirectory"`
	SourceError            *httpV1ReaderSourceError `json:"sourceError"`
}

type httpV1ReaderSourceError struct {
	Category     string `json:"category"`
	Operation    string `json:"operation"`
	RelativePath string `json:"relativePath"`
	Message      string `json:"message"`
}

type httpV1ThrottlerStatus struct {
	RequestedTps     int     `json:"requestedTps"`
	AdmittedTps      float64 `json:"admittedTps"`
	InstallationMode string  `json:"installationMode"`
}

type httpV1SenderStatus struct {
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

type httpV1ChannelStatus struct {
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

type httpV1ConfigStatus struct {
	ReaderReadBatchSize       httpV1RangeConfig            `json:"readerReadBatchSize"`
	ReaderWorkers             httpV1RangeConfig            `json:"readerWorkers"`
	ReaderChannelCapacity     httpV1AllowedConfig          `json:"readerChannelCapacity"`
	SenderChannelCapacity     httpV1AllowedConfig          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     httpV1RangeConfig            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode httpV1InstallationModeConfig `json:"throttlerInstallationMode"`
	MetricsWindowMS           httpV1RangeConfig            `json:"metricsWindowMs"`
	SenderWorkers             httpV1RangeConfig            `json:"senderWorkers"`
	Logging                   httpV1LoggingConfig          `json:"logging"`
}

type httpV1RangeConfig struct {
	Default    int    `json:"default"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpV1AllowedConfig struct {
	Default    int    `json:"default"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpV1InstallationModeConfig struct {
	Default    string   `json:"default"`
	Allowed    []string `json:"allowed"`
	Mutability string   `json:"mutability"`
}

type httpV1LoggingConfig struct {
	Level      string `json:"level"`
	Mutability string `json:"mutability"`
}

func httpV1StatusFromRuntime(status runtimeStatus) httpV1Status {
	return httpV1Status{
		Run: httpV1RunStatus{
			State:             status.Run.State,
			TotalTransactions: status.Run.TotalTransactions,
			ElapsedMs:         status.Run.ElapsedMs,
		},
		Reader: httpV1ReaderStatus{
			Workers:                status.Reader.Workers,
			LiveWorkers:            status.Reader.LiveWorkers,
			IdleWorkers:            status.Reader.IdleWorkers,
			ReadingWorkers:         status.Reader.ReadingWorkers,
			BlockedWorkers:         status.Reader.BlockedWorkers,
			DrainingWorkers:        status.Reader.DrainingWorkers,
			DrainingIdleWorkers:    status.Reader.DrainingIdleWorkers,
			DrainingReadingWorkers: status.Reader.DrainingReadingWorkers,
			DrainingBlockedWorkers: status.Reader.DrainingBlockedWorkers,
			ReadBatchSize:          status.Reader.ReadBatchSize,
			ReadTps:                status.Reader.ReadTps,
			RowsRead:               status.Reader.RowsRead,
			SourceDirectory:        status.Reader.SourceDirectory,
			SourceError:            httpV1ReaderSourceErrorFromRuntime(status.Reader.SourceError),
		},
		Throttler: httpV1ThrottlerStatus{
			RequestedTps:     status.Throttler.RequestedTps,
			AdmittedTps:      status.Throttler.AdmittedTps,
			InstallationMode: status.Throttler.InstallationMode,
		},
		Sender: httpV1SenderStatus{
			Workers:                 status.Sender.Workers,
			LiveWorkers:             status.Sender.LiveWorkers,
			IdleWorkers:             status.Sender.IdleWorkers,
			InFlightWorkers:         status.Sender.InFlightWorkers,
			BackoffWorkers:          status.Sender.BackoffWorkers,
			DrainingWorkers:         status.Sender.DrainingWorkers,
			DrainingIdleWorkers:     status.Sender.DrainingIdleWorkers,
			DrainingInFlightWorkers: status.Sender.DrainingInFlightWorkers,
			DrainingBackoffWorkers:  status.Sender.DrainingBackoffWorkers,
		},
		ReaderChannel: httpV1ChannelStatusFromRuntime(status.ReaderChannel),
		SenderChannel: httpV1ChannelStatusFromRuntime(status.SenderChannel),
		Config:        httpV1ConfigStatusFromRuntime(status.Config),
	}
}

func (status httpV1Status) runtimeStatus() runtimeStatus {
	return runtimeStatus{
		Run: runtimeRunStatus{
			State:             status.Run.State,
			TotalTransactions: status.Run.TotalTransactions,
			ElapsedMs:         status.Run.ElapsedMs,
		},
		Reader: runtimeReaderStatus{
			Workers:                status.Reader.Workers,
			LiveWorkers:            status.Reader.LiveWorkers,
			IdleWorkers:            status.Reader.IdleWorkers,
			ReadingWorkers:         status.Reader.ReadingWorkers,
			BlockedWorkers:         status.Reader.BlockedWorkers,
			DrainingWorkers:        status.Reader.DrainingWorkers,
			DrainingIdleWorkers:    status.Reader.DrainingIdleWorkers,
			DrainingReadingWorkers: status.Reader.DrainingReadingWorkers,
			DrainingBlockedWorkers: status.Reader.DrainingBlockedWorkers,
			ReadBatchSize:          status.Reader.ReadBatchSize,
			ReadTps:                status.Reader.ReadTps,
			RowsRead:               status.Reader.RowsRead,
			SourceDirectory:        status.Reader.SourceDirectory,
			SourceError:            status.Reader.SourceError.runtimeError(),
		},
		Throttler: runtimeThrottlerStatus{
			RequestedTps:     status.Throttler.RequestedTps,
			AdmittedTps:      status.Throttler.AdmittedTps,
			InstallationMode: status.Throttler.InstallationMode,
		},
		Sender: runtimeSenderStatus{
			Workers:                 status.Sender.Workers,
			LiveWorkers:             status.Sender.LiveWorkers,
			IdleWorkers:             status.Sender.IdleWorkers,
			InFlightWorkers:         status.Sender.InFlightWorkers,
			BackoffWorkers:          status.Sender.BackoffWorkers,
			DrainingWorkers:         status.Sender.DrainingWorkers,
			DrainingIdleWorkers:     status.Sender.DrainingIdleWorkers,
			DrainingInFlightWorkers: status.Sender.DrainingInFlightWorkers,
			DrainingBackoffWorkers:  status.Sender.DrainingBackoffWorkers,
		},
		ReaderChannel: status.ReaderChannel.runtimeStatus(),
		SenderChannel: status.SenderChannel.runtimeStatus(),
		Config:        status.Config.runtimeStatus(),
	}
}

func httpV1ConfigStatusFromRuntime(status runtimeConfigStatus) httpV1ConfigStatus {
	return httpV1ConfigStatus{
		ReaderReadBatchSize:       httpV1RangeConfigFromRuntime(status.ReaderReadBatchSize),
		ReaderWorkers:             httpV1RangeConfigFromRuntime(status.ReaderWorkers),
		ReaderChannelCapacity:     httpV1AllowedConfigFromRuntime(status.ReaderChannelCapacity),
		SenderChannelCapacity:     httpV1AllowedConfigFromRuntime(status.SenderChannelCapacity),
		ThrottlerRequestedTPS:     httpV1RangeConfigFromRuntime(status.ThrottlerRequestedTPS),
		ThrottlerInstallationMode: httpV1InstallationModeConfigFromRuntime(status.ThrottlerInstallationMode),
		MetricsWindowMS:           httpV1RangeConfigFromRuntime(status.MetricsWindowMS),
		SenderWorkers:             httpV1RangeConfigFromRuntime(status.SenderWorkers),
		Logging:                   httpV1LoggingConfigFromRuntime(status.Logging),
	}
}

func (status httpV1ConfigStatus) runtimeStatus() runtimeConfigStatus {
	return runtimeConfigStatus{
		ReaderReadBatchSize:       status.ReaderReadBatchSize.runtimeStatus(),
		ReaderWorkers:             status.ReaderWorkers.runtimeStatus(),
		ReaderChannelCapacity:     status.ReaderChannelCapacity.runtimeStatus(),
		SenderChannelCapacity:     status.SenderChannelCapacity.runtimeStatus(),
		ThrottlerRequestedTPS:     status.ThrottlerRequestedTPS.runtimeStatus(),
		ThrottlerInstallationMode: status.ThrottlerInstallationMode.runtimeStatus(),
		MetricsWindowMS:           status.MetricsWindowMS.runtimeStatus(),
		SenderWorkers:             status.SenderWorkers.runtimeStatus(),
		Logging:                   status.Logging.runtimeStatus(),
	}
}

func httpV1RangeConfigFromRuntime(config runtimeRangeConfig) httpV1RangeConfig {
	return httpV1RangeConfig{
		Default:    config.Default,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func (config httpV1RangeConfig) runtimeStatus() runtimeRangeConfig {
	return runtimeRangeConfig{
		Default:    config.Default,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func httpV1AllowedConfigFromRuntime(config runtimeAllowedConfig) httpV1AllowedConfig {
	return httpV1AllowedConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func (config httpV1AllowedConfig) runtimeStatus() runtimeAllowedConfig {
	return runtimeAllowedConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func httpV1InstallationModeConfigFromRuntime(config runtimeInstallationModeConfig) httpV1InstallationModeConfig {
	return httpV1InstallationModeConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func (config httpV1InstallationModeConfig) runtimeStatus() runtimeInstallationModeConfig {
	return runtimeInstallationModeConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func httpV1LoggingConfigFromRuntime(config runtimeLoggingConfig) httpV1LoggingConfig {
	return httpV1LoggingConfig{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

func (config httpV1LoggingConfig) runtimeStatus() runtimeLoggingConfig {
	return runtimeLoggingConfig{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

func httpV1ReaderSourceErrorFromRuntime(sourceError *readerSourceError) *httpV1ReaderSourceError {
	if sourceError == nil {
		return nil
	}
	return &httpV1ReaderSourceError{
		Category:     sourceError.Category,
		Operation:    sourceError.Operation,
		RelativePath: sourceError.RelativePath,
		Message:      sourceError.Message,
	}
}

func (sourceError *httpV1ReaderSourceError) runtimeError() *readerSourceError {
	if sourceError == nil {
		return nil
	}
	return &readerSourceError{
		Category:     sourceError.Category,
		Operation:    sourceError.Operation,
		RelativePath: sourceError.RelativePath,
		Message:      sourceError.Message,
	}
}

func httpV1ChannelStatusFromRuntime(status runtimeChannelStatus) httpV1ChannelStatus {
	return httpV1ChannelStatus{
		Capacity:                      status.Capacity,
		DepthBatches:                  status.DepthBatches,
		BufferedTransactions:          status.BufferedTransactions,
		BlockedSenders:                status.BlockedSenders,
		OldestBlockedSenderMs:         status.OldestBlockedSenderMs,
		BlockedMs:                     status.BlockedMs,
		SentBatchesTotal:              status.SentBatchesTotal,
		SentTransactionsTotal:         status.SentTransactionsTotal,
		ReceivedBatchesTotal:          status.ReceivedBatchesTotal,
		ReceivedTransactionsTotal:     status.ReceivedTransactionsTotal,
		SentBatchesPerSecond:          status.SentBatchesPerSecond,
		SentTransactionsPerSecond:     status.SentTransactionsPerSecond,
		ReceivedBatchesPerSecond:      status.ReceivedBatchesPerSecond,
		ReceivedTransactionsPerSecond: status.ReceivedTransactionsPerSecond,
	}
}

func (status httpV1ChannelStatus) runtimeStatus() runtimeChannelStatus {
	return runtimeChannelStatus{
		Capacity:                      status.Capacity,
		DepthBatches:                  status.DepthBatches,
		BufferedTransactions:          status.BufferedTransactions,
		BlockedSenders:                status.BlockedSenders,
		OldestBlockedSenderMs:         status.OldestBlockedSenderMs,
		BlockedMs:                     status.BlockedMs,
		SentBatchesTotal:              status.SentBatchesTotal,
		SentTransactionsTotal:         status.SentTransactionsTotal,
		ReceivedBatchesTotal:          status.ReceivedBatchesTotal,
		ReceivedTransactionsTotal:     status.ReceivedTransactionsTotal,
		SentBatchesPerSecond:          status.SentBatchesPerSecond,
		SentTransactionsPerSecond:     status.SentTransactionsPerSecond,
		ReceivedBatchesPerSecond:      status.ReceivedBatchesPerSecond,
		ReceivedTransactionsPerSecond: status.ReceivedTransactionsPerSecond,
	}
}

func snapshotHandler(control runtimeControl, loggers ...*zap.Logger) http.Handler {
	logger := loggerOrNop(loggers)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status := control.status()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(httpV1StatusFromRuntime(status))
		if err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
			return
		}
	}
	return http.HandlerFunc(handler)
}
