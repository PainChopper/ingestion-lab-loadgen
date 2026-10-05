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
	cmdSetThrottlerInstalled
	cmdSetSenderWorkers
)

type runtimeCommandReceipt struct {
	rejected bool
	err      error
}

type runtimeCommand struct {
	kind         runtimeCommandKind
	statusReply  chan runtimeStatus
	receiptReply chan runtimeCommandReceipt
	value        int
	installed    bool
}

func (command runtimeCommand) respond(receipt runtimeCommandReceipt) {
	if command.receiptReply != nil {
		command.receiptReply <- receipt
	}
}

func (command runtimeCommand) respondStatus(status runtimeStatus) {
	command.statusReply <- status
}

func requestRuntimeStatus(requests chan<- runtimeCommand) runtimeStatus {
	reply := make(chan runtimeStatus, 1)
	requests <- runtimeCommand{kind: getRuntimeStatus, statusReply: reply}
	return <-reply
}

func executeRuntimeCommand(requests chan<- runtimeCommand, command runtimeCommand) runtimeCommandReceipt {
	reply := make(chan runtimeCommandReceipt, 1)
	command.receiptReply = reply
	requests <- command
	return <-reply
}
