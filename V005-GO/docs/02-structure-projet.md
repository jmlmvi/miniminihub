# Structure de projet

---

## Layout standard

Tous les programmes Go LMVI suivent le même layout, dérivé de la convention Go communautaire (pas du "Standard Layout" non officiel).

```
lmvi-backup/
├── cmd/
│   └── lmvi-backup/
│       └── main.go              ← Point d'entrée — uniquement parse flags, init config, appelle run()
│
├── internal/                    ← Code privé, non importable par d'autres modules
│   ├── backup/                  ← Logique principale du programme
│   │   ├── runner.go            ← Runner : orchestration du cycle de backup
│   │   ├── runner_test.go
│   │   ├── scheduler.go         ← Planification cron/intervalle
│   │   └── scheduler_test.go
│   ├── socle/                   ← Client Socle V005
│   │   ├── client.go            ← HTTP client (actions REST/MCP)
│   │   ├── client_test.go
│   │   └── types.go             ← DTOs des réponses Socle
│   ├── storage/                 ← Backends de stockage
│   │   ├── storage.go           ← Interface Storage
│   │   ├── local.go             ← Implémentation locale (filesystem)
│   │   ├── s3.go                ← Implémentation AWS S3
│   │   └── gcs.go               ← Implémentation GCS
│   ├── config/
│   │   ├── config.go            ← Struct Config + chargement YAML + env
│   │   └── config_test.go
│   └── notify/
│       └── notify.go            ← Alertes (email, webhook, Slack)
│
├── config.yaml.example          ← Exemple de configuration commenté
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
└── README.md
```

### Règles de layout

**`cmd/{nom}/main.go` ne fait que :**
```go
func main() {
    cfg, err := config.Load()
    if err != nil {
        fmt.Fprintf(os.Stderr, "config error: %v\n", err)
        os.Exit(1)
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := run(ctx, cfg); err != nil {
        slog.Error("fatal", "err", err)
        os.Exit(1)
    }
}
```

`run()` est testable. `main()` ne l'est pas — elle reste minimale.

**`internal/` est obligatoire.** Aucun package à la racine du module sauf `main`. Pas de package `pkg/` (anti-pattern Go).

**Un package = une responsabilité cohérente.** Pas de package `util`, `helper`, `common`, `shared` — ces noms indiquent un manque de conception.

---

## Module Go

```go
// go.mod
module github.com/lmvi/lmvi-backup

go 1.22

require (
    gopkg.in/yaml.v3 v3.0.1
    // Ajouter uniquement les dépendances réellement utilisées
)
```

Convention de nommage des modules : `github.com/lmvi/{nom-programme}`.

---

## Makefile standard

Chaque projet dispose d'un `Makefile` avec les cibles suivantes :

```makefile
.PHONY: build test lint clean docker

BINARY  := lmvi-backup
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -s -w"

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)/

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

# Cross-compilation pour Linux/amd64 (cible de déploiement)
build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 ./cmd/$(BINARY)/

docker:
	docker build -t lmvi/$(BINARY):$(VERSION) .
```

---

## Dockerfile standard

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /lmvi-backup ./cmd/lmvi-backup/

FROM scratch
COPY --from=builder /lmvi-backup /lmvi-backup
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/lmvi-backup"]
```

`FROM scratch` : image minimale, aucune surface d'attaque OS. Les certificats TLS sont copiés pour les appels HTTPS vers Socle.

`CGO_ENABLED=0` : binaire statique, aucune dépendance libc.

---

## Configuration

### Struct de configuration

```go
// internal/config/config.go
package config

import (
    "fmt"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type Config struct {
    Socle   SocleConfig   `yaml:"socle"`
    Backup  BackupConfig  `yaml:"backup"`
    Storage StorageConfig `yaml:"storage"`
    Log     LogConfig     `yaml:"log"`
}

type SocleConfig struct {
    BaseURL        string        `yaml:"base_url"`
    APIKey         string        `yaml:"api_key"`   // override par LMVI_SOCLE_API_KEY
    TimeoutSeconds int           `yaml:"timeout_seconds"`
    RetryMax       int           `yaml:"retry_max"`
}

type BackupConfig struct {
    Schedule        string        `yaml:"schedule"`         // cron expression
    Workers         []string      `yaml:"workers"`          // noms des workers Socle à backuper
    RetentionDays   int           `yaml:"retention_days"`
    Compress        bool          `yaml:"compress"`
}

type StorageConfig struct {
    Type      string      `yaml:"type"`       // "local", "s3", "gcs", "sftp"
    LocalPath string      `yaml:"local_path"`
    S3        S3Config    `yaml:"s3"`
    GCS       GCSConfig   `yaml:"gcs"`
}

// Load charge la config depuis un fichier YAML puis surcharge avec les variables d'environnement.
func Load() (*Config, error) {
    path := os.Getenv("LMVI_CONFIG_PATH")
    if path == "" {
        path = "config.yaml"
    }

    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config %s: %w", path, err)
    }

    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parse config: %w", err)
    }

    // Variables d'environnement ont priorité sur le YAML
    if key := os.Getenv("LMVI_SOCLE_API_KEY"); key != "" {
        cfg.Socle.APIKey = key
    }
    if url := os.Getenv("LMVI_SOCLE_BASE_URL"); url != "" {
        cfg.Socle.BaseURL = url
    }

    if err := cfg.validate(); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }

    return &cfg, nil
}

func (c *Config) validate() error {
    if c.Socle.BaseURL == "" {
        return fmt.Errorf("socle.base_url is required")
    }
    if c.Socle.APIKey == "" {
        return fmt.Errorf("socle.api_key is required (or set LMVI_SOCLE_API_KEY)")
    }
    if c.Socle.TimeoutSeconds <= 0 {
        c.Socle.TimeoutSeconds = 30
    }
    return nil
}
```

### Fichier `config.yaml.example`

```yaml
socle:
  base_url: "https://app.thesocle.net"
  api_key: ""                    # Surcharger avec LMVI_SOCLE_API_KEY
  timeout_seconds: 30
  retry_max: 2

backup:
  schedule: "0 3 * * *"          # Cron : tous les jours à 3h
  workers:
    - "techdb_backup_worker"
  retention_days: 30
  compress: true

storage:
  type: "s3"                     # local | s3 | gcs | sftp
  local_path: "/backup/lmvi"
  s3:
    bucket: "lmvi-backups"
    region: "eu-west-3"
    prefix: "techdb/"

log:
  level: "info"                  # debug | info | warn | error
  format: "json"                 # json | text
```
