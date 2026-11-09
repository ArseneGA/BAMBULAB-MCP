#!/usr/bin/env bash
# Installe (ou met à jour) l'Atelier Bambu sur le serveur Linux préparé par docs/SETUP-LINUX-PROMPT.md.
# Idempotent : relancer le script met à jour les binaires et la configuration sans rien casser ni perdre.
#
# Usage (en root) :
#   sudo ./install.sh --from <dossier de la livraison> [options]
#
# Options :
#   --from DIR            dossier contenant bambud-linux-<arch>, bambu-mcp-linux-<arch> et atelier-cad-worker-*.tar.gz
#                         (défaut : dist/<version> le plus récent trouvé près du dépôt, sinon le dossier courant)
#   --public-url URL      URL sous laquelle l'humain ouvre l'Atelier (ex. http://192.168.1.20:7878) ; défaut : http://<IP>:7878
#   --listen ADDR         adresse d'écoute (défaut 0.0.0.0:7878)
#   --allowed-hosts LIST  noms d'hôte acceptés en plus du loopback, séparés par des virgules (défaut : nom d'hôte, .local, IP)
#   --import-root DIR     racine d'import autorisée (répétable)
#   --studio PATH         AppImage (ou exécutable) de Bambu Studio (défaut : /opt/atelier/bambustudio/*.AppImage)
#   --backup-dir DIR      dossier des sauvegardes quotidiennes (défaut /var/backups/atelier ; mettre un disque externe)
#   --backup-keep N       nombre de sauvegardes conservées (défaut 14)
#   --backup-time HH:MM   heure de la sauvegarde quotidienne (défaut 03:30)
#   --no-cad              n'installe pas le worker CAO
#   --no-backup-timer     n'installe pas la sauvegarde quotidienne
#   --no-start            n'active ni ne démarre le service
#   --no-restart          met à jour sans redémarrer un service déjà en marche (à relancer hors impression)
#   --auto-update         active ensuite les mises à jour automatiques (deploy/linux/enable-auto-update.sh) ; désactivé par défaut
#   --update-window H-H   avec --auto-update : plage horaire d'installation (ex. 07:00-23:00)
#   -h, --help            cette aide
#
# Sur un serveur dont les mises à jour automatiques sont activées (lien /opt/atelier/current), ce script installe les
# binaires comme une NOUVELLE version dans /opt/atelier/releases/manual-* et bascule `current` : il n'écrase jamais
# les liens de /opt/atelier/bin, et prend le même verrou que la mise à jour automatique.
set -euo pipefail

APP_USER=atelier
OPT=/opt/atelier
BIN_DIR=$OPT/bin
CAD_DIR=$OPT/cad
STUDIO_DIR=$OPT/bambustudio
DATA_DIR=/var/lib/atelier
LOG_DIR=/var/log/atelier
CONF_DIR=/etc/atelier
ENV_FILE=$CONF_DIR/atelier.env
BACKUP_ENV=$CONF_DIR/backup.env
UNIT_DIR=/etc/systemd/system
MARK="# Géré par deploy/linux/install.sh - relancer le script pour le régénérer"

FROM=""
PUBLIC_URL=""
LISTEN="0.0.0.0:7878"
ALLOWED_HOSTS=""
IMPORT_ROOTS=()
STUDIO=""
BACKUP_DIR=/var/backups/atelier
BACKUP_KEEP=14
BACKUP_TIME="03:30"
WITH_CAD=1
WITH_TIMER=1
START=1
RESTART=1
AUTO_UPDATE=0
UPDATE_WINDOW=""

say()  { printf '\n==> %s\n' "$*"; }
warn() { printf 'ATTENTION : %s\n' "$*" >&2; }
die()  { printf 'ERREUR : %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --from)             FROM="${2:?--from attend un dossier}"; shift 2 ;;
    --public-url)       PUBLIC_URL="${2:?}"; shift 2 ;;
    --listen)           LISTEN="${2:?}"; shift 2 ;;
    --allowed-hosts)    ALLOWED_HOSTS="${2:?}"; shift 2 ;;
    --import-root)      IMPORT_ROOTS+=("${2:?}"); shift 2 ;;
    --studio)           STUDIO="${2:?}"; shift 2 ;;
    --backup-dir)       BACKUP_DIR="${2:?}"; shift 2 ;;
    --backup-keep)      BACKUP_KEEP="${2:?}"; shift 2 ;;
    --backup-time)      BACKUP_TIME="${2:?}"; shift 2 ;;
    --no-cad)           WITH_CAD=0; shift ;;
    --no-backup-timer)  WITH_TIMER=0; shift ;;
    --no-start)         START=0; shift ;;
    --no-restart)       RESTART=0; shift ;;
    --auto-update)      AUTO_UPDATE=1; shift ;;
    --update-window)    UPDATE_WINDOW="${2:?}"; shift 2 ;;
    -h|--help)          sed -n '2,29p' "$0"; exit 0 ;;
    *) die "option inconnue : $1 (voir --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "à lancer en root : sudo $0 ..."
command -v systemctl >/dev/null || die "systemd est requis"
case "$BACKUP_KEEP" in ''|*[!0-9]*) die "--backup-keep doit être un entier" ;; esac
[ "$BACKUP_KEEP" -ge 1 ] || die "--backup-keep doit être >= 1"
case "$BACKUP_TIME" in [0-2][0-9]:[0-5][0-9]) ;; *) die "--backup-time attend HH:MM" ;; esac
case "$BACKUP_DIR" in /*) ;; *) die "--backup-dir doit être un chemin absolu" ;; esac
case "$BACKUP_DIR" in "$DATA_DIR"|"$DATA_DIR"/*) die "les sauvegardes ne doivent pas être dans le dossier de données" ;; esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Architecture et provenance des binaires -------------------------------------------------
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "architecture non prise en charge : $(uname -m) (amd64 ou arm64)" ;;
esac

if [ -z "$FROM" ]; then
  for c in "$HERE/../../dist"/*/ "$HERE/.." "$PWD"; do
    if compgen -G "$c/bambud-linux-$ARCH" >/dev/null 2>&1; then FROM="$c"; fi
  done
fi
[ -n "$FROM" ] || die "dossier de livraison introuvable : indiquer --from <dossier> (celui produit par scripts/release.sh)"
FROM="$(cd "$FROM" && pwd)"

find_bin() { # find_bin NOM -> chemin du binaire pour cette architecture
  for c in "$FROM/$1-linux-$ARCH" "$FROM/linux-$ARCH/$1" "$FROM/$1"; do
    [ -f "$c" ] && { echo "$c"; return 0; }
  done
  return 1
}
BAMBUD_SRC="$(find_bin bambud)" || die "bambud-linux-$ARCH introuvable dans $FROM"
MCP_SRC="$(find_bin bambu-mcp)" || die "bambu-mcp-linux-$ARCH introuvable dans $FROM"

# Empreintes : si SHA256SUMS est présent, les binaires doivent correspondre.
if [ -f "$FROM/SHA256SUMS" ]; then
  say "Vérification des empreintes (SHA256SUMS)"
  ( cd "$FROM" && grep -E "[ *](bambud|bambu-mcp)-linux-$ARCH\$|[ *]atelier-cad-worker-" SHA256SUMS | sha256sum -c - ) \
    || die "empreinte différente de SHA256SUMS : livraison corrompue ou modifiée"
else
  warn "pas de SHA256SUMS dans $FROM : empreintes non vérifiées"
fi

# --- Utilisateur et dossiers ------------------------------------------------------------------
say "Utilisateur et dossiers"
if ! id "$APP_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin "$APP_USER"
  echo "utilisateur $APP_USER créé"
fi
install -d -m 0755 -o root -g root "$OPT" "$BIN_DIR"
install -d -m 0750 -o "$APP_USER" -g "$APP_USER" "$DATA_DIR" "$DATA_DIR/home" "$LOG_DIR"
install -d -m 0750 -o root -g "$APP_USER" "$CONF_DIR"
install -d -m 0700 -o "$APP_USER" -g "$APP_USER" "$BACKUP_DIR"

# --- Binaires (remplacement atomique : le service en marche n'est jamais tronqué) -------------
say "Binaires dans $BIN_DIR"
install_bin() { # install_bin SRC DST
  local tmp
  tmp="$(mktemp "$2.XXXXXX")"
  install -m 0755 -o root -g root "$1" "$tmp"
  mv -f "$tmp" "$2"
}
SIM_SRC="$(find_bin printer-sim || true)"
atomic_link() { # atomic_link CIBLE LIEN : remplacement atomique d'un lien (rename)
  ln -s "$1" "$2.new.$$" && mv -T "$2.new.$$" "$2"
}
if [ -L "$OPT/current" ] || [ -L "$BIN_DIR/bambud" ]; then
  # Mises à jour automatiques activées (enable-auto-update.sh) : /opt/atelier/bin/* sont des liens vers current/bin.
  # Les remplacer par des fichiers (mv sur un lien) casserait le schéma releases/current : on installe une nouvelle
  # version à côté et on bascule `current`, sous le même verrou que la mise à jour automatique.
  echo "schéma releases/current détecté : installation comme nouvelle version"
  mkdir -p /run/lock
  exec 9>/run/lock/atelier-update.lock
  flock -w 900 9 || die "une mise à jour automatique est en cours (verrou /run/lock/atelier-update.lock) : réessayer dans quelques minutes"
  ver="$("$BAMBUD_SRC" --version 2>/dev/null | awk '{print $2; exit}')"; ver="${ver//[^A-Za-z0-9._-]/_}"; ver="${ver:-inconnue}"
  rname="manual-$ver-$(date -u +%Y%m%dT%H%M%SZ)"
  rdir="$OPT/releases/$rname"
  install -d -m 0755 -o root -g root "$OPT/releases" "$rdir" "$rdir/bin"
  install -m 0755 -o root -g root "$BAMBUD_SRC" "$rdir/bin/bambud"
  install -m 0755 -o root -g root "$MCP_SRC" "$rdir/bin/bambu-mcp"
  [ -n "$SIM_SRC" ] && install -m 0755 -o root -g root "$SIM_SRC" "$rdir/bin/printer-sim"
  AGENT_SRC="$(find_bin bambu-agent || true)"
  [ -n "$AGENT_SRC" ] && install -m 0755 -o root -g root "$AGENT_SRC" "$rdir/bin/bambu-agent"
  schema=0
  if grep -aq 'schema-version' "$BAMBUD_SRC"; then # jamais d'exécution d'un binaire ancien avec un argument inconnu : il démarrerait le service
    schema="$("$BAMBUD_SRC" schema-version 2>/dev/null | head -n1)"; case "$schema" in ''|*[!0-9]*) schema=0 ;; esac
  fi
  printf '{\n  "sha": "",\n  "version": "%s",\n  "schema": %s,\n  "built_at": "%s",\n  "legacy": true\n}\n' "$ver" "$schema" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$rdir/REVISION"
  oldcur="$(readlink "$OPT/current" || true)"
  atomic_link "releases/$rname" "$OPT/current"
  [ -z "$oldcur" ] || atomic_link "$oldcur" "$OPT/previous"
  for n in bambud bambu-mcp printer-sim; do
    [ -f "$rdir/bin/$n" ] && { [ -L "$BIN_DIR/$n" ] || [ ! -e "$BIN_DIR/$n" ]; } && atomic_link "../current/bin/$n" "$BIN_DIR/$n"
  done
  # L'outil de mise à jour suit le binaire livré s'il le connaît.
  if [ -x "$OPT/updater/bambud" ] && grep -aq 'upgrade-probe' "$BAMBUD_SRC"; then install_bin "$BAMBUD_SRC" "$OPT/updater/bambud"; fi
else
  install_bin "$BAMBUD_SRC" "$BIN_DIR/bambud"
  install_bin "$MCP_SRC" "$BIN_DIR/bambu-mcp"
  [ -n "$SIM_SRC" ] && install_bin "$SIM_SRC" "$BIN_DIR/printer-sim"
fi
"$BIN_DIR/bambud" --version

# --- Worker CAO ---------------------------------------------------------------------------------
UV_BIN=""
find_uv() {
  local home c
  home="$(getent passwd "$APP_USER" | cut -d: -f6)"
  for c in "$BIN_DIR/uv" "$(command -v uv 2>/dev/null || true)" "$home/.local/bin/uv" "$DATA_DIR/home/.local/bin/uv" \
           /usr/local/bin/uv /root/.local/bin/uv /home/*/.local/bin/uv; do
    [ -n "$c" ] && [ -x "$c" ] && { echo "$c"; return 0; }
  done
  return 1
}
if [ "$WITH_CAD" = 1 ]; then
  say "Worker CAO dans $CAD_DIR"
  CAD_ARCHIVE="$(ls -1 "$FROM"/atelier-cad-worker-*.tar.gz 2>/dev/null | tail -n1 || true)"
  if [ -z "$CAD_ARCHIVE" ]; then
    warn "archive du worker CAO introuvable dans $FROM : import STEP et modélisation paramétrique indisponibles (--no-cad pour ne plus avertir)"
  elif UV_FOUND="$(find_uv)"; then
    install -d -m 0755 -o "$APP_USER" -g "$APP_USER" "$CAD_DIR" "$OPT/uv-python"
    # Mise à jour : le code est remplacé, l'environnement .venv est conservé.
    stage="$(mktemp -d "$CAD_DIR/.stage.XXXXXX")"
    tar -xzf "$CAD_ARCHIVE" -C "$stage"
    rm -rf "$CAD_DIR/geometry.new"
    mv "$stage/geometry" "$CAD_DIR/geometry.new"
    rmdir "$stage"
    if [ -d "$CAD_DIR/geometry/.venv" ]; then mv "$CAD_DIR/geometry/.venv" "$CAD_DIR/geometry.new/.venv"; fi
    rm -rf "$CAD_DIR/geometry.old"
    [ -d "$CAD_DIR/geometry" ] && mv "$CAD_DIR/geometry" "$CAD_DIR/geometry.old"
    mv "$CAD_DIR/geometry.new" "$CAD_DIR/geometry"
    rm -rf "$CAD_DIR/geometry.old"
    install -m 0755 -o root -g root "$UV_FOUND" "$BIN_DIR/uv.new" && mv -f "$BIN_DIR/uv.new" "$BIN_DIR/uv"
    UV_BIN="$BIN_DIR/uv"
    chown -R "$APP_USER:$APP_USER" "$CAD_DIR" "$OPT/uv-python"
    echo "uv synchronise le worker (uv sync --extra cad) : peut durer plusieurs minutes au premier passage"
    runuser -u "$APP_USER" -- env HOME="$DATA_DIR/home" UV_CACHE_DIR="$DATA_DIR/home/.cache/uv" UV_PYTHON_INSTALL_DIR="$OPT/uv-python" \
      bash -c "cd '$CAD_DIR/geometry' && '$UV_BIN' sync --extra cad" \
      || warn "uv sync --extra cad a échoué : le worker CAO est installé mais sans CadQuery (voir le message ci-dessus ; relancer le script après correction)"
  else
    warn "uv est introuvable : installer uv (étape 4 du prompt de préparation) puis relancer ce script pour activer le worker CAO"
  fi
fi

# --- Bambu Studio -------------------------------------------------------------------------------
say "Bambu Studio"
STUDIO_BIN=""
if [ -z "$STUDIO" ]; then
  STUDIO="$(ls -1t "$STUDIO_DIR"/*.AppImage 2>/dev/null | head -n1 || true)"
fi
if [ -n "$STUDIO" ] && [ -f "$STUDIO" ]; then
  chmod 0755 "$STUDIO"
  case "$STUDIO" in
    *.AppImage)
      # Extraite une fois (et ré-extraite si l'AppImage est plus récente) : évite d'extraire ~300 Mo à chaque
      # découpe et expose resources/profiles/BBL à côté de AppRun, où bambud les cherche.
      if [ ! -x "$STUDIO_DIR/squashfs-root/AppRun" ] || [ "$STUDIO" -nt "$STUDIO_DIR/squashfs-root/AppRun" ]; then
        rm -rf "$STUDIO_DIR/squashfs-root"
        ( cd "$STUDIO_DIR" && runuser -u "$APP_USER" -- env HOME="$DATA_DIR/home" "$STUDIO" --appimage-extract >/dev/null 2>&1 ) || true
      fi
      if [ -x "$STUDIO_DIR/squashfs-root/AppRun" ]; then
        STUDIO_BIN="$STUDIO_DIR/squashfs-root/AppRun"
        echo "AppImage extraite dans $STUDIO_DIR/squashfs-root"
      else
        STUDIO_BIN="$STUDIO"
        warn "extraction de l'AppImage impossible : utilisation directe (--appimage-extract-and-run à chaque découpe, plus lent)"
      fi ;;
    *) STUDIO_BIN="$STUDIO" ;;
  esac
elif [ -x "$STUDIO_DIR/squashfs-root/AppRun" ]; then
  STUDIO_BIN="$STUDIO_DIR/squashfs-root/AppRun"
else
  warn "Bambu Studio introuvable (cherché dans $STUDIO_DIR) : la découpe sera indisponible tant que --studio n'est pas fourni"
fi
[ -n "$STUDIO_BIN" ] && echo "BAMBU_STUDIO_BIN = $STUDIO_BIN"
STUDIO_WRAPPER=""
if command -v xvfb-run >/dev/null 2>&1; then STUDIO_WRAPPER="xvfb-run -a"; echo "xvfb-run détecté : $(command -v xvfb-run)"; else
  warn "xvfb-run absent (paquet xvfb) : Bambu Studio ne pourra pas découper sans écran"
fi

# --- /etc/atelier/atelier.env (fusion : les réglages existants sont conservés) ------------------
say "Configuration $ENV_FILE"
touch "$ENV_FILE"; chmod 0600 "$ENV_FILE"; chown root:root "$ENV_FILE"
env_get() { grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//' || true; }
env_set() { # env_set KEY VALUE : remplace ou ajoute (valeur toujours entre guillemets)
  local tmp
  tmp="$(mktemp "$ENV_FILE.XXXXXX")"
  grep -vE "^$1=" "$ENV_FILE" > "$tmp" || true
  printf '%s="%s"\n' "$1" "$2" >> "$tmp"
  chmod 0600 "$tmp"; chown root:root "$tmp"
  mv -f "$tmp" "$ENV_FILE"
}
if ! grep -q '^# Atelier Bambu' "$ENV_FILE"; then
  { printf '# Atelier Bambu - lu par atelier.service (systemd EnvironmentFile). Ne contient que des réglages de ce serveur.\n# Droits 600 : ne pas copier ce fichier dans un dépôt ni dans une conversation.\n'; cat "$ENV_FILE"; } > "$ENV_FILE.tmp"
  chmod 0600 "$ENV_FILE.tmp"; mv -f "$ENV_FILE.tmp" "$ENV_FILE"
fi

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
HOST="$(hostname)"
[ -n "$PUBLIC_URL" ] || PUBLIC_URL="$(env_get BAMBU_PUBLIC_URL)"
[ -n "$PUBLIC_URL" ] || { [ -n "$IP" ] && PUBLIC_URL="http://$IP:7878"; }
if [ -z "$ALLOWED_HOSTS" ]; then
  ALLOWED_HOSTS="$HOST,$HOST.local"
  for ip in $(hostname -I 2>/dev/null); do ALLOWED_HOSTS="$ALLOWED_HOSTS,$ip"; done
  prev="$(env_get BAMBU_ALLOWED_HOSTS)"; [ -n "$prev" ] && ALLOWED_HOSTS="$prev,$ALLOWED_HOSTS"
  ALLOWED_HOSTS="$(printf '%s' "$ALLOWED_HOSTS" | tr ',' '\n' | awk 'NF && !seen[$0]++' | paste -sd, -)"
fi

env_set BAMBU_DATA_DIR "$DATA_DIR"
env_set BAMBU_ADDR "$LISTEN"
[ -n "$PUBLIC_URL" ] && env_set BAMBU_PUBLIC_URL "$PUBLIC_URL"
env_set BAMBU_ALLOWED_HOSTS "$ALLOWED_HOSTS"
[ -n "$(env_get BAMBU_LOG_LEVEL)" ] || env_set BAMBU_LOG_LEVEL info
env_set HOME "$DATA_DIR/home"
if [ -n "$STUDIO_BIN" ]; then env_set BAMBU_STUDIO_BIN "$STUDIO_BIN"; fi
if [ -n "$STUDIO_WRAPPER" ]; then env_set BAMBU_STUDIO_WRAPPER "$STUDIO_WRAPPER"; fi
if [ -n "$UV_BIN" ]; then
  env_set BAMBU_UV_PATH "$UV_BIN"
  env_set BAMBU_GEOMETRY_DIR "$CAD_DIR/geometry"
  env_set UV_CACHE_DIR "$DATA_DIR/home/.cache/uv"
  env_set UV_PYTHON_INSTALL_DIR "$OPT/uv-python"
fi
if [ "${#IMPORT_ROOTS[@]}" -gt 0 ]; then
  roots="$(printf '%s:' "${IMPORT_ROOTS[@]}")"; env_set BAMBU_IMPORT_ROOTS "${roots%:}"
fi
# Jeton d'API : généré une seule fois, jamais affiché.
if [ -z "$(env_get BAMBU_API_TOKEN)" ]; then
  env_set BAMBU_API_TOKEN "$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  echo "jeton d'API généré (non affiché) : sudo grep BAMBU_API_TOKEN $ENV_FILE"
else
  echo "jeton d'API existant conservé"
fi

# --- Unité systemd -------------------------------------------------------------------------------
say "Service systemd"
write_if_changed() { # write_if_changed DEST MODE  (contenu sur stdin)
  local tmp
  tmp="$(mktemp)"
  cat > "$tmp"
  if [ -f "$1" ] && cmp -s "$tmp" "$1"; then rm -f "$tmp"; return 1; fi
  if [ -f "$1" ] && ! grep -q "Géré par deploy/linux/install.sh" "$1"; then
    cp -p "$1" "$1.bak-$(date +%Y%m%d%H%M%S)"; echo "ancienne version sauvegardée : $1.bak-*"
  fi
  install -m "$2" -o root -g root "$tmp" "$1"; rm -f "$tmp"; return 0
}
write_if_changed "$UNIT_DIR/atelier.service" 0644 <<EOF || true
$MARK
[Unit]
Description=Atelier Bambu (bambud : catalogue, découpe, impression)
Documentation=file://$OPT/README.md
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$APP_USER
Group=$APP_USER
EnvironmentFile=$ENV_FILE
WorkingDirectory=$DATA_DIR
ExecStart=$BIN_DIR/bambud
Restart=on-failure
RestartSec=5
# bambud vide ses travaux en cours à l'arrêt (30 s par défaut) avant de fermer la base.
TimeoutStopSec=60
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
ReadWritePaths=$DATA_DIR $LOG_DIR -$CAD_DIR -$OPT/uv-python

[Install]
WantedBy=multi-user.target
EOF

# Outil d'administration : exécute bambud en tant qu'utilisateur du service avec sa configuration, sans jamais
# afficher le jeton d'API (sudo atelier-admin enroll | agent pair | backup | verify | restore ...).
cat > "$BIN_DIR/atelier-admin.new" <<'EOS'
#!/usr/bin/env bash
set -euo pipefail
[ "$(id -u)" -eq 0 ] || { echo "à lancer avec sudo : sudo atelier-admin <commande de bambud>" >&2; exit 1; }
set -a; . /etc/atelier/atelier.env; set +a
exec runuser -u atelier -- env HOME="${HOME:-/var/lib/atelier/home}" BAMBU_DATA_DIR="${BAMBU_DATA_DIR:-/var/lib/atelier}" \
  BAMBU_ADDR="${BAMBU_ADDR:-127.0.0.1:7878}" BAMBU_PUBLIC_URL="${BAMBU_PUBLIC_URL:-}" /opt/atelier/bin/bambud "$@"
EOS
chmod 0755 "$BIN_DIR/atelier-admin.new"; mv -f "$BIN_DIR/atelier-admin.new" "$BIN_DIR/atelier-admin"
ln -sfn "$BIN_DIR/atelier-admin" /usr/local/bin/atelier-admin

# --- Sauvegarde quotidienne ----------------------------------------------------------------------
if [ "$WITH_TIMER" = 1 ]; then
  say "Sauvegarde quotidienne ($BACKUP_TIME, $BACKUP_KEEP conservées) dans $BACKUP_DIR"
  umask 077
  { printf '%s\nBACKUP_DIR="%s"\nBACKUP_KEEP=%s\nBAMBU_DATA_DIR="%s"\n' "# Réglages de la sauvegarde quotidienne (modifiable ; relancer install.sh pour changer l'heure)" "$BACKUP_DIR" "$BACKUP_KEEP" "$DATA_DIR"; } > "$BACKUP_ENV.tmp"
  chown root:"$APP_USER" "$BACKUP_ENV.tmp"; chmod 0640 "$BACKUP_ENV.tmp"; mv -f "$BACKUP_ENV.tmp" "$BACKUP_ENV"
  umask 022
  cat > "$BIN_DIR/atelier-backup.sh.new" <<'EOS'
#!/usr/bin/env bash
# Sauvegarde cohérente à chaud (service non arrêté), vérification, puis rotation des anciennes archives.
set -euo pipefail
set -a; . /etc/atelier/backup.env; set +a
: "${BACKUP_DIR:?}" "${BACKUP_KEEP:?}" "${BAMBU_DATA_DIR:?}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
out="$BACKUP_DIR/atelier-$stamp.tar.gz"
/opt/atelier/bin/bambud backup --data-dir "$BAMBU_DATA_DIR" --out "$out"
/opt/atelier/bin/bambud verify --archive "$out"
# Rotation : on ne supprime QUE les archives atelier-*.tar.gz (et leur .sha256) au-delà des BACKUP_KEEP plus récentes.
mapfile -t old < <(ls -1t "$BACKUP_DIR"/atelier-*.tar.gz 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))")
for f in "${old[@]}"; do rm -f -- "$f" "$f.sha256"; echo "ancienne sauvegarde supprimée : $(basename "$f")"; done
echo "sauvegarde terminée : $out"
EOS
  chmod 0755 "$BIN_DIR/atelier-backup.sh.new"; chown root:root "$BIN_DIR/atelier-backup.sh.new"; mv -f "$BIN_DIR/atelier-backup.sh.new" "$BIN_DIR/atelier-backup.sh"

  write_if_changed "$UNIT_DIR/atelier-backup.service" 0644 <<EOF || true
$MARK
[Unit]
Description=Sauvegarde de l'Atelier Bambu
After=atelier.service

[Service]
Type=oneshot
User=$APP_USER
Group=$APP_USER
ExecStart=$BIN_DIR/atelier-backup.sh
Nice=10
IOSchedulingClass=idle
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# La base est ouverte en lecture seule, mais SQLite (mode WAL) a besoin d'écrire le fichier -shm voisin.
ReadWritePaths=$BACKUP_DIR $DATA_DIR
EOF
  write_if_changed "$UNIT_DIR/atelier-backup.timer" 0644 <<EOF || true
$MARK
[Unit]
Description=Sauvegarde quotidienne de l'Atelier Bambu

[Timer]
OnCalendar=*-*-* $BACKUP_TIME:00
RandomizedDelaySec=5min
Persistent=true

[Install]
WantedBy=timers.target
EOF
fi

systemctl daemon-reload
if [ "$START" = 1 ]; then
  say "Démarrage"
  systemctl enable atelier.service >/dev/null
  if systemctl is-active --quiet atelier.service; then
    if [ "$RESTART" = 1 ]; then
      echo "redémarrage du service (les travaux en cours sont terminés proprement ; une impression en cours sur l'imprimante n'est pas interrompue)"
      systemctl restart atelier.service
    else
      echo "service laissé en marche avec l'ancienne version : sudo systemctl restart atelier quand aucune impression n'est en cours"
    fi
  else
    systemctl start atelier.service
  fi
  [ "$WITH_TIMER" = 1 ] && systemctl enable --now atelier-backup.timer >/dev/null
  ok=0
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${LISTEN##*:}/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  if [ "$ok" = 1 ]; then echo "service en ligne : /health répond"; else
    warn "le service ne répond pas encore sur /health : journalctl -u atelier -n 50 --no-pager"
  fi
else
  echo "démarrage ignoré (--no-start) : sudo systemctl enable --now atelier.service"
fi

if [ "$AUTO_UPDATE" = 1 ]; then
  say "Mises à jour automatiques"
  # Migration sans interruption : le service qui vient de démarrer n'est pas touché (voir enable-auto-update.sh).
  bash "$HERE/enable-auto-update.sh" --from "$FROM" ${UPDATE_WINDOW:+--window "$UPDATE_WINDOW"} \
    || warn "l'activation des mises à jour automatiques a échoué (le service, lui, est installé) : relancer sudo bash $HERE/enable-auto-update.sh"
else
  echo "Mises à jour automatiques : non activées (sudo bash $HERE/enable-auto-update.sh pour les activer)"
fi

say "Terminé"
cat <<EOF
  Service        : systemctl status atelier        (journal : journalctl -u atelier -f)
  Adresse        : ${PUBLIC_URL:-http://<IP>:7878}
  Jeton d'API    : sudo grep BAMBU_API_TOKEN $ENV_FILE      (à copier sur le PC Windows, jamais dans une conversation)
  Sauvegardes    : $BACKUP_DIR  (systemctl list-timers atelier-backup.timer)
  Ensuite        : sudo atelier-admin enroll        -> code pour enregistrer le navigateur (docs/INSTALL.md)
  Autres         : sudo atelier-admin agent pair | backup --out DIR | verify | restore ...
EOF
