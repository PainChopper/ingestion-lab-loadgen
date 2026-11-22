// Package cli parses and executes LoadGen command-line commands.
package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PainChopper/ingestion-lab-loadgen/loadgen/internal/api"
)

// Run executes the requested command and returns its process exit code.
// The serve callback starts the local service for the requested configuration path.
func Run(args []string, stdout, stderr io.Writer, serve func(configPath string) error) int {
	var diagnostics bytes.Buffer
	command, err := parseCommand(args, &diagnostics)
	if err == flag.ErrHelp {
		_, _ = diagnostics.WriteTo(stdout)
		return cliExitSuccess
	}
	if err != nil {
		if diagnostics.Len() != 0 {
			_, _ = diagnostics.WriteTo(stderr)
		} else {
			_, _ = fmt.Fprintln(stderr, err)
			_, _ = fmt.Fprintln(stderr, command.usage())
		}
		return cliExitUsage
	}
	if command.remoteAction != "" {
		if err := runRemoteCLI(context.Background(), command, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return cliExitCode(err)
		}
		return cliExitSuccess
	}

	if err := serve(command.configPath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return cliExitLocal
	}
	return cliExitSuccess
}

const cliUsage = `usage: ingestion-lab-loadgen <command> [arguments]

  serve [--config <path>]    start with config.toml or the specified file
  status                     show the current run status
  set readers <N>            set Reader workers
  set senders <N>            set Sender workers
  set tps <N>                set target TPS
  set throttle <true|false>  enable or bypass the TPS limit

  --url <url>  for status and set (default: http://127.0.0.1:8080)
  --help       show this help`

type command struct {
	configPath   string
	remoteAction string
	remoteURL    string
	remoteSet    remoteSetCommand
}

func parseCommand(args []string, output io.Writer) (command, error) {
	if len(args) == 0 {
		return command{}, nil
	}

	switch args[0] {
	case "serve":
		return parseServe(newCommandFlags("serve", output, cliUsage), args[1:])
	case "status":
		cmd := command{remoteAction: "status"}
		return parseURL(cmd, newCommandFlags("status", output, cmd.usage()), args[1:])
	case "set":
		return parseSet(newCommandFlags("set", output, cliSetUsage), args[1:])
	}

	fs := newCommandFlags("ingestion-lab-loadgen", output, cliUsage)
	if err := fs.Parse(args); err != nil {
		return command{}, err
	}
	if fs.NArg() == 1 && fs.Arg(0) == "help" {
		fs.Usage()
		return command{}, flag.ErrHelp
	}
	return command{}, fmt.Errorf("unexpected command %q", args[0])
}

func parseServe(fs *flag.FlagSet, args []string) (command, error) {
	path := fs.String("config", "", "path to the LoadGen TOML configuration")
	if err := fs.Parse(args); err != nil {
		return command{}, err
	}
	if *path == "" && fs.NFlag() != 0 {
		return command{}, errors.New("invalid serve arguments: --config requires one non-empty path")
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return command{configPath: *path}, nil
}

func newCommandFlags(name string, output io.Writer, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() { _, _ = fmt.Fprintln(fs.Output(), usage) }
	return fs
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

func (c command) usage() string {
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

func parseURL(cmd command, fs *flag.FlagSet, args []string) (command, error) {
	rawURL := fs.String("url", defaultCLIBaseURL, "base URL of the LoadGen HTTP API")
	if err := fs.Parse(args); err != nil {
		return cmd, err
	}
	if fs.NArg() != 0 || *rawURL == "" {
		return cmd, fmt.Errorf("invalid %s arguments", cmd.remoteAction)
	}
	baseURL, err := validateCLIBaseURL(*rawURL)
	if err != nil {
		return cmd, err
	}
	cmd.remoteURL = baseURL
	return cmd, nil
}

func parseSet(fs *flag.FlagSet, args []string) (command, error) {
	cmd := command{remoteAction: "set"}
	var target, rawValue string
	var setting remoteSetTarget
	if len(args) != 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
		var ok bool
		setting, ok = remoteSetTargets()[target]
		if !ok {
			return cmd, fmt.Errorf("unknown set target %q", target)
		}
		if len(args) != 0 {
			// A negative integer occupies the value position before the flags.
			_, err := strconv.Atoi(args[0])
			if err == nil || !strings.HasPrefix(args[0], "-") {
				rawValue, args = args[0], args[1:]
			}
		}
	}
	cmd, err := parseURL(cmd, fs, args)
	if err != nil {
		return cmd, err
	}
	if rawValue == "" {
		return cmd, errors.New("invalid set arguments")
	}
	value, err := setting.parse(rawValue)
	if err != nil {
		return cmd, fmt.Errorf("invalid %s value %q", target, rawValue)
	}
	cmd.remoteSet = remoteSetCommand{
		target: target, requestAction: setting.action, value: value,
		field: setting.field, actual: setting.actual,
	}
	return cmd, nil
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
