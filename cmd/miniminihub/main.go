// Commande miniminihub : agent relais frugal (Phase 0 = walking skeleton).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jmlmvi/miniminihub/internal/config"
	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/worker"
)

// version est injectée au build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	path := os.Getenv("MMH_BOOTSTRAP")
	if path == "" {
		path = "bootstrap.json"
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Info("miniminihub starting",
		"version", version, "slug", cfg.Slug, "id", cfg.MiniminihubID,
		"parent", cfg.ParentEndpoint, "mode", cfg.Mode, "roles", cfg.Roles)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := mop.Deps{Cfg: cfg, Log: log}

	// Phase 0 : seul le TunnelWorker. Les rôles (proxy/smtp/jobs) viennent ensuite.
	sup := mop.New(deps, worker.NewTunnel())

	return sup.Run(ctx)
}
