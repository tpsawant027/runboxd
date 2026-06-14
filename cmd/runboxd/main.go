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
	"github.com/tpsawant027/runboxd/internal/config"
	"github.com/tpsawant027/runboxd/internal/sandbox"
)

const reapInterval = 2 * time.Minute

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

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.AuthToken == "" {
		logger.Warn("running WITHOUT authentication - this is not recommended for production use", "auth_token_set", cfg.AuthToken != "")
	}

	pool := api.NewWorkerPool(cfg.WorkerPoolSize, cfg.MaxQueueSize)

	var sb sandbox.Sandbox

	if cfg.SandboxBackend == "nsjail" {
		var err error
		sb, err = sandbox.NewNsjailSandbox(cfg.RegistryPath, cfg.NsjailPath, cfg.RootfsPath, cfg.CgroupV2Mount, logger)
		if err != nil {
			return err
		}
	} else {
		var err error
		sb, err = sandbox.NewDockerSandbox(cfg.RegistryPath, logger)
		if err != nil {
			return err
		}
	}
	defer sb.Close()

	if reaper, ok := sb.(sandbox.Reaper); ok {
		reaperCtx, reaperCancel := context.WithCancel(context.Background())
		reaperDone := make(chan struct{})
		go func() { defer close(reaperDone); reapLoop(reaperCtx, reaper) }()
		defer func() { reaperCancel(); <-reaperDone }()
		reaper.ReapOrphans(reaperCtx)
	}

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.NewServer(logger, cfg.AuthToken, sb, pool).Routes(),
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

func reapLoop(ctx context.Context, sb sandbox.Reaper) {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sb.ReapOrphans(ctx)
		}
	}
}
