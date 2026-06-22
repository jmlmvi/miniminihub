// Package mop est le mini-noyau MOP/Worker (principes Socle V005 transposés en Go).
// Voir docs/ARCHI-GO-MOP-V001.md (D-16).
package mop

import (
	"context"
	"log/slog"

	"github.com/jmlmvi/miniminihub/internal/config"
	"github.com/jmlmvi/miniminihub/internal/store"
)

// Deps = dépendances injectées aux workers (jamais de global mutable — R03).
type Deps struct {
	Cfg   *config.Config
	Log   *slog.Logger
	Store *store.Store
}

// Worker = unité fonctionnelle autonome (équivalent Go d'un Worker Socle V005).
// 1 worker = 1 fichier.
type Worker interface {
	// Name identifie le worker dans les logs et la supervision.
	Name() string
	// StartPriority ordonne l'initialisation (petit = tôt).
	StartPriority() int
	// Init prépare le worker (abonnements, ressources). PAS de boucle ici.
	Init(ctx context.Context, d Deps) error
	// Run exécute le worker. Bloque jusqu'à ctx.Done() (arrêt propre) ou erreur fatale.
	Run(ctx context.Context) error
}
