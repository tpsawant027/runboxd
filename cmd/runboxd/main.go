// Command runboxd is an HTTP daemon that runs untrusted code inside sandboxes
// and returns structured execution reports.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tpsawant027/runboxd/internal/api"
	"github.com/tpsawant027/runboxd/internal/sandbox"
)

func main() {
	if err := run(); err != nil {
		slog.Error("runboxd exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	sb, err := sandbox.NewDockerSandbox()
	if err != nil {
		return err
	}
	defer sb.Close()

	addr := ":8080" // TODO: env-driven config as a cross-cutting concern (S4).
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(logger, sb).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("runboxd listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// S4: drain in-flight jobs from the worker pool before exiting.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return err
	}
	logger.Info("shutdown complete")
	return nil
}
