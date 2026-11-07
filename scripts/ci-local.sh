#!/usr/bin/env bash
# Rejoue le job « Go (vet + tests) » de .github/workflows/ci.yml dans Docker (Linux, comme GitHub).
# Usage : scripts/ci-local.sh [user|root]   (défaut : user = non-root, comme le runner GitHub)
# Le dépôt est copié par tar (pas de montage : permissions Linux réelles, arbre de travail intact).
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL="*"
cd "$(dirname "$0")/.."
WHO="${1:-user}"
GOVER="$(sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' go.mod | head -1)"
GOIMG="${GOIMG:-golang:${GOVER}}"
NODEIMG="${NODEIMG:-node:22}"
UIDARGS=()
[ "$WHO" = "user" ] && UIDARGS=(--user 1001:1001 -e HOME=/tmp/home)

echo ">> étape 1/2 : build web (${NODEIMG})" >&2
tar -c --exclude=./.git --exclude=node_modules --exclude=./.claude --exclude=./web/dist . \
  | docker run --rm -i --pull=missing "${UIDARGS[@]}" "$NODEIMG" sh -c '
    set -e; mkdir -p /tmp/home /tmp/w; tar -x -C /tmp/w; cd /tmp/w/web
    (npm ci --no-audit --no-fund && mkdir -p ../internal/webui/assets ../internal/mcpserver/ui/views && npm run build) >&2
    cd /tmp/w && tar -c --exclude=node_modules .' \
  | docker run --rm -i --pull=missing "${UIDARGS[@]}" -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath "$GOIMG" sh -c '
    set -e; mkdir -p /tmp/home /tmp/w; tar -x -C /tmp/w; cd /tmp/w
    echo ">> étape 2/2 : go vet" >&2; go vet ./...
    echo ">> go test -short" >&2; go test -short ./... -timeout 30m'
