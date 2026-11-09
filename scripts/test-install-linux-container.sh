#!/usr/bin/env bash
# Joue deploy/linux/install.sh puis enable-auto-update.sh puis install.sh (mise à jour manuelle sur une installation « releases/current »)
# DANS UN CONTENEUR JETABLE : le script écrit dans /opt, /etc et /var/lib, il refuse donc de tourner ailleurs.
#
#   docker run --rm -v "$PWD:/src:ro" -w /src golang:1.26 bash scripts/test-install-linux-container.sh
#
# Prouve : une installation neuve ne change pas (fichiers réels dans /opt/atelier/bin, aucune unité de mise à jour) ;
# l'activation migre sans toucher au service ; une réinstallation manuelle NE casse PAS le schéma (les liens de bin/ restent
# des liens, une nouvelle version manual-* devient current, l'ancienne devient previous) ; --auto-update est désactivé par défaut.
set -uo pipefail

[ -f /.dockerenv ] && [ "${ATELIER_ALLOW_DESTRUCTIVE:-1}" = 1 ] || { echo "REFUS : ce test écrit dans /opt, /etc et /var/lib ; à lancer uniquement dans un conteneur jetable" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "REFUS : root requis (conteneur)" >&2; exit 2; }

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAILS=0
ok()   { printf 'OK     %s\n' "$*"; }
fail() { printf 'ECHEC  %s\n' "$*"; FAILS=$((FAILS + 1)); }
check() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then ok "$d"; else fail "$d"; fi; }

# systemctl factice (pas de systemd dans un conteneur) ; il journalise tout.
LOG=/tmp/systemctl.log; : > "$LOG"
cat > /usr/local/bin/systemctl <<EOF
#!/usr/bin/env bash
echo "\$*" >> $LOG
exit 0
EOF
chmod 0755 /usr/local/bin/systemctl

mkdelivery() { # mkdelivery DIR VERSION
  mkdir -p "$1"
  for a in amd64 arm64; do
    printf '#!/usr/bin/env bash\ncase "${1:-}" in --version) echo "bambud %s" ;; schema-version) echo 280 ;; upgrade-probe) echo "{\\"schema_version\\":270,\\"busy\\":false,\\"reasons\\":[]}" ;; *) echo service ;; esac\n' "$2" > "$1/bambud-linux-$a"
    printf '#!/usr/bin/env bash\necho mcp-%s\n' "$2" > "$1/bambu-mcp-linux-$a"
    chmod 0755 "$1/bambud-linux-$a" "$1/bambu-mcp-linux-$a"
  done
}
mkdelivery /tmp/dist-1 v1
mkdelivery /tmp/dist-2 v2

COMMON=(--no-start --no-cad --no-backup-timer --studio /nonexistent --listen 127.0.0.1:7878 --public-url http://127.0.0.1:7878)

# 1. Installation neuve : comportement INCHANGÉ (aucune unité de mise à jour, binaires réels).
bash "$SRC/deploy/linux/install.sh" --from /tmp/dist-1 "${COMMON[@]}" >/tmp/install1.log 2>&1 || { fail "install.sh (neuf)"; tail -20 /tmp/install1.log; }
check "installation neuve : bin/bambud est un fichier réel" bash -c '[ -f /opt/atelier/bin/bambud ] && [ ! -L /opt/atelier/bin/bambud ]'
check "installation neuve : aucune unité de mise à jour créée" bash -c '[ ! -e /etc/systemd/system/atelier-update.service ] && [ ! -e /etc/systemd/system/atelier-update.timer ]'
check "installation neuve : pas de lien current, pas de dossier releases" bash -c '[ ! -e /opt/atelier/current ] && [ ! -d /opt/atelier/releases ]'
check "installation neuve : l'automatique est annoncé non activé" grep -q 'non activées' /tmp/install1.log
check "installation neuve : --version" test "$(/opt/atelier/bin/bambud --version)" = "bambud v1"

# 2. Réinstallation SANS le schéma : toujours le comportement historique.
bash "$SRC/deploy/linux/install.sh" --from /tmp/dist-1 "${COMMON[@]}" >/tmp/install1b.log 2>&1 || fail "install.sh (idempotence)"
check "réinstallation classique : toujours un fichier réel" bash -c '[ -f /opt/atelier/bin/bambud ] && [ ! -L /opt/atelier/bin/bambud ] && [ ! -e /opt/atelier/current ]'

# 3. Activation (migration sans interruption), en mode harnais (sans useradd ni systemd réels).
: > "$LOG"
ATELIER_ENABLE_TEST=1 ATELIER_TEST_PATH=/usr/local/go/bin bash "$SRC/deploy/linux/enable-auto-update.sh" --updater-bin /tmp/dist-2/bambud-linux-amd64 >/tmp/enable.log 2>&1 \
  || { fail "enable-auto-update.sh"; tail -20 /tmp/enable.log; }
check "migration : current -> releases/legacy-*" bash -c "readlink /opt/atelier/current | grep -q '^releases/legacy-'"
check "migration : bin/bambud est un lien vers ../current/bin/bambud" test "$(readlink /opt/atelier/bin/bambud)" = "../current/bin/bambud"
check "migration : le service n'a été ni arrêté ni redémarré" bash -c "! grep -E '^(stop|restart)' $LOG"
check "migration : atelier-admin inchangé (fichier)" bash -c '[ -f /opt/atelier/bin/atelier-admin ] && [ ! -L /opt/atelier/bin/atelier-admin ]'
check "migration : la version en service est toujours v1" test "$(/opt/atelier/bin/bambud --version)" = "bambud v1"

# 4. install.sh sur une installation migrée : nouvelle version à côté, liens intacts.
: > "$LOG"
bash "$SRC/deploy/linux/install.sh" --from /tmp/dist-2 "${COMMON[@]}" >/tmp/install2.log 2>&1 || { fail "install.sh (sur releases/current)"; tail -20 /tmp/install2.log; }
check "install.sh sur releases/current : bin/bambud reste un lien" test "$(readlink /opt/atelier/bin/bambud)" = "../current/bin/bambud"
check "install.sh sur releases/current : current -> releases/manual-v2-*" bash -c "readlink /opt/atelier/current | grep -q '^releases/manual-v2-'"
check "install.sh sur releases/current : previous -> l'ancienne version (legacy)" bash -c "readlink /opt/atelier/previous | grep -q '^releases/legacy-'"
check "install.sh sur releases/current : la nouvelle version est v2" test "$(/opt/atelier/bin/bambud --version)" = "bambud v2"
check "install.sh sur releases/current : l'ancienne version reste sur disque (retour arrière possible)" bash -c 'ls -d /opt/atelier/releases/legacy-* >/dev/null && /opt/atelier/releases/legacy-*/bin/bambud --version | grep -q v1'
check "install.sh sur releases/current : REVISION de la version manuelle (schéma 280)" bash -c "grep -q '\"schema\": 280' /opt/atelier/releases/manual-v2-*/REVISION"
check "install.sh sur releases/current : outil de mise à jour rafraîchi" bash -c '/opt/atelier/updater/bambud --version | grep -q v2'

echo
if [ "$FAILS" = 0 ]; then echo "Test conteneur install.sh : TOUT EST OK"; else echo "Test conteneur install.sh : $FAILS ECHEC(S)"; fi
exit "$FAILS"
