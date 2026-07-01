# V005-GO — Règles de développement Go

> Standards de développement pour les programmes Go de l'écosystème Socle V005.
> Audience : développeurs Go senior. Ces règles sont **prescriptives**.

---

## Contexte

Les programmes Go de l'écosystème LMVI sont des **daemons opérationnels** qui gravitent autour du backend Socle V005 :

| Programme | Rôle |
|-----------|------|
| `lmvi-backup` | Daemon de backup généralisé — appelle les actions MCP TechDbBackupWorker, stocke sur S3/local |
| `lmvi-watchdog` | Surveillance d'instances Socle, alertes, redémarrage automatique |
| `lmvi-exporter` | Export de données TechDB vers des systèmes externes (BI, Datawarehouse) |
| `lmvi-agent` | Agent local sur les serveurs, exécute des commandes MCP depuis SocleHub |

Go est choisi pour ces programmes car : binaire unique sans dépendance runtime, démarrage < 10ms, faible consommation mémoire, goroutines natives pour la concurrence, cross-compilation triviale.

---

## Structure de cette documentation

```
docs/
├── README.md                      ← Ce fichier
├── 01-principes.md                ← Philosophie Go dans l'écosystème Socle V005
├── 02-structure-projet.md         ← Modules, packages, organisation des fichiers
├── 03-conventions-go.md           ← Nommage, formatting, idiomes Go
├── 04-concurrence.md              ← Goroutines, channels, sync — patterns et pièges
├── 05-erreurs-et-logs.md          ← Gestion d'erreurs, logging structuré
├── 06-integration-socle.md        ← Client HTTP Socle V005, MCP, authentification
├── 07-backup-daemon.md            ← Spec du daemon de backup généralisé
└── 08-regles-absolues.md          ← Les interdictions non-négociables
```

## Lecture rapide par besoin

| Besoin | Fichier |
|--------|---------|
| Comprendre le rôle des programmes Go | `01-principes.md` |
| Démarrer un nouveau programme | `02-structure-projet.md` |
| Nommer, formater, structurer le code | `03-conventions-go.md` |
| Goroutines, shutdown gracieux, timeouts | `04-concurrence.md` |
| Retourner et logger les erreurs | `05-erreurs-et-logs.md` |
| Appeler Socle V005 depuis Go | `06-integration-socle.md` |
| Implémenter le daemon de backup | `07-backup-daemon.md` |
| Ce qu'il ne faut JAMAIS faire | `08-regles-absolues.md` |
