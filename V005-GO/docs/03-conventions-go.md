# Conventions Go

---

## Formatting

**`gofmt` et `goimports` sont non-négociables.** Tout commit passe par ces deux outils. Le CI rejette le code non formaté.

```bash
gofmt -w .
goimports -w .
```

Pas de débat sur le style : Go a un style officiel, on le suit.

---

## Nommage

### Packages

- Minuscules, pas d'underscore, pas de camelCase : `backup`, `socle`, `storage`
- Nom court et précis : le contexte du package élimine l'ambiguïté (`storage.Local`, pas `localstorage.Storage`)
- Jamais `util`, `helper`, `common`, `misc`, `shared`
- Le nom du package n'est **pas** répété dans les types exportés : `storage.Local` (bon), `storage.LocalStorage` (redondant)

### Variables et fonctions

camelCase, intention claire, longueur proportionnelle à la portée :

```go
// Portée locale courte → nom court
for i, v := range items { ... }
if err != nil { ... }

// Portée de fonction → nom descriptif
retryCount := 0
backupPath := filepath.Join(cfg.Storage.LocalPath, filename)

// Portée de package → nom explicite
var defaultTimeout = 30 * time.Second
```

**Acronymes :** tout en majuscules ou tout en minuscules selon la position.
```go
// CORRECT
type HTTPClient struct{}
func parseURL(s string) (*url.URL, error)
var apiKey string

// INCORRECT
type HttpClient struct{}    // Http au milieu d'un nom
func parseUrl(s string)    // Url au milieu d'un nom
```

### Types exportés

PascalCase, noms substantifs :

```go
type BackupRunner struct { ... }
type SocleClient struct { ... }
type StorageBackend interface { ... }
type ActionResult struct { ... }
```

### Interfaces

Nom = ce que fait l'objet + suffixe `er` si possible, sinon nom descriptif :

```go
type Storer interface {      // Storer : stocke quelque chose
    Store(ctx context.Context, name string, r io.Reader) error
    Delete(ctx context.Context, name string) error
}

type Notifier interface {    // Notifier : notifie
    Notify(ctx context.Context, msg Message) error
}

type BackupRunner interface { // BackupRunner quand "er" ne convient pas
    Run(ctx context.Context) error
}
```

Interfaces de 1 à 3 méthodes. Une interface à 10 méthodes est une classe Java déguisée.

### Erreurs

Variables d'erreur sentinelles : préfixe `Err` :
```go
var (
    ErrNotFound      = errors.New("not found")
    ErrUnauthorized  = errors.New("unauthorized")
    ErrTimeout       = errors.New("timeout")
)
```

Types d'erreur : suffixe `Error` :
```go
type APIError struct {
    StatusCode int
    Body       string
    Op         string
}

func (e *APIError) Error() string {
    return fmt.Sprintf("[%s] HTTP %d: %s", e.Op, e.StatusCode, e.Body)
}
```

---

## Idiomes obligatoires

### Receivers

- Receiver court (1-2 lettres, initiale du type) :
```go
func (c *SocleClient) Call(...) error { ... }
func (r *BackupRunner) Run(...) error { ... }
```
- Cohérence : si une méthode utilise un pointer receiver, toutes les méthodes de ce type utilisent un pointer receiver.

### Constructeurs

Toujours une fonction `New...` qui retourne le type et une erreur :
```go
func NewSocleClient(cfg *config.SocleConfig) (*SocleClient, error) {
    if cfg.BaseURL == "" {
        return nil, errors.New("base_url is required")
    }
    return &SocleClient{
        baseURL:    cfg.BaseURL,
        apiKey:     cfg.APIKey,
        httpClient: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
        logger:     slog.Default().With("component", "socle_client"),
    }, nil
}
```

### Early return

Pas de `if !err { ... }` imbriqué. Early return systématique :
```go
// CORRECT
func (r *BackupRunner) runOnce(ctx context.Context) error {
    dumpResult, err := r.client.DumpSQL(ctx, r.cfg.Compress)
    if err != nil {
        return fmt.Errorf("dump: %w", err)
    }

    downloadURL, err := r.client.GetDownloadURL(ctx, dumpResult.FileName)
    if err != nil {
        return fmt.Errorf("get download url: %w", err)
    }

    if err := r.storage.Store(ctx, dumpResult.FileName, downloadURL); err != nil {
        return fmt.Errorf("store: %w", err)
    }

    return nil
}

// INCORRECT — imbrication inutile
func (r *BackupRunner) runOnce(ctx context.Context) error {
    dumpResult, err := r.client.DumpSQL(ctx, r.cfg.Compress)
    if err == nil {
        downloadURL, err := r.client.GetDownloadURL(ctx, dumpResult.FileName)
        if err == nil {
            if err := r.storage.Store(...); err == nil {
                return nil
            }
        }
    }
    return err
}
```

### Pas de `init()`

`init()` est une source d'effets de bord invisibles et rend les tests difficiles. Toute initialisation passe par `New...()` ou `Load()`.

### Pas de globals mutables

Variables globales en lecture seule uniquement (`var defaultTimeout = 30 * time.Second`). Pas de globals qui changent après l'initialisation. Toute dépendance est injectée.

```go
// INCORRECT
var globalClient *SocleClient

func init() {
    globalClient = NewSocleClient(...)
}

// CORRECT
type BackupRunner struct {
    client *SocleClient  // injecté dans NewBackupRunner()
}
```

### Structs avec options fonctionnelles

Pour les types avec de nombreuses options optionnelles :

```go
type SocleClient struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
    logger     *slog.Logger
    maxRetries int
}

type Option func(*SocleClient)

func WithMaxRetries(n int) Option {
    return func(c *SocleClient) { c.maxRetries = n }
}

func WithHTTPClient(hc *http.Client) Option {
    return func(c *SocleClient) { c.httpClient = hc }
}

func NewSocleClient(baseURL, apiKey string, opts ...Option) (*SocleClient, error) {
    c := &SocleClient{
        baseURL:    baseURL,
        apiKey:     apiKey,
        httpClient: &http.Client{Timeout: 30 * time.Second},
        logger:     slog.Default(),
        maxRetries: 2,
    }
    for _, opt := range opts {
        opt(c)
    }
    return c, nil
}
```

---

## Tests

### Nomenclature

```go
// Fichier : runner_test.go
// Package : même package que le code testé (boîte blanche) OU package_test (boîte noire)

func TestBackupRunner_RunOnce_Success(t *testing.T) { ... }
func TestBackupRunner_RunOnce_SocleDown(t *testing.T) { ... }
func TestBackupRunner_Schedule_CronParsing(t *testing.T) { ... }
```

Format : `Test{Type}_{Méthode}_{Scénario}`.

### Table-driven tests

```go
func TestParseSchedule(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {name: "daily cron",    input: "0 3 * * *", wantErr: false},
        {name: "every 5 min",   input: "@every 5m",  wantErr: false},
        {name: "invalid",       input: "not-a-cron", wantErr: true},
        {name: "empty",         input: "",            wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := parseSchedule(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("parseSchedule(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
            }
        })
    }
}
```

### Pas de framework de test

Uniquement `testing` stdlib + `testify/assert` si les assertions deviennent verbeuses. Pas de Ginkgo, Gomega, ou autres DSL.

### Interfaces pour les dépendances externes

Toute dépendance externe (Socle, S3, filesystem) est derrière une interface pour permettre les mocks dans les tests :

```go
// internal/backup/runner.go
type SocleBackupper interface {
    DumpSQL(ctx context.Context, compress bool) (*DumpResult, error)
    GetDownloadURL(ctx context.Context, fileName string) (string, error)
    CleanupStaging(ctx context.Context, olderThanMinutes int) error
}

// Les tests utilisent un mock minimal, pas de library
type mockBackupper struct {
    dumpResult *DumpResult
    dumpErr    error
}

func (m *mockBackupper) DumpSQL(_ context.Context, _ bool) (*DumpResult, error) {
    return m.dumpResult, m.dumpErr
}
```

### `-race` obligatoire

```bash
go test -race ./...
```

Tous les tests passent sans race condition. Le `-race` est activé dans le CI.
