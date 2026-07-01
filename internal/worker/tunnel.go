// Package worker contient les Workers du miniMiniHub (1 worker = 1 fichier).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmlmvi/miniminihub/internal/bus"
	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
	"github.com/jmlmvi/miniminihub/internal/tunnel"
	pb "github.com/jmlmvi/miniminihub/proto/mmhpb"
)

// TopicEgress = topic bus des demandes d'ouverture de sortie (proxy).
const TopicEgress = "egress"

// TopicRotate = topic bus des demandes de rotation TOR (NEWNYM, V002 P2).
const TopicRotate = "egress_rotate"

// TopicSmtp = topic bus des demandes de remise SMTP (rôle smtp, V002 P3).
const TopicSmtp = "smtp_send"

// TunnelWorker maintient le canal sortant partagé : heartbeat + pollcommand.
// Le canal gRPC lui-même est connecté dans main et partagé via Deps.Tunnel.
// Toujours actif (priorité 100).
type TunnelWorker struct {
	log    *slog.Logger
	store  *store.Store
	bus    *bus.Bus
	client *tunnel.Client
	hbMs   int
}

// NewTunnel construit le worker tunnel.
func NewTunnel() *TunnelWorker { return &TunnelWorker{} }

func (w *TunnelWorker) Name() string       { return "tunnel" }
func (w *TunnelWorker) StartPriority() int { return 100 }

func (w *TunnelWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "tunnel")
	w.store = d.Store
	w.bus = d.Bus
	w.client = d.Tunnel
	w.hbMs = d.Cfg.HeartbeatMs
	return nil
}

// Run lance une session heartbeat + pollcommand sur le canal partagé.
// En cas d'erreur, retourne : le MOP relance (le canal gRPC se reconnecte seul).
func (w *TunnelWorker) Run(ctx context.Context) error {
	n, _ := w.store.Incr("tunnel_sessions")
	w.record("tunnel_session_start", fmt.Sprintf("session #%d", n))
	return w.session(ctx)
}

// session lance heartbeat + pollcommand en parallèle et retourne à la première
// erreur (ou à l'arrêt du context).
func (w *TunnelWorker) session(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		curMs := w.hbMs
		ticker := time.NewTicker(time.Duration(curMs) * time.Millisecond)
		defer ticker.Stop()
		var seq uint64
		prevCPU, prevOK := readCPUSample()

		send := func() error {
			seq++
			// Charge instantanée (léger) : %CPU = delta /proc/stat entre deux battements.
			load := tunnel.Load{MemPct: memPct(), DiskPct: diskPct("/"), Conns: connsByService()}
			if cur, ok := readCPUSample(); ok {
				if prevOK {
					if pct, ok2 := cpuPct(prevCPU, cur); ok2 {
						load.CPUPct = pct
					}
				}
				prevCPU, prevOK = cur, true
			}
			resp, err := w.client.Heartbeat(sctx, seq, load)
			if err != nil {
				return err
			}
			total, _ := w.store.Incr("heartbeats")
			w.log.Info("heartbeat ack", "seq", seq, "accepted", resp.Accepted,
				"next_interval_ms", resp.NextIntervalMs, "trace_id", resp.TraceId, "total_persisted", total)
			// Le mh dicte la cadence via next_interval_ms (présence rapide, CDC-1 :
			// évite le flapping HEALTHY/DEAD quand le bootstrap fixe un intervalle plus lent).
			if n := int(resp.NextIntervalMs); n > 0 && n != curMs {
				w.log.Info("adopte la cadence heartbeat du parent", "old_ms", curMs, "new_ms", n)
				curMs = n
				ticker.Reset(time.Duration(curMs) * time.Millisecond)
			}
			return nil
		}
		if err := send(); err != nil {
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

// handleCommand route une commande descendue vers le worker compétent (via bus).
func (w *TunnelWorker) handleCommand(cmd *pb.Command) {
	switch p := cmd.Payload.(type) {
	case *pb.Command_Ping:
		n, _ := w.store.Incr("pings_received")
		w.record("ping_received", cmd.CommandId)
		w.log.Info("PING received from parent", "command_id", cmd.CommandId,
			"note", p.Ping.Note, "total_persisted", n)
	case *pb.Command_EgressOpen:
		w.log.Info("egress open requested", "conn_id", p.EgressOpen.ConnId,
			"host", p.EgressOpen.Host, "port", p.EgressOpen.Port)
		w.bus.Publish(TopicEgress, p.EgressOpen)
	case *pb.Command_RotateEgress:
		w.log.Info("egress rotate (NEWNYM) requested", "request_id", p.RotateEgress.RequestId)
		w.bus.Publish(TopicRotate, p.RotateEgress)
	case *pb.Command_SmtpSend:
		w.log.Info("smtp send requested", "request_id", p.SmtpSend.RequestId, "rcpts", len(p.SmtpSend.RcptTo))
		w.bus.Publish(TopicSmtp, p.SmtpSend)
	default:
		w.log.Warn("unknown command", "command_id", cmd.CommandId)
	}
}

// record journalise un événement dans le store local (best-effort).
func (w *TunnelWorker) record(kind, detail string) {
	if err := w.store.AppendEvent(time.Now().UnixMilli(), kind, detail); err != nil {
		w.log.Warn("store append failed", "kind", kind, "err", err)
	}
}
