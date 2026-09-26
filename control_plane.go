package main

type requestKind int

const (
	getSnapshot requestKind = iota
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

type request struct {
	kind          requestKind
	snapshotReply chan statusSnapshot
	commandReply  chan commandResult
	value         int
	textValue     string
}

type commandStatus int

const (
	commandAccepted commandStatus = iota
	commandConflict
)

type commandResult struct {
	status commandStatus
	err    error
}

type controlPlane struct {
	requests chan request
}

func newControlPlane(buffer int) controlPlane {
	return controlPlane{requests: make(chan request, buffer)}
}

func (plane controlPlane) snapshot() statusSnapshot {
	reply := make(chan statusSnapshot, 1)
	plane.requests <- request{kind: getSnapshot, snapshotReply: reply}
	return <-reply
}

func (plane controlPlane) dispatch(kind requestKind, value int, textValue string) commandResult {
	reply := make(chan commandResult, 1)
	plane.requests <- request{
		kind:         kind,
		value:        value,
		textValue:    textValue,
		commandReply: reply,
	}
	return <-reply
}

func (plane controlPlane) dispatchAsync(kind requestKind) {
	plane.requests <- request{kind: kind}
}

type statusSnapshot struct {
	Run           runSnapshot       `json:"run"`
	Reader        readerSnapshot    `json:"reader"`
	Throttler     throttlerSnapshot `json:"throttler"`
	Sender        senderSnapshot    `json:"sender"`
	ReaderChannel channelSnapshot   `json:"readerChannel"`
	SenderChannel channelSnapshot   `json:"senderChannel"`
	Policy        policySnapshot    `json:"policy"`
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

func (error readerSourceError) Error() string {
	return error.Message
}

type throttlerSnapshot struct {
	RequestedTps     int     `json:"requestedTps"`
	AdmittedTps      float64 `json:"admittedTps"`
	InstallationMode string  `json:"installationMode"`
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
