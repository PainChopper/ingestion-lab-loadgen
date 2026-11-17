package main

import (
	"context"
	"errors"
)

type runtimeCommandKind int

const (
	getRuntimeStatus runtimeCommandKind = iota
	cmdSetReaderWorkers
	cmdSetRequestedTPS
	cmdSetThrottlerInstalled
	cmdSetSenderWorkers
)

type runtimeCommandReceipt struct {
	rejected bool
}

var errRuntimeUnavailable = errors.New("runtime unavailable")

type runtimeRequests struct {
	commands chan<- runtimeCommand
	done     <-chan struct{}
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

func requestRuntimeStatus(ctx context.Context, requests runtimeRequests) (runtimeStatus, error) {
	reply := make(chan runtimeStatus, 1)
	return awaitRuntimeReply(ctx, requests, runtimeCommand{kind: getRuntimeStatus, statusReply: reply}, reply)
}

func executeRuntimeCommand(ctx context.Context, requests runtimeRequests, command runtimeCommand) (runtimeCommandReceipt, error) {
	reply := make(chan runtimeCommandReceipt, 1)
	command.receiptReply = reply
	return awaitRuntimeReply(ctx, requests, command, reply)
}

func awaitRuntimeReply[T any](ctx context.Context, requests runtimeRequests, command runtimeCommand, reply <-chan T) (T, error) {
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-requests.done:
		return zero, errRuntimeUnavailable
	case requests.commands <- command:
	}
	return receiveRuntimeReply(ctx, requests.done, reply)
}

func receiveRuntimeReply[T any](ctx context.Context, done <-chan struct{}, reply <-chan T) (T, error) {
	var zero T
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-done:
		// The loop publishes replies before closing done. Prefer an already published result.
		select {
		case result := <-reply:
			return result, nil
		default:
			return zero, errRuntimeUnavailable
		}
	case result := <-reply:
		return result, nil
	}
}
