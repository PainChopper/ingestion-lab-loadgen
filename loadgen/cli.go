package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const cliUsage = `usage: ingestion-lab-loadgen <command> [arguments]

  serve [--config <path>]    start with config.toml or the specified file
  status                     show the current run status
  set readers <N>            set Reader workers
  set senders <N>            set Sender workers
  set tps <N>                set target TPS
  set throttle <true|false>  enable or bypass the TPS limit

  --url <url>  for status and set (default: http://127.0.0.1:8080)
  --help       show this help`

type cliCommand struct {
	configPath   string
	showUsage    bool
	remoteAction string
	remoteURL    string
	remoteSet    remoteSetCommand
}

func parseCLI(args []string) (cliCommand, error) {
	if len(args) == 0 {
		return cliCommand{}, nil
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return cliCommand{showUsage: true}, nil
	}

	if args[0] == "status" {
		return parseRemoteCLI(args)
	}
	if args[0] == "set" {
		return parseSetCLI(args)
	}
	if args[0] != "serve" {
		return cliCommand{}, fmt.Errorf("unexpected command %q", args[0])
	}

	command := cliCommand{}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--help":
			if len(args) == 2 {
				command.showUsage = true
				return command, nil
			}
			return cliCommand{}, fmt.Errorf("invalid serve arguments")
		case "--config":
			if command.configPath != "" || index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
				return cliCommand{}, fmt.Errorf("invalid serve arguments: --config requires one non-empty path")
			}
			index++
			command.configPath = args[index]
		default:
			return cliCommand{}, fmt.Errorf("unexpected argument %q", args[index])
		}
	}
	return command, nil
}

const (
	defaultCLIBaseURL   = "http://127.0.0.1:8080"
	remoteClientTimeout = 10 * time.Second

	cliExitSuccess = 0
	cliExitLocal   = 1
	cliExitUsage   = 2
	cliExitHTTP    = 3
	cliExitRemote  = 4
)

func cliExitCode(err error) int {
	var httpErr remoteHTTPError
	if errors.As(err, &httpErr) {
		return cliExitHTTP
	}
	return cliExitRemote
}

func (command cliCommand) usage() string {
	if command.remoteAction == "set" {
		return cliSetUsage
	}
	if command.remoteAction == "" {
		return cliUsage
	}
	return fmt.Sprintf("usage: ingestion-lab-loadgen %s [--url <url>]", command.remoteAction)
}

const cliSetUsage = `usage:
  ingestion-lab-loadgen set readers <N> [--url <url>]
  ingestion-lab-loadgen set senders <N> [--url <url>]
  ingestion-lab-loadgen set tps <N> [--url <url>]
  ingestion-lab-loadgen set throttle <true|false> [--url <url>]`

type remoteSetCommand struct {
	target        string
	requestAction string
	value         any
}

func parseRemoteCLI(args []string) (cliCommand, error) {
	command := cliCommand{remoteAction: args[0], remoteURL: defaultCLIBaseURL}
	if len(args) == 2 && args[1] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) == 1 {
		return command, nil
	}
	if len(args) != 3 || args[1] != "--url" || args[2] == "" {
		return command, fmt.Errorf("invalid %s arguments", args[0])
	}
	baseURL, err := validateCLIBaseURL(args[2])
	if err != nil {
		return command, err
	}
	command.remoteURL = baseURL
	return command, nil
}

func parseSetCLI(args []string) (cliCommand, error) {
	command := cliCommand{remoteAction: "set", remoteURL: defaultCLIBaseURL}
	if len(args) == 2 && args[1] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) < 2 {
		return command, fmt.Errorf("invalid set arguments")
	}
	setCommand, err := newRemoteSetCommand(args[1], "")
	if err != nil {
		return command, err
	}
	if len(args) == 3 && args[2] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) != 3 && len(args) != 5 {
		return command, fmt.Errorf("invalid set arguments")
	}
	if args[2] == "" {
		return command, fmt.Errorf("invalid set arguments")
	}
	if len(args) == 5 {
		if args[3] != "--url" || args[4] == "" {
			return command, fmt.Errorf("invalid set arguments")
		}
		baseURL, err := validateCLIBaseURL(args[4])
		if err != nil {
			return command, err
		}
		command.remoteURL = baseURL
	}
	setCommand, err = newRemoteSetCommand(args[1], args[2])
	if err != nil {
		return command, err
	}
	command.remoteSet = setCommand
	return command, nil
}

func newRemoteSetCommand(target, rawValue string) (remoteSetCommand, error) {
	setCommand := remoteSetCommand{target: target}
	switch target {
	case "readers":
		setCommand.requestAction = "set-reader-workers"
	case "senders":
		setCommand.requestAction = "set-sender-workers"
	case "tps":
		setCommand.requestAction = "set-requested-tps"
	case "throttle":
		setCommand.requestAction = "set-throttler-installed"
		if rawValue == "" {
			return setCommand, nil
		}
		if rawValue != "true" && rawValue != "false" {
			return remoteSetCommand{}, fmt.Errorf("invalid %s value %q", target, rawValue)
		}
		setCommand.value = rawValue == "true"
		return setCommand, nil
	default:
		return remoteSetCommand{}, fmt.Errorf("unknown set target %q", target)
	}
	if rawValue == "" {
		return setCommand, nil
	}
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return remoteSetCommand{}, fmt.Errorf("invalid %s value %q", target, rawValue)
	}
	setCommand.value = value
	return setCommand, nil
}

func validateCLIBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("--url must be an absolute HTTP(S) URL with host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("--url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("--url must not include userinfo, query, fragment, or a base path")
	}
	parsed.Path = ""
	return parsed.String(), nil
}
