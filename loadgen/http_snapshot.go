package main

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

type httpStatus struct {
	Run           httpRunStatus       `json:"run"`
	Reader        httpReaderStatus    `json:"reader"`
	Throttler     httpThrottlerStatus `json:"throttler"`
	Sender        httpSenderStatus    `json:"sender"`
	ReaderChannel httpChannelStatus   `json:"readerChannel"`
	SenderChannel httpChannelStatus   `json:"senderChannel"`
	Config        httpConfigStatus    `json:"config"`
}

type httpRunStatus struct {
	State             runState `json:"state"`
	TotalTransactions int64    `json:"totalTransactions"`
	ElapsedMs         int64    `json:"elapsedMs"`
}

type httpReaderStatus struct {
	Workers                int                    `json:"workers"`
	LiveWorkers            int                    `json:"liveWorkers"`
	IdleWorkers            int                    `json:"idleWorkers"`
	ReadingWorkers         int                    `json:"readingWorkers"`
	BlockedWorkers         int                    `json:"blockedWorkers"`
	DrainingWorkers        int                    `json:"drainingWorkers"`
	DrainingIdleWorkers    int                    `json:"drainingIdleWorkers"`
	DrainingReadingWorkers int                    `json:"drainingReadingWorkers"`
	DrainingBlockedWorkers int                    `json:"drainingBlockedWorkers"`
	ReadBatchSize          int                    `json:"readBatchSize"`
	ReadTps                float64                `json:"readTps"`
	RowsRead               int64                  `json:"rowsRead"`
	SourceDirectory        string                 `json:"sourceDirectory"`
	SourceError            *httpReaderSourceError `json:"sourceError"`
}

type httpReaderSourceError struct {
	Category     string `json:"category"`
	Operation    string `json:"operation"`
	RelativePath string `json:"relativePath"`
	Message      string `json:"message"`
}

type httpThrottlerStatus struct {
	RequestedTps     int     `json:"requestedTps"`
	AdmittedTps      float64 `json:"admittedTps"`
	InstallationMode string  `json:"installationMode"`
}

type httpSenderStatus struct {
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

type httpChannelStatus struct {
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

type httpConfigStatus struct {
	ReaderReadBatchSize       httpRangeConfig            `json:"readerReadBatchSize"`
	ReaderWorkers             httpRangeConfig            `json:"readerWorkers"`
	ReaderChannelCapacity     httpAllowedConfig          `json:"readerChannelCapacity"`
	SenderChannelCapacity     httpAllowedConfig          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     httpRangeConfig            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode httpInstallationModeConfig `json:"throttlerInstallationMode"`
	MetricsWindowMS           httpRangeConfig            `json:"metricsWindowMs"`
	SenderWorkers             httpRangeConfig            `json:"senderWorkers"`
	Logging                   httpLoggingConfig          `json:"logging"`
}

type httpRangeConfig struct {
	Default    int    `json:"default"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpAllowedConfig struct {
	Default    int    `json:"default"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpInstallationModeConfig struct {
	Default    string   `json:"default"`
	Allowed    []string `json:"allowed"`
	Mutability string   `json:"mutability"`
}

type httpLoggingConfig struct {
	Level      string `json:"level"`
	Mutability string `json:"mutability"`
}

func httpStatusFromRuntime(status runtimeStatus) httpStatus {
	return httpStatus{
		Run: httpRunStatus{
			State:             status.Run.State,
			TotalTransactions: status.Run.TotalTransactions,
			ElapsedMs:         status.Run.ElapsedMs,
		},
		Reader: httpReaderStatus{
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
			SourceError:            httpReaderSourceErrorFromRuntime(status.Reader.SourceError),
		},
		Throttler: httpThrottlerStatus{
			RequestedTps:     status.Throttler.RequestedTps,
			AdmittedTps:      status.Throttler.AdmittedTps,
			InstallationMode: status.Throttler.InstallationMode,
		},
		Sender: httpSenderStatus{
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
		ReaderChannel: httpChannelStatusFromRuntime(status.ReaderChannel),
		SenderChannel: httpChannelStatusFromRuntime(status.SenderChannel),
		Config:        httpConfigStatusFromRuntime(status.Config),
	}
}

func (status httpStatus) runtimeStatus() runtimeStatus {
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

func httpConfigStatusFromRuntime(status runtimeConfigStatus) httpConfigStatus {
	return httpConfigStatus{
		ReaderReadBatchSize:       httpRangeConfigFromRuntime(status.ReaderReadBatchSize),
		ReaderWorkers:             httpRangeConfigFromRuntime(status.ReaderWorkers),
		ReaderChannelCapacity:     httpAllowedConfigFromRuntime(status.ReaderChannelCapacity),
		SenderChannelCapacity:     httpAllowedConfigFromRuntime(status.SenderChannelCapacity),
		ThrottlerRequestedTPS:     httpRangeConfigFromRuntime(status.ThrottlerRequestedTPS),
		ThrottlerInstallationMode: httpInstallationModeConfigFromRuntime(status.ThrottlerInstallationMode),
		MetricsWindowMS:           httpRangeConfigFromRuntime(status.MetricsWindowMS),
		SenderWorkers:             httpRangeConfigFromRuntime(status.SenderWorkers),
		Logging:                   httpLoggingConfigFromRuntime(status.Logging),
	}
}

func (status httpConfigStatus) runtimeStatus() runtimeConfigStatus {
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

func httpRangeConfigFromRuntime(config runtimeRangeConfig) httpRangeConfig {
	return httpRangeConfig{
		Default:    config.Default,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func (config httpRangeConfig) runtimeStatus() runtimeRangeConfig {
	return runtimeRangeConfig{
		Default:    config.Default,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func httpAllowedConfigFromRuntime(config runtimeAllowedConfig) httpAllowedConfig {
	return httpAllowedConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func (config httpAllowedConfig) runtimeStatus() runtimeAllowedConfig {
	return runtimeAllowedConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func httpInstallationModeConfigFromRuntime(config runtimeInstallationModeConfig) httpInstallationModeConfig {
	return httpInstallationModeConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func (config httpInstallationModeConfig) runtimeStatus() runtimeInstallationModeConfig {
	return runtimeInstallationModeConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Mutability: config.Mutability,
	}
}

func httpLoggingConfigFromRuntime(config runtimeLoggingConfig) httpLoggingConfig {
	return httpLoggingConfig{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

func (config httpLoggingConfig) runtimeStatus() runtimeLoggingConfig {
	return runtimeLoggingConfig{
		Level:      config.Level,
		Mutability: config.Mutability,
	}
}

func httpReaderSourceErrorFromRuntime(sourceError *readerSourceError) *httpReaderSourceError {
	if sourceError == nil {
		return nil
	}
	return &httpReaderSourceError{
		Category:     sourceError.Category,
		Operation:    sourceError.Operation,
		RelativePath: sourceError.RelativePath,
		Message:      sourceError.Message,
	}
}

func (sourceError *httpReaderSourceError) runtimeError() *readerSourceError {
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

func httpChannelStatusFromRuntime(status runtimeChannelStatus) httpChannelStatus {
	return httpChannelStatus{
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

func (status httpChannelStatus) runtimeStatus() runtimeChannelStatus {
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

func snapshotHandler(control controlPlane, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status := control.status()
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(httpStatusFromRuntime(status))
		if err != nil {
			logger.Error("snapshot response encoding failed", zap.String("event", "run_failed"))
			return
		}
	}
	return http.HandlerFunc(handler)
}
