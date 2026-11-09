#!/usr/bin/env bash
# Désinstalle le LOGICIEL de l'Atelier Bambu (service, binaires, worker CAO). Ne supprime JAMAIS :
#   - /var/lib/atelier (base, fichiers 3D, secrets) ni /var/log/atelier ;
#   - les sauvegardes (dossier de /etc/atelier/backup.env) ;
#   - /etc/atelier (jeton d'API, réglages) ;
#   - l'utilisateur système "atelier" ;
#   - Bambu Studio (/opt/atelier/bambustudio) ni uv installés par la préparation du serveur.
# Pour effacer des données, le faire à la main, en connaissance de cause : ce script ne le propose pas.
#
# Usage : sudo ./uninstall.sh [--yes]
set -euo pipefail

APP_USER=atelier
OPT=/opt/atelier
DATA_DIR=/var/lib/atelier
UNIT_DIR=/etc/systemd/system
ASSUME_YES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y)  ASSUME_YES=1; shift ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "option inconnue : $1" >&2; exit 2 ;;
  esac
done
[ "$(id -u)" -eq 0 ] || { echo "ERREUR : à lancer en root (sudo $0)" >&2; exit 1; }

# Garde-fou : ne jamais toucher aux données, même par erreur de maintenance de ce script.
safe_rm() { # safe_rm CHEMIN... : refuse tout ce qui est, contient ou se trouve dans les données / sauvegardes
  local p real backup_dir=""
  [ -f /etc/atelier/backup.env ] && backup_dir="$(grep -E '^BACKUP_DIR=' /etc/atelier/backup.env | tail -n1 | cut -d= -f2- | tr -d '"')"
  for p in "$@"; do
    [ -e "$p" ] || [ -L "$p" ] || continue
    real="$(readlink -f -- "$p")"
    case "$real/" in
      "$DATA_DIR"/*|/var/lib/atelier/*|/var/log/atelier/*|/etc/atelier/*|/var/backups/*) echo "REFUS de supprimer $p (données ou configuration)" >&2; exit 3 ;;
    esac
    if [ -n "$backup_dir" ]; then
      case "$real/" in "$backup_dir"/*|"$(readlink -f -- "$backup_dir")"/*) echo "REFUS de supprimer $p (sauvegardes)" >&2; exit 3 ;; esac
      case "$(readlink -f -- "$backup_dir")/" in "$real"/*) echo "REFUS de supprimer $p (contient les sauvegardes)" >&2; exit 3 ;; esac
    fi
    case "$real/" in "$DATA_DIR"/|/|/opt/|/var/|/etc/|/usr/) echo "REFUS de supprimer $p" >&2; exit 3 ;; esac
    rm -rf -- "$p"
    echo "supprimé : $p"
  done
}

cat <<EOF
Cette opération arrête l'Atelier Bambu et retire son logiciel :
  - services systemd atelier.service, atelier-backup.service/.timer
  - $OPT/bin (bambud, bambu-mcp, atelier-admin, atelier-backup.sh, uv) et $OPT/cad, $OPT/uv-python
  - si les mises à jour automatiques sont activées : atelier-update.service/.timer, $OPT/releases, $OPT/updater, liens current/previous
Conservés : $DATA_DIR (vos données), les sauvegardes, /etc/atelier, les utilisateurs système (atelier, atelier-update), Bambu Studio.
EOF
if [ "$ASSUME_YES" != 1 ]; then
  printf 'Continuer ? [o/N] '
  read -r ans
  case "$ans" in o|O|oui|OUI|y|Y) ;; *) echo "annulé"; exit 0 ;; esac
fi

for u in atelier-update.timer atelier-update.service atelier-backup.timer atelier-backup.service atelier.service; do
  if systemctl list-unit-files "$u" >/dev/null 2>&1; then
    systemctl disable --now "$u" >/dev/null 2>&1 || true
  fi
done
for f in "$UNIT_DIR/atelier.service" "$UNIT_DIR/atelier-backup.service" "$UNIT_DIR/atelier-backup.timer" "$UNIT_DIR/atelier-update.service" "$UNIT_DIR/atelier-update.timer"; do
  # Seulement les unités écrites par install.sh (une unité personnalisée à la main est laissée).
  if [ -f "$f" ] && grep -qE "Géré par deploy/linux/(install|enable-auto-update).sh" "$f"; then safe_rm "$f"; elif [ -f "$f" ]; then echo "conservé (non géré par install.sh) : $f"; fi
done
systemctl daemon-reload

[ -L /usr/local/bin/atelier-admin ] && rm -f /usr/local/bin/atelier-admin
# Mises à jour automatiques (si activées) : versions construites, liens current/previous, outil de mise à jour.
[ -L /usr/local/sbin/atelier-update ] || [ -f /usr/local/sbin/atelier-update ] && rm -f /usr/local/sbin/atelier-update
rm -f "$OPT/current" "$OPT/previous"
safe_rm "$OPT/bin" "$OPT/cad" "$OPT/uv-python" "$OPT/releases" "$OPT/updater"

echo
echo "Logiciel retiré. Vos données sont intactes : $DATA_DIR"
echo "Pour réinstaller : sudo deploy/linux/install.sh --from <livraison>   (les données et le jeton d'API sont réutilisés)"
