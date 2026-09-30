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

type runtimeControl struct {
	requests chan runtimeCommand
}

func newRuntimeControl(buffer int) runtimeControl {
	return runtimeControl{requests: make(chan runtimeCommand, buffer)}
}

func (control runtimeControl) status() runtimeStatus {
	reply := make(chan runtimeStatus, 1)
	control.requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}
	return <-reply
}

func (control runtimeControl) execute(command runtimeCommand) runtimeCommandReceipt {
	reply := make(chan runtimeCommandReceipt, 1)
	command.receiptReply = reply
	control.requests <- command
	return <-reply
}

func (control runtimeControl) executeAsync(command runtimeCommand) {
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
	Policy        runtimePolicyStatus
}

type runtimePolicyStatus struct {
	ReaderReadBatchSize       runtimeRangePolicy
	ReaderWorkers             runtimeRangePolicy
	ReaderChannelCapacity     runtimeAllowedPolicy
	SenderChannelCapacity     runtimeAllowedPolicy
	ThrottlerRequestedTPS     runtimeRangePolicy
	ThrottlerInstallationMode runtimeInstallationModePolicy
	MetricsWindowMS           runtimeRangePolicy
	SenderWorkers             runtimeRangePolicy
	Logging                   runtimeLoggingPolicy
}

type runtimeRangePolicy struct {
	Default    int
	Min        int
	Max        int
	Step       int
	Unit       string
	Mutability string
}

type runtimeAllowedPolicy struct {
	Default    int
	Allowed    []int
	Unit       string
	Mutability string
}

type runtimeInstallationModePolicy struct {
	Default    string
	Allowed    []string
	Mutability string
}

type runtimeLoggingPolicy struct {
	Level      string
	Mutability string
}

func runtimePolicyStatusFromPolicy(policy policy) runtimePolicyStatus {
	return runtimePolicyStatus{
		ReaderReadBatchSize:       runtimeRangePolicyFromPolicy(policy.Reader.ReadBatchSize),
		ReaderWorkers:             runtimeRangePolicyFromPolicy(policy.Reader.Workers),
		ReaderChannelCapacity:     runtimeAllowedPolicyFromPolicy(policy.ReaderChannel.Capacity),
		SenderChannelCapacity:     runtimeAllowedPolicyFromPolicy(policy.SenderChannel.Capacity),
		ThrottlerRequestedTPS:     runtimeRangePolicyFromPolicy(policy.Throttler.RequestedTPS),
		ThrottlerInstallationMode: runtimeInstallationModePolicyFromPolicy(policy.Throttler.InstallationMode),
		MetricsWindowMS:           runtimeRangePolicyFromPolicy(policy.Metrics.WindowMS),
		SenderWorkers:             runtimeRangePolicyFromPolicy(policy.Sender.Workers),
		Logging:                   runtimeLoggingPolicyFromPolicy(policy.Logging),
	}
}

func runtimeRangePolicyFromPolicy(policy rangePolicy) runtimeRangePolicy {
	return runtimeRangePolicy{
		Default:    policy.Default,
		Min:        policy.Min,
		Max:        policy.Max,
		Step:       policy.Step,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func runtimeAllowedPolicyFromPolicy(policy allowedPolicy) runtimeAllowedPolicy {
	return runtimeAllowedPolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func runtimeInstallationModePolicyFromPolicy(policy installationModePolicy) runtimeInstallationModePolicy {
	return runtimeInstallationModePolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Mutability: policy.Mutability,
	}
}

func runtimeLoggingPolicyFromPolicy(policy loggingPolicy) runtimeLoggingPolicy {
	return runtimeLoggingPolicy{
		Level:      policy.Level,
		Mutability: policy.Mutability,
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
