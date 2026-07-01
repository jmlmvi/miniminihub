# miniMiniHub — bundle d'auto-installation (mode "pull")

Pour installer un mmh sur une VM **non joignable en SSH depuis le mh** (système fermé, pas de connexion entrante) mais disposant d'un **accès internet sortant**. Le mmh dial en **sortant** vers son minihub parent — aucun port entrant à ouvrir.

> Alternative au zéro-touch V002 P4 (qui pousse par SSH *vers* la VM). Ici, **c'est toi** qui déposes le bundle sur la VM et lances l'installation. Le résultat est identique : un mmh enrôlé qui remonte au mh.

## Contenu du bundle

| Fichier | Rôle | Origine |
|---|---|---|
| `install.sh` | installateur (idempotent, sudo-aware, tor optionnel) | fourni |
| `uninstall.sh` | désinstallation propre | fourni |
| `bootstrap.example.json` | gabarit de config | fourni |
| `bootstrap.json` | **config réelle** (id, slug, parent, roles) | **à minter depuis le Hub** |
| `ca.crt`, `client.crt`, `client.key` | **bundle mTLS** de l'identité mmh | **à minter depuis le Hub** |
| `agent` (optionnel) | binaire Go | fourni OU téléchargé depuis MinIO |

## Étapes

### 1. Générer l'identité (depuis le Hub)
Minter l'`miniminihub_id` + le certificat mTLS (Vault PKI) — comme le fait le zéro-touch, mais en **mode bundle** (récupération des fichiers au lieu d'un push SSH). Récupérer : `bootstrap.json` (renseigner `miniminihub_id`, `slug`, `parent_minihub_endpoint`, `roles`) + `ca.crt` + `client.crt` + `client.key`.

> Une action Hub `generate_install_bundle` (produit un `.tgz` prêt) est prévue au plan (voir `../../docs/agentIA4Batch2Go/PLAN-AGENT-BATCH-V1.md`). En attendant, ces fichiers se récupèrent manuellement côté Hub/Vault.

### 2. Déposer le bundle sur la VM
Copier tout ce dossier sur la VM (scp, clé USB, artefact CI interne…) puis :

```bash
chmod +x install.sh
./install.sh
```

Options (variables d'environnement) :
```bash
WITH_TOR=1 MODE=systemd ./install.sh          # forcer tor + systemd
BINARY_URL=https://.../agent ./install.sh     # source binaire alternative
BINARY_SHA256=<sha> ./install.sh              # vérifier le binaire
REMOTE_DIR=/opt/miniminihub ./install.sh      # répertoire cible
```

Par défaut : `WITH_TOR` déduit des `roles` (1 si `proxy`), `MODE=auto` (systemd si droits root/sudo, sinon nohup), binaire téléchargé depuis MinIO si absent du bundle.

### 3. Vérifier l'enrôlement
```bash
tail -f /opt/miniminihub/agent.log      # attendre "heartbeat ack"
```
Côté Hub, le nœud doit apparaître dans le pool d'égress (`/api/minihub/egress/pool`).

## Ce que fait `install.sh`
1. `/opt/miniminihub` créé (sudo si non-root) + chown à l'utilisateur courant.
2. Dépose `ca.crt` (0444), `client.crt` (0444), `client.key` (0400), `bootstrap.json` (0400).
3. Binaire : utilise `./agent` du bundle, sinon télécharge `BINARY_URL` (+ vérif sha256 optionnelle).
4. Si `WITH_TOR=1` : installe tor (apt), configure `torrc` (ControlPort 9051 + CookieAuthentication + GroupReadable), ajoute l'utilisateur à `debian-tor`, redémarre tor.
5. Démarre l'agent : **systemd** (`SupplementaryGroups=debian-tor`) si droits, sinon **nohup via `sg debian-tor`** (pour lire le cookie NEWNYM sans re-login).
6. Vérifie : ControlPort 9051 à l'écoute + `heartbeat ack` dans le log.

## Désinstallation
```bash
./uninstall.sh      # arrête + retire /opt/miniminihub (laisse tor en place)
```

## Sécurité
- `client.key` en `0400`. Ne jamais commiter un `bootstrap.json`/`client.key` **réel** dans le repo (ce dossier ne contient que des gabarits).
- Le bundle porte l'identité d'**un** mmh : le générer par VM, ne pas réutiliser un `client.key` entre VMs.
