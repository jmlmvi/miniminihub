# SPEC — Agent Batch sur miniMiniHub (V1)

> **Statut** : spécification (aucun code). Rédigée le 2026-07-01. Remplace/affine `specs.md` (qui décrivait un worker autonome générique) en **inversant l'architecture** pour l'aligner sur la fabric miniMiniHub V002 (égress).
> **Périmètre** : réutiliser le **mmh** (agent Go léger) comme **bras de collecte** dans un système fermé, piloté par le **mh**, avec remontée + diffusion côté **Hub**.

---

## 0. Idée en une phrase

Déposer un **mmh léger** sur une VM d'un **système fermé** (ex. GCP), lui faire **lancer des agents IA planifiés** (cron) qui produisent des **rapports Markdown**, et **remonter** ces rapports par le tunnel gRPC sortant existant (mmh → mh → Hub) pour **diffusion** — le tout **configuré côté mh**, **créé depuis le Hub**.

Le mmh **n'est pas un diffuseur** : c'est une **aide non-lourde de récupération d'information** dans un réseau fermé (il dial en **sortant** → aucune règle firewall/inbound à ouvrir, D-01). La diffusion (mail) se fait **côté Hub**.

**Cas fondateur** : remplacer un `cron + psql` par un rapport **rédigé par Claude Code** exécuté sur la VM fermée (qui a déjà internet via SSH), remonté et **envoyé par mail via `smtpSend` (P3)**.

---

## 1. Décisions gelées (tranchées en discussion)

| # | Décision | Choix |
|---|----------|-------|
| D1 | Où tourne l'agent (LLM) ? | **En local sur la VM** (la VM a internet). Runner `claude_code` pour le cas fondateur. |
| D2 | Runners supportés | **Tous** (abstraction `AgentRunner`) : `claude_code`, `custom_exec`, `custom_http`, `agentia`. |
| D3 | Diffusion mail | **`smtpSend` (P3)** côté Hub (remise MX via un mmh à rôle `smtp`), **pas** james. |
| D4 | Source de vérité de la config | **Le mh** (table PG `mmh.z_batch_job`). **Créée depuis le Hub**, mais **vit sur le mh**. |
| D5 | Persistance côté mmh | **File durable bbolt** (le mmh a déjà son store). 2 workers : `QueueWorker` (**générique, réutilisable**) + `BatchWorker`. |
| D6 | Nombre de jobs / runs | **N jobs, N agents, N exécutions** par mmh (planning multiple). |
| D7 | Multi-Hub | Le **mh peut être connecté à N Hubs**. Le rapport est routé vers **`report_hubs[]`** ; le **mh est le point de fan-out**. |
| D8 | Doublon de mail | **(b) chaque Hub a ses propres destinataires** → pas de doublon par construction. `recipients` vide → **on n'envoie pas** (remonté/archivé seulement). |

---

## 2. Topologie & flux

```
  Hub A ─┐  (crée le job : action MCP set_batch_job)
  Hub B ─┼──────────────▶  mh  (PG mmh.z_batch_job = SOURCE DE VÉRITÉ)
  Hub C ─┘                  │  push config (ConfigureBatchCommand) à la connexion / au changement
                            ▼
   [ réseau fermé / VM GCP ]        mmh :
        Claude Code (local) ◀── BatchWorker (cron, N jobs, résout prompt, lance runner)
              │ rapport .md + métriques
              ▼
        QueueWorker (bbolt, durable, retry, survit reconnexion)
              │ flush
        BatchReport ── PushResult ─▶ mh
                                     │  FAN-OUT vers report_hubs[] (buffering par Hub)
                        ┌────────────┼────────────┐
                        ▼            ▼            ▼
                     Hub A        Hub B        Hub C
                  smtpSend(dest A)  (dest B)   recipients=[] → pas de mail (archive)
```

**Invariant** : le mmh ne parle qu'à **son** mh (un seul parent). Le **fan-out multi-Hub est la responsabilité du mh** (c'est lui qui est enrôlé sur N Hubs, cf. multi-Hub V002 `HubLink[]`).

---

## 3. Ce qu'on RÉUTILISE (déjà livré + prouvé en V002)

| Brique | Réutilisation |
|--------|---------------|
| Squelette Go mmh (`internal/mop` MOP+`Worker`, `internal/bus`, `internal/store` bbolt) | héberge les 2 nouveaux workers |
| Tunnel gRPC mTLS sortant + `PollCommand` (Hub→mh→mmh) | transport des commandes (`ConfigureBatchCommand`) |
| RPC `PushResult` (mmh→mh) + `oneof` `ResultMsg` | remontée du `BatchReport` (on ajoute une branche au `oneof`, comme `SmtpResult` en P3) |
| Pattern `SmtpBridge` (mh) : mémorise + relaie au bon Hub | **jumeau** `BatchBridge` (fan-out multi-Hub) |
| `EgressService.smtpSend` + `POST /api/minihub/egress/smtp` (P3) | **diffusion mail** des rapports |
| Zéro-touch `deploy_miniminihub` (P4) | déposer le mmh sur la VM fermée |
| `MiniMiniHubRegistry` (PG schéma `mmh`) | accueille la table `z_batch_job` |
| Multi-Hub `HubLink[]` + `getChannelByRole(hubKey,...)` | canaux de fan-out vers les Hubs |

## 4. Ce qu'on AJOUTE

### 4.1 Côté mmh (Go) — 2 workers

- **`QueueWorker`** (générique, réutilisable) : file **durable bbolt** avec `enqueue / lease / ack / nack+backoff`, ordre FIFO par file nommée, survit au restart et à la déconnexion. Réutilisable ailleurs (buffer résultats égress, HB hors-ligne…). **Ne connaît rien du métier batch** — c'est une primitive.
- **`BatchWorker`** (rôle `batch`) :
  - détient les **JobSpecs** (reçus par `ConfigureBatchCommand`, persistés bbolt) ;
  - **scheduler cron local** (tz par job, anti-overlap) → à échéance, résout le prompt (template+vars) et lance le **`AgentRunner`** choisi ;
  - capture **Markdown + métriques** → construit un `ReportEnvelope` → **enqueue** dans `QueueWorker` (file `reports`) ;
  - un consommateur draine la file → `PushResult(BatchReport)` vers le mh ; `ack` sur succès, `nack` (retry/backoff) sinon.
  - `AgentRunner` pluggable : `claude_code` (`claude -p … --output-format json`, cwd projet, parse `result`/coût/turns), `custom_exec`, `custom_http`, `agentia`.

### 4.2 Proto (grpc-contracts + copie Go)

- `ConfigureBatchCommand` (Hub→mh→mmh via `Command`/`MiniHubCommand`) : liste de `JobSpec` (id, cron, tz, agent, prompt, timeout, overlap, retry, **report_hubs[]**) — remplace intégralement la config du mmh (déclaratif).
- `BatchReport` ajouté au `oneof` de `ResultMsg` (mmh→mh) : `job_id`, `run_id`, `status`, `report_md`, `metrics`, `started_at`, `report_hubs[]` (recopiés du job pour le routage).
- `SetBatchJobCommand` (Hub→mh, via `MiniHubCommand`) : crée/modifie un job pour un `miniminihub_id`.
- RPC (mh→Hub) : `PushBatchReport(BatchReportMsg)` sur `MiniHubObservability` (fan-out non corrélé, contrairement à `PushMiniHubState` requête/réponse).

### 4.3 Côté mh (Java)

- Table `mmh.z_batch_job` (jsonb `spec_json`, `state_json`, `owner_hub`, standard THESOCLE) + historique `mmh.tr_batch_run`.
- `BatchBridge` (jumeau `SmtpBridge`) : à la connexion d'un mmh → pousse ses `JobSpec` (`ConfigureBatchCommand`) ; à réception d'un `BatchReport` → **fan-out** vers chaque `report_hubs[]` via `getChannelByRole(hubKey)`, **buffering par Hub** si un Hub est down (file mh).
- Dispatcher : `hasSetBatchJob` (Hub→mh) → persistance + push.
- Handler `PushResult` étendu : `hasBatchReport` → `BatchBridge.forwardReport`.

### 4.4 Côté Hub (Java)

- Action MCP/REST `set_batch_job(miniminihub_id, cron, tz, agent, prompt, report_hubs[])` (+ `list_batch_jobs`, `get_last_report`, `run_now`, `disable_job`) → envoie `SetBatchJobCommand` au mh.
- Handler `PushBatchReport` : à réception d'un rapport pour CE Hub → si `recipients` non vide → **`smtpSend`** (P3) au(x) destinataire(s) ; sinon **archive seulement** (log/store). Idempotence par `run_id`.

---

## 5. Modèle de données (JobSpec)

```yaml
job:
  id: odhprod-flux2-check
  enabled: true
  schedule: "3 19 * * *"          # cron 5 champs
  timezone: UTC
  overlap: skip                    # skip | queue | cancel_previous
  timeout: 5m
  retry: { max_attempts: 1, backoff: 30s }
  agent:
    kind: claude_code              # claude_code | custom_exec | custom_http | agentia
    params: { cwd: "/home/.../projet", model: "...", max_cost_usd: 1.0 }
  prompt:
    template: "…{{.date}}…"
    vars: { date: "{{today}}" }
  report_hubs:                     # FAN-OUT (D7/D8)
    - hub: net
      recipients: ["jm@henry.sh"]  # → smtpSend
      subject_tpl: "[ETL] {{job}} — {{status}} ({{date}})"
    - hub: io
      recipients: []               # → archivé, PAS de mail
```

---

## 6. Sécurité & garde-fous

- **Secrets** (clé LLM, creds DB) : hors config claire (env/Vault) ; jamais dans un rapport ou un log.
- **Redaction** configurable avant remontée (masquage tokens/mots de passe).
- **Confinement** : l'agent tourne avec les droits du mmh (user SSH) ; `cwd` + mode permissions **restreints** (Claude Code : pas de « tout autorisé »).
- **Plafond coût** (`max_cost_usd`/job) qui coupe le run.
- **Idempotence** : le prompt demande des actions **lecture seule** sur le système fermé.
- **Données prod dans les rapports** : `recipients` en **allowlist** ; `report_hubs[]` explicite (pas de diffusion large par défaut).

## 7. Observabilité

Logs `[worker:batch|queue][step:schedule|run|enqueue|flush|error]` corrélés `run_id`. Stats mmh : `jobs_total`, `runs_ok/failed/skipped`, `queue_depth`, `queue_oldest_ms`, `avg_duration_ms`, `total_cost_usd`. Côté mh/Hub : `batch_reports_relayed`, `mail_sent`, `mail_skipped_no_recipient`, `fanout_buffered_by_hub`.

## 8. Points à confirmer (mineurs, non bloquants)

1. `agentia` runner : appel direct API AgentIA depuis la VM, ou via le tunnel (mmh→mh→Hub→AgentIA) ? (défaut : direct, la VM a internet.)
2. Taille max d'un rapport remonté (chunking `PushResult` si > N Mo ? sinon simple `bytes`).
3. Rétention des rapports archivés côté Hub (table `z_batch_report` + purge).

---

*Voir le phasage : `PLAN-AGENT-BATCH-V1.md`.*
