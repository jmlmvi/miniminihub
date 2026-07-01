# Concurrence — Goroutines, channels, shutdown

---

## Règle fondamentale

**Chaque goroutine lancée a une durée de vie contrôlée.** Si une goroutine peut fuir (ne jamais se terminer), c'est un bug. Le `context.Context` est le mécanisme universel de contrôle.

---

## Shutdown gracieux — pattern obligatoire

Tout programme LMVI implémente un shutdown gracieux via `context` et `os/signal`.

```go
// cmd/lmvi-backup/main.go
func main() {
    cfg, err := config.Load()
    if err != nil {
        fmt.Fprintf(os.Stderr, "config: %v\n", err)
        os.Exit(1)
    }

    // Créer un context annulable sur SIGINT/SIGTERM
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := run(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
        slog.Error("fatal error", "err", err)
        os.Exit(1)
    }
    slog.Info("shutdown complete")
}

func run(ctx context.Context, cfg *config.Config) error {
    runner, err := backup.NewRunner(cfg)
    if err != nil {
        return fmt.Errorf("init runner: %w", err)
    }

    // Lancer en background avec WaitGroup
    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
            slog.Error("runner error", "err", err)
        }
    }()

    // Attendre le signal
    <-ctx.Done()
    slog.Info("shutdown signal received, draining...")

    // Attendre max 30s que le runner finisse proprement
    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-time.After(30 * time.Second):
        return fmt.Errorf("shutdown timeout: goroutines still running after 30s")
    }
}
```

---

## Context — règles d'utilisation

### Context comme premier paramètre

Toute fonction qui peut bloquer, faire un I/O réseau, ou être annulée reçoit `ctx context.Context` en **premier paramètre**.

```go
// CORRECT
func (c *SocleClient) DumpSQL(ctx context.Context, compress bool) (*DumpResult, error)
func (s *S3Storage) Store(ctx context.Context, name string, r io.Reader) error

// INCORRECT — context oublié
func (c *SocleClient) DumpSQL(compress bool) (*DumpResult, error)
```

### Ne jamais stocker un context dans une struct

```go
// INCORRECT
type SocleClient struct {
    ctx context.Context  // jamais
}

// CORRECT : context passé à chaque appel de méthode
func (c *SocleClient) Call(ctx context.Context, ...) error
```

### Vérifier l'annulation

```go
// Dans une boucle longue, vérifier régulièrement
for _, worker := range workers {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
    }

    if err := r.backupWorker(ctx, worker); err != nil {
        slog.Error("backup failed", "worker", worker, "err", err)
        // Continuer avec le worker suivant — ne pas abandonner l'ensemble
    }
}
```

### Timeout par opération

Chaque appel réseau a son propre timeout dérivé du context parent :

```go
func (c *SocleClient) DumpSQL(ctx context.Context, compress bool) (*DumpResult, error) {
    // Timeout spécifique pour le dump (peut être long)
    dumpCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
    defer cancel()

    req, err := http.NewRequestWithContext(dumpCtx, http.MethodPost, c.actionURL("techdb_backup_worker", "techdb_dump_sql"), body)
    // ...
}
```

---

## Goroutines — règles

### Toujours nommer les goroutines dans les logs

```go
go func() {
    slog.Info("scheduler started")
    defer slog.Info("scheduler stopped")

    for {
        select {
        case <-ctx.Done():
            return
        case t := <-ticker.C:
            slog.Debug("tick", "time", t)
            if err := runner.RunOnce(ctx); err != nil {
                slog.Error("backup cycle failed", "err", err)
            }
        }
    }
}()
```

### Channels directionnels

Toujours typer les channels en entrée/sortie dans les signatures :

```go
// CORRECT
func produce(ctx context.Context, out chan<- Item) error
func consume(ctx context.Context, in <-chan Item) error

// INCORRECT — bidirectionnel inutile
func produce(ctx context.Context, out chan Item) error
```

### Fermeture des channels

Seul le producteur ferme le channel. Jamais le consommateur.

```go
func producer(ctx context.Context, out chan<- string) {
    defer close(out)  // ferme quand le producteur a fini
    for _, item := range items {
        select {
        case out <- item:
        case <-ctx.Done():
            return
        }
    }
}

func consumer(ctx context.Context, in <-chan string) {
    for item := range in {  // range se termine quand in est fermé
        process(item)
    }
}
```

### Pas de goroutine sans WaitGroup ou channel de synchronisation

```go
// INCORRECT — goroutine non trackée
go func() {
    doSomething()  // si main() se termine, cette goroutine est tuée sans cleanup
}()

// CORRECT — trackée via WaitGroup
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    doSomething()
}()
wg.Wait()
```

---

## Scheduler — pattern pour les daemons périodiques

```go
// internal/backup/scheduler.go
package backup

import (
    "context"
    "fmt"
    "log/slog"
    "time"
)

type Scheduler struct {
    runner   Runner
    interval time.Duration
    logger   *slog.Logger
}

func NewScheduler(runner Runner, interval time.Duration) *Scheduler {
    return &Scheduler{
        runner:   runner,
        interval: interval,
        logger:   slog.Default().With("component", "scheduler"),
    }
}

// Run démarre la boucle de scheduling. Bloque jusqu'à ctx.Done().
func (s *Scheduler) Run(ctx context.Context) error {
    s.logger.Info("scheduler started", "interval", s.interval)

    // Premier run immédiat au démarrage
    if err := s.runOnce(ctx); err != nil {
        s.logger.Error("initial run failed", "err", err)
        // Continuer malgré l'erreur initiale
    }

    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            s.logger.Info("scheduler stopping")
            return nil
        case t := <-ticker.C:
            s.logger.Info("scheduled run", "at", t)
            if err := s.runOnce(ctx); err != nil {
                s.logger.Error("scheduled run failed", "err", err)
                // Ne pas arrêter le scheduler pour un run en échec
            }
        }
    }
}

func (s *Scheduler) runOnce(ctx context.Context) error {
    start := time.Now()
    err := s.runner.Run(ctx)
    duration := time.Since(start)

    if err != nil {
        s.logger.Error("run failed", "duration", duration, "err", err)
        return fmt.Errorf("run at %s: %w", start.Format(time.RFC3339), err)
    }

    s.logger.Info("run completed", "duration", duration)
    return nil
}
```

---

## sync.Mutex — règles

```go
type State struct {
    mu          sync.Mutex
    lastRun     time.Time
    runCount    int
    inProgress  bool
}

// Méthode avec lock
func (s *State) StartRun() bool {
    s.mu.Lock()
    defer s.mu.Unlock()  // defer IMMÉDIATEMENT après Lock

    if s.inProgress {
        return false
    }
    s.inProgress = true
    return true
}

func (s *State) EndRun() {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.inProgress = false
    s.lastRun = time.Now()
    s.runCount++
}
```

**Règles mutex :**
- `defer mu.Unlock()` immédiatement après `mu.Lock()`
- Ne jamais appeler une fonction externe sous un mutex (deadlock possible)
- Mutex dans la struct qu'il protège, pas ailleurs
- `sync.RWMutex` si les lectures sont fréquentes et les écritures rares

---

## Erreurs de concurrence fréquentes à éviter

### Fermeture sur une variable de boucle

```go
// INCORRECT (Go < 1.22 — le problème est résolu en 1.22)
for _, worker := range workers {
    go func() {
        backup(worker)  // worker capturé par référence
    }()
}

// CORRECT (toutes versions)
for _, worker := range workers {
    worker := worker  // copie locale
    go func() {
        backup(worker)
    }()
}
```

### Double-close d'un channel

```go
// INCORRECT — panic si close appelé deux fois
close(ch)
close(ch)

// CORRECT — utiliser sync.Once si plusieurs goroutines peuvent fermer
var once sync.Once
closeOnce := func() { once.Do(func() { close(ch) }) }
```
