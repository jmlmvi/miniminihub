# Erreurs et Logs

---

## Gestion des erreurs

### Règle fondamentale : traiter ou wrapper, jamais ignorer

```go
// INCORRECT — erreur ignorée
result, _ := client.DumpSQL(ctx, true)

// INCORRECT — erreur "avalée"
if err := store.Save(ctx, data); err != nil {
    // TODO: handle
}

// CORRECT — wrapper avec contexte
result, err := client.DumpSQL(ctx, true)
if err != nil {
    return fmt.Errorf("dump SQL for worker %s: %w", workerName, err)
}
```

### Wrapping avec `%w`

Toujours wrapper avec `fmt.Errorf("contexte: %w", err)` pour préserver la chaîne d'erreurs et permettre `errors.Is` / `errors.As`.

```go
// Chaîne d'erreurs lisible
// "backup run: dump SQL for worker techdb_backup_worker: HTTP 503: service unavailable"
return fmt.Errorf("backup run: %w",
    fmt.Errorf("dump SQL for worker %s: %w", workerName,
        fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)))
```

Le message de wrapping décrit **l'opération qui a échoué**, pas l'erreur elle-même (l'erreur parle d'elle-même) :

```go
// CORRECT — décrit l'opération
return fmt.Errorf("read config file %s: %w", path, err)
return fmt.Errorf("connect to Socle %s: %w", baseURL, err)

// INCORRECT — répète l'erreur
return fmt.Errorf("error reading config: %w", err)
return fmt.Errorf("failed to connect: %w", err)
```

### Types d'erreurs personnalisés

Pour les erreurs qui doivent être inspectées par l'appelant :

```go
// internal/socle/errors.go
package socle

import "fmt"

// APIError représente une réponse d'erreur de l'API Socle.
type APIError struct {
    StatusCode int
    ErrorCode  string // code métier Socle (ex: "CONCURRENT_DUMP_IN_PROGRESS")
    Message    string
    Operation  string
}

func (e *APIError) Error() string {
    return fmt.Sprintf("[%s] HTTP %d %s: %s", e.Operation, e.StatusCode, e.ErrorCode, e.Message)
}

// IsRetryable indique si l'erreur peut être réessayée.
func (e *APIError) IsRetryable() bool {
    return e.StatusCode >= 500 && e.StatusCode != 501
}

// Utilisation côté appelant
var apiErr *socle.APIError
if errors.As(err, &apiErr) {
    if apiErr.ErrorCode == "CONCURRENT_DUMP_IN_PROGRESS" {
        // Attendre et réessayer plus tard
        return nil // pas un échec définitif
    }
}
```

### Erreurs sentinelles

```go
var (
    ErrNotConfigured   = errors.New("socle client not configured")
    ErrNoAPIKey        = errors.New("API key is required")
    ErrDumpInProgress  = errors.New("dump already in progress")
    ErrStorageFull     = errors.New("storage quota exceeded")
)

// Utilisation
if errors.Is(err, ErrDumpInProgress) {
    slog.Warn("dump already running, skipping this cycle")
    return nil
}
```

### Accumulation d'erreurs (multi-opérations)

Quand plusieurs opérations indépendantes doivent toutes être tentées même si l'une échoue :

```go
// Pour Go 1.20+ : errors.Join
var errs []error
for _, worker := range workers {
    if err := r.backupWorker(ctx, worker); err != nil {
        errs = append(errs, fmt.Errorf("worker %s: %w", worker, err))
    }
}
if len(errs) > 0 {
    return fmt.Errorf("backup cycle had %d failure(s): %w", len(errs), errors.Join(errs...))
}
```

---

## Logging avec `slog`

`slog` (stdlib depuis Go 1.21) est le logger standard. Pas de logrus, zap, ou zerolog.

### Initialisation au démarrage

```go
// cmd/lmvi-backup/main.go ou run()
func initLogger(cfg *config.LogConfig) {
    level := slog.LevelInfo
    switch cfg.Level {
    case "debug": level = slog.LevelDebug
    case "warn":  level = slog.LevelWarn
    case "error": level = slog.LevelError
    }

    var handler slog.Handler
    opts := &slog.HandlerOptions{Level: level}

    if cfg.Format == "json" {
        handler = slog.NewJSONHandler(os.Stdout, opts)
    } else {
        handler = slog.NewTextHandler(os.Stdout, opts)
    }

    slog.SetDefault(slog.New(handler))
}
```

### Format JSON de production

En production (`format: json`), chaque ligne de log est un objet JSON :

```json
{"time":"2026-04-17T03:00:01.123Z","level":"INFO","msg":"backup started","worker":"techdb_backup_worker","compress":true}
{"time":"2026-04-17T03:00:12.456Z","level":"INFO","msg":"dump completed","worker":"techdb_backup_worker","file":"techdb-dump-2026-04-17T030001Z.sql.gz","size_bytes":1048576,"duration_ms":11333}
{"time":"2026-04-17T03:00:15.789Z","level":"INFO","msg":"stored to S3","bucket":"lmvi-backups","key":"techdb/techdb-dump-2026-04-17T030001Z.sql.gz"}
{"time":"2026-04-17T03:00:16.012Z","level":"INFO","msg":"backup completed","worker":"techdb_backup_worker","total_duration_ms":14889}
```

### Champs structurés obligatoires

Tous les logs d'opération incluent :

| Champ | Type | Description |
|-------|------|-------------|
| `component` | string | Composant qui logue (`scheduler`, `socle_client`, `s3_storage`) |
| `worker` | string | Nom du worker Socle concerné |
| `operation` | string | Opération en cours (`dump`, `store`, `cleanup`) |

```go
// Logger par composant — créé dans New...() et transmis
logger := slog.Default().With(
    "component", "backup_runner",
    "worker", workerName,
)

// Usage dans les méthodes
logger.Info("dump started", "compress", compress)
logger.Info("dump completed", "file", result.FileName, "size_bytes", result.SizeBytes, "duration_ms", duration.Milliseconds())
logger.Error("dump failed", "err", err, "attempt", attempt)
```

### Niveaux de log

| Niveau | Quand l'utiliser |
|--------|-----------------|
| `DEBUG` | Détails techniques utiles en développement (corps des requêtes HTTP, valeurs intermédiaires) |
| `INFO` | Événements normaux du cycle de vie (démarrage, fin d'opération, résumé de run) |
| `WARN` | Situation anormale mais récupérée (retry réussi, fallback déclenché, donnée manquante ignorée) |
| `ERROR` | Échec d'une opération, action requise (backup échoué, Socle inaccessible) |

### Ce qu'on NE logue pas

- Clés API, tokens, mots de passe — jamais
- Corps de réponse Socle en entier si volumineux (tronquer à 500 chars)
- Stacktraces en production (elles s'affichent si le programme panic — pas manuellement)

```go
// INCORRECT — logue la clé API
logger.Debug("request", "url", url, "api_key", apiKey)

// CORRECT — masqué
logger.Debug("request", "url", url, "api_key", "***")
```

### Logs de début et fin d'opération

Toute opération significative a un log de début et un log de fin avec durée :

```go
func (r *Runner) backupWorker(ctx context.Context, workerName string) error {
    start := time.Now()
    logger := r.logger.With("worker", workerName)

    logger.Info("backup started")
    defer func() {
        logger.Info("backup finished", "duration_ms", time.Since(start).Milliseconds())
    }()

    // ... opérations
    return nil
}
```

### Métriques via logs

Les métriques opérationnelles sont émises sous forme de logs JSON structurés (parsables par Grafana Loki, ELK, etc.) :

```go
// Métrique : taille du dump
slog.Info("metric",
    "metric_name",  "backup_dump_size_bytes",
    "value",        dumpResult.SizeBytes,
    "worker",       workerName,
    "compressed",   compress,
)

// Métrique : durée totale
slog.Info("metric",
    "metric_name",  "backup_duration_ms",
    "value",        duration.Milliseconds(),
    "worker",       workerName,
    "success",      err == nil,
)
```
