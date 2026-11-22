package main

import (
	"encoding/json"
	"errors"
	"strings"
)

type cliJSONTypeError struct {
	cause   error
	message string
}

func (err cliJSONTypeError) Error() string { return err.message }

func (err cliJSONTypeError) Unwrap() error { return err.cause }

// Keep the CLI diagnostic stable when a JSON destination moves to another package.
func restoreJSONTypeDiagnostic(err error) error {
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		return err
	}
	message := err.Error()
	prefix, found := strings.CutSuffix(message, typeErr.Error())
	if !found {
		return err
	}

	currentType := typeErr.Type.String()
	dest := currentType
	slice := ""
	if elem, found := strings.CutPrefix(dest, "[]"); found {
		dest, slice = elem, "[]"
	}
	if name, found := strings.CutPrefix(dest, "api."); found {
		if old := oldWireTypeName(name); old != name || name == "workerStateItem" {
			dest = "main." + old
		}
	}
	dest = slice + dest
	structName := oldWireTypeName(typeErr.Struct)
	if dest == currentType && structName == typeErr.Struct {
		return err
	}

	legacyErr := *typeErr
	legacyErr.Struct = structName
	detail := strings.Replace(legacyErr.Error(), " of type "+currentType, " of type "+dest, 1)
	return cliJSONTypeError{cause: err, message: prefix + detail}
}

func oldWireTypeName(name string) string {
	switch name {
	case "ConfigResponse":
		return "configResponse"
	case "Snapshot":
		return "runtimeSnapshot"
	case "RunSnapshot":
		return "runSnapshot"
	case "ReaderSnapshot":
		return "readerSnapshot"
	case "SourceError":
		return "readerSourceError"
	case "ThrottlerSnapshot":
		return "throttlerSnapshot"
	case "SenderSnapshot":
		return "senderSnapshot"
	case "ChannelSnapshot":
		return "channelSnapshot"
	case "RunState":
		return "runState"
	default:
		return name
	}
}
