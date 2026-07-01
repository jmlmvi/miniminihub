# Intégration avec Socle V005

---

## Client HTTP Socle

Toutes les interactions avec Socle V005 passent par un seul client centralisé dans `internal/socle/client.go`.

```go
// internal/socle/client.go
package socle

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "net/url"
    "time"
)

// Client appelle les actions MCP/REST d'une instance Socle V005.
type Client struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
    logger     *slog.Logger
}

func NewClient(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
    if baseURL == "" {
        return nil, fmt.Errorf("baseURL is required")
    }
    if apiKey == "" {
        return nil, fmt.Errorf("apiKey is required")
    }
    return &Client{
        baseURL: baseURL,
        apiKey:  apiKey,
        httpClient: &http.Client{
            Timeout: timeout,
        },
        logger: slog.Default().With("component", "socle_client"),
    }, nil
}

// ActionResult est le format de réponse standard de Socle V005.
type ActionResult struct {
    Success   bool                   `json:"success"`
    Data      map[string]interface{} `json:"data"`
    Error     string                 `json:"error"`
    ErrorType string                 `json:"errorType"`
    DurationMs int64                 `json:"durationMs"`
}

// CallAction invoque une action Worker Socle V005 (POST).
func (c *Client) CallAction(ctx context.Context, worker, action string, params interface{}) (*ActionResult, error) {
    body, err := json.Marshal(params)
    if err != nil {
        return nil, fmt.Errorf("marshal params: %w", err)
    }

    u := fmt.Sprintf("%s/api/actions/%s/%s",
        c.baseURL,
        url.PathEscape(worker),
        url.PathEscape(action),
    )

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }
    req.Header.Set("X-Api-Key", c.apiKey)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json")

    c.logger.Debug("action call", "worker", worker, "action", action)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("HTTP call %s.%s: %w", worker, action, err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // max 10 MB
    if err != nil {
        return nil, fmt.Errorf("read response body: %w", err)
    }

    if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
        return nil, &APIError{StatusCode: resp.StatusCode, Operation: worker + "." + action, Message: "auth error"}
    }

    if resp.StatusCode >= 500 {
        return nil, &APIError{
            StatusCode: resp.StatusCode,
            Operation:  worker + "." + action,
            Message:    truncate(string(respBody), 200),
        }
    }

    var result ActionResult
    if err := json.Unmarshal(respBody, &result); err != nil {
        return nil, fmt.Errorf("unmarshal response: %w", err)
    }

    if !result.Success {
        return nil, &APIError{
            StatusCode: resp.StatusCode,
            ErrorCode:  result.ErrorType,
            Message:    result.Error,
            Operation:  worker + "." + action,
        }
    }

    return &result, nil
}

// Get effectue un GET sur un endpoint Socle.
func (c *Client) Get(ctx context.Context, path string, queryParams url.Values) ([]byte, error) {
    u := c.baseURL + "/" + path
    if len(queryParams) > 0 {
        u += "?" + queryParams.Encode()
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
    if err != nil {
        return nil, fmt.Errorf("create request GET %s: %w", path, err)
    }
    req.Header.Set("X-Api-Key", c.apiKey)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("GET %s: %w", path, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 400 {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
        return nil, &APIError{StatusCode: resp.StatusCode, Operation: "GET " + path, Message: string(body)}
    }

    return io.ReadAll(io.LimitReader(resp.Body, 100<<20)) // max 100 MB
}

// DownloadStream télécharge un fichier dump en streaming (pour les gros fichiers).
// Le caller est responsable de fermer le ReadCloser.
func (c *Client) DownloadStream(ctx context.Context, downloadPath string) (io.ReadCloser, int64, error) {
    u := c.baseURL + downloadPath

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
    if err != nil {
        return nil, 0, fmt.Errorf("create request: %w", err)
    }
    req.Header.Set("X-Api-Key", c.apiKey)

    // Pas de timeout sur le client pour le streaming (le context gère)
    streamClient := &http.Client{}

    resp, err := streamClient.Do(req)
    if err != nil {
        return nil, 0, fmt.Errorf("download stream: %w", err)
    }

    if resp.StatusCode >= 400 {
        resp.Body.Close()
        return nil, 0, &APIError{StatusCode: resp.StatusCode, Operation: "download " + downloadPath}
    }

    return resp.Body, resp.ContentLength, nil
}

func truncate(s string, n int) string {
    if len(s) <= n {
        return s
    }
    return s[:n] + "..."
}
```

---

## Client spécialisé TechDbBackup

Interface et implémentation pour les 9 actions du `TechDbBackupWorker` :

```go
// internal/socle/techdb_backup.go
package socle

import (
    "context"
    "fmt"
    "io"
    "time"
)

const techdbBackupWorker = "techdb_backup_worker"

// DumpResult est le résultat d'une action techdb_dump_sql.
type DumpResult struct {
    FilePath         string    `json:"file_path"`
    FileName         string    `json:"file_name"`
    SizeBytes        int64     `json:"size_bytes"`
    SHA256           string    `json:"sha256"`
    Compressed       bool      `json:"compressed"`
    TableCount       int       `json:"table_count"`
    RowCountTotal    int64     `json:"row_count_total"`
    DumpedAt         time.Time `json:"dumped_at"`
    DurationMs       int64     `json:"duration_ms"`
}

// DownloadInfo est le résultat d'une action techdb_dump_download.
type DownloadInfo struct {
    FileName    string `json:"file_name"`
    SizeBytes   int64  `json:"size_bytes"`
    SHA256      string `json:"sha256"`
    DownloadURL string `json:"download_url"`
}

// CleanupResult est le résultat d'une action techdb_staging_cleanup.
type CleanupResult struct {
    ScannedFiles   int   `json:"scanned_files"`
    DeletedFiles   int   `json:"deleted_files"`
    TotalFreedBytes int64 `json:"total_freed_bytes"`
}

// TechDbBackupClient appelle les actions du TechDbBackupWorker.
type TechDbBackupClient struct {
    client *Client
}

func NewTechDbBackupClient(client *Client) *TechDbBackupClient {
    return &TechDbBackupClient{client: client}
}

// DumpSQL déclenche un dump SQL (action ASYNC — attend la complétion via polling).
func (t *TechDbBackupClient) DumpSQL(ctx context.Context, compress bool, tables []string) (*DumpResult, error) {
    params := map[string]interface{}{
        "compress":     compress,
        "include_data": true,
    }
    if len(tables) > 0 {
        params["tables"] = tables
    }

    // L'action est ASYNC dans Socle — le framework Go attend la complétion
    result, err := t.client.CallAction(ctx, techdbBackupWorker, "techdb_dump_sql", params)
    if err != nil {
        return nil, fmt.Errorf("techdb_dump_sql: %w", err)
    }

    var dump DumpResult
    if err := mapToStruct(result.Data, &dump); err != nil {
        return nil, fmt.Errorf("parse dump result: %w", err)
    }
    return &dump, nil
}

// GetDownloadInfo retourne l'URL de téléchargement d'un fichier dump.
func (t *TechDbBackupClient) GetDownloadInfo(ctx context.Context, fileName string) (*DownloadInfo, error) {
    result, err := t.client.CallAction(ctx, techdbBackupWorker, "techdb_dump_download", map[string]interface{}{
        "file_name": fileName,
    })
    if err != nil {
        return nil, fmt.Errorf("techdb_dump_download: %w", err)
    }

    var info DownloadInfo
    if err := mapToStruct(result.Data, &info); err != nil {
        return nil, fmt.Errorf("parse download info: %w", err)
    }
    return &info, nil
}

// DownloadDump télécharge le fichier dump en streaming.
// Le caller doit fermer le ReadCloser.
func (t *TechDbBackupClient) DownloadDump(ctx context.Context, downloadURL string) (io.ReadCloser, int64, error) {
    return t.client.DownloadStream(ctx, downloadURL)
}

// CleanupStaging purge les fichiers staging anciens.
func (t *TechDbBackupClient) CleanupStaging(ctx context.Context, olderThanMinutes int) (*CleanupResult, error) {
    result, err := t.client.CallAction(ctx, techdbBackupWorker, "techdb_staging_cleanup", map[string]interface{}{
        "older_than_minutes": olderThanMinutes,
    })
    if err != nil {
        return nil, fmt.Errorf("techdb_staging_cleanup: %w", err)
    }

    var cleanup CleanupResult
    if err := mapToStruct(result.Data, &cleanup); err != nil {
        return nil, fmt.Errorf("parse cleanup result: %w", err)
    }
    return &cleanup, nil
}

// RestorePrepare demande un token de confirmation pour un restore.
func (t *TechDbBackupClient) RestorePrepare(ctx context.Context, filePath string) (string, error) {
    result, err := t.client.CallAction(ctx, techdbBackupWorker, "techdb_restore_prepare", map[string]interface{}{
        "file_path": filePath,
    })
    if err != nil {
        return "", fmt.Errorf("techdb_restore_prepare: %w", err)
    }
    token, _ := result.Data["confirm_token"].(string)
    if token == "" {
        return "", fmt.Errorf("no confirm_token in response")
    }
    return token, nil
}

// RestoreSQL exécute un restore (nécessite un token de RestorePrepare).
func (t *TechDbBackupClient) RestoreSQL(ctx context.Context, filePath, confirmToken string, dropFirst bool) error {
    _, err := t.client.CallAction(ctx, techdbBackupWorker, "techdb_restore_sql", map[string]interface{}{
        "file_path":     filePath,
        "confirm_token": confirmToken,
        "drop_first":    dropFirst,
    })
    return err
}
```

---

## Retry avec backoff exponentiel

Le retry s'applique uniquement aux opérations idempotentes (lectures, dumps).

```go
// internal/socle/retry.go
package socle

import (
    "context"
    "errors"
    "log/slog"
    "math"
    "time"
)

type RetryConfig struct {
    MaxAttempts int
    BaseDelay   time.Duration
    MaxDelay    time.Duration
}

var DefaultRetry = RetryConfig{
    MaxAttempts: 3,
    BaseDelay:   1 * time.Second,
    MaxDelay:    30 * time.Second,
}

// WithRetry exécute fn avec retry exponentiel sur les erreurs retryables.
func WithRetry(ctx context.Context, cfg RetryConfig, logger *slog.Logger, op string, fn func() error) error {
    var lastErr error

    for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
        if err := ctx.Err(); err != nil {
            return err
        }

        lastErr = fn()
        if lastErr == nil {
            return nil
        }

        // Ne pas retenter les erreurs non-retryables
        var apiErr *APIError
        if errors.As(lastErr, &apiErr) && !apiErr.IsRetryable() {
            return lastErr
        }

        if attempt == cfg.MaxAttempts {
            break
        }

        delay := time.Duration(math.Pow(2, float64(attempt-1))) * cfg.BaseDelay
        if delay > cfg.MaxDelay {
            delay = cfg.MaxDelay
        }

        logger.Warn("retrying after error",
            "operation", op,
            "attempt", attempt,
            "max_attempts", cfg.MaxAttempts,
            "delay", delay,
            "err", lastErr,
        )

        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(delay):
        }
    }

    return fmt.Errorf("all %d attempts failed for %s: %w", cfg.MaxAttempts, op, lastErr)
}
```

---

## Health check Socle

```go
func (c *Client) IsHealthy(ctx context.Context) bool {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    body, err := c.Get(ctx, "health", nil)
    if err != nil {
        return false
    }

    var health struct {
        Status string `json:"status"`
    }
    return json.Unmarshal(body, &health) == nil && health.Status == "UP"
}
```

---

## Vérification SHA-256

Après téléchargement, toujours vérifier l'intégrité du fichier contre le SHA-256 retourné par Socle :

```go
func verifySHA256(r io.Reader, expected string) error {
    h := sha256.New()
    if _, err := io.Copy(h, r); err != nil {
        return fmt.Errorf("compute sha256: %w", err)
    }
    actual := hex.EncodeToString(h.Sum(nil))
    if actual != expected {
        return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
    }
    return nil
}

// Utilisation avec téléchargement en streaming
func (r *Runner) downloadAndStore(ctx context.Context, info *socle.DownloadInfo) error {
    stream, _, err := r.techdb.DownloadDump(ctx, info.DownloadURL)
    if err != nil {
        return fmt.Errorf("open download stream: %w", err)
    }
    defer stream.Close()

    // Tee : lire une seule fois → écrire vers storage ET calculer SHA256 simultanément
    h := sha256.New()
    tee := io.TeeReader(stream, h)

    if err := r.storage.Store(ctx, info.FileName, tee); err != nil {
        return fmt.Errorf("store: %w", err)
    }

    actual := hex.EncodeToString(h.Sum(nil))
    if actual != info.SHA256 {
        // Supprimer le fichier corrompu
        _ = r.storage.Delete(ctx, info.FileName)
        return fmt.Errorf("SHA256 mismatch for %s: expected %s got %s", info.FileName, info.SHA256, actual)
    }

    return nil
}
```

`io.TeeReader` permet de calculer le hash pendant le transfert sans relire le fichier — crucial pour les dumps de plusieurs Go.
