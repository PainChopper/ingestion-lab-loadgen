package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	command, err := parseCLI(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		_, _ = fmt.Fprintln(stderr, command.usage())
		return cliExitUsage
	}
	if command.showUsage {
		_, _ = fmt.Fprintln(stdout, command.usage())
		return cliExitSuccess
	}
	if command.remoteAction != "" {
		if err := runRemoteCLI(context.Background(), command, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return cliExitCode(err)
		}
		return cliExitSuccess
	}

	appCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := runServe(appCtx, command.configPath, command.runAfterStart); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return cliExitLocal
	}
	return cliExitSuccess
}
