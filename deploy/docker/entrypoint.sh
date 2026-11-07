#!/bin/sh
# Point d'entrée du conteneur : prépare /data, fournit un jeton d'API et lance bambud.
# - Jeton d'API : BAMBU_API_TOKEN, sinon BAMBU_API_TOKEN_FILE, sinon /data/api-token (créé au premier démarrage,
#   droits 0600, JAMAIS écrit dans les journaux). Le lire : docker compose exec atelier cat /data/api-token
# - Bambu Studio : /opt/bambustudio/AppRun s'il est dans l'image (sinon BAMBU_STUDIO_BIN, ex. volume monté).
set -eu

mkdir -p "$HOME" "$TMPDIR" "$BAMBU_DATA_DIR" /data/import

if [ -z "${BAMBU_API_TOKEN:-}" ]; then
  f="${BAMBU_API_TOKEN_FILE:-/data/api-token}"
  if [ ! -s "$f" ]; then
    umask 077
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$f"
    echo "atelier : jeton d'API créé dans $f (non affiché)" >&2
  fi
  BAMBU_API_TOKEN="$(head -n1 "$f" | tr -d '\r\n ')"
  export BAMBU_API_TOKEN
fi

if [ -z "${BAMBU_STUDIO_BIN:-}" ] && [ -x /opt/bambustudio/AppRun ]; then
  BAMBU_STUDIO_BIN=/opt/bambustudio/AppRun
  export BAMBU_STUDIO_BIN
fi

if [ "${1:-}" = "bambud" ]; then
  shift
  exec bambud "$@"
fi
exec "$@"
