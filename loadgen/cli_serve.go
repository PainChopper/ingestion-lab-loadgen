package main

import (
	"fmt"
	"strings"
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
