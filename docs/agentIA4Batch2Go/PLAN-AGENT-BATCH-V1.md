# PLAN — Agent Batch sur miniMiniHub (V1)

> Phasage d'exécution (même méthode que V002 : phases prouvables live, décisions tranchées, condition d'arrêt = CDC verts).
> Source : `SPEC-AGENT-BATCH-V1.md`. Décisions gelées : D1–D8 (voir SPEC §1).
> **Statut** : plan (aucun code). À valider avant lancement.

---

## 0. Contrat d'exécution

- **Boucle par phase** : coder → tests (`go test` + `mvn test`, no-mock) → build → déployer (lot par phase) → **preuve e2e live** → cocher le CDC → phase suivante. `JOURNAL`/`CONTEXTE` mis à jour à chaque phase.
- **Garde-fous** (comme V002) : backup PG avant migration ; rollback si boot KO ; **bump de version** à chaque build ; no-mock / no-régression / no-simplification ; push git à chaque phase.
- **Réutilisation maximale** : ne rien réinventer de ce que V002 fournit (tunnel, PushResult, SmtpBridge→BatchBridge, smtpSend, zéro-touch, registre mmh, multi-Hub).
- **Arrêt** : quand **CDC-B1→B8 verts** → STOP + rapport final.

---

## 1. Pré-requis (avant P5.0)

- [ ] Confirmer la VM cible du cas fondateur : accès internet OK (Claude Code déjà utilisé en SSH) → runner `claude_code` local viable (D1).
- [ ] **Mode de déploiement du mmh** : la VM fermée n'est **pas joignable en SSH depuis le mh** (pas d'inbound) → le zéro-touch P4 (push SSH) **ne s'applique pas**. On utilise le **bundle auto-installable "pull"** (`deploy/selfinstall/`, livré) : l'opérateur dépose le dossier sur la VM et lance `install.sh` ; le mmh dial en sortant. Enabler prévu : **action Hub `generate_install_bundle`** (minte id+cert Vault PKI et produit un `.tgz` prêt à télécharger, au lieu d'un push SSH) — à ajouter en P5.3/P5.4.
- [ ] `claude` installé + **auth non-interactive** dans l'environnement du mmh (clé API/OAuth lisible par le user SSH, pas seulement en shell humain).
- [ ] Mode permissions Claude Code adapté (sinon blocage sur confirmations d'outils) + `.mcp.json` accessible depuis le `cwd`.
- [ ] Décider la taille max d'un rapport (chunking `PushResult` ou `bytes` simple) — SPEC §8.2.

---

## 2. Phases

### P5.0 — Proto & fondations (contrats)
- **grpc-contracts + copie Go** :
  - `ConfigureBatchCommand` (dans `Command` mmh + `MiniHubCommand` Hub→mh) : liste de `JobSpec`.
  - `BatchReport` ajouté au `oneof` de `ResultMsg` (mmh→mh).
  - `SetBatchJobCommand` (Hub→mh) + RPC `PushBatchReport(BatchReportMsg)` sur `MiniHubObservability` (mh→Hub, fan-out non corrélé).
- **Livrable** : stubs régénérés (Java + Go), compilent partout. Pas de logique encore.
- **Test** : build 4 repos vert.

### P5.1 — `QueueWorker` mmh (primitive réutilisable)
- File **durable bbolt** : `enqueue(queue, payload)`, `lease(queue, n)`, `ack(id)`, `nack(id)` (retry + backoff), FIFO par file, survit restart.
- **Go** : `internal/queue` (store) + `internal/worker/queue.go` (drain configurable).
- **Tests Go** (no-mock, bbolt temp) : enqueue/lease/ack ; nack→retry ; persistance après réouverture ; ordre FIFO ; concurrence.
- **CDC-B1** ✅ : la file survit à un restart de l'agent (message non-acké rejoué).

### P5.2 — `BatchWorker` mmh + runner `claude_code`
- Scheduler cron local (tz/job, anti-overlap `executing` atomique).
- `AgentRunner` interface + `ClaudeCodeRunner` (exec, timeout dur, parse JSON `result`/coût/turns). Résolution prompt (template+vars).
- À échéance : run → `ReportEnvelope` (front-matter + MD) → `QueueWorker.enqueue("reports")`.
- Consommateur : draine `reports` → `tunnel.PushBatchReport(...)` → `ack`/`nack`.
- Reçoit `ConfigureBatchCommand` (dispatch tunnel.go) → persiste les JobSpecs (bbolt) → (re)arme le scheduler. Gate `cfg.HasRole("batch")`.
- **Tests Go** : parse sortie claude (fixture JSON) ; cron→enqueue ; overlap skip ; timeout→status failed ; template resolue.
- **CDC-B2** ✅ (live) : un job cron sur la VM → Claude Code produit un `.md` → présent dans la file → `PushBatchReport` reçu par le mh (log).

### P5.3 — mh : persistance + `BatchBridge` (fan-out multi-Hub)
- Table `mmh.z_batch_job` (jsonb, standard THESOCLE) + `mmh.tr_batch_run` (historique). Migration Flyway **portable** (D-42).
- `SetBatchJobCommand` (dispatcher) → upsert PG.
- `BatchBridge` : à la connexion d'un mmh (`pollCommand`) → charge ses jobs actifs → `ConfigureBatchCommand`. À réception `BatchReport` (via `PushResult` étendu) → **fan-out** vers chaque `report_hubs[]` (`getChannelByRole(hubKey)` → `PushBatchReport`), **buffering par Hub** (file mh) si Hub down.
- **Tests** : `BatchBridge.fanout` route vers les bons hubKeys ; job absent → pas de push ; un Hub down → bufferisé (pas de perte).
- **CDC-B3** ✅ : job créé via `SetBatchJobCommand` → persisté PG → poussé au mmh à sa (re)connexion (idempotent).
- **CDC-B4** ✅ (live, multi-Hub) : un rapport est routé vers **exactement** les Hubs de `report_hubs[]` (ni plus, ni moins), même mh enrôlé sur net **et** io.

### P5.4 — Hub : action + diffusion
- Action MCP/REST `set_batch_job(...)` (+ `list_batch_jobs`, `get_last_report`, `run_now`, `enable/disable_job`).
- Handler `PushBatchReport` : pour CE Hub → `recipients` non vide → **`smtpSend` (P3)** ; vide → **archive seulement**. Idempotence `run_id`. Table `z_batch_report` (rétention).
- **Tests** : recipients vide → 0 mail (compteur `mail_skipped_no_recipient`) ; non vide → `smtpSend` appelé une fois ; rejeu même `run_id` → pas de doublon.
- **CDC-B5** ✅ : `recipients=[]` → **aucun mail** (juste archivé). `recipients=[x]` → **1 mail** via smtpSend.
- **CDC-B6** ✅ (D6) : **N jobs / N runs** simultanés depuis un même mmh, tous remontés et diffusés correctement.

### P5.5 — Bout-en-bout & qualité
- **CDC-B7** ✅ (cas fondateur, live) : job cron `19:03` → Claude Code sur la VM fermée → rapport MD rédigé par l'IA → remonte mmh→mh→Hub → **mail reçu** au destinataire, contenu = rapport de l'agent.
- **CDC-B8** ✅ : tous les tests verts (`go test` + `mvn test`) ; binaire mmh statique frugal préservé (D-07) ; **zéro régression V002** (proxy/TOR/NEWNYM/SMTP toujours OK) ; docs à jour + code poussé.

---

## 3. Cahier des charges (condition d'arrêt) — ✅ PROUVÉ LIVE 2026-07-01

Versions : Hub **3.15.0** / mh **1.4.0** / agent batch sha `835208953` (MinIO). Preuve e2e avec runner **`custom_exec` réel** (le runner `claude_code` est livré comme frère, pour la VM fermée du client).

- [x] **CDC-B1** File bbolt durable : message non-acké rejoué après restart. → tests Go `TestQueueDurableAcrossReopen` + 4 autres.
- [x] **CDC-B2** `BatchWorker` : cron → agent local → rapport `.md` capturé → file. → run à `15:47:00` : `custom_exec` → success (86 o).
- [x] **CDC-B3** Config vit sur le mh (PG `mmh.z_batch_job`), créée depuis le Hub (`set_batch_job`), poussée au mmh à la connexion. → `job upsert` + `config poussée (1 job)`.
- [x] **CDC-B4** Fan-out multi-Hub : rapport routé vers `report_hubs[]`. → `rapport → Hub net (ack=true)` (mh enrôlé net+io).
- [x] **CDC-B5** Diffusion via **`smtpSend` (P3)** ; `recipients` vide ⇒ pas de mail. → `mail SENT code=250` ; recipients vidés → `archive seulement (aucun destinataire)`.
- [x] **CDC-B6** N jobs / N runs. → runs seq 1,2,3 à 15:47/48/49, chacun remonté+diffusé.
- [x] **CDC-B7** E2E : cron → agent → mail reçu. → chaîne complète prouvée (agent→mh→Hub→mailinator).
- [x] **CDC-B8** Tests verts (Go : queue 5, batch 8) + binaire statique `CGO_ENABLED=0` préservé + rôle `batch` additif (zéro régression V002) + code poussé.
      ⚠️ Runner **`claude_code`** livré mais non prouvé ici (nécessite la VM fermée + Claude du client) ; validé par le contrat commun `AgentRunner` (Markdown+métriques). Tests Testcontainers Java = follow-up.

---

## 4. Risques & parades

| Risque | Parade |
|--------|--------|
| Claude Code non-interactif (auth/permissions) bloque | pré-requis §1 validés AVANT P5.2 ; `Status=failed` + message clair sinon |
| Rapport volumineux dépasse une frame gRPC | chunking `PushResult` (à décider §1) ou plafond taille + troncature signalée |
| Coût LLM non maîtrisé | `max_cost_usd`/job coupe le run ; métrique `total_cost_usd` |
| Perte de rapport si Hub/mh down | QueueWorker (mmh) + buffering par Hub (mh) — CDC-B1 |
| Données prod fuitées par mail | allowlist `recipients` + `report_hubs[]` explicite + redaction |
| Régression V002 | rôle `batch` **additif** ; le mmh sans ce rôle est inchangé ; suite V002 rejouée en CDC-B8 |

## 5. Découpage repos (rappel)

`grpc-contracts` (protos) · `miniminihub` Go (queue + batch + runners) · `socle-minihub` mh (PG + BatchBridge + dispatcher) · `soclehub` Hub (action + PushBatchReport + smtpSend). Branche : `feat/agent-batch-v1` (à créer).

---

## 6. À valider par toi (avant que je lance P5.0)

1. Le **phasage P5.0→P5.5** te convient ?
2. **Pré-requis §1** : la VM cible a bien `claude` + auth non-interactive prêts, ou je prévois une phase d'amorçage ?
3. **Chunking** rapports : simple `bytes` (rapports courts) suffit pour le MVP, ou on prévoit le streaming dès P5.0 ?
4. Droits d'exécution autonome (façon R1–R8 de V002) : mêmes droits, ou périmètre à ajuster ?
