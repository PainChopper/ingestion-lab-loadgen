package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
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
	err := run(ctx, *configPath, os.Stdout)
	cancel()
	<-signalsDone
	if err != nil {
		fmt.Fprintln(os.Stderr, "receiver:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string, output io.Writer) error {
	cfg, err := receiver.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
	}
	logger, err := newApplicationLogger(cfg.Logging.Level, output)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	handler, err := receiver.NewHandler(cfg)
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       time.Minute,
		// A fixed write deadline could interrupt a configured lab response delay.
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		logger.Error("receiver failed to listen", "event", "http_server_failed", "http_addr", server.Addr, "error", err)
		return fmt.Errorf("listen on %q: %w", server.Addr, err)
	}
	logger.Info("receiver started", "event", "service_started", "http_addr", listener.Addr().String())
	defer logger.Info("receiver stopped", "event", "service_stopped")

	done := make(chan error, 1)
	go func() {
		defer close(done)
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			done <- nil
			return
		}
		done <- fmt.Errorf("serve http: %w", err)
	}()

	select {
	case err := <-done:
		if err != nil {
			logger.Error("receiver HTTP server failed", "event", "http_server_failed", "error", err)
		}
		return err
	case <-ctx.Done():
		logger.Info("receiver stopping", "event", "service_stopping")
	}

	return shutdownHTTPServer(server, done, cfg.Server.ShutdownTimeout, logger)
}

func shutdownHTTPServer(server *http.Server, done <-chan error, timeout time.Duration, logger *slog.Logger) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("receiver HTTP shutdown failed", "event", "http_shutdown_failed", "error", err)
		shutdownErr := fmt.Errorf("shut down http server: %w", err)
		if err := server.Close(); err != nil {
			logger.Error("receiver HTTP close failed", "event", "http_shutdown_failed", "error", err)
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close http server: %w", err))
		}
		return errors.Join(shutdownErr, <-done)
	}
	return <-done
}

func newApplicationLogger(level string, output io.Writer) (*slog.Logger, error) {
	var minimum slog.Level
	if err := minimum.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: minimum})), nil
}
