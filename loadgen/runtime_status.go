package main

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
	RequestedTps     int     `json:"requestedTps"`
	AdmittedTps      float64 `json:"admittedTps"`
	InstallationMode string  `json:"installationMode"`
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
	ReaderReadBatchSize       runtimeRangeConfig            `json:"readerReadBatchSize"`
	ReaderWorkers             runtimeRangeConfig            `json:"readerWorkers"`
	ReaderChannelCapacity     runtimeAllowedConfig          `json:"readerChannelCapacity"`
	SenderChannelCapacity     runtimeAllowedConfig          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     runtimeRangeConfig            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode runtimeInstallationModeConfig `json:"throttlerInstallationMode"`
	MetricsWindowMS           runtimeRangeConfig            `json:"metricsWindowMs"`
	SenderWorkers             runtimeRangeConfig            `json:"senderWorkers"`
	Logging                   runtimeLoggingConfig          `json:"logging"`
}

type runtimeRangeConfig struct {
	Default    int    `json:"default"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type runtimeAllowedConfig struct {
	Default    int    `json:"default"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type runtimeInstallationModeConfig struct {
	Default    string   `json:"default"`
	Allowed    []string `json:"allowed"`
	Mutability string   `json:"mutability"`
}

type runtimeLoggingConfig struct {
	Level      string `json:"level"`
	Mutability string `json:"mutability"`
}

func runtimeConfigStatusFromConfig(config config) runtimeConfigStatus {
	return runtimeConfigStatus{
		ReaderReadBatchSize:       runtimeRangeConfigFromConfig(config.Reader.ReadBatchSize),
		ReaderWorkers:             runtimeRangeConfigFromConfig(config.Reader.Workers),
		ReaderChannelCapacity:     runtimeAllowedConfigFromConfig(config.ReaderChannel.Capacity),
		SenderChannelCapacity:     runtimeAllowedConfigFromConfig(config.SenderChannel.Capacity),
		ThrottlerRequestedTPS:     runtimeRangeConfigFromConfig(config.Throttler.RequestedTPS),
		ThrottlerInstallationMode: runtimeInstallationModeConfigFromConfig(config.Throttler.InstallationMode),
		MetricsWindowMS:           runtimeRangeConfigFromConfig(config.Metrics.WindowMS),
		SenderWorkers:             runtimeRangeConfigFromConfig(config.Sender.Workers),
		Logging:                   runtimeLoggingConfigFromConfig(config.Logging),
	}
}

func runtimeRangeConfigFromConfig(config rangeConfig) runtimeRangeConfig {
	return runtimeRangeConfig{
		Default:    config.Default,
		Min:        config.Min,
		Max:        config.Max,
		Step:       config.Step,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func runtimeAllowedConfigFromConfig(config allowedConfig) runtimeAllowedConfig {
	return runtimeAllowedConfig{
		Default:    config.Default,
		Allowed:    config.Allowed,
		Unit:       config.Unit,
		Mutability: config.Mutability,
	}
}

func runtimeInstallationModeConfigFromConfig(config installationModeConfig) runtimeInstallationModeConfig {
	return runtimeInstallationModeConfig{
		Default:    config.Default,
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
