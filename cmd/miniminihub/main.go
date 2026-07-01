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

	"github.com/jmlmvi/miniminihub/internal/bus"
	"github.com/jmlmvi/miniminihub/internal/config"
	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
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
		"parent", cfg.ParentEndpoint, "mode", cfg.Mode, "roles", cfg.Roles,
		"mtls", cfg.TLS.Enabled, "store", cfg.StorePath)

	st, err := store.Open(cfg.StorePath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Canal gRPC sortant partagé (connexion paresseuse ; les RPC pilotent le dial).
	tun := tunnel.New(cfg.ParentEndpoint, cfg.MiniminihubID, cfg.Slug, cfg.TLS, log)
	if err := tun.Connect(ctx); err != nil {
		return fmt.Errorf("connect tunnel: %w", err)
	}
	defer tun.Close()

	deps := mop.Deps{Cfg: cfg, Log: log, Store: st, Bus: bus.New(), Tunnel: tun}

	// Noyau : TunnelWorker (100) + StateWorker (200). Rôles activés par bootstrap.roles[].
	workers := []mop.Worker{worker.NewTunnel(), worker.NewState()}
	if cfg.HasRole("proxy") {
		workers = append(workers, worker.NewProxy())
	}
	if cfg.HasRole("smtp") {
		workers = append(workers, worker.NewSmtp())
	}
	log.Info("workers registered", "count", len(workers), "roles", cfg.Roles)

	return mop.New(deps, workers...).Run(ctx)
}
