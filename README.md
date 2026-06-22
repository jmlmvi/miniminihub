# miniMiniHub

Relais frugal en Go — **exit node** rattaché à un minihub parent (modèle fractal
`Hub ◄─ minihub ◄─ miniMiniHub`). Binaire statique, sans Docker/JVM, pour VM légères.

Spécification & plan : `../docs/` (SPEC / PLAN / ARCHI-GO-MOP / DECISIONS).

## Architecture (noyau)

MOP (`internal/mop`) supervisant des Workers (goroutines), principes Socle V005 transposés :

```
cmd/miniminihub/   agent (lit bootstrap.json -> mop.Supervisor.Run)
cmd/parent-stub/   faux minihub parent (Phase 0 — preuve de tunnel uniquement)
internal/mop/      Worker (interface) + Deps + Supervisor
internal/tunnel/   canal gRPC sortant : dial, heartbeat, pollcommand
internal/worker/   TunnelWorker (1 worker = 1 fichier)
internal/config/   bootstrap.json
proto/mmhpb/       contrat gRPC (MiniMiniHubControl)
```

## Build

```bash
make proto         # génère les stubs gRPC (protoc requis)
make build-linux   # agent statique linux/amd64 (CGO_ENABLED=0)
make stub          # parent-stub (Phase 0)
```

## État

- **Phase 0 (walking skeleton)** ✅ : tunnel sortant prouvé (001 → stub sur 002),
  heartbeat + PollCommand + Ping aller-retour. Binaire ~10 Mo, RSS ~11 Mo.
- Phase 1+ : identité/mTLS, store bbolt, déploiement SSH, rôles proxy/SMTP/jobs.
  Voir le PLAN.

> `parent-stub` est un outil de test (preuve de tunnel), **pas** un composant de production.
