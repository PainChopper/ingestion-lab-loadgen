package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
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
			return cliCommand{}, errors.New("invalid serve arguments")
		case "--config":
			if command.configPath != "" || index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
				return cliCommand{}, errors.New("invalid serve arguments: --config requires one non-empty path")
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

func (c cliCommand) usage() string {
	if c.remoteAction == "set" {
		return cliSetUsage
	}
	if c.remoteAction == "" {
		return cliUsage
	}
	return fmt.Sprintf("usage: ingestion-lab-loadgen %s [--url <url>]", c.remoteAction)
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
	field         string
	actual        func(remoteSetSnapshot) any
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
		return command, errors.New("invalid set arguments")
	}
	setting, ok := remoteSetTargets()[args[1]]
	if !ok {
		return command, fmt.Errorf("unknown set target %q", args[1])
	}
	if len(args) == 3 && args[2] == "--help" {
		command.showUsage = true
		return command, nil
	}
	if len(args) != 3 && len(args) != 5 {
		return command, errors.New("invalid set arguments")
	}
	if args[2] == "" {
		return command, errors.New("invalid set arguments")
	}
	if len(args) == 5 {
		if args[3] != "--url" || args[4] == "" {
			return command, errors.New("invalid set arguments")
		}
		baseURL, err := validateCLIBaseURL(args[4])
		if err != nil {
			return command, err
		}
		command.remoteURL = baseURL
	}
	value, err := setting.parse(args[2])
	if err != nil {
		return command, fmt.Errorf("invalid %s value %q", args[1], args[2])
	}
	command.remoteSet = remoteSetCommand{
		target: args[1], requestAction: setting.action, value: value,
		field: setting.field, actual: setting.actual,
	}
	return command, nil
}

type remoteSetTarget struct {
	action, field string
	parse         func(string) (any, error)
	actual        func(remoteSetSnapshot) any
}

func remoteSetTargets() map[string]remoteSetTarget {
	return map[string]remoteSetTarget{
		"readers": {
			action: api.SetReaderWorkers, field: "reader.workers", parse: parseRemoteInteger,
			actual: func(s remoteSetSnapshot) any { return verificationValue(s.Reader.Workers) },
		},
		"senders": {
			action: api.SetSenderWorkers, field: "sender.workers", parse: parseRemoteInteger,
			actual: func(s remoteSetSnapshot) any { return verificationValue(s.Sender.Workers) },
		},
		"tps": {
			action: api.SetRequestedTPS, field: "throttler.requestedTps", parse: parseRemoteInteger,
			actual: func(s remoteSetSnapshot) any { return verificationValue(s.Throttler.RequestedTps) },
		},
		"throttle": {
			action: api.SetThrottlerInstalled, field: "throttler.installed", parse: parseRemoteBool,
			actual: func(s remoteSetSnapshot) any { return verificationValue(s.Throttler.Installed) },
		},
	}
}

func parseRemoteInteger(raw string) (any, error) { return strconv.Atoi(raw) }

func parseRemoteBool(raw string) (any, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return nil, errors.New("must be true or false")
	}
}

func verificationValue[T comparable](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

func validateCLIBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", errors.New("--url must be an absolute HTTP(S) URL with host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("--url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("--url must not include userinfo, query, fragment, or a base path")
	}
	parsed.Path = ""
	return parsed.String(), nil
}
