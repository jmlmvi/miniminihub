# Principes — Go dans l'écosystème Socle V005

---

## 1. Rôle des programmes Go

Les programmes Go sont des **opérateurs autonomes** qui interagissent avec Socle V005 via ses interfaces publiques (REST, MCP). Ils ne partagent aucun code avec le backend Java.

```
┌──────────────────────────────────────────────────────────────┐
│  PROGRAMMES GO (daemons)                                      │
│                                                               │
│  lmvi-backup ──────────────────────────────────────────┐      │
│  lmvi-watchdog ─────────────────────────────────────┐  │      │
│  lmvi-exporter ──────────────────────────────────┐  │  │      │
│                                                  ↓  ↓  ↓      │
│              HTTPS / API Key                                  │
│  ════════════════════════════════════════════════════════════ │
│              Gateway Socle V005 (port 443)                    │
│  ════════════════════════════════════════════════════════════ │
│              Workers Java (TechDbBackupWorker, etc.)          │
└──────────────────────────────────────────────────────────────┘
```

**Ce que les programmes Go FONT :**
- Orchestrer des opérations longues (backup, export, surveillance)
- Réagir à des événements système (cron, signaux OS, webhooks)
- Transférer des données entre Socle et des systèmes externes (S3, GCS, SFTP)
- Surveiller et alerter sur l'état des instances Socle

**Ce que les programmes Go NE FONT PAS :**
- Implémenter de la logique métier (elle reste dans Socle V005)
- Accéder directement aux bases de données de Socle
- Remplacer les Workers Java — ils les *appellent*

---

## 2. Alignement avec Socle V005

| Concept Socle V005 | Équivalent Go |
|--------------------|---------------|
| Worker (unité fonctionnelle) | `cmd/{nom}/` — un binaire par programme |
| ActionProvider (interface MCP) | `internal/socle/client.go` — client HTTP centralisé |
| Lifecycle (initialize/start/stop) | `Run(ctx) error` + `os.Signal` + `context.WithCancel` |
| Configuration YAML | `config.yaml` + variables d'environnement |
| Logs MDC structurés | `slog` structuré avec champs constants |
| TechDB (état technique) | Fichier d'état local JSON ou SQLite embarqué |
| CircuitBreaker | `golang.org/x/time/rate` + retry avec backoff exponentiel |
| Graceful shutdown | `context.Context` propagé, `sync.WaitGroup` sur les goroutines |

### Un binaire = une responsabilité

Même règle que les Workers Socle : un programme Go a **une seule raison d'exister**. `lmvi-backup` fait du backup. `lmvi-watchdog` surveille. On ne combine pas.

### Explicite > implicite

Go favorise l'explicite. Pas de magie, pas de réflexion à l'exécution, pas de globals cachés. Chaque dépendance est injectée par le constructeur, chaque configuration est lue une fois au démarrage.

### Erreurs comme valeurs

Go traite les erreurs comme des valeurs de retour, pas comme des exceptions. Chaque erreur est traitée au point où elle se produit ou wrappée avec contexte et propagée. Ignorer une erreur est un bug intentionnel.

---

## 3. Version Go et toolchain

- **Version minimale : Go 1.22**
- Utiliser les fonctionnalités de la stdlib récente : `slog` (1.21+), `net/http` avec `http.ServeMux` amélioré (1.22+), `slices` et `maps` packages (1.21+)
- **Pas de dépendances externes non justifiées.** La stdlib Go est suffisante pour 90% des besoins. Une dépendance externe nécessite une justification explicite dans `go.mod` (commentaire ou ADR).

Dépendances autorisées sans justification :
- `github.com/aws/aws-sdk-go-v2` — S3, stockage cloud AWS
- `cloud.google.com/go/storage` — GCS
- `github.com/pkg/sftp` — SFTP
- `gopkg.in/yaml.v3` — lecture YAML config
- `github.com/spf13/cobra` — CLI si le programme a plusieurs sous-commandes
- `golang.org/x/crypto` — crypto complémentaire stdlib

---

## 4. Relation avec TechDbBackupWorker

Le daemon `lmvi-backup` est le **client Go du TechDbBackupWorker** (Java, Socle V005). Il n'implémente pas le backup lui-même — il orchestre en appelant les 9 actions MCP :

```
lmvi-backup (Go)                    TechDbBackupWorker (Java)
     │                                        │
     ├── POST /api/actions/.../techdb_dump_sql ──→ produit le fichier dans staging
     ├── POST /api/actions/.../techdb_dump_download ──→ récupère l'URL
     ├── GET  /api/techdb/backup/download/{file} ──→ stream le fichier
     ├── POST /api/actions/.../techdb_staging_cleanup ──→ purge
     └── (stockage local/S3/GCS)
```

Le daemon Go est responsable de :
- La planification (cron ou intervalle)
- Le stockage final (local, S3, GCS, SFTP)
- La rotation et la rétention des backups
- Les alertes en cas d'échec
- Le logging et les métriques
