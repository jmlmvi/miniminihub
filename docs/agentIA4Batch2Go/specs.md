# Spécification — Worker `agent_batch` (AgentI4Batch2Go)

> **But en une phrase.** Un worker de type *TheSocle* (écrit en **Go**, réutilisant le programme Go existant qui sait déjà se connecter au Hub) qui **déclenche un agent IA à heure précise avec un prompt**, **récupère son rapport au format Markdown**, puis le **diffuse** — en priorité **par mail via la fonction mail du Hub**, avec en option Slack, NATS/Kafka et fichier/log.

- **Statut** : spécification (aucun code). Rédigée le 2026-07-01.
- **Emplacement** : `docs/AgentI4Batch2Go/specs.md`
- **Positionnement** : brique planificatrice + exécutrice d'agents IA + diffuseur, branchée sur le Hub.
- **Cas d'usage fondateur** : remplacer le cron+psql actuel (rapport odhprod à 19:00) par un vrai rapport **rédigé par un agent IA** puis **envoyé par mail**.

---

## 1. Périmètre & non-périmètre

**Dans le périmètre**
- Planifier des **jobs** (à heure fixe / cron / intervalle) qui lancent un agent IA avec un prompt.
- Abstraire l'agent : **Claude Code (headless)** OU **un agent maison** — au choix par job (runner pluggable).
- Récupérer la sortie de l'agent sous forme d'un **rapport `.md`** normalisé (front-matter + corps).
- **Diffuser** le rapport vers 1..N canaux : **Mail (Hub)** [prioritaire], Slack, NATS/Kafka, Fichier/Log.
- S'intégrer proprement au Hub comme un worker TheSocle (cycle de vie, actions, supervision, logs).

**Hors périmètre (pour cette version)**
- L'implémentation des agents eux-mêmes (Claude Code et l'agent maison sont des exécutables/services externes).
- La gestion fine des permissions internes de Claude Code (documentée comme *prérequis*, §12).
- Une UI dédiée (le pilotage passe par les **actions** exposées + le dashboard Socle existant).

---

## 2. Rappel du vocabulaire TheSocle (ce qu'on réimplémente en Go)

Le worker doit respecter les conventions du Socle V005 (framework Java d'origine), transposées en Go :

| Concept Socle | Rôle | Transposition Go |
|---|---|---|
| **MOP** (Main Orchestrator Process) | Orchestrateur unique : démarre/arrête les workers par priorité, porte le scheduler, la boucle de santé | Le programme Go hôte (déjà existant) joue le MOP ou s'y connecte |
| **Worker** | Unité de travail : `initialize() → start() → doWork()* → stop()`, triée par `startPriority/stopPriority`, `isHealthy()`, `getStats()` | Interface Go `Worker` (voir §4) |
| **KvBus** | Bus interne clé-valeur + **pub/sub** par topics ; `pub(topic, payload)` / `sub(topic, handler)` — **`sub` uniquement dans `initialize()`** | Client Hub (le prog Go sait déjà s'y connecter) |
| **ActionProvider** | Expose des **actions** invocables (REST/MCP) : `getActions()` + `executeAction(name, params, ctx)`, modes `SYNC`/`ASYNC` | Interface Go `ActionProvider` (voir §7) |
| **SchedulerManager / WorkerScheduler** | Modes `INTERVAL` / `CRON` / `PASSIVE`, anti-exécution concurrente | Scheduler interne du worker (voir §6.1) |
| **Supervisor + SharedDataRegistry** | Heartbeats, `HealthLevel`, statut via `isHealthy()` + métriques | Heartbeat + stats vers le Hub (voir §10) |
| **gRPC (9400)** | Communication **inter-instances** Socle | Optionnel : diffusion/contrôle inter-nœuds |
| **REST/MCP (8080/9401)** | Clients externes / LLM ; nommage MCP `{worker}__{action}` | Actions exposées au Hub |
| Convention de log | `[worker:{name}][step:{étape}] …` | Idem en Go |

> Note importante issue de l'analyse du framework : **il n'existe pas de brique mail native dans le Socle Java**. Ici, la fonction mail est fournie par **ton Hub** (produit *TheSocle*, auquel le programme Go se connecte déjà). Le worker **appelle** donc la fonction mail du Hub ; il ne réimplémente pas un serveur SMTP.

---

## 3. Vue d'ensemble

```
              ┌─────────────────────────────────────────────────────────────┐
              │  Host avec accès réseau privé (VM odh-sup-prod ou pod)        │
              │                                                              │
   config /   │   ┌──────────────────────────── Worker agent_batch ──────┐  │
   PG jsonb ──┼──▶│  Scheduler (cron/tz, anti-overlap)                    │  │
              │   │        │ déclenche un Job                             │  │
              │   │        ▼                                             │  │
              │   │  AgentRunner (pluggable)                             │  │
              │   │   ├─ ClaudeCodeRunner   → exec `claude -p <prompt>`  │  │
              │   │   └─ CustomAgentRunner  → HTTP/exec agent maison     │  │
              │   │        │ capture stdout → Rapport .md + métriques     │  │
              │   │        ▼                                             │  │
              │   │  ReportStore (fichier/log) + ReportEnvelope          │  │
              │   │        │                                             │  │
              │   │        ▼   fan-out                                    │  │
              │   │  Diffusers ──┬─ MailDiffuser  ─▶ Hub.mail()  [prio]   │  │
              │   │              ├─ SlackDiffuser ─▶ webhook/Hub          │  │
              │   │              ├─ BusDiffuser   ─▶ NATS/Kafka existant  │  │
              │   │              └─ FileDiffuser  ─▶ .md sur disque       │  │
              │   └───────────────────────────────────────────────────────┘ │
              │            │ heartbeat/stats/log        │ actions (run_now…)  │
              └────────────┼────────────────────────────┼────────────────────┘
                           ▼                            ▼
                    Hub / KvBus / Supervisor      REST/MCP (pilotage)
```

**Principe clé** : *un Job = un déclencheur + un agent + un prompt + une liste de diffuseurs*. Le worker lit une **une seule fois** l'agent (une exécution), et **fan-out** le rapport vers tous les diffuseurs du job.

---

## 4. Le worker `agent_batch` — contrat de cycle de vie

Nom du worker : **`agent_batch`** (snake_case, convention Socle). Interface Go à respecter (transposition du contrat `Worker`) :

```go
type Worker interface {
    Name() string                 // "agent_batch"
    StartPriority() int           // ex. 150 (après les gateways/bus, avant l'orchestrateur métier)
    StopPriority() int            // ex. 60
    CycleIntervalMs() int64       // 0 si piloté par le scheduler interne (PASSIVE)
    Schedule() string             // "PASSIVE" (le worker gère ses propres crons de jobs)
    Initialize(ctx Context) error // création ressources + abonnements KvBus (UNE fois)
    Start(ctx Context) error      // ouverture connexions (Hub, agents, diffuseurs)
    DoWork(ctx Context) error     // tick léger : housekeeping (jobs échus, purge, retries) — JAMAIS de boucle bloquante
    Stop(ctx Context) error       // arrêt propre : draine les runs en cours, ferme le pool
    IsHealthy() bool              // < 100ms, sans exception
    Stats() map[string]any        // métriques exposées au dashboard
}
```

**Règles absolues transposées du Socle**
- Pas de `for {}` / `time.Sleep` bloquant dans `DoWork()` : le worker est piloté par le scheduler ; `DoWork()` est un tick court.
- Les abonnements pub/sub (`Hub.Sub(topic, handler)`) se font **uniquement dans `Initialize()`** (sinon accumulation de handlers).
- `Stop()` ne panique pas ; il draine les runs en cours (respect d'un `drainTimeout`).
- Log structuré `[worker:agent_batch][step:…]`.

**Mode d'exécution** : le worker est **PASSIVE** au sens Socle ; c'est son **scheduler interne** (§6.1) qui déclenche les jobs. `DoWork()` sert au *housekeeping* (détecter les jobs échus si scheduler tick-based, relancer les retries, purger les vieux rapports).

---

## 5. Modèle de données

### 5.1 JobSpec (définition d'un job)

```go
type JobSpec struct {
    ID          string            // "odhprod-flux2-check"
    Enabled     bool
    Description  string
    // --- déclenchement ---
    Schedule    string            // cron 5 champs "0 19 * * *" | "@daily" | "" (manuel)
    Timezone    string            // "UTC" | "Europe/Paris"
    // --- agent ---
    Agent       AgentRef          // quel runner + params (voir 5.2)
    Prompt      PromptSpec        // texte ou template + variables (voir 5.3)
    Timeout     Duration          // budget max d'exécution de l'agent
    // --- diffusion ---
    Diffusers   []DiffuserRef     // 1..N canaux (voir 5.5)
    // --- robustesse ---
    OnError     string            // "continue" | "stop" | "alert"
    Retry       RetryPolicy       // maxAttempts, backoff
    Overlap     string            // "skip" (défaut) | "queue" | "cancel_previous"
}
```

### 5.2 AgentRef + AgentRunner (pluggable — cœur de l'abstraction)

L'agent est **abstrait** : le worker ne sait pas *comment* l'agent produit son rapport, seulement qu'il reçoit un prompt et renvoie du Markdown.

```go
type AgentRef struct {
    Kind    string            // "claude_code" | "custom_http" | "custom_exec"
    Params  map[string]string // spécifique au runner (voir ci-dessous)
}

type AgentRunner interface {
    // Exécute l'agent avec le prompt résolu ; renvoie le rapport brut + métriques.
    Run(ctx Context, prompt string, cfg AgentRef) (RunResult, error)
    Kind() string
}
```

Implémentations prévues :

- **`ClaudeCodeRunner`** (`kind="claude_code"`)
  - Commande : `claude -p "<prompt>" --output-format json` (ou `stream-json`), `cwd` = répertoire projet (pour la config MCP).
  - Params utiles : `binary` (chemin de `claude`), `model`, `permission_mode`, `mcp_config`, `allowed_tools`, `max_turns`, `cwd`, `env_file`.
  - Sortie : le rapport `.md` est extrait du champ `result` du JSON ; les métriques (durée, tokens, coût, `num_turns`) alimentent `RunResult.Metrics`.
  - **Prérequis critiques** (voir §12) : auth non-interactive, mode permissions, accès réseau privé.
- **`CustomHttpRunner`** (`kind="custom_http"`)
  - POST vers l'endpoint de ton agent maison : `{prompt, context}` → réponse `{markdown, metrics}`.
  - Params : `url`, `auth_header`, `timeout`, `response_md_path` (JSONPath du champ Markdown).
- **`CustomExecRunner`** (`kind="custom_exec"`)
  - Exécute un binaire/script de ton agent, prompt sur stdin, Markdown sur stdout.

> **Contrat de sortie commun** : quel que soit le runner, le worker reçoit **du Markdown** + un `map` de métriques. C'est ce qui rend « n'importe quel agent » interchangeable.

### 5.3 PromptSpec

```go
type PromptSpec struct {
    Text      string            // prompt littéral, OU
    Template  string            // gabarit avec variables {{.date}}, {{.cible}}, …
    Vars      map[string]string // valeurs injectées à la résolution
    Context   []ContextItem     // pièces jointes optionnelles (fichiers, requêtes pré-exécutées)
}
```

Le prompt est **résolu au moment du déclenchement** (variables de date/heure, résultats de pré-requêtes). Exemple : injecter la date du jour et le nom de la base à vérifier.

### 5.4 RunResult + ReportEnvelope

```go
type RunResult struct {
    JobID     string
    RunID     string            // ULID/UUID, corrélation logs↔diffusion
    StartedAt time.Time
    EndedAt   time.Time
    Status    string            // "success" | "failed" | "timeout" | "skipped"
    ReportMD  string            // le rapport Markdown
    Metrics   map[string]any    // durée, tokens, coût, exit_code, num_turns…
    Error     string
}

// Enveloppe normalisée écrite/diffusée (front-matter YAML + corps MD)
type ReportEnvelope struct {
    Meta ReportMeta // job_id, run_id, agent_kind, started_at, status, metrics, tags
    Body string     // ReportMD
}
```

Format de fichier diffusé (`.md`) :
```markdown
---
job_id: odhprod-flux2-check
run_id: 01J...
agent: claude_code
generated_at: 2026-07-01T19:03:11Z
status: success
metrics: { duration_s: 42, tokens: 18034, cost_usd: 0.21 }
---
# <titre du rapport rédigé par l'agent>
… corps Markdown …
```

### 5.5 DiffuserRef + Diffuser (pluggable)

```go
type DiffuserRef struct {
    Kind   string            // "mail" | "slack" | "bus" | "file"
    Params map[string]string // destinataires, canal, sujet, topic, chemin…
}

type Diffuser interface {
    Diffuse(ctx Context, env ReportEnvelope, cfg DiffuserRef) error
    Kind() string
}
```

---

## 6. Composants internes

### 6.1 Scheduler (déclenchement à heure précise)

- Modèle **`WorkerScheduler`** du Socle : par job, mode `CRON` (`Schedule` non vide) ou `MANUAL` (déclenché par action `run_now`).
- **Cron 5 champs POSIX** + **timezone explicite** par job (comme `WorkflowService` : `CronTrigger(expr, ZoneId.of(tz))`). Ne pas dépendre de la TZ du process.
- **Anti-overlap** (repris du `SchedulerManager` Socle) : un `executing` atomique par job → si le run précédent tourne encore, appliquer `Overlap` (`skip` par défaut, incrémente un compteur `scheduler.<job>.skipped`).
- **Durabilité** : deux options (à trancher, §15) —
  1. *In-memory* (jobs déclarés en YAML, rechargés au démarrage) — simple.
  2. *Persistant PG* (table `jsonb` façon `etl.z_schedule`/`z_workflow`, `@PostConstruct loadActiveJobs()`) — survit aux redémarrages, éditable à chaud.
- **Jitter** conseillé : éviter les minutes `:00`/`:30` pile pour ne pas empiler les runs (et pour les agents cloud, lisser la charge API).

### 6.2 AgentRunner (exécution de l'agent)

Responsabilités :
1. Résoudre le prompt (template + vars + contexte).
2. Lancer le runner choisi avec un **timeout dur** (`context.WithTimeout`) et capture de `stdout`/`stderr`.
3. Normaliser la sortie en `RunResult` (Markdown + métriques).
4. Isolation : chaque run dans son propre process/goroutine ; pas d'état partagé entre jobs.
5. Redaction : ne jamais logger le prompt/rapport en clair s'il contient des secrets (masquage configurable).

Détails **ClaudeCodeRunner** (le plus délicat) :
- Exécuter dans le `cwd` du projet pour hériter du `.mcp.json` et des permissions.
- Passer `--output-format json` et parser `result`, `total_cost_usd`, `num_turns`, `duration_ms`.
- Gérer les codes de sortie non-zéro (auth manquante, permission refusée, timeout) → `Status=failed` + message clair.
- Budget tokens/coût : exposer `cost_usd` dans les métriques, permettre un plafond par job (`max_cost_usd`) qui coupe le run.

### 6.3 ReportStore (fichier / log)

- Écrit systématiquement le `ReportEnvelope` sur disque (même si d'autres diffuseurs sont configurés) — traçabilité.
- Chemin : `reports/<job_id>/<YYYYMMDD-HHMM>-<run_id>.md` + un `latest.md` par job.
- Rétention configurable (purge dans `DoWork()`).

### 6.4 Diffusion (fan-out)

Le worker envoie le rapport à **tous** les diffuseurs du job, indépendamment (l'échec d'un canal n'empêche pas les autres ; chaque échec est loggé + compté).

- **`MailDiffuser`** *(prioritaire)* — appelle la **fonction mail du Hub** via la connexion Go existante. Params : `to`, `cc`, `subject` (template, ex. `[ETL] Rapport {{.job_id}} — {{.status}}`), `format` (`markdown`→HTML ou pièce jointe `.md`). **Le worker ne fait pas de SMTP lui-même** : il délègue au Hub (appel KvBus/gRPC/REST selon ce que le prog Go sait déjà faire — *à confirmer, §16*).
- **`SlackDiffuser`** — webhook Slack ou fonction Slack du Hub. Params : `channel`, `mrkdwn`.
- **`BusDiffuser`** (NATS/Kafka existant) — publie l'enveloppe sur un sujet/topic de ta chaîne de messages existante (ex. sujet `reports.agent_batch.<job_id>`). Réutilise ton bridge actuel plutôt qu'un canal neuf.
- **`FileDiffuser`** — équivalent du cron actuel, mais le contenu est **rédigé par l'agent**.

---

## 7. Actions exposées (ActionProvider)

Le worker implémente `ActionProvider` ; actions invocables via REST et MCP (nommage `agent_batch__<action>`) :

| Action | Mode | Read-only | Description | Paramètres |
|---|---|---|---|---|
| `run_now` | ASYNC | non | Déclenche immédiatement un job (hors planning) | `job_id` (req) |
| `list_jobs` | SYNC | oui | Liste les jobs et leur état (dernier run, prochain déclenchement) | — |
| `get_last_report` | SYNC | oui | Renvoie le dernier rapport `.md` d'un job | `job_id` (req) |
| `enable_job` / `disable_job` | SYNC | non | Active/désactive un job | `job_id` (req) |
| `add_job` / `update_job` | SYNC | non | Crée/modifie un JobSpec (si durabilité PG) | JobSpec (jsonb) |
| `preview_prompt` | SYNC | oui | Résout et renvoie le prompt final sans lancer l'agent | `job_id` (req) |
| `test_diffuser` | SYNC | non | Envoie un rapport factice sur un canal (test mail/slack) | `job_id`, `kind` |

Chaque action renvoie un `ActionResult` (`ofSuccess(map)` / `ofFailure(msg)`), auditée par le Hub (équivalent `techdb_worker_actions`).

---

## 8. Configuration

### 8.1 Config worker (yaml, façon `socle:`)

```yaml
agent_batch:
  enabled: true
  reports_dir: /var/lib/agent_batch/reports
  retention_days: 30
  default_timezone: UTC
  drain_timeout: 120s
  runners:
    claude_code:
      binary: /home/jmhenry_amarena_io/.local/bin/claude
      default_cwd: /home/jmhenry_amarena_io/2025-As400IUI/2026-ETL-InitialLoad-kube
      permission_mode: acceptEdits        # ou un allowlist d'outils
      max_cost_usd: 1.0
  hub:
    mail_enabled: true                     # utilise la fonction mail du Hub
  jobs_source: yaml                        # yaml | pg
```

### 8.2 Exemple de JobSpec (cas fondateur : rapport odhprod à 19:00 → mail)

```yaml
jobs:
  - id: odhprod-flux2-check
    enabled: true
    description: "Vérifie que le Flux 2 DommMarket alimente odhprod, rédige un rapport et l'envoie par mail"
    schedule: "3 19 * * *"                 # 19:03 UTC (off-minute)
    timezone: UTC
    timeout: 5m
    overlap: skip
    on_error: alert
    retry: { max_attempts: 1, backoff: 30s }
    agent:
      kind: claude_code
      params: { cwd: "/home/jmhenry_amarena_io/2025-As400IUI/2026-ETL-InitialLoad-kube" }
    prompt:
      template: |
        Connecte-toi à la base d'état ETL (etl.tr_workflow_run) et aux bases odhprod-db/odhtest-db.
        Vérifie que le workflow #38 (Flux 2 DommMarket) a bien écrit dans odhprod lors du cycle de {{.date}} 18:00 UTC :
        1) statut et message du dernier run #38 (doit mentionner odhprod dans targets),
        2) comparaison des comptages odhprod vs odhtest sur les tables dom_* clés,
        3) verdict clair (succès / partiel / échec).
        Rends UNIQUEMENT un rapport Markdown, titré, avec un tableau de comparaison.
      vars: { date: "{{today}}" }
    diffusers:
      - kind: mail
        params:
          to: "jm@henry.sh"
          subject: "[ETL] odhprod Flux 2 — {{.status}} ({{.date}})"
          format: markdown
      - kind: file
        params: { }                        # copie sur disque, traçabilité
```

### 8.3 Durabilité PG (option)
Table `agent_batch.z_job` (`jsonb` : `spec_json`, `state_json`), historique `agent_batch.tr_run` (façon `tr_workflow_run`). Rechargement `loadActiveJobs()` au démarrage. Permet l'édition à chaud via `add_job/update_job` sans redémarrage.

---

## 9. Intégration au Hub

- **Connexion** : réutiliser le programme Go existant qui sait déjà se connecter au Hub. Le worker publie/consomme sur le **KvBus** du Hub et appelle ses fonctions (mail, éventuellement Slack).
- **Topics KvBus** (proposition) :
  - `agent_batch.run.started` / `agent_batch.run.finished` — enveloppe de run (pour dashboards, chaînage).
  - `agent_batch.report.ready` — publie le `ReportEnvelope` (un `BusDiffuser` ou un autre worker peut s'y abonner).
  - Abonnements admin (dans `Initialize()`) : `agent_batch.run_request` (déclenchement distant), `worker.restart`.
- **Fonction mail du Hub** : appelée par le `MailDiffuser`. *Transport exact à confirmer* (§16) — appel KvBus (`hub.mail.send`), gRPC, ou REST selon l'API du Hub.
- **gRPC (inter-instances)** : optionnel — si plusieurs nœuds, permet à un nœud de déclencher/diffuser vers les pairs (`WorkerService.TriggerWorker`).
- **REST/MCP** : les actions (§7) sont exposées pour pilotage humain/LLM (`agent_batch__run_now`, etc.).

---

## 10. Observabilité

- **Logs** : SLF4J-like structuré, préfixe `[worker:agent_batch][step:schedule|run|diffuse|error]`, corrélation par `run_id` (MDC/champ). Sortie JSON si le Hub l'exige.
- **Supervision** : `IsHealthy()` (< 100ms), `heartbeat(name, meta)` périodique, auto-restart après N checks unhealthy (géré par le MOP/Hub).
- **Stats** (`Stats()` → dashboard) : `jobs_total`, `jobs_enabled`, `runs_ok`, `runs_failed`, `runs_skipped`, `last_run_at`, `last_status_by_job`, `avg_duration_ms`, `total_cost_usd`, `diffuse_failures_by_kind`.
- **Métriques Prometheus** (si dispo) : compteurs/gauges équivalents.

---

## 11. Sécurité

- **Secrets** : credentials des agents (clé API Claude), du Hub, des bases → **hors config en clair** (variables d'env / secret manager). Le worker ne met jamais un secret dans un rapport ou un log.
- **Données sensibles dans les rapports** : un rapport peut contenir des extraits de base (prod). ⚠️
  - Mail : **allowlist de destinataires** ; pas de diffusion large par défaut.
  - Bus/Slack : topics/canaux restreints ; TLS + auth sur le canal.
- **Redaction** configurable (masquage de motifs : mots de passe, tokens) avant diffusion.
- **Confinement d'exécution** : l'agent tourne avec les droits du worker ; définir un `cwd` et un mode permissions **restreints** (surtout Claude Code : éviter un mode « tout autorisé » non nécessaire).

---

## 12. Prérequis & contraintes (lire avant tout)

1. **Réseau** : le worker **doit tourner sur un host ayant accès aux ressources privées** que l'agent doit inspecter (VM `odh-sup-prod` ou pod in-cluster). Un agent cloud externe ne verrait pas `10.193.0.2` ni le cluster.
2. **Claude Code headless** (si `kind=claude_code`) — le vrai point dur :
   - **Auth non-interactive** disponible dans l'environnement du worker (clé API / OAuth), pas seulement dans un shell humain.
   - **Mode permissions** adapté (sinon l'agent bloque sur les confirmations d'outils).
   - **Config MCP** accessible depuis le `cwd` (`.mcp.json`).
   - **Coût tokens** maîtrisé (`max_cost_usd` par job).
3. **Idempotence côté agent** : le prompt doit demander des actions **lecture seule** ou idempotentes si l'agent a des droits d'écriture.
4. **Horloge** : la VM est en **UTC** ; fixer `timezone` par job explicitement.

---

## 13. Flux nominal (séquence)

```
19:03 UTC ── Scheduler: job "odhprod-flux2-check" échu
   └─ executing.CAS(false→true) ok (sinon skip)
   └─ Résolution prompt (vars: date=2026-07-01)
   └─ AgentRunner=ClaudeCodeRunner.Run(prompt, timeout=5m)
        exec: claude -p "<prompt>" --output-format json  (cwd=projet)
        capture stdout → result(Markdown) + metrics(cost, tokens, duration)
   └─ RunResult.Status=success ; ReportEnvelope (front-matter + MD)
   └─ ReportStore.write(reports/odhprod-flux2-check/20260701-1903-<run_id>.md)
   └─ Fan-out diffusion:
        ├─ MailDiffuser → Hub.mail(to=jm@henry.sh, subject="[ETL] odhprod Flux 2 — success (2026-07-01)", body=MD→HTML)
        └─ FileDiffuser → latest.md
   └─ pub("agent_batch.run.finished", enveloppe)   ; Stats++ ; executing=false
```

En cas d'échec (`timeout`/`failed`) : `on_error=alert` → mail d'alerte + `pub("agent_batch.run.finished", status=failed)` ; retry selon `RetryPolicy`.

---

## 14. Découpage en phases (proposition)

1. **MVP mail-only** : worker Go minimal (cycle de vie + scheduler cron 1 job) + `ClaudeCodeRunner` + `MailDiffuser` (Hub) + `FileDiffuser`. Cible : reproduire le check odhprod 19:00 mais rédigé par Claude et envoyé par mail.
2. **Multi-diffusion** : ajouter `SlackDiffuser` + `BusDiffuser` (NATS/Kafka), fan-out + gestion d'échec par canal.
3. **Actions & pilotage** : `ActionProvider` complet (`run_now`, `list_jobs`, `preview_prompt`…), exposition MCP.
4. **Durabilité PG** : jobs + historique en `jsonb`, édition à chaud.
5. **Agents multiples** : `CustomHttpRunner`/`CustomExecRunner` pour tes agents maison ; sélection par job.
6. **Durcissement** : redaction, allowlist, plafonds de coût, métriques Prometheus, gRPC inter-instances.

---

## 15. Décisions à trancher (durant le design)

- **Durabilité des jobs** : YAML (simple) vs PG `jsonb` (édition à chaud, historique). → *reco : PG dès la phase 4.*
- **Scheduler** : réimplémenter un `WorkerScheduler` cron/tz en Go, ou réutiliser une lib (`robfig/cron`) enveloppée dans le contrat Worker.
- **Placement** : VM `odh-sup-prod` (systemd) vs pod in-cluster. → *reco : là où le prog Go existant tourne déjà.*

## 16. Points à confirmer avec toi (dépendent de ton Hub)

1. **Transport de la fonction mail du Hub** : comment le programme Go appelle-t-il l'envoi de mail aujourd'hui ? (topic KvBus `hub.mail.send` ? appel gRPC ? REST ?) — ça fixe l'implémentation de `MailDiffuser`.
2. **API du Hub** que le prog Go expose déjà (pub/sub ? actions ? mail ? slack ?) — pour ne pas réinventer.
3. **Chaîne NATS/Kafka** cible pour `BusDiffuser` : quel sujet/topic, quel format d'enveloppe attendu par les consommateurs.
4. **Agent maison** : contrat d'entrée/sortie (HTTP ? exec ? format du Markdown renvoyé) pour dimensionner `CustomRunner`.

---

*Rédigé sans code, conformément à la demande. Cas d'usage fondateur relié au rapport odhprod (voir `docs/RAPPORT-odhprod-flux2-*.md` et le script transitoire `2026-RUN/odhprod_flux2_check.sh`).*
