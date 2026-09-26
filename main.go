package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	command, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, cliUsage)
		return
	}
	if command.showUsage {
		fmt.Fprintln(os.Stdout, cliUsage)
		return
	}

	appCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := runServe(appCtx, command.configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
