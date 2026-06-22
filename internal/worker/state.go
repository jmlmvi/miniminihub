package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jmlmvi/miniminihub/internal/mop"
	"github.com/jmlmvi/miniminihub/internal/store"
)

// StateWorker assemble périodiquement un snapshot d'état depuis le store local
// (compteurs persistés) et le journalise. Préfigure la remontée au Hub (graphe).
// Priorité 200 (après le tunnel).
type StateWorker struct {
	log    *slog.Logger
	store  *store.Store
	slug   string
	period time.Duration
}

// NewState construit le worker d'état.
func NewState() *StateWorker { return &StateWorker{} }

func (w *StateWorker) Name() string       { return "state" }
func (w *StateWorker) StartPriority() int { return 200 }

func (w *StateWorker) Init(_ context.Context, d mop.Deps) error {
	w.log = d.Log.With("worker", "state")
	w.store = d.Store
	w.slug = d.Cfg.Slug
	w.period = 10 * time.Second
	return nil
}

// Run émet un snapshot toutes les `period`, jusqu'à l'arrêt (R02/R05).
func (w *StateWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.period)
	defer ticker.Stop()
	w.snapshot() // immédiat
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.snapshot()
		}
	}
}

func (w *StateWorker) snapshot() {
	connects, _ := w.store.Counter("tunnel_connects")
	heartbeats, _ := w.store.Counter("heartbeats")
	pings, _ := w.store.Counter("pings_received")
	events, _ := w.store.CountEvents()
	w.log.Info("state snapshot",
		"slug", w.slug,
		"tunnel_connects", connects,
		"heartbeats", heartbeats,
		"pings_received", pings,
		"events_stored", events)
}
