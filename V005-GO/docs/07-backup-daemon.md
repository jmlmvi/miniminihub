# Daemon de backup généralisé — `lmvi-backup`

---

## Objectif

`lmvi-backup` est un daemon Go qui orchestre le backup de **toutes les TechDB** des instances Socle V005 déployées. Il appelle le `TechDbBackupWorker` (Java) de chaque instance, récupère les dumps, les stocke dans le backend configuré (S3, GCS, SFTP, local), et gère la rétention.

---

## Architecture du daemon

```
┌──────────────────────────────────────────────────────────────┐
│  lmvi-backup                                                  │
│                                                               │
│  main.go                                                      │
│    └── run(ctx, cfg)                                          │
│          └── Scheduler.Run(ctx)                               │
│                └── BackupOrchestrator.RunAll(ctx)             │
│                      ├── InstanceRunner.Run(ctx, instance_1) │
│                      ├── InstanceRunner.Run(ctx, instance_2) │
│                      └── InstanceRunner.Run(ctx, instance_N) │
│                                                               │
│  InstanceRunner (pour chaque instance Socle) :               │
│    1. DumpSQL        → TechDbBackupWorker                     │
│    2. GetDownloadInfo → TechDbBackupWorker                    │
│    3. DownloadStream → Gateway /api/techdb/backup/download/   │
│    4. VerifySHA256                                            │
│    5. Store          → S3 / GCS / SFTP / Local               │
│    6. CleanupStaging → TechDbBackupWorker                     │
│    7. ApplyRetention → Storage backend                        │
└──────────────────────────────────────────────────────────────┘
```

---

## Configuration multi-instances

Le daemon supporte plusieurs instances Socle dans un seul fichier de configuration :

```yaml
# config.yaml
instances:
  - name: "appdemo"
    base_url: "https://appdemo.thesocle.net"
    api_key: ""                         # surcharger avec LMVI_APPDEMO_API_KEY
    timeout_seconds: 300                # 5 min pour les gros dumps
    backup:
      compress: true
      tables: []                        # vide = toutes les tables
      filename_prefix: "appdemo"

  - name: "smartreach"
    base_url: "https://smartreach.thesocle.net"
    api_key: ""                         # surcharger avec LMVI_SMARTREACH_API_KEY
    timeout_seconds: 300
    backup:
      compress: true
      filename_prefix: "smartreach"

schedule:
  cron: "0 3 * * *"                    # Tous les jours à 3h UTC
  # OU
  interval: "6h"                       # Toutes les 6 heures

storage:
  type: "s3"
  s3:
    bucket: "lmvi-backups-prod"
    region: "eu-west-3"
    prefix: "techdb/"
    # Credentials via variables d'environnement AWS standard (AWS_ACCESS_KEY_ID, etc.)

retention:
  keep_daily: 7                        # 7 derniers jours
  keep_weekly: 4                       # 4 dernières semaines (dimanche)
  keep_monthly: 12                     # 12 derniers mois (1er du mois)

notify:
  on_failure: true
  on_success: false
  webhook_url: ""                      # Slack, Teams, ou endpoint custom
  email: ""

log:
  level: "info"
  format: "json"
```

---

## Struct de configuration

```go
// internal/config/config.go
type Config struct {
    Instances []InstanceConfig `yaml:"instances"`
    Schedule  ScheduleConfig   `yaml:"schedule"`
    Storage   StorageConfig    `yaml:"storage"`
    Retention RetentionConfig  `yaml:"retention"`
    Notify    NotifyConfig     `yaml:"notify"`
    Log       LogConfig        `yaml:"log"`
}

type InstanceConfig struct {
    Name           string       `yaml:"name"`
    BaseURL        string       `yaml:"base_url"`
    APIKey         string       `yaml:"api_key"`
    TimeoutSeconds int          `yaml:"timeout_seconds"`
    Backup         BackupParams `yaml:"backup"`
}

type BackupParams struct {
    Compress       bool     `yaml:"compress"`
    Tables         []string `yaml:"tables"`
    FilenamePrefix string   `yaml:"filename_prefix"`
}

type ScheduleConfig struct {
    Cron     string `yaml:"cron"`
    Interval string `yaml:"interval"`
}

type RetentionConfig struct {
    KeepDaily   int `yaml:"keep_daily"`
    KeepWeekly  int `yaml:"keep_weekly"`
    KeepMonthly int `yaml:"keep_monthly"`
}
```

---

## Orchestrateur

```go
// internal/backup/orchestrator.go
package backup

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "sync"

    "github.com/lmvi/lmvi-backup/internal/config"
    "github.com/lmvi/lmvi-backup/internal/storage"
)

// Orchestrator exécute le backup de toutes les instances configurées.
type Orchestrator struct {
    instances []*InstanceRunner
    logger    *slog.Logger
}

func NewOrchestrator(cfg *config.Config, store storage.Storer) (*Orchestrator, error) {
    runners := make([]*InstanceRunner, 0, len(cfg.Instances))

    for _, inst := range cfg.Instances {
        runner, err := NewInstanceRunner(inst, store, cfg.Retention)
        if err != nil {
            return nil, fmt.Errorf("init runner for %s: %w", inst.Name, err)
        }
        runners = append(runners, runner)
    }

    return &Orchestrator{
        instances: runners,
        logger:    slog.Default().With("component", "orchestrator"),
    }, nil
}

// RunAll exécute le backup de toutes les instances en parallèle.
// Les erreurs sont collectées — une instance en échec n'arrête pas les autres.
func (o *Orchestrator) RunAll(ctx context.Context) error {
    var (
        wg   sync.WaitGroup
        mu   sync.Mutex
        errs []error
    )

    for _, runner := range o.instances {
        runner := runner // capture
        wg.Add(1)
        go func() {
            defer wg.Done()
            if err := runner.Run(ctx); err != nil {
                mu.Lock()
                errs = append(errs, fmt.Errorf("instance %s: %w", runner.Name(), err))
                mu.Unlock()
            }
        }()
    }

    wg.Wait()

    if len(errs) > 0 {
        return fmt.Errorf("%d instance(s) failed: %w", len(errs), errors.Join(errs...))
    }
    return nil
}
```

---

## InstanceRunner — cycle complet d'un backup

```go
// internal/backup/instance_runner.go
package backup

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "log/slog"
    "time"

    "github.com/lmvi/lmvi-backup/internal/config"
    "github.com/lmvi/lmvi-backup/internal/socle"
    "github.com/lmvi/lmvi-backup/internal/storage"
)

type InstanceRunner struct {
    name      string
    cfg       config.InstanceConfig
    retention config.RetentionConfig
    techdb    *socle.TechDbBackupClient
    store     storage.Storer
    logger    *slog.Logger
}

func NewInstanceRunner(cfg config.InstanceConfig, store storage.Storer, ret config.RetentionConfig) (*InstanceRunner, error) {
    timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
    if cfg.TimeoutSeconds <= 0 {
        timeout = 300 * time.Second
    }

    socleClient, err := socle.NewClient(cfg.BaseURL, cfg.APIKey, timeout)
    if err != nil {
        return nil, fmt.Errorf("socle client: %w", err)
    }

    return &InstanceRunner{
        name:      cfg.Name,
        cfg:       cfg,
        retention: ret,
        techdb:    socle.NewTechDbBackupClient(socleClient),
        store:     store,
        logger:    slog.Default().With("component", "instance_runner", "instance", cfg.Name),
    }, nil
}

func (r *InstanceRunner) Name() string { return r.name }

// Run exécute le cycle complet de backup pour cette instance.
func (r *InstanceRunner) Run(ctx context.Context) error {
    start := time.Now()
    r.logger.Info("backup started")

    defer func() {
        r.logger.Info("backup finished",
            "duration_ms", time.Since(start).Milliseconds(),
        )
    }()

    // Étape 1 : Déclencher le dump
    r.logger.Info("requesting dump", "compress", r.cfg.Backup.Compress)
    dumpResult, err := r.techdb.DumpSQL(ctx, r.cfg.Backup.Compress, r.cfg.Backup.Tables)
    if err != nil {
        return fmt.Errorf("dump: %w", err)
    }
    r.logger.Info("dump ready",
        "file", dumpResult.FileName,
        "size_bytes", dumpResult.SizeBytes,
        "table_count", dumpResult.TableCount,
        "dump_duration_ms", dumpResult.DurationMs,
    )

    // Étape 2 : Récupérer l'URL de téléchargement
    downloadInfo, err := r.techdb.GetDownloadInfo(ctx, dumpResult.FileName)
    if err != nil {
        return fmt.Errorf("get download info: %w", err)
    }

    // Étape 3 : Télécharger et stocker (streaming + vérification SHA256)
    storageKey := r.storageKey(dumpResult.FileName)
    r.logger.Info("downloading and storing", "storage_key", storageKey)

    if err := r.downloadAndStore(ctx, downloadInfo, storageKey); err != nil {
        return fmt.Errorf("download and store: %w", err)
    }
    r.logger.Info("stored successfully", "key", storageKey)

    // Étape 4 : Nettoyer le staging Socle
    cleanup, err := r.techdb.CleanupStaging(ctx, 60)
    if err != nil {
        // Non-fatal : le staging sera nettoyé au prochain run
        r.logger.Warn("staging cleanup failed", "err", err)
    } else {
        r.logger.Info("staging cleaned",
            "deleted_files", cleanup.DeletedFiles,
            "freed_bytes", cleanup.TotalFreedBytes,
        )
    }

    // Étape 5 : Appliquer la politique de rétention
    if err := r.applyRetention(ctx); err != nil {
        r.logger.Warn("retention policy failed", "err", err)
        // Non-fatal
    }

    return nil
}

func (r *InstanceRunner) downloadAndStore(ctx context.Context, info *socle.DownloadInfo, key string) error {
    stream, _, err := r.techdb.DownloadDump(ctx, info.DownloadURL)
    if err != nil {
        return fmt.Errorf("open stream: %w", err)
    }
    defer stream.Close()

    // TeeReader : écrire vers storage ET calculer SHA256 en un seul passage
    h := sha256.New()
    tee := io.TeeReader(stream, h)

    if err := r.store.Store(ctx, key, tee); err != nil {
        return fmt.Errorf("store to backend: %w", err)
    }

    actual := hex.EncodeToString(h.Sum(nil))
    if actual != info.SHA256 {
        // Supprimer le fichier corrompu
        if delErr := r.store.Delete(ctx, key); delErr != nil {
            r.logger.Error("failed to delete corrupted backup", "key", key, "err", delErr)
        }
        return fmt.Errorf("SHA256 mismatch: expected %s got %s", info.SHA256, actual)
    }

    r.logger.Debug("SHA256 verified", "sha256", actual)
    return nil
}

// storageKey construit la clé de stockage finale :
// {instance}/{YYYY}/{MM}/{DD}/{filename}
func (r *InstanceRunner) storageKey(fileName string) string {
    now := time.Now().UTC()
    return fmt.Sprintf("%s/%d/%02d/%02d/%s",
        r.name,
        now.Year(), now.Month(), now.Day(),
        fileName,
    )
}

func (r *InstanceRunner) applyRetention(ctx context.Context) error {
    if r.retention.KeepDaily <= 0 && r.retention.KeepWeekly <= 0 && r.retention.KeepMonthly <= 0 {
        return nil // pas de rétention configurée
    }

    prefix := r.name + "/"
    files, err := r.store.List(ctx, prefix)
    if err != nil {
        return fmt.Errorf("list files: %w", err)
    }

    toDelete := applyRetentionPolicy(files, r.retention)

    for _, key := range toDelete {
        r.logger.Info("deleting old backup", "key", key)
        if err := r.store.Delete(ctx, key); err != nil {
            r.logger.Warn("delete failed", "key", key, "err", err)
        }
    }

    return nil
}
```

---

## Interface Storage

```go
// internal/storage/storage.go
package storage

import (
    "context"
    "io"
    "time"
)

// FileInfo décrit un fichier stocké.
type FileInfo struct {
    Key          string
    SizeBytes    int64
    LastModified time.Time
}

// Storer est l'interface implémentée par tous les backends de stockage.
type Storer interface {
    // Store stocke le contenu de r sous la clé key.
    Store(ctx context.Context, key string, r io.Reader) error
    // Delete supprime le fichier identifié par key.
    Delete(ctx context.Context, key string) error
    // List retourne les fichiers dont la clé commence par prefix.
    List(ctx context.Context, prefix string) ([]FileInfo, error)
    // Exists vérifie si un fichier existe.
    Exists(ctx context.Context, key string) (bool, error)
}
```

---

## Politique de rétention

```go
// internal/backup/retention.go
package backup

import (
    "sort"
    "time"

    "github.com/lmvi/lmvi-backup/internal/config"
    "github.com/lmvi/lmvi-backup/internal/storage"
)

// applyRetentionPolicy retourne les clés à supprimer selon la politique GFS
// (Grandfather-Father-Son : monthly, weekly, daily).
func applyRetentionPolicy(files []storage.FileInfo, cfg config.RetentionConfig) []string {
    if len(files) == 0 {
        return nil
    }

    // Trier du plus récent au plus ancien
    sort.Slice(files, func(i, j int) bool {
        return files[i].LastModified.After(files[j].LastModified)
    })

    now := time.Now().UTC()
    keep := make(map[string]bool)

    // Conserver les N derniers jours
    for _, f := range files {
        age := int(now.Sub(f.LastModified).Hours() / 24)
        if age < cfg.KeepDaily {
            keep[f.Key] = true
        }
    }

    // Conserver le premier fichier de chaque semaine (dimanche) sur N semaines
    seenWeeks := map[string]bool{}
    for _, f := range files {
        weekKey := f.LastModified.Format("2006-W") + fmt.Sprintf("%02d", isoWeek(f.LastModified))
        age := int(now.Sub(f.LastModified).Hours() / 24 / 7)
        if !seenWeeks[weekKey] && age < cfg.KeepWeekly {
            keep[f.Key] = true
            seenWeeks[weekKey] = true
        }
    }

    // Conserver le premier fichier de chaque mois sur N mois
    seenMonths := map[string]bool{}
    for _, f := range files {
        monthKey := f.LastModified.Format("2006-01")
        age := monthsSince(f.LastModified, now)
        if !seenMonths[monthKey] && age < cfg.KeepMonthly {
            keep[f.Key] = true
            seenMonths[monthKey] = true
        }
    }

    // Retourner les fichiers non conservés
    var toDelete []string
    for _, f := range files {
        if !keep[f.Key] {
            toDelete = append(toDelete, f.Key)
        }
    }
    return toDelete
}
```

---

## Notifications

```go
// internal/notify/notify.go
package notify

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

type Message struct {
    Instance string
    Success  bool
    Error    string
    Duration time.Duration
    Details  map[string]interface{}
}

type WebhookNotifier struct {
    url        string
    httpClient *http.Client
}

func (n *WebhookNotifier) Notify(ctx context.Context, msg Message) error {
    payload := map[string]interface{}{
        "text":     n.formatText(msg),
        "instance": msg.Instance,
        "success":  msg.Success,
        "duration": msg.Duration.String(),
    }
    if msg.Error != "" {
        payload["error"] = msg.Error
    }

    body, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := n.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("webhook POST: %w", err)
    }
    resp.Body.Close()

    if resp.StatusCode >= 400 {
        return fmt.Errorf("webhook returned %d", resp.StatusCode)
    }
    return nil
}

func (n *WebhookNotifier) formatText(msg Message) string {
    if msg.Success {
        return fmt.Sprintf("✅ Backup *%s* completed in %s", msg.Instance, msg.Duration)
    }
    return fmt.Sprintf("❌ Backup *%s* FAILED: %s", msg.Instance, msg.Error)
}
```

---

## Variables d'environnement

| Variable | Description | Défaut |
|----------|-------------|--------|
| `LMVI_CONFIG_PATH` | Chemin vers config.yaml | `config.yaml` |
| `LMVI_{INSTANCE}_API_KEY` | API key pour l'instance (ex: `LMVI_APPDEMO_API_KEY`) | — |
| `LMVI_LOG_LEVEL` | Niveau de log | `info` |
| `LMVI_LOG_FORMAT` | Format de log | `json` |
| `AWS_ACCESS_KEY_ID` | AWS credentials (si storage S3) | — |
| `AWS_SECRET_ACCESS_KEY` | AWS credentials | — |
| `AWS_REGION` | AWS region | — |

---

## Codes de sortie

| Code | Signification |
|------|--------------|
| `0` | Tous les backups ont réussi |
| `1` | Erreur fatale (config invalide, impossible de démarrer) |
| `2` | Au moins un backup a échoué (les autres ont réussi) |

Les codes permettent l'intégration dans des systèmes de monitoring (Nagios, Prometheus Alertmanager).
