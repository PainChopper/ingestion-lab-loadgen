package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Start binds addr, serves handler asynchronously and reports the server result on the returned channel.
func Start(addr string, handler http.Handler, logger *zap.Logger) (*http.Server, <-chan error, error) {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       time.Minute,
		// A fixed write deadline could interrupt pprof requests.
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen HTTP server: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		defer close(done)
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	logger.Info("service started", zap.String("event", "service_started"), zap.String("http_addr", listener.Addr().String()))
	return server, done, nil
}

// Shutdown drains active HTTP requests and closes any remaining connections on failure.
func Shutdown(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return closeErr
		}
		return errors.Join(err, closeErr)
	}
	return nil
}
