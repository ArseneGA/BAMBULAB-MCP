#!/usr/bin/env bash
# Active les mises à jour AUTOMATIQUES de l'Atelier Bambu sur un serveur déjà installé par install.sh.
# Le serveur TIRE les mises à jour (git fetch d'origin/main, rien d'entrant) : un timer systemd lance toutes les 5 minutes
# `bambud upgrade --auto`, qui n'installe qu'un commit dont la CI GitHub est verte, sans interrompre une impression en cours (ni pendant un dépôt ou une confirmation),
# après sauvegarde vérifiée, avec bascule atomique et retour arrière automatique (docs/DEPLOY-LINUX.md, « Mises à jour automatiques »).
#
# Usage (en root) :   sudo bash deploy/linux/enable-auto-update.sh [options]
#
# Idempotent, SANS interruption du service ni perte de données : l'installation actuelle est migrée vers le schéma
# /opt/atelier/releases/<version> + lien /opt/atelier/current (les binaires de /opt/atelier/bin deviennent des liens vers
# current/bin). Le service en marche n'est NI arrêté NI redémarré ; il ne le sera qu'à la première mise à jour installée.
#
# Options :
#   --from DIR            dossier de livraison (bambud-linux-<arch>) d'où prendre l'outil de mise à jour ;
#                         défaut : compilé depuis ce dépôt (go requis)
#   --updater-bin FILE    binaire bambud contenant `bambud upgrade` (prioritaire sur --from)
#   --assume-sha SHA      commit (40 caractères) de la version actuellement installée, si le script ne peut pas le deviner
#   --window HH:MM-HH:MM  n'installer que dans cette plage horaire locale (ex. 07:00-23:00 : pas la nuit) ; défaut : tout le temps
#   --smoke MODE          required (défaut : un échec du test de fumée déclenche le retour arrière) | advisory | off
#   --keep-releases N     versions conservées sur disque (défaut 3)
#   --repo URL            dépôt à suivre (défaut https://github.com/ArseneGA/BAMBULAB-MCP)
#   --branch NOM          branche suivie (défaut main)
#   --no-start            installe tout mais n'active pas le timer
#   --disable             désactive l'automatique (fichier /etc/atelier/auto-update.disabled, timer arrêté) puis sort
#   --enable              réactive après --disable (retire le fichier, relance le timer) puis sort
#   -h, --help            cette aide
set -euo pipefail

APP_USER=${ATELIER_APP_USER:-atelier}
BUILD_USER=${ATELIER_BUILD_USER:-atelier-update}
OPT=${ATELIER_OPT:-/opt/atelier}
CONF_DIR=${ATELIER_CONF_DIR:-/etc/atelier}
UNIT_DIR=${ATELIER_UNIT_DIR:-/etc/systemd/system}
SBIN_DIR=${ATELIER_SBIN_DIR:-/usr/local/sbin}
CACHE_DIR=${ATELIER_UPDATE_CACHE_DIR:-/var/lib/atelier-update}
SYSTEMCTL=${ATELIER_SYSTEMCTL:-systemctl}
TEST_MODE=${ATELIER_ENABLE_TEST:-0}      # harnais scripts/test-enable-auto-update.sh : ni root, ni utilisateurs, ni systemd réels
ENV_FILE=$CONF_DIR/atelier.env
BACKUP_ENV=$CONF_DIR/backup.env
UPDATE_ENV=$CONF_DIR/auto-update.env
DISABLED_FILE=$CONF_DIR/auto-update.disabled
MARK="# Géré par deploy/linux/enable-auto-update.sh - relancer le script pour le régénérer"

FROM=""; UPDATER_SRC=""; ASSUME_SHA=""; WINDOW=""; SMOKE=""; KEEP=""; REPO=""; BRANCH=""
START=1; ACTION=enable

say()  { printf '\n==> %s\n' "$*"; }
warn() { printf 'ATTENTION : %s\n' "$*" >&2; }
die()  { printf 'ERREUR : %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --from)          FROM="${2:?--from attend un dossier}"; shift 2 ;;
    --updater-bin)   UPDATER_SRC="${2:?--updater-bin attend un fichier}"; shift 2 ;;
    --assume-sha)    ASSUME_SHA="${2:?}"; shift 2 ;;
    --window)        WINDOW="${2:?}"; shift 2 ;;
    --smoke)         SMOKE="${2:?}"; shift 2 ;;
    --keep-releases) KEEP="${2:?}"; shift 2 ;;
    --repo)          REPO="${2:?}"; shift 2 ;;
    --branch)        BRANCH="${2:?}"; shift 2 ;;
    --no-start)      START=0; shift ;;
    --disable)       ACTION=disable; shift ;;
    --enable)        ACTION=reenable; shift ;;
    -h|--help)       sed -n '2,30p' "$0"; exit 0 ;;
    *) die "option inconnue : $1 (voir --help)" ;;
  esac
done

[ "$TEST_MODE" = 1 ] || [ "$(id -u)" -eq 0 ] || die "à lancer en root : sudo bash $0"
case "$SMOKE" in ''|required|advisory|off) ;; *) die "--smoke : required, advisory ou off" ;; esac
case "$KEEP" in ''|*[!0-9]*) [ -z "$KEEP" ] || die "--keep-releases doit être un entier >= 2" ;; *) [ "$KEEP" -ge 2 ] || die "--keep-releases doit être >= 2" ;; esac
case "$WINDOW" in ''|[0-2][0-9]:[0-5][0-9]-[0-2][0-9]:[0-5][0-9]) ;; *) die "--window attend HH:MM-HH:MM" ;; esac
case "$ASSUME_SHA" in '') ;; *[!0-9a-f]*) die "--assume-sha : 40 caractères hexadécimaux" ;; *) [ "${#ASSUME_SHA}" -eq 40 ] || die "--assume-sha : 40 caractères hexadécimaux" ;; esac
case "$REPO" in ''|https://*|file://*) ;; *) die "--repo : une URL https://" ;; esac

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"

# Toute commande lancée sous un autre utilisateur part de « / » : le dossier courant de root (souvent /home/<admin>, illisible
# pour les comptes de service) ferait échouer go (« cannot determine current directory »).
as_app() { if [ "$TEST_MODE" = 1 ]; then "$@"; else ( cd / && runuser -u "$APP_USER" -- "$@" ); fi; }
do_chown() { [ "$TEST_MODE" = 1 ] || chown "$@"; }
write_if_changed() { # write_if_changed DEST MODE  (contenu sur stdin) ; code 0 = écrit, 1 = inchangé
  local tmp
  tmp="$(mktemp)"
  cat > "$tmp"
  if [ -f "$1" ] && cmp -s "$tmp" "$1"; then rm -f "$tmp"; return 1; fi
  install -m "$2" "$tmp" "$1"; rm -f "$tmp"; return 0
}
atomic_link() { # atomic_link CIBLE LIEN : remplacement atomique d'un lien (rename), jamais d'état intermédiaire
  ln -s "$1" "$2.new.$$" && mv -T "$2.new.$$" "$2"
}
env_get() { [ -f "$2" ] || return 0; grep -E "^$1=" "$2" | tail -n1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//' || true; }
env_set() { # env_set FICHIER CLE VALEUR : remplace sur place ou ajoute (valeur entre guillemets) ; rien si déjà identique
  local tmp
  if grep -q "^$2=" "$1"; then
    [ "$(env_get "$2" "$1")" = "$3" ] && return 0
    tmp="$(mktemp "$1.XXXXXX")"
    awk -v k="$2" -v v="$3" 'index($0, k "=") == 1 { print k "=\"" v "\""; next } { print }' "$1" > "$tmp"
  else
    tmp="$(mktemp "$1.XXXXXX")"
    cat "$1" > "$tmp"; printf '%s="%s"\n' "$2" "$3" >> "$tmp"
  fi
  chmod 0644 "$tmp"; mv -f "$tmp" "$1"
}

# --- --disable / --enable ---------------------------------------------------------------------------------------------
if [ "$ACTION" = disable ]; then
  install -d -m 0750 "$CONF_DIR"; : > "$DISABLED_FILE"; chmod 0644 "$DISABLED_FILE"
  "$SYSTEMCTL" disable --now atelier-update.timer >/dev/null 2>&1 || true
  echo "Mises à jour automatiques DÉSACTIVÉES ($DISABLED_FILE créé, timer arrêté). Réactiver : sudo bash $0 --enable"
  exit 0
fi
if [ "$ACTION" = reenable ]; then
  rm -f "$DISABLED_FILE"
  "$SYSTEMCTL" enable --now atelier-update.timer >/dev/null
  echo "Mises à jour automatiques RÉACTIVÉES."
  exit 0
fi

# --- Contrôles préalables : rien n'est modifié tant qu'ils ne passent pas ------------------------------------------------
say "Contrôles préalables"
[ -x "$OPT/bin/bambud" ] || [ -L "$OPT/current" ] || die "installation introuvable ($OPT/bin/bambud) : lancer d'abord deploy/linux/install.sh"
[ -f "$ENV_FILE" ] || die "$ENV_FILE introuvable : installation incomplète (relancer install.sh)"
missing=""
for c in git curl tar flock sha256sum; do command -v "$c" >/dev/null 2>&1 || missing="$missing $c"; done
if [ "$TEST_MODE" != 1 ]; then
  command -v "$SYSTEMCTL" >/dev/null 2>&1 || die "systemd est requis"
  for c in runuser go node npm; do command -v "$c" >/dev/null 2>&1 || missing="$missing $c"; done
fi
[ -z "$missing" ] || die "commandes manquantes :$missing (go : https://go.dev/dl, node >= 22.12 : https://nodejs.org ; installés pour TOUS les utilisateurs, ex. /usr/local/go, /usr/local/bin)"
if [ "$TEST_MODE" != 1 ]; then
  nv="$(node -p 'process.versions.node' 2>/dev/null || echo 0.0.0)"
  IFS=. read -r nmaj nmin _ <<<"$nv"
  if ! { [ "$nmaj" -gt 22 ] || { [ "$nmaj" -eq 22 ] && [ "$nmin" -ge 12 ]; } || { [ "$nmaj" -eq 20 ] && [ "$nmin" -ge 19 ]; }; }; then
    die "node $nv trop ancien : la construction de l'interface web exige node >= 22.12 (ou >= 20.19)"
  fi
  if [ -f "$REPO_ROOT/go.mod" ]; then
    need="$(awk '/^go /{print $2; exit}' "$REPO_ROOT/go.mod")"
    have="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
    if [ -n "$need" ] && [ -n "$have" ] && [ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | head -n1)" != "$need" ]; then
      warn "go $have installé, go.mod exige $need : la construction téléchargera la chaîne d'outils (GOTOOLCHAIN=auto) ou échouera hors ligne"
    fi
  fi
fi
avail_kb="$(df -Pk "$OPT" 2>/dev/null | awk 'NR==2{print $4}')"
if [ -n "$avail_kb" ] && [ "$avail_kb" -lt 3145728 ]; then
  warn "moins de 3 Gio libres sur le disque de $OPT : une construction (node_modules, cache Go) en demande davantage"
fi

# --- Outil de mise à jour ---------------------------------------------------------------------------------------------------
case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) die "architecture non prise en charge : $(uname -m)" ;; esac
TMPD="$(mktemp -d)"; trap 'rm -rf "$TMPD"' EXIT
if [ -z "$UPDATER_SRC" ] && [ -n "$FROM" ]; then
  for c in "$FROM/bambud-linux-$ARCH" "$FROM/linux-$ARCH/bambud" "$FROM/bambud"; do [ -f "$c" ] && { UPDATER_SRC="$c"; break; }; done
  [ -n "$UPDATER_SRC" ] || die "bambud-linux-$ARCH introuvable dans $FROM"
fi
if [ -z "$UPDATER_SRC" ]; then
  [ -f "$REPO_ROOT/go.mod" ] || die "pas de dépôt autour de ce script : fournir --from DIR ou --updater-bin FILE"
  command -v go >/dev/null 2>&1 || die "go est requis pour compiler l'outil de mise à jour (ou fournir --from / --updater-bin)"
  say "Compilation de l'outil de mise à jour depuis $REPO_ROOT"
  VER="$(git -C "$REPO_ROOT" describe --always --tags 2>/dev/null || echo dev)"
  ( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=$VER" -o "$TMPD/bambud" ./cmd/bambud ) \
    || die "compilation de bambud échouée"
  UPDATER_SRC="$TMPD/bambud"
fi
[ -x "$UPDATER_SRC" ] || chmod 0755 "$UPDATER_SRC" 2>/dev/null || true
# Contrôle SANS exécuter le binaire : un bambud ancien lancé avec un argument inconnu démarrerait le service (il ignore les arguments).
if ! grep -aq "upgrade-probe" "$UPDATER_SRC" || ! grep -aq "schema-version" "$UPDATER_SRC"; then
  die "$UPDATER_SRC ne contient pas « bambud upgrade » : binaire trop ancien (prendre celui de ce dépôt ou d'une livraison récente)"
fi

# --- Utilisateur de construction, dossiers --------------------------------------------------------------------------------
say "Utilisateur de construction et dossiers"
if [ "$TEST_MODE" != 1 ]; then
  if ! id "$BUILD_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$CACHE_DIR" --no-create-home --shell /usr/sbin/nologin "$BUILD_USER"
    echo "utilisateur $BUILD_USER créé (il exécute git, npm, go et les tests : aucun accès aux données ni aux secrets)"
  fi
fi
install -d -m 0755 "$OPT/releases" "$OPT/updater"
install -d -m 0750 "$CACHE_DIR"; do_chown "$BUILD_USER:$BUILD_USER" "$CACHE_DIR"
install -d -m 0750 "$CONF_DIR" 2>/dev/null || true
if [ "$TEST_MODE" != 1 ]; then
  # Le compte de construction doit pouvoir lancer go et node (installés pour tous les utilisateurs, pas dans un $HOME).
  BPATH=""
  for t in go node npm; do
    d="$(dirname "$(command -v "$t")")"
    case ":$BPATH:" in *":$d:"*) ;; *) BPATH="${BPATH:+$BPATH:}$d" ;; esac
  done
  for t in go node npm git; do
    # go n'a pas d'option de version : sous-commande « go version ».
    case "$t" in go) vflag=version ;; *) vflag=--version ;; esac
    ( cd / && runuser -u "$BUILD_USER" -- env -i HOME="$CACHE_DIR" PATH="$BPATH:/usr/local/bin:/usr/bin:/bin" "$t" "$vflag" >/dev/null 2>&1 ) \
      || die "l'utilisateur $BUILD_USER ne peut pas lancer « $t » (PATH $BPATH) : l'installer pour tous les utilisateurs (pas dans un dossier personnel)"
  done
else
  BPATH="${ATELIER_TEST_PATH:-/usr/bin}"
fi

# L'outil de mise à jour est installé AVANT la migration : la sonde de schéma (sous l'utilisateur du service) doit pouvoir
# l'exécuter, ce qu'un fichier dans un dossier temporaire de root ne permet pas.
UPDATER="$OPT/updater/bambud"
install -m 0755 "$UPDATER_SRC" "$UPDATER.new" && mv -f "$UPDATER.new" "$UPDATER"

# --- Migration vers releases/current, SANS interruption ----------------------------------------------------------------------
say "Schéma releases/current"
DATA_DIR="$(env_get BAMBU_DATA_DIR "$ENV_FILE")"; DATA_DIR="${DATA_DIR:-/var/lib/atelier}"
if [ -L "$OPT/current" ]; then
  echo "déjà migré : $OPT/current -> $(readlink "$OPT/current")"
else
  [ -f "$OPT/bin/bambud" ] && [ ! -L "$OPT/bin/bambud" ] || die "$OPT/bin/bambud n'est pas un fichier : état inattendu, rien n'a été modifié"
  STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
  LEGACY="$OPT/releases/legacy-$STAMP"
  mkdir -p "$LEGACY/bin"
  for n in bambud bambu-mcp printer-sim; do
    [ -f "$OPT/bin/$n" ] && [ ! -L "$OPT/bin/$n" ] && install -m 0755 "$OPT/bin/$n" "$LEGACY/bin/$n"
  done
  LVER="$("$LEGACY/bin/bambud" --version 2>/dev/null | awk '{print $2; exit}')"; LVER="${LVER:-inconnue}"
  LSHA="$ASSUME_SHA"
  if [ -z "$LSHA" ] && [ -d "$REPO_ROOT/.git" ] && [ "$(git -C "$REPO_ROOT" describe --always --tags 2>/dev/null)" = "$LVER" ]; then
    LSHA="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  fi
  LSCHEMA="$(as_app "$UPDATER" upgrade-probe --data-dir "$DATA_DIR" 2>/dev/null | grep -o '"schema_version":[0-9]*' | head -n1 | cut -d: -f2 || true)"
  printf '{\n  "sha": "%s",\n  "version": "%s",\n  "schema": %s,\n  "built_at": "%s",\n  "legacy": true\n}\n' "$LSHA" "$LVER" "${LSCHEMA:-0}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$LEGACY/REVISION"
  chmod -R go-w "$LEGACY"
  echo "installation actuelle copiée dans $LEGACY (version $LVER, commit ${LSHA:-inconnu}, schéma ${LSCHEMA:-?})"
  atomic_link "releases/legacy-$STAMP" "$OPT/current"
fi
# Les binaires de /opt/atelier/bin deviennent des liens vers current/bin : l'unité atelier.service, atelier-admin et la
# sauvegarde quotidienne (qui appellent /opt/atelier/bin/bambud) suivent ainsi `current`, sans changement d'unité.
# Le processus en marche garde son ancien fichier ouvert (inode conservé) : aucune interruption.
for n in bambud bambu-mcp printer-sim; do
  if [ -f "$OPT/bin/$n" ] && [ ! -L "$OPT/bin/$n" ] && [ -f "$OPT/current/bin/$n" ]; then
    atomic_link "../current/bin/$n" "$OPT/bin/$n"
    echo "$OPT/bin/$n -> ../current/bin/$n"
  fi
done

# --- Outil de mise à jour, commande atelier-update ---------------------------------------------------------------------------
say "Outil de mise à jour"
echo "outil installé : $UPDATER"
install -d -m 0755 "$SBIN_DIR"
write_if_changed "$SBIN_DIR/atelier-update" 0755 <<'EOS' || true
#!/usr/bin/env bash
# Mises à jour de l'Atelier Bambu : sudo atelier-update [--status | --check | --now | --retry | --pin SHA|current | --unpin]
[ "$(id -u)" -eq 0 ] || { echo "à lancer avec sudo : sudo atelier-update ..." >&2; exit 1; }
exec /opt/atelier/updater/bambud upgrade "$@"
EOS
echo "commande : $SBIN_DIR/atelier-update"

# --- Configuration -------------------------------------------------------------------------------------------------------------
say "Configuration $UPDATE_ENV"
FIRST_CONF=0
if [ ! -f "$UPDATE_ENV" ]; then
  FIRST_CONF=1
  cat > "$UPDATE_ENV" <<EOF
# Réglages de la mise à jour automatique (modifiable ; pris en compte au prochain passage du timer, sans redémarrage).
# Aucun secret ici. Le jeton GitHub facultatif (lecture seule) va dans $CONF_DIR/github-token, droits 600, pour éviter
# la limite de débit de l'API publique (60 requêtes/heure) ; sans jeton, tout fonctionne.
ATELIER_UPDATE_REPO="${REPO:-https://github.com/ArseneGA/BAMBULAB-MCP}"
ATELIER_UPDATE_BRANCH="${BRANCH:-main}"
# Plage horaire locale d'installation (ex. 07:00-23:00 : jamais la nuit) ; vide = à toute heure.
ATELIER_UPDATE_WINDOW="${WINDOW}"
ATELIER_UPDATE_KEEP_RELEASES="${KEEP:-3}"
ATELIER_UPDATE_KEEP_BACKUPS="5"
# 1 (défaut) : une impression en cours n'empêche pas la mise à jour (le fichier est sur la carte SD de l'imprimante, le suivi reprend
# après le redémarrage). 0 : attendre la fin de l'impression. Un dépôt FTPS, une confirmation en attente, un lancement en cours
# d'accusé ou un travail lourd retardent toujours la mise à jour.
ATELIER_UPDATE_ALLOW_DURING_PRINT="1"
# required : un test de fumée en échec après bascule déclenche le retour arrière ; advisory : alerte seulement ; off.
ATELIER_UPDATE_SMOKE="${SMOKE:-required}"
# PATH de l'utilisateur de construction (go, node, npm).
ATELIER_UPDATE_PATH="$BPATH"
# Contrôles de CI exigés, séparés par « ; » (noms exacts des jobs). Vide : tous les contrôles du commit.
#ATELIER_UPDATE_REQUIRED_CHECKS="Go (vet + tests);Web (build + vitest);Conformité MCP (validateur du SDK TypeScript)"
#ATELIER_UPDATE_TOKEN_FILE="$CONF_DIR/github-token"
EOF
  chmod 0644 "$UPDATE_ENV"
  echo "créé (valeurs par défaut)"
else
  echo "existant conservé : $UPDATE_ENV"
  [ -z "$REPO" ]    || env_set "$UPDATE_ENV" ATELIER_UPDATE_REPO "$REPO"
  [ -z "$BRANCH" ]  || env_set "$UPDATE_ENV" ATELIER_UPDATE_BRANCH "$BRANCH"
  [ -z "$WINDOW" ]  || env_set "$UPDATE_ENV" ATELIER_UPDATE_WINDOW "$WINDOW"
  [ -z "$SMOKE" ]   || env_set "$UPDATE_ENV" ATELIER_UPDATE_SMOKE "$SMOKE"
  [ -z "$KEEP" ]    || env_set "$UPDATE_ENV" ATELIER_UPDATE_KEEP_RELEASES "$KEEP"
  [ -n "$(env_get ATELIER_UPDATE_PATH "$UPDATE_ENV")" ] || env_set "$UPDATE_ENV" ATELIER_UPDATE_PATH "$BPATH"
fi

# Test de fumée de référence : si le serveur ne le passe pas AUJOURD'HUI, le rendre bloquant provoquerait un retour arrière
# à chaque mise à jour. On bascule alors en « advisory » (alerte sans retour arrière) et on le dit.
PORT="$(env_get BAMBU_ADDR "$ENV_FILE")"; PORT="${PORT##*:}"; PORT="${PORT:-7878}"
SMOKE_SCRIPT="$REPO_ROOT/scripts/smoke-linux.sh"
if [ "$FIRST_CONF" = 1 ] && [ "$TEST_MODE" != 1 ] && [ -f "$SMOKE_SCRIPT" ] && "$SYSTEMCTL" is-active --quiet atelier.service 2>/dev/null; then
  say "Test de fumée de référence sur la version en service"
  nocad=(); [ -n "$(env_get BAMBU_UV_PATH "$ENV_FILE")" ] || nocad=(--no-cad)
  if bash "$SMOKE_SCRIPT" --url "http://127.0.0.1:$PORT" --token-file "$ENV_FILE" "${nocad[@]}"; then
    echo "le serveur passe le test de fumée : mode « required » (retour arrière automatique s'il échoue après une mise à jour)"
  else
    warn "le test de fumée échoue DÉJÀ sur la version en service : mode « advisory » (alerte sans retour arrière). Corriger le serveur puis : sudo sed -i 's/SMOKE=\"advisory\"/SMOKE=\"required\"/' $UPDATE_ENV"
    env_set "$UPDATE_ENV" ATELIER_UPDATE_SMOKE advisory
  fi
fi

# --- Unités systemd ----------------------------------------------------------------------------------------------------------
say "Unités systemd"
BACKUP_DIR="$(env_get BACKUP_DIR "$BACKUP_ENV")"; BACKUP_DIR="${BACKUP_DIR:-/var/backups/atelier}"
changed=0
write_if_changed "$UNIT_DIR/atelier-update.service" 0644 <<EOF && changed=1 || true
$MARK
[Unit]
Description=Mise à jour automatique de l'Atelier Bambu (CI verte, impression en cours tolérée)
Documentation=file://$OPT/README.md
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$OPT/updater/bambud upgrade --auto
# Une construction complète (npm ci, go test) peut durer longtemps sur un petit serveur.
TimeoutStartSec=3h
# Ne jamais gêner le service ni la caméra : priorité minimale.
Nice=19
IOSchedulingClass=idle
CPUSchedulingPolicy=batch
CPUWeight=20
IOWeight=20
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
ReadWritePaths=$OPT $CACHE_DIR $DATA_DIR -$BACKUP_DIR -/run/lock
EOF
write_if_changed "$UNIT_DIR/atelier-update.timer" 0644 <<EOF && changed=1 || true
$MARK
[Unit]
Description=Recherche de mises à jour de l'Atelier Bambu (toutes les 5 minutes)

[Timer]
OnBootSec=5min
# 5 minutes APRÈS la fin du passage précédent : jamais deux passages qui se chevauchent.
OnUnitInactiveSec=5min
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
EOF
[ "$changed" = 1 ] && echo "unités écrites" || echo "unités inchangées"
"$SYSTEMCTL" daemon-reload
rm -f "$DISABLED_FILE.tmp"
if [ "$START" = 1 ] && [ ! -f "$DISABLED_FILE" ]; then
  "$SYSTEMCTL" enable --now atelier-update.timer >/dev/null
  echo "timer actif : systemctl list-timers atelier-update.timer"
elif [ -f "$DISABLED_FILE" ]; then
  warn "$DISABLED_FILE existe : l'automatique reste désactivé (sudo bash $0 --enable pour le réactiver)"
else
  echo "timer non activé (--no-start) : sudo systemctl enable --now atelier-update.timer"
fi

# --- Premier contrôle à blanc (ne change rien) -------------------------------------------------------------------------------
if [ "$TEST_MODE" != 1 ]; then
  say "Contrôle à blanc : que ferait la mise à jour maintenant ?"
  "$OPT/updater/bambud" upgrade --check || warn "le contrôle à blanc a échoué (réseau ? accès à GitHub ?) : voir le message ci-dessus ; le timer réessaiera"
fi

say "Terminé"
cat <<EOF
  Le service n'a PAS été redémarré : il tourne toujours avec sa version actuelle.
  État            : sudo atelier-update --status
  Journal         : journalctl -u atelier-update -f          (un passage toutes les 5 minutes ; silencieux s'il n'y a rien de nouveau)
  À blanc         : sudo atelier-update --check
  Maintenant      : sudo atelier-update --now                (CI verte et service libre restent exigés)
  Désactiver      : sudo bash $0 --disable                   (ou : sudo touch $DISABLED_FILE)
  Épingler        : sudo atelier-update --pin current        (retirer : sudo atelier-update --unpin)
  Notifications   : abonner un canal à l'évènement « system_update » (page /setup/notifications)
  Recommandé      : protéger la branche main sur GitHub (Settings > Branches : exiger la CI verte, interdire le force-push) — docs/SECURITY.md
EOF
