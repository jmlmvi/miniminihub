package mop

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Supervisor = le MOP : ordonne, initialise, exécute et supervise les workers.
type Supervisor struct {
	workers []Worker
	deps    Deps
	log     *slog.Logger
}

// New construit un Supervisor avec ses workers.
func New(deps Deps, workers ...Worker) *Supervisor {
	return &Supervisor{
		workers: workers,
		deps:    deps,
		log:     deps.Log.With("component", "mop"),
	}
}

// Run initialise les workers par priorité puis les exécute concurremment,
// avec restart-par-backoff. Bloque jusqu'à ctx.Done().
func (s *Supervisor) Run(ctx context.Context) error {
	sort.SliceStable(s.workers, func(i, j int) bool {
		return s.workers[i].StartPriority() < s.workers[j].StartPriority()
	})

	// Phase Init, ordonnée.
	for _, w := range s.workers {
		s.log.Info("init worker", "worker", w.Name(), "priority", w.StartPriority())
		if err := w.Init(ctx, s.deps); err != nil {
			return fmt.Errorf("init worker %s: %w", w.Name(), err)
		}
	}

	// Phase Run, concurrente + supervisée.
	var wg sync.WaitGroup
	for _, w := range s.workers {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.superviseRun(ctx, w)
		}()
	}

	<-ctx.Done()
	s.log.Info("shutdown signal received, draining workers")

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		s.log.Info("shutdown complete")
		return nil
	case <-time.After(30 * time.Second): // R12
		return fmt.Errorf("shutdown timeout: workers still running after 30s")
	}
}

// superviseRun relance le worker avec backoff borné tant que le context vit.
func (s *Supervisor) superviseRun(ctx context.Context, w Worker) {
	const (
		base = 1 * time.Second
		max  = 30 * time.Second
	)
	backoff := base
	log := s.log.With("worker", w.Name())

	for {
		if ctx.Err() != nil {
			return
		}
		err := w.Run(ctx)
		if ctx.Err() != nil {
			return // arrêt propre demandé
		}
		if err != nil {
			log.Error("worker exited with error, will restart", "err", err, "backoff", backoff)
		} else {
			log.Warn("worker returned without error, restarting", "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff): // R05 : jamais de Sleep nu
		}
		if backoff *= 2; backoff > max {
			backoff = max
		}
	}
}
