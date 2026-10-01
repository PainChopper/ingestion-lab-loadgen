package main

type runtimeCommandKind int

const (
	getRuntimeStatus runtimeCommandKind = iota
	cmdRun
	cmdPause
	cmdReset
	cmdSetReadBatchSize
	cmdSetReaderWorkers
	cmdSetReaderChannelCapacity
	cmdSetSenderChannelCapacity
	cmdSetRequestedTPS
	cmdSetThrottlerInstallationMode
	cmdSetSenderWorkers
)

type runtimeCommand struct {
	kind         runtimeCommandKind
	statusReply  chan runtimeStatus
	receiptReply chan runtimeCommandReceipt
	value        int
	textValue    string
}

type runtimeCommandStatus int

const (
	commandAccepted runtimeCommandStatus = iota
	commandConflict
)

type runtimeCommandReceipt struct {
	status runtimeCommandStatus
	err    error
}

type controlPlane struct {
	requests chan runtimeCommand
}

func newControlPlane(buffer int) controlPlane {
	return controlPlane{requests: make(chan runtimeCommand, buffer)}
}

func (control controlPlane) status() runtimeStatus {
	reply := make(chan runtimeStatus, 1)
	control.requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}
	return <-reply
}

func (control controlPlane) execute(command runtimeCommand) runtimeCommandReceipt {
	reply := make(chan runtimeCommandReceipt, 1)
	command.receiptReply = reply
	control.requests <- command
	return <-reply
}

func (control controlPlane) executeAsync(command runtimeCommand) {
	control.requests <- command
}

func (command runtimeCommand) respond(receipt runtimeCommandReceipt) {
	if command.receiptReply != nil {
		command.receiptReply <- receipt
	}
}

func (command runtimeCommand) respondStatus(status runtimeStatus) {
	command.statusReply <- status
}

type runtimeStatus struct {
	Run           runtimeRunStatus
	Reader        runtimeReaderStatus
	Throttler     runtimeThrottlerStatus
	Sender        runtimeSenderStatus
	ReaderChannel runtimeChannelStatus
	SenderChannel runtimeChannelStatus
	Config        runtimeConfigStatus
}

type runtimeConfigStatus struct {
	ReaderReadBatchSize       runtimeRangeConfig
	ReaderWorkers             runtimeRangeConfig
	ReaderChannelCapacity     runtimeAllowedConfig
	SenderChannelCapacity     runtimeAllowedConfig
	ThrottlerRequestedTPS     runtimeRangeConfig
	ThrottlerInstallationMode runtimeInstallationModeConfig
	MetricsWindowMS           runtimeRangeConfig
	SenderWorkers             runtimeRangeConfig
	Logging                   runtimeLoggingConfig
}

type runtimeRangeConfig struct {
	Default    int
	Min        int
	Max        int
	Step       int
	Unit       string
	Mutability string
}

type runtimeAllowedConfig struct {
	Default    int
	Allowed    []int
	Unit       string
	Mutability string
}

type runtimeInstallationModeConfig struct {
	Default    string
	Allowed    []string
	Mutability string
}

type runtimeLoggingConfig struct {
	Level      string
	Mutability string
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

type runtimeRunStatus struct {
	State             runState
	TotalTransactions int64
	ElapsedMs         int64
}

type runtimeReaderStatus struct {
	Workers                int
	LiveWorkers            int
	IdleWorkers            int
	ReadingWorkers         int
	BlockedWorkers         int
	DrainingWorkers        int
	DrainingIdleWorkers    int
	DrainingReadingWorkers int
	DrainingBlockedWorkers int
	ReadBatchSize          int
	ReadTps                float64
	RowsRead               int64
	SourceDirectory        string
	SourceError            *readerSourceError
}

type readerSourceError struct {
	Category     string
	Operation    string
	RelativePath string
	Message      string
}

func (sourceError readerSourceError) Error() string {
	return sourceError.Message
}

type runtimeThrottlerStatus struct {
	RequestedTps     int
	AdmittedTps      float64
	InstallationMode string
}

type runtimeSenderStatus struct {
	Workers                 int
	LiveWorkers             int
	IdleWorkers             int
	InFlightWorkers         int
	BackoffWorkers          int
	DrainingWorkers         int
	DrainingIdleWorkers     int
	DrainingInFlightWorkers int
	DrainingBackoffWorkers  int
}

type runtimeChannelStatus struct {
	Capacity                      int
	DepthBatches                  int
	BufferedTransactions          int
	BlockedSenders                int
	OldestBlockedSenderMs         int64
	BlockedMs                     int64
	SentBatchesTotal              int64
	SentTransactionsTotal         int64
	ReceivedBatchesTotal          int64
	ReceivedTransactionsTotal     int64
	SentBatchesPerSecond          float64
	SentTransactionsPerSecond     float64
	ReceivedBatchesPerSecond      float64
	ReceivedTransactionsPerSecond float64
}
