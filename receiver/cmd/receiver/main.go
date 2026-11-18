package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ingestion-lab-receiver/internal/receiver"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to the Receiver TOML configuration")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "receiver: unexpected positional arguments")
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	signalsDone := make(chan struct{})
	go func() {
		defer close(signalsDone)
		select {
		case <-signals:
		case <-ctx.Done():
		}
		signal.Stop(signals)
		cancel()
	}()
	err := run(ctx, *configPath)
	cancel()
	<-signalsDone
	if err != nil {
		fmt.Fprintln(os.Stderr, "receiver:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string) error {
	cfg, err := receiver.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
	}
	app, err := receiver.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       time.Minute,
		// A fixed write deadline could interrupt a configured lab response delay.
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", server.Addr, err)
	}

	done := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			done <- nil
			return
		}
		done <- fmt.Errorf("serve http: %w", err)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		shutdownErr := fmt.Errorf("shut down http server: %w", err)
		if err := server.Close(); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close http server: %w", err))
		}
		return errors.Join(shutdownErr, <-done)
	}
	return <-done
}
