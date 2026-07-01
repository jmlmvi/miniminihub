#!/usr/bin/env bash
#
# install.sh — Auto-installation d'un agent miniMiniHub sur une VM en "pull".
#
# Cas d'usage : la VM (système fermé) n'est PAS joignable en SSH depuis le mh
# (pas de connexion entrante), mais a un accès internet SORTANT. On dépose donc
# ce bundle manuellement sur la VM (scp/USB) et on lance ce script : le mmh
# s'installe et dial en SORTANT vers son minihub parent (aucun port entrant).
#
# Transcription locale de la logique zéro-touch V002 P4 (MiniMiniHubDeployHandler) :
# mkdir/chown /opt, dépôt certs+bootstrap, binaire, tor optionnel (NEWNYM), start.
#
# Idempotent : relançable sans casse. Sudo-aware (root => pas de sudo).
#
# Le bundle DOIT contenir (mintés par le Hub, cf. README) :
#   bootstrap.json  ca.crt  client.crt  client.key
# Optionnel : agent (binaire) — sinon téléchargé depuis BINARY_URL.
#
# Variables (surchargeables par l'environnement) :
#   REMOTE_DIR   (def: /opt/miniminihub)
#   WITH_TOR     (def: 1 si roles contient "proxy" dans bootstrap.json, sinon 0)
#   MODE         (def: auto → systemd si droits, sinon nohup)
#   BINARY_URL   (def: https://minio.thesocle.net/templates/miniminihub-linux-amd64)
#   BINARY_SHA256(def: vide → pas de vérif)
#
set -euo pipefail

BUNDLE_DIR="$(cd "$(dirname "$0")" && pwd)"
REMOTE_DIR="${REMOTE_DIR:-/opt/miniminihub}"
BINARY_URL="${BINARY_URL:-https://minio.thesocle.net/templates/miniminihub-linux-amd64}"
BINARY_SHA256="${BINARY_SHA256:-}"
MODE="${MODE:-auto}"

log() { printf '[mmh-install] %s\n' "$*"; }
die() { printf '[mmh-install][ERREUR] %s\n' "$*" >&2; exit 1; }

# ---- sudo-aware -------------------------------------------------------------
if [ "$(id -u)" = "0" ]; then SUDO=""; else SUDO="sudo -n"; fi
run_priv() { $SUDO "$@"; }
USER_NAME="$(id -un)"

# ---- pré-requis bundle ------------------------------------------------------
for f in bootstrap.json ca.crt client.crt client.key; do
  [ -f "$BUNDLE_DIR/$f" ] || die "fichier manquant dans le bundle : $f (à minter depuis le Hub — voir README)"
done

# roles → décide WITH_TOR par défaut (tor requis pour via_tor + NEWNYM)
if [ -z "${WITH_TOR:-}" ]; then
  if grep -q '"proxy"' "$BUNDLE_DIR/bootstrap.json"; then WITH_TOR=1; else WITH_TOR=0; fi
fi

log "cible=$REMOTE_DIR user=$USER_NAME with_tor=$WITH_TOR mode=$MODE"

# ---- 1. arborescence (/opt est root : sudo + chown à l'user courant) --------
run_priv mkdir -p "$REMOTE_DIR"
run_priv chown "$USER_NAME:$USER_NAME" "$REMOTE_DIR"
chmod 0700 "$REMOTE_DIR"

# ---- 2. bundle mTLS + bootstrap --------------------------------------------
install -m 0444 "$BUNDLE_DIR/ca.crt"     "$REMOTE_DIR/ca.crt"
install -m 0444 "$BUNDLE_DIR/client.crt" "$REMOTE_DIR/client.crt"
install -m 0400 "$BUNDLE_DIR/client.key" "$REMOTE_DIR/client.key"
install -m 0400 "$BUNDLE_DIR/bootstrap.json" "$REMOTE_DIR/bootstrap.json"

# ---- 3. binaire : bundle local sinon téléchargement ------------------------
if [ -f "$BUNDLE_DIR/agent" ]; then
  log "binaire fourni dans le bundle"
  install -m 0755 "$BUNDLE_DIR/agent" "$REMOTE_DIR/agent"
else
  log "téléchargement du binaire : $BINARY_URL"
  curl -fsSL "$BINARY_URL" -o "$REMOTE_DIR/agent"
  chmod 0755 "$REMOTE_DIR/agent"
fi
if [ -n "$BINARY_SHA256" ]; then
  echo "$BINARY_SHA256  $REMOTE_DIR/agent" | sha256sum -c - || die "sha256 du binaire invalide"
fi

# ---- 4. tor optionnel (via_tor + NEWNYM, idempotent) -----------------------
if [ "$WITH_TOR" = "1" ]; then
  log "installation/config tor (ControlPort 9051 + NEWNYM)"
  run_priv sh -c '
    set -e
    if ! command -v tor >/dev/null 2>&1; then
      apt-get update -qq
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq tor
    fi
    sed -i "/# --- mmh NEWNYM ---/,/# --- end mmh ---/d" /etc/tor/torrc
    printf "\n# --- mmh NEWNYM ---\nControlPort 9051\nCookieAuthentication 1\nCookieAuthFileGroupReadable 1\n# --- end mmh ---\n" >> /etc/tor/torrc
    usermod -aG debian-tor '"$USER_NAME"'
    systemctl restart tor@default 2>/dev/null || systemctl restart tor 2>/dev/null || service tor restart
  '
fi

# ---- 5. démarrage : systemd (si droits) sinon nohup ------------------------
start_nohup() {
  # sg debian-tor : le groupe ajouté n'est pas visible de la session courante,
  # mais sg relit /etc/group à chaud → l'agent lit le cookie NEWNYM sans re-login.
  local cmd="MMH_BOOTSTRAP=$REMOTE_DIR/bootstrap.json setsid nohup ./agent > $REMOTE_DIR/agent.log 2>&1 < /dev/null &"
  ( cd "$REMOTE_DIR"
    if [ "$WITH_TOR" = "1" ]; then sg debian-tor -c "$cmd"; else eval "$cmd"; fi )
  log "agent démarré (nohup)"
}

start_systemd() {
  run_priv sh -c "cat > /etc/systemd/system/miniminihub.service <<'UNIT'
[Unit]
Description=miniMiniHub agent (LMVI, self-install)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$USER_NAME
$([ "$WITH_TOR" = "1" ] && echo 'SupplementaryGroups=debian-tor')
WorkingDirectory=$REMOTE_DIR
Environment=MMH_BOOTSTRAP=$REMOTE_DIR/bootstrap.json
ExecStart=$REMOTE_DIR/agent
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable miniminihub
  systemctl restart miniminihub"
  log "agent démarré (systemd unit miniminihub)"
}

# stoppe une éventuelle instance précédente (idempotence)
run_priv pkill -9 -x agent 2>/dev/null || true
rm -f "$REMOTE_DIR/store.db"   # évite un lock bbolt résiduel

case "$MODE" in
  systemd) start_systemd ;;
  nohup)   start_nohup ;;
  auto)
    if $SUDO true 2>/dev/null; then start_systemd; else start_nohup; fi ;;
  *) die "MODE inconnu : $MODE (systemd|nohup|auto)" ;;
esac

# ---- 6. vérification --------------------------------------------------------
sleep 6
if [ "$WITH_TOR" = "1" ]; then
  run_priv ss -ltn 2>/dev/null | grep -q 9051 && log "tor ControlPort 9051 OK" || log "AVERTISSEMENT: 9051 non à l'écoute"
fi
if grep -q "heartbeat ack" "$REMOTE_DIR/agent.log" 2>/dev/null; then
  log "✅ enrôlé : heartbeat ack reçu du minihub parent."
else
  log "agent lancé — vérifier l'enrôlement : tail -f $REMOTE_DIR/agent.log"
fi
log "terminé."
