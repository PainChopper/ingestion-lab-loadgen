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

type runtimeCommandStatus int

const (
	commandAccepted runtimeCommandStatus = iota
	commandConflict
)

type runtimeCommandReceipt struct {
	status runtimeCommandStatus
	err    error
}

type runtimeCommand struct {
	kind         runtimeCommandKind
	statusReply  chan runtimeStatus
	receiptReply chan runtimeCommandReceipt
	value        int
	textValue    string
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
