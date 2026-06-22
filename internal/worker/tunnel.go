// Package worker contient les Workers du miniMiniHub (1 worker = 1 fichier).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// TunnelWorker maintient le canal sortant : heartbeat + pollcommand + reconnexion.
// Toujours actif (priorité 100). Phase 0 = walking skeleton.
type TunnelWorker struct {
	log    *slog.Logger
	client *tunnel.Client
	hbMs   int
}

// NewTunnel construit le worker tunnel.
func NewTunnel() *TunnelWorker { return &TunnelWorker{} }

func (w *TunnelWorker) Name() string       { return "tunnel" }
func (w *TunnelWorker) StartPriority() int { return 100 }

func (w *TunnelWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "tunnel")
	w.hbMs = d.Cfg.HeartbeatMs
	w.client = tunnel.New(d.Cfg.ParentEndpoint, d.Cfg.MiniminihubID, d.Cfg.Slug, d.Log)
	return nil
}

// Run connecte le tunnel et le maintient ; reconnecte sur erreur (la supervision
// du MOP gère le backoff inter-session ; ici on couvre la durée d'une session).
func (w *TunnelWorker) Run(ctx context.Context) error {
	if err := w.client.Connect(ctx); err != nil {
		return err
	}
	defer w.client.Close()
	return w.session(ctx)
}

// session lance heartbeat + pollcommand en parallèle et retourne à la première
// erreur (ou à l'arrêt du context).
func (w *TunnelWorker) session(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	// Heartbeat périodique (R02/R05 : borné par context, select sur Done).
	go func() {
		ticker := time.NewTicker(time.Duration(w.hbMs) * time.Millisecond)
		defer ticker.Stop()
		var seq uint64

		send := func() error {
			seq++
			resp, err := w.client.Heartbeat(sctx, seq)
			if err != nil {
				return err
			}
			w.log.Info("heartbeat ack", "seq", seq, "accepted", resp.Accepted,
				"next_interval_ms", resp.NextIntervalMs, "trace_id", resp.TraceId)
			return nil
		}
		if err := send(); err != nil { // premier battement immédiat
			errCh <- fmt.Errorf("heartbeat: %w", err)
			return
		}
		for {
			select {
			case <-sctx.Done():
				return
			case <-ticker.C:
				if err := send(); err != nil {
					errCh <- fmt.Errorf("heartbeat: %w", err)
					return
				}
			}
		}
	}()

	// PollCommand : réception des commandes poussées par le parent.
	go func() {
		errCh <- w.client.Poll(sctx, w.handleCommand)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// handleCommand traite une commande descendue (Phase 0 : Ping no-op).
func (w *TunnelWorker) handleCommand(cmd *pb.Command) {
	switch p := cmd.Payload.(type) {
	case *pb.Command_Ping:
		w.log.Info("PING received from parent", "command_id", cmd.CommandId, "note", p.Ping.Note)
	default:
		w.log.Warn("unknown command", "command_id", cmd.CommandId)
	}
}
