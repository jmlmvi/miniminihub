#!/usr/bin/env bash
#
# uninstall.sh — Retire proprement un mmh installé en "pull" (self-install).
# Ne désinstalle PAS tor (partagé/système). Idempotent.
#
set -euo pipefail
REMOTE_DIR="${REMOTE_DIR:-/opt/miniminihub}"
if [ "$(id -u)" = "0" ]; then SUDO=""; else SUDO="sudo -n"; fi

echo "[mmh-uninstall] arrêt de l'agent…"
$SUDO systemctl stop miniminihub 2>/dev/null || true
$SUDO systemctl disable miniminihub 2>/dev/null || true
$SUDO rm -f /etc/systemd/system/miniminihub.service 2>/dev/null || true
$SUDO systemctl daemon-reload 2>/dev/null || true
$SUDO pkill -9 -x agent 2>/dev/null || true

echo "[mmh-uninstall] suppression de $REMOTE_DIR…"
$SUDO rm -rf "$REMOTE_DIR"
echo "[mmh-uninstall] terminé (tor laissé en place ; retirer le bloc '# --- mmh NEWNYM ---' de /etc/tor/torrc si besoin)."
