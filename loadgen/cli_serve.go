package main

import "fmt"

const cliUsage = `usage:
  ingestion-lab-loadgen serve [--config <path>] [--run]
  ingestion-lab-loadgen snapshot [--url <url>]
  ingestion-lab-loadgen status [--url <url>]
  ingestion-lab-loadgen run [--url <url>]
  ingestion-lab-loadgen pause [--url <url>]
  ingestion-lab-loadgen resume [--url <url>]
  ingestion-lab-loadgen reset [--url <url>]
  ingestion-lab-loadgen set reader-workers <N> [--url <url>]
  ingestion-lab-loadgen set sender-workers <N> [--url <url>]
  ingestion-lab-loadgen set requested-tps <N> [--url <url>]
  ingestion-lab-loadgen set throttler-installed <true|false> [--url <url>]`

type cliCommand struct {
	configPath    string
	showUsage     bool
	runAfterStart bool
	remoteAction  string
	remoteURL     string
	remoteSet     remoteSetCommand
}

func parseCLI(args []string) (cliCommand, error) {
	if len(args) == 0 {
		return cliCommand{}, nil
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		return cliCommand{showUsage: true}, nil
	}

	if isRemoteCLIAction(args[0]) {
		return parseRemoteCLI(args)
	}
	if args[0] == "set" {
		return parseSetCLI(args)
	}
	if args[0] != "serve" {
		if len(args) == 1 {
			return cliCommand{configPath: args[0]}, nil
		}
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
			if command.configPath != "" || index+1 >= len(args) || args[index+1] == "" {
				return cliCommand{}, fmt.Errorf("invalid serve arguments: --config requires one non-empty path")
			}
			index++
			command.configPath = args[index]
		case "--run":
			if command.runAfterStart {
				return cliCommand{}, fmt.Errorf("--run may be specified once")
			}
			command.runAfterStart = true
		default:
			return cliCommand{}, fmt.Errorf("unexpected argument %q", args[index])
		}
	}
	return command, nil
}
