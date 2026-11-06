#!/usr/bin/env bash
# Vérification post-déploiement d'un bambud (Linux ou autre) : santé, capacités réelles (Bambu Studio, profils,
# CadQuery), MCP (tools/list) et profils filament d'une machine. Aucun secret n'est affiché : le jeton est lu
# dans un fichier et passé à curl par stdin (jamais en argument, donc absent de `ps`).
#
# Usage : scripts/smoke-linux.sh [--url URL] [--token-file FICHIER] [--machine NOM] [--wait SECONDES] [--no-cad]
#   --url          URL du service (défaut : $BAMBU_URL, sinon http://127.0.0.1:7878)
#   --token-file   fichier contenant le jeton d'API seul, ou un fichier d'environnement avec une ligne BAMBU_API_TOKEN=…
#                  (ex. /etc/atelier/atelier.env, à lire avec sudo) ; défaut : $BAMBU_API_TOKEN_FILE ; omis si le service n'en exige pas
#   --machine      preset machine pour les profils filament (défaut : "Bambu Lab P1S 0.4 nozzle")
#   --wait         attente max. (s) du chargement des profils et du ping CadQuery au démarrage (défaut 90)
#   --no-cad       ne pas exiger le backend CAO (CadQuery)
# Sortie : une ligne OK/ECHEC par contrôle ; code de sortie 0 si tout passe, 1 sinon.
set -u

URL="${BAMBU_URL:-http://127.0.0.1:7878}"
TOKEN_FILE="${BAMBU_API_TOKEN_FILE:-}"
MACHINE="Bambu Lab P1S 0.4 nozzle"
WAIT=90
NEED_CAD=1
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="$2"; shift 2 ;;
    --token-file) TOKEN_FILE="$2"; shift 2 ;;
    --machine) MACHINE="$2"; shift 2 ;;
    --wait) WAIT="$2"; shift 2 ;;
    --no-cad) NEED_CAD=0; shift ;;
    -h|--help) sed -n "2,14p" "$0"; exit 0 ;;
    *) echo "option inconnue : $1" >&2; exit 2 ;;
  esac
done
URL="${URL%/}"
command -v curl >/dev/null 2>&1 || { echo "curl est requis" >&2; exit 2; }

TOKEN=""
if [ -n "$TOKEN_FILE" ]; then
  [ -r "$TOKEN_FILE" ] || { echo "ECHEC  fichier de jeton illisible : $TOKEN_FILE" >&2; exit 2; }
  # fichier d'une ligne (le jeton seul) ou fichier d'environnement (/etc/atelier/atelier.env : ligne BAMBU_API_TOKEN=…)
  if grep -q '^BAMBU_API_TOKEN=' "$TOKEN_FILE"; then
    TOKEN="$(grep -m1 '^BAMBU_API_TOKEN=' "$TOKEN_FILE" | cut -d= -f2- | tr -d '[:space:]"'"'"'')"
  else
    TOKEN="$(tr -d '[:space:]' < "$TOKEN_FILE")"
  fi
fi

FAILED=0
ok()   { printf 'OK     %s\n' "$*"; }
fail() { printf 'ECHEC  %s\n' "$*"; FAILED=1; }

# curl authentifié : l'en-tête Authorization passe par stdin (-K -), jamais par argv.
acurl() {
  if [ -n "$TOKEN" ]; then
    printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" | curl -sS -K - --max-time 30 "$@"
  else
    curl -sS --max-time 30 "$@"
  fi
}
# Extrait la valeur d'un champ JSON simple (chaîne ou nombre) : jget '"version"' < json
jget() { grep -o "$1\"*:*\"*[^,}\"]*" | head -1 | sed 's/^[^:]*:"\{0,1\}//'; }

# 1. Santé (GET et HEAD, sans jeton)
code="$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' "$URL/health" 2>/dev/null || echo 000)"
if [ "$code" = "200" ]; then ok "GET /health"; else fail "GET /health -> HTTP $code (service arrêté ? mauvaise URL ?)"; fi
code="$(curl -sS --max-time 15 -I -o /dev/null -w '%{http_code}' "$URL/health" 2>/dev/null || echo 000)"
if [ "$code" = "200" ]; then ok "HEAD /health"; else fail "HEAD /health -> HTTP $code"; fi
HEALTH="$(curl -sS --max-time 15 "$URL/health" 2>/dev/null || true)"
ok "version $(printf '%s' "$HEALTH" | jget '"version"'), schéma $(printf '%s' "$HEALTH" | jget '"schema_version"')"

# 2. Authentification : sans jeton l'API doit refuser quand un jeton est exigé
if [ -n "$TOKEN" ]; then
  code="$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' "$URL/api/capabilities" 2>/dev/null || echo 000)"
  if [ "$code" = "401" ]; then ok "API fermée sans jeton (401)"; else fail "API sans jeton -> HTTP $code (401 attendu)"; fi
fi

# 3. Capacités réelles (attend le chargement des profils et le ping CadQuery, lancés en arrière-plan)
deadline=$(( $(date +%s) + WAIT ))
CAPS=""
while :; do
  CAPS="$(acurl "$URL/api/capabilities" 2>/dev/null || true)"
  loading=0
  printf '%s' "$CAPS" | grep -q '"profiles":{' || printf '%s' "$CAPS" | grep -q '"studio":{"available":false' || loading=1
  if [ "$NEED_CAD" = 1 ] && printf '%s' "$CAPS" | grep -q 'vérification de CadQuery en cours'; then loading=1; fi
  [ "$loading" = 0 ] && break
  [ "$(date +%s)" -ge "$deadline" ] && break
  sleep 2
done
if ! printf '%s' "$CAPS" | grep -q '"service":"bambud"'; then
  fail "GET /api/capabilities illisible (jeton refusé ?) : $(printf '%s' "$CAPS" | head -c 120)"
else
  if printf '%s' "$CAPS" | grep -q '"studio":{"available":true'; then
    STUDIO="$(printf '%s' "$CAPS" | grep -o '"studio":{[^}]*}' | head -1)"
    ok "Bambu Studio $(printf '%s' "$STUDIO" | jget '"version"') ($(printf '%s' "$STUDIO" | jget '"path"'))"
    PROF="$(printf '%s' "$CAPS" | grep -o '"profiles":{[^}]*}' | head -1)"
    if [ -n "$PROF" ]; then
      m="$(printf '%s' "$PROF" | jget '"machines"')"; f="$(printf '%s' "$PROF" | jget '"filaments"')"; p="$(printf '%s' "$PROF" | jget '"processes"')"
      if [ "${m:-0}" -gt 0 ] && [ "${f:-0}" -gt 0 ] && [ "${p:-0}" -gt 0 ]; then
        ok "profils chargés : $(printf '%s' "$PROF" | jget '"models"') modèles, $m machines, $p process, $f filaments"
      else fail "profils chargés mais vides : $PROF"; fi
    else fail "profils non chargés dans le délai de ${WAIT} s"; fi
  else
    fail "Bambu Studio indisponible : $(printf '%s' "$CAPS" | grep -o '"studio":{[^}]*}' | head -c 300)"
  fi
  if printf '%s' "$CAPS" | grep -q '"id":"cad_worker","available":true'; then
    ok "backend CAO : $(printf '%s' "$CAPS" | grep -o '"id":"cad_worker"[^}]*' | jget '"note"')"
  elif [ "$NEED_CAD" = 1 ]; then
    fail "backend CAO indisponible : $(printf '%s' "$CAPS" | grep -o '"id":"cad_worker"[^}]*' | jget '"note"')"
  else
    ok "backend CAO non exigé (--no-cad)"
  fi
fi

# 4. MCP : initialize puis tools/list (Streamable HTTP ; réponse JSON ou SSE)
HDRS="$(mktemp)"; trap 'rm -f "$HDRS"' EXIT
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke-linux","version":"1"}}}'
acurl -D "$HDRS" -o /dev/null -X POST "$URL/mcp" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$INIT" 2>/dev/null
status="$(head -1 "$HDRS" | awk '{print $2}')"
SID="$(grep -i '^mcp-session-id:' "$HDRS" | head -1 | cut -d: -f2- | tr -d '[:space:]')"
if [ "$status" != "200" ]; then
  fail "MCP initialize -> HTTP ${status:-?} (jeton, Host ou proxy ?)"
else
  SIDH=(); [ -n "$SID" ] && SIDH=(-H "Mcp-Session-Id: $SID")
  acurl -o /dev/null -X POST "$URL/mcp" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' "${SIDH[@]}" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' 2>/dev/null
  TOOLS="$(acurl -X POST "$URL/mcp" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' "${SIDH[@]}" \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' 2>/dev/null || true)"
  n="$(printf '%s' "$TOOLS" | grep -o '"name":"[a-z_]*"' | wc -l | tr -d ' ')"
  if [ "${n:-0}" -ge 5 ] && printf '%s' "$TOOLS" | grep -q '"get_capabilities"'; then ok "MCP tools/list : $n outils (dont get_capabilities)"
  else fail "MCP tools/list inattendu ($n outils) : $(printf '%s' "$TOOLS" | head -c 160)"; fi
fi

# 5. Profils filament d'une machine
ENC="$(printf '%s' "$MACHINE" | sed 's/ /%20/g')"
FIL="$(acurl "$URL/api/profiles/filaments?machine=$ENC" 2>/dev/null || true)"
nf="$(printf '%s' "$FIL" | grep -o '"[^"]*@BBL[^"]*"' | wc -l | tr -d ' ')"
if [ "${nf:-0}" -gt 0 ]; then ok "profils filament de « $MACHINE » : $nf"
else fail "profils filament de « $MACHINE » introuvables : $(printf '%s' "$FIL" | head -c 160)"; fi

echo
if [ "$FAILED" = 0 ]; then echo "Smoke test : TOUT EST OK"; else echo "Smoke test : DES CONTROLES ONT ECHOUE"; fi
exit "$FAILED"
