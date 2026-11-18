package main

import (
	"fmt"
	"strings"
)

const cliUsage = `usage:
  ingestion-lab-loadgen serve [--config <path>]
  ingestion-lab-loadgen status [--url <url>]
  ingestion-lab-loadgen set readers <N> [--url <url>]
  ingestion-lab-loadgen set senders <N> [--url <url>]
  ingestion-lab-loadgen set tps <N> [--url <url>]
  ingestion-lab-loadgen set throttle <true|false> [--url <url>]`

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
