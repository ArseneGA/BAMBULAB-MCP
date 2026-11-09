#!/usr/bin/env bash
# Harnais de deploy/linux/enable-auto-update.sh : joue la migration d'une installation « historique » vers
# releases/current sur un arbre FACTICE (aucun root, aucun systemd, aucun réseau) avec un faux systemctl et un faux bambud.
# Prouve : migration sans arrêt ni redémarrage du service, binaires conservés (liens), idempotence, réglages utilisateur
# préservés, interrupteurs, refus d'un outil trop ancien sans rien modifier.
#
# Usage : scripts/test-enable-auto-update.sh        (Linux ; code 77 = « sauté », liens symboliques indisponibles)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../deploy/linux/enable-auto-update.sh"
FAILS=0
ok()   { printf 'OK     %s\n' "$*"; }
fail() { printf 'ECHEC  %s\n' "$*"; FAILS=$((FAILS + 1)); }
check() { # check "description" commande...
  local d="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$d"; else fail "$d"; fi
}

ROOT="$(mktemp -d)"; trap 'rm -rf "$ROOT"' EXIT
ln -s a "$ROOT/lnk" 2>/dev/null
if [ ! -L "$ROOT/lnk" ]; then echo "SAUTÉ : liens symboliques indisponibles ici (à jouer sur Linux)"; exit 77; fi
rm -f "$ROOT/lnk"

OPT="$ROOT/opt"; ETC="$ROOT/etc"; UNITS="$ROOT/units"; SBIN="$ROOT/sbin"; CACHE="$ROOT/cache"; LOG="$ROOT/systemctl.log"
mkdir -p "$OPT/bin" "$ETC" "$UNITS" "$SBIN" "$ROOT/data" "$ROOT/backups"

# Faux binaires : le « bambud » d'avant (sans sous-commande upgrade) et l'outil de mise à jour (qui en contient les noms).
cat > "$OPT/bin/bambud" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in --version) echo "bambud v-legacy" ;; *) echo "ancien bambud (service)" ;; esac
EOF
printf '#!/usr/bin/env bash\necho ancien-mcp\n' > "$OPT/bin/bambu-mcp"
printf '#!/usr/bin/env bash\necho admin-inchange\n' > "$OPT/bin/atelier-admin"
printf '#!/usr/bin/env bash\necho backup-inchange\n' > "$OPT/bin/atelier-backup.sh"
chmod 0755 "$OPT/bin/"*
ADMIN_SUM="$(sha256sum "$OPT/bin/atelier-admin" | cut -d' ' -f1)"
OLD_BAMBUD_SUM="$(sha256sum "$OPT/bin/bambud" | cut -d' ' -f1)"

cat > "$ROOT/updater-bambud" <<'EOF'
#!/usr/bin/env bash
# faux outil : contient les mots upgrade-probe et schema-version
case "${1:-}" in
  upgrade-probe) echo '{"schema_version":270,"busy":false,"reasons":[]}' ;;
  schema-version) echo 280 ;;
  --version) echo "bambud v-nouveau" ;;
  *) echo "upgrade $*" ;;
esac
EOF
chmod 0755 "$ROOT/updater-bambud"
printf '#!/usr/bin/env bash\necho trop-ancien\n' > "$ROOT/old-updater"; chmod 0755 "$ROOT/old-updater"

cat > "$ROOT/fake-systemctl" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "$LOG"
case "\$1" in is-active) exit 0 ;; esac
exit 0
EOF
chmod 0755 "$ROOT/fake-systemctl"

printf '# Atelier Bambu\nBAMBU_DATA_DIR="%s"\nBAMBU_ADDR="0.0.0.0:7878"\nBAMBU_API_TOKEN="jeton-de-test-jamais-affiche"\n' "$ROOT/data" > "$ETC/atelier.env"
printf 'BACKUP_DIR="%s"\nBACKUP_KEEP=14\n' "$ROOT/backups" > "$ETC/backup.env"

export ATELIER_ENABLE_TEST=1 ATELIER_OPT="$OPT" ATELIER_CONF_DIR="$ETC" ATELIER_UNIT_DIR="$UNITS" ATELIER_SBIN_DIR="$SBIN" \
  ATELIER_UPDATE_CACHE_DIR="$CACHE" ATELIER_SYSTEMCTL="$ROOT/fake-systemctl" ATELIER_TEST_PATH=/usr/bin

snapshot() { ( cd "$ROOT" && find opt etc units sbin -printf '%p %y %l %s\n' 2>/dev/null | sort; sha256sum etc/* units/* sbin/* 2>/dev/null ) ; }

# --- 1. Outil trop ancien : refus, RIEN n'est modifié ---------------------------------------------------------------------
BEFORE="$(snapshot)"
if bash "$SCRIPT" --updater-bin "$ROOT/old-updater" >"$ROOT/out0" 2>&1; then fail "outil sans « upgrade » accepté"; else ok "outil sans « upgrade » refusé"; fi
check "refus : aucune modification de l'arbre" test "$BEFORE" = "$(snapshot)"
check "refus : aucun appel systemctl" test ! -s "$LOG"

# --- 2. Migration -----------------------------------------------------------------------------------------------------------
if bash "$SCRIPT" --updater-bin "$ROOT/updater-bambud" --window 07:00-23:00 >"$ROOT/out1" 2>&1; then ok "activation (première fois)"; else fail "activation échouée"; cat "$ROOT/out1"; fi
check "current est un lien vers releases/legacy-*" bash -c "readlink '$OPT/current' | grep -q '^releases/legacy-'"
check "bin/bambud est un lien vers ../current/bin/bambud" test "$(readlink "$OPT/bin/bambud")" = "../current/bin/bambud"
check "bin/bambu-mcp est un lien vers ../current/bin/bambu-mcp" test "$(readlink "$OPT/bin/bambu-mcp")" = "../current/bin/bambu-mcp"
check "le binaire lancé via bin/bambud est bien l'ancien (--version)" test "$("$OPT/bin/bambud" --version)" = "bambud v-legacy"
check "la copie de l'ancien binaire est identique" test "$(sha256sum "$OPT/current/bin/bambud" | cut -d' ' -f1)" = "$OLD_BAMBUD_SUM"
check "atelier-admin et atelier-backup.sh restent des fichiers inchangés" test "$(sha256sum "$OPT/bin/atelier-admin" | cut -d' ' -f1)" = "$ADMIN_SUM" -a ! -L "$OPT/bin/atelier-admin" -a ! -L "$OPT/bin/atelier-backup.sh"
check "REVISION : version, schéma 270 et drapeau legacy" bash -c "grep -q '\"version\": \"v-legacy\"' '$OPT/current/REVISION' && grep -q '\"schema\": 270' '$OPT/current/REVISION' && grep -q '\"legacy\": true' '$OPT/current/REVISION'"
check "outil installé dans updater/" test -x "$OPT/updater/bambud"
check "commande atelier-update installée" test -x "$SBIN/atelier-update"
check "unité de service : ExecStart sur l'outil, priorité minimale, une exécution à la fois" bash -c "grep -q 'ExecStart=$OPT/updater/bambud upgrade --auto' '$UNITS/atelier-update.service' && grep -q '^Nice=19' '$UNITS/atelier-update.service' && grep -q '^Type=oneshot' '$UNITS/atelier-update.service'"
check "timer : 5 minutes après la fin du passage précédent" bash -c "grep -q '^OnUnitInactiveSec=5min' '$UNITS/atelier-update.timer' && grep -q '^OnBootSec=5min' '$UNITS/atelier-update.timer'"
check "réglages : fenêtre horaire et chemin de construction" bash -c "grep -q '^ATELIER_UPDATE_WINDOW=\"07:00-23:00\"' '$ETC/auto-update.env' && grep -q '^ATELIER_UPDATE_PATH=\"/usr/bin\"' '$ETC/auto-update.env'"
check "auto-update.env sans aucun secret" bash -c "! grep -qi 'jeton-de-test' '$ETC/auto-update.env' '$UNITS/atelier-update.service'"
check "le timer est activé" grep -q '^enable --now atelier-update.timer' "$LOG"
check "réglage : impression en cours tolérée par défaut (ATELIER_UPDATE_ALLOW_DURING_PRINT=1)" grep -q '^ATELIER_UPDATE_ALLOW_DURING_PRINT="1"' "$ETC/auto-update.env"
# Contrôles de la branche « root réel » (runuser, non jouable en mode test : vérifiés dans le texte du script ; la preuve réelle
# est à jouer sur le serveur Linux avec sudo bash deploy/linux/enable-auto-update.sh).
check "contrôle du compte de construction : « go version » (go refuse « go --version »)" bash -c "grep -q 'go) vflag=version' '$SCRIPT' && ! grep -E 'go --version' '$SCRIPT'"
check "toute commande runuser part de « / » (dossier courant de root illisible)" bash -c "! grep -E 'runuser -u' '$SCRIPT' | grep -v 'cd / &&' | grep -v '^ *#' | grep -q ."
# SANS INTERRUPTION : le service n'est jamais arrêté ni redémarré.
check "aucun stop / restart / reload de atelier.service" bash -c "! grep -E '(stop|restart|reload|kill).*atelier\.service|^(stop|restart) ' '$LOG'"

# --- 3. Idempotence ---------------------------------------------------------------------------------------------------------
AFTER1="$(snapshot)"
LEGACY_COUNT="$(ls "$OPT/releases" | wc -l)"
if bash "$SCRIPT" --updater-bin "$ROOT/updater-bambud" --window 07:00-23:00 >"$ROOT/out2" 2>&1; then ok "seconde activation"; else fail "seconde activation échouée"; cat "$ROOT/out2"; fi
check "idempotent : aucune différence dans l'arbre, les unités et la configuration" test "$AFTER1" = "$(snapshot)"
[ "$AFTER1" = "$(snapshot)" ] || diff <(printf '%s\n' "$AFTER1") <(snapshot) | head -20
check "idempotent : pas de seconde version « legacy »" test "$(ls "$OPT/releases" | wc -l)" = "$LEGACY_COUNT"
check "idempotent : « déjà migré »" grep -q 'déjà migré' "$ROOT/out2"

# --- 4. Les réglages de l'utilisateur survivent à une réactivation -------------------------------------------------------
echo '# réglage maison' >> "$ETC/auto-update.env"
sed -i 's/^ATELIER_UPDATE_KEEP_BACKUPS=.*/ATELIER_UPDATE_KEEP_BACKUPS="9"/' "$ETC/auto-update.env"
bash "$SCRIPT" --updater-bin "$ROOT/updater-bambud" --smoke advisory >"$ROOT/out3" 2>&1 || fail "réactivation avec --smoke"
check "réglage maison et KEEP_BACKUPS=9 conservés" bash -c "grep -q 'réglage maison' '$ETC/auto-update.env' && grep -q 'KEEP_BACKUPS=\"9\"' '$ETC/auto-update.env'"
check "--smoke advisory appliqué sans écraser le reste" bash -c "grep -q 'ATELIER_UPDATE_SMOKE=\"advisory\"' '$ETC/auto-update.env' && grep -q 'WINDOW=\"07:00-23:00\"' '$ETC/auto-update.env'"

# --- 5. Interrupteurs -------------------------------------------------------------------------------------------------------
bash "$SCRIPT" --disable >"$ROOT/out4" 2>&1 || fail "--disable"
check "--disable : fichier auto-update.disabled créé, timer arrêté" bash -c "test -f '$ETC/auto-update.disabled' && grep -q '^disable --now atelier-update.timer' '$LOG'"
bash "$SCRIPT" --updater-bin "$ROOT/updater-bambud" >"$ROOT/out5" 2>&1 || fail "activation alors que désactivé"
check "un fichier auto-update.disabled existant est respecté par l'activation" test -f "$ETC/auto-update.disabled"
bash "$SCRIPT" --enable >"$ROOT/out6" 2>&1 || fail "--enable"
check "--enable : fichier retiré" test ! -f "$ETC/auto-update.disabled"

# --- 6. Installation incomplète : refus net ------------------------------------------------------------------------------
if ATELIER_OPT="$ROOT/vide" bash "$SCRIPT" --updater-bin "$ROOT/updater-bambud" >"$ROOT/out7" 2>&1; then fail "installation absente acceptée"; else ok "installation absente refusée"; fi
check "refus : rien créé" test ! -e "$ROOT/vide"

echo
if [ "$FAILS" = 0 ]; then echo "Harnais enable-auto-update : TOUT EST OK"; else echo "Harnais enable-auto-update : $FAILS ECHEC(S)"; fi
exit "$FAILS"
