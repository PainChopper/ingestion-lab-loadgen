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
	Policy        httpV1PolicyStatus    `json:"policy"`
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

type httpV1PolicyStatus struct {
	ReaderReadBatchSize       httpV1RangePolicy            `json:"readerReadBatchSize"`
	ReaderWorkers             httpV1RangePolicy            `json:"readerWorkers"`
	ReaderChannelCapacity     httpV1AllowedPolicy          `json:"readerChannelCapacity"`
	SenderChannelCapacity     httpV1AllowedPolicy          `json:"senderChannelCapacity"`
	ThrottlerRequestedTPS     httpV1RangePolicy            `json:"throttlerRequestedTps"`
	ThrottlerInstallationMode httpV1InstallationModePolicy `json:"throttlerInstallationMode"`
	MetricsWindowMS           httpV1RangePolicy            `json:"metricsWindowMs"`
	SenderWorkers             httpV1RangePolicy            `json:"senderWorkers"`
	Logging                   httpV1LoggingPolicy          `json:"logging"`
}

type httpV1RangePolicy struct {
	Default    int    `json:"default"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Step       int    `json:"step"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpV1AllowedPolicy struct {
	Default    int    `json:"default"`
	Allowed    []int  `json:"allowed"`
	Unit       string `json:"unit"`
	Mutability string `json:"mutability"`
}

type httpV1InstallationModePolicy struct {
	Default    string   `json:"default"`
	Allowed    []string `json:"allowed"`
	Mutability string   `json:"mutability"`
}

type httpV1LoggingPolicy struct {
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
		Policy:        httpV1PolicyStatusFromRuntime(status.Policy),
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
		Policy:        status.Policy.runtimeStatus(),
	}
}

func httpV1PolicyStatusFromRuntime(status runtimePolicyStatus) httpV1PolicyStatus {
	return httpV1PolicyStatus{
		ReaderReadBatchSize:       httpV1RangePolicyFromRuntime(status.ReaderReadBatchSize),
		ReaderWorkers:             httpV1RangePolicyFromRuntime(status.ReaderWorkers),
		ReaderChannelCapacity:     httpV1AllowedPolicyFromRuntime(status.ReaderChannelCapacity),
		SenderChannelCapacity:     httpV1AllowedPolicyFromRuntime(status.SenderChannelCapacity),
		ThrottlerRequestedTPS:     httpV1RangePolicyFromRuntime(status.ThrottlerRequestedTPS),
		ThrottlerInstallationMode: httpV1InstallationModePolicyFromRuntime(status.ThrottlerInstallationMode),
		MetricsWindowMS:           httpV1RangePolicyFromRuntime(status.MetricsWindowMS),
		SenderWorkers:             httpV1RangePolicyFromRuntime(status.SenderWorkers),
		Logging:                   httpV1LoggingPolicyFromRuntime(status.Logging),
	}
}

func (status httpV1PolicyStatus) runtimeStatus() runtimePolicyStatus {
	return runtimePolicyStatus{
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

func httpV1RangePolicyFromRuntime(policy runtimeRangePolicy) httpV1RangePolicy {
	return httpV1RangePolicy{
		Default:    policy.Default,
		Min:        policy.Min,
		Max:        policy.Max,
		Step:       policy.Step,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func (policy httpV1RangePolicy) runtimeStatus() runtimeRangePolicy {
	return runtimeRangePolicy{
		Default:    policy.Default,
		Min:        policy.Min,
		Max:        policy.Max,
		Step:       policy.Step,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func httpV1AllowedPolicyFromRuntime(policy runtimeAllowedPolicy) httpV1AllowedPolicy {
	return httpV1AllowedPolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func (policy httpV1AllowedPolicy) runtimeStatus() runtimeAllowedPolicy {
	return runtimeAllowedPolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Unit:       policy.Unit,
		Mutability: policy.Mutability,
	}
}

func httpV1InstallationModePolicyFromRuntime(policy runtimeInstallationModePolicy) httpV1InstallationModePolicy {
	return httpV1InstallationModePolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Mutability: policy.Mutability,
	}
}

func (policy httpV1InstallationModePolicy) runtimeStatus() runtimeInstallationModePolicy {
	return runtimeInstallationModePolicy{
		Default:    policy.Default,
		Allowed:    policy.Allowed,
		Mutability: policy.Mutability,
	}
}

func httpV1LoggingPolicyFromRuntime(policy runtimeLoggingPolicy) httpV1LoggingPolicy {
	return httpV1LoggingPolicy{
		Level:      policy.Level,
		Mutability: policy.Mutability,
	}
}

func (policy httpV1LoggingPolicy) runtimeStatus() runtimeLoggingPolicy {
	return runtimeLoggingPolicy{
		Level:      policy.Level,
		Mutability: policy.Mutability,
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
