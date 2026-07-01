# Règles absolues — Interdictions non-négociables

> Ces règles ne souffrent aucune exception. Elles sont issues des patterns Go les plus dangereux et des contraintes spécifiques à l'écosystème Socle V005.

---

## Go

### R01 — Toute erreur est traitée ou wrappée, jamais ignorée

**Mécanisme :** en Go, ignorer une erreur avec `_` est syntaxiquement valide mais sémantiquement un bug délibéré.
**Symptôme si violé :** données silencieusement corrompues, fichiers partiellement écrits, état incohérent.

```go
// INCORRECT
result, _ := client.DumpSQL(ctx, true)     // _ cache une erreur potentielle
file.Close()                                // erreur Close ignorée (flush non garanti)

// CORRECT
result, err := client.DumpSQL(ctx, true)
if err != nil {
    return fmt.Errorf("dump: %w", err)
}
if err := file.Close(); err != nil {
    return fmt.Errorf("close file: %w", err)
}
```

---

### R02 — Toute goroutine lancée a une durée de vie bornée par un context

**Mécanisme :** une goroutine non contrôlée vit jusqu'à la fin du processus. En cas de shutdown, elle peut bloquer indéfiniment ou corrompre des données.
**Symptôme si violé :** fuite mémoire progressive, shutdown qui ne se termine jamais, goroutines zombies.

```go
// INCORRECT — goroutine non bornée
go func() {
    for {
        doWork()
        time.Sleep(1 * time.Minute)
    }
}()

// CORRECT — bornée par context
go func() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            doWork()
        }
    }
}()
```

---

### R03 — Pas de variable globale mutable

**Mécanisme :** les globals mutables créent des dépendances implicites entre packages, rendent les tests impossibles en parallèle, et introduisent des races conditions.
**Symptôme si violé :** tests qui s'interfèrent, races détectées par `-race`, comportement non déterministe.

```go
// INCORRECT
var globalClient *socle.Client
func init() { globalClient = socle.NewClient(...) }

// CORRECT — injection explicite
type Runner struct {
    client *socle.Client
}
func NewRunner(client *socle.Client) *Runner { return &Runner{client: client} }
```

---

### R04 — `context.Context` est le premier paramètre, jamais dans une struct

**Mécanisme :** un context stocké dans une struct ne peut pas être annulé à temps ; il "traîne" au-delà de la portée de la requête ou de l'opération.
**Symptôme si violé :** opérations qui continuent après shutdown, contextes qui fuient entre requêtes.

```go
// INCORRECT
type Client struct {
    ctx context.Context
}

// CORRECT
func (c *Client) DumpSQL(ctx context.Context, ...) error
```

---

### R05 — Jamais de `time.Sleep` dans une goroutine longue durée sans select sur ctx.Done()

**Mécanisme :** `time.Sleep` est imperméable à l'annulation de context. Un daemon avec `time.Sleep` peut mettre des minutes à s'arrêter.
**Symptôme si violé :** shutdown lent, systemd qui `SIGKILL` le processus après le timeout, données incomplètes.

```go
// INCORRECT
for {
    doWork()
    time.Sleep(5 * time.Minute)  // bloque le shutdown pendant 5 min
}

// CORRECT
ticker := time.NewTicker(5 * time.Minute)
defer ticker.Stop()
for {
    select {
    case <-ctx.Done():
        return nil
    case <-ticker.C:
        doWork()
    }
}
```

---

### R06 — Pas de `panic` dans le code de production sauf violation d'invariant de programmation

**Mécanisme :** `panic` est réservé aux bugs de programmation détectés à l'exécution (nil pointer sur une interface qu'on garantit non-nil). Les erreurs métier et réseau retournent des `error`.
**Symptôme si violé :** crash du daemon sur une erreur récupérable (timeout, Socle down).

```go
// INCORRECT — panic sur une erreur opérationnelle
func (r *Runner) Run(ctx context.Context) {
    if err := r.doBackup(ctx); err != nil {
        panic(err)
    }
}

// CORRECT
func (r *Runner) Run(ctx context.Context) error {
    if err := r.doBackup(ctx); err != nil {
        return fmt.Errorf("backup: %w", err)
    }
    return nil
}
```

---

### R07 — Tests avec `-race` obligatoirement verts

**Mécanisme :** le race detector Go identifie les accès concurrents non synchronisés.
**Symptôme si violé :** corruption de données en production lors de pics de concurrence, bugs non reproductibles.

```bash
# CI obligatoire
go test -race -count=1 ./...
```

---

## Intégration Socle V005

### R08 — Jamais de retry sur une action mutante (write)

**Mécanisme :** les actions qui créent ou modifient des données dans Socle ne sont pas idempotentes. Un retry crée un double backup, un double envoi, une double commande.
**Symptôme si violé :** doublons en staging Socle, consommation de stockage multipliée, alertes sur doublons.

```go
// INCORRECT — retry sur DumpSQL (action mutante)
for attempt := 0; attempt < 3; attempt++ {
    dumpResult, err = client.DumpSQL(ctx, compress)
    if err == nil { break }
}

// CORRECT — retry uniquement sur les lectures
// DumpSQL : pas de retry (crée un fichier)
// GetDownloadInfo : retry OK (lecture idempotente)
// DownloadDump : retry OK (lecture idempotente)
// CleanupStaging : retry OK (idempotent)
```

---

### R09 — Vérification SHA-256 obligatoire après chaque téléchargement

**Mécanisme :** un dump corrompu en transit est pire qu'un backup manquant — il peut passer inaperçu jusqu'au moment du restore.
**Symptôme si violé :** restore d'un dump corrompu lors d'un incident, perte de données irrécouvrable.

```go
// La vérification SHA-256 est non-négociable après DownloadDump.
// Utiliser io.TeeReader pour calculer le hash pendant le transfert.
// Supprimer le fichier stocké si la vérification échoue.
```

---

### R10 — La clé API Socle ne passe jamais dans les logs

**Mécanisme :** les logs sont souvent collectés par des systèmes tiers (ELK, Datadog, S3). Une clé API loguée est une clé compromis.
**Symptôme si violé :** accès non autorisé à Socle V005, backup exfiltré.

```go
// INCORRECT
logger.Debug("request headers", "X-Api-Key", c.apiKey)

// CORRECT
logger.Debug("request", "url", url, "has_api_key", c.apiKey != "")
```

---

### R11 — Timeout sur chaque appel HTTP, sans exception

**Mécanisme :** un `http.Client` sans timeout attend indéfiniment. Une instance Socle down peut bloquer le daemon pour toujours.
**Symptôme si violé :** daemon bloqué sur une seule instance down, les autres instances ne sont pas backupées.

```go
// INCORRECT
http.Get(url)  // pas de timeout

// CORRECT
ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
defer cancel()
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
```

---

### R12 — Graceful shutdown : attendre max 30s, puis exit

**Mécanisme :** un daemon qui ne se termine pas empêche les déploiements, les redémarrages cron, les rotations de secrets.
**Symptôme si violé :** rolling deployments bloqués, accumulation de processus zombies.

```go
// Timeout de shutdown obligatoire
select {
case <-done:
    return nil
case <-time.After(30 * time.Second):
    return fmt.Errorf("shutdown timeout: forcing exit")
}
```

---

### R13 — Pas de connexion directe à la base de données de Socle

**Mécanisme :** Socle V005 expose ses données via ses actions MCP/REST, pas via une connexion DB directe. Une connexion directe bypasse la validation, le cache, les métriques, et la sécurité IAM de Socle.
**Symptôme si violé :** données incohérentes (TechDbManager cache invalidé), couplage fort incompatible avec les migrations.

---

## Tableau récapitulatif

| # | Règle | Risque si violée |
|---|-------|-----------------|
| R01 | Toute erreur traitée ou wrappée | Données corrompues silencieusement |
| R02 | Goroutine bornée par context | Fuite mémoire, shutdown bloqué |
| R03 | Pas de global mutable | Race conditions, tests instables |
| R04 | Context en paramètre, pas en struct | Context fuitant entre opérations |
| R05 | Pas de Sleep sans select ctx.Done | Shutdown lent, SIGKILL forcé |
| R06 | Pas de panic pour erreurs opérationnelles | Crash daemon sur erreur récupérable |
| R07 | Tests -race verts | Corruption en production sous charge |
| R08 | Pas de retry sur actions mutantes | Doublons backup, double facturation |
| R09 | SHA-256 vérifié après téléchargement | Restore d'un dump corrompu |
| R10 | API key hors des logs | Credentials compromis |
| R11 | Timeout sur chaque appel HTTP | Daemon bloqué indéfiniment |
| R12 | Shutdown en max 30s | Déploiements bloqués |
| R13 | Pas de connexion directe DB Socle | Données incohérentes, couplage fort |
