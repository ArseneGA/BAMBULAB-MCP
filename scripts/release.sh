#!/usr/bin/env bash
# Construit une livraison complète dans dist/<version>/ :
#   - interface web + vues MCP App (si web/ existe : npm ci puis npm run build[, build:mcp-app]) ;
#   - bambud, bambu-mcp, printer-sim pour linux/amd64 et linux/arm64 ;
#   - bambu-mcp.exe et bambu-agent.exe pour windows/amd64 ;
#   - l'archive du worker CAO (workers/geometry sans .venv ni caches) ;
#   - l'archive de déploiement (deploy/ + docs utilisateur) ;
#   - SHA256SUMS.
#
# Usage : scripts/release.sh [--version V] [--skip-web] [--require-web] [--skip-tests] [--out DIR]
# SQLite est pur Go : CGO_ENABLED=0, aucune chaîne C nécessaire pour la compilation croisée.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=""
SKIP_WEB=0
REQUIRE_WEB=0
SKIP_TESTS=0
OUT_ROOT="dist"
while [ $# -gt 0 ]; do
  case "$1" in
    --version)      VERSION="${2:?--version attend une valeur}"; shift 2 ;;
    --skip-web)     SKIP_WEB=1; shift ;;
    --require-web)  REQUIRE_WEB=1; shift ;;
    --skip-tests)   SKIP_TESTS=1; shift ;;
    --out)          OUT_ROOT="${2:?--out attend un dossier}"; shift 2 ;;
    -h|--help)      sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "option inconnue : $1" >&2; exit 2 ;;
  esac
done

if [ -z "$VERSION" ]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
fi
OUT="$OUT_ROOT/$VERSION"
export CGO_ENABLED=0
PKG="github.com/ArseneGA/BAMBULAB-MCP/internal/core"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION}"

# GNU tar prend « C:\... » pour une machine distante (Git Bash sous Windows) : --force-local.
TAR_LOCAL=()
if tar --version 2>/dev/null | grep -q 'GNU tar'; then TAR_LOCAL=(--force-local); fi

say() { printf '\n==> %s\n' "$*"; }

# --- 1. Interface web et vues MCP App ----------------------------------------
if [ "$SKIP_WEB" = 1 ]; then
  say "web : ignoré (--skip-web)"
elif [ -f web/package.json ]; then
  say "web : npm ci && npm run build"
  command -v npm >/dev/null || { echo "npm est requis pour construire web/ (ou utiliser --skip-web)" >&2; exit 1; }
  ( cd web
    npm ci
    npm run build
    if node -e "process.exit((require('./package.json').scripts||{})['build:mcp-app']?0:1)"; then
      echo "vues MCP App : npm run build:mcp-app"
      npm run build:mcp-app
    fi )
elif [ "$REQUIRE_WEB" = 1 ]; then
  echo "web/ est absent et --require-web est demandé" >&2
  exit 1
else
  say "web : dossier web/ absent, étape ignorée (les binaires embarquent l'interface déjà présente dans le dépôt)"
fi

# --- 2. Contrôles -------------------------------------------------------------
say "go vet"
go vet ./...
if [ "$SKIP_TESTS" = 0 ]; then
  say "go test -short"
  go test -short ./... -count=1
fi

# --- 3. Binaires ---------------------------------------------------------------
rm -rf "$OUT"
mkdir -p "$OUT"

build() { # build GOOS GOARCH CMD SORTIE
  echo "  $1/$2 $3 -> $4"
  GOOS="$1" GOARCH="$2" go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$4" "./cmd/$3"
}

say "binaires Linux"
for arch in amd64 arm64; do
  build linux "$arch" bambud      "bambud-linux-$arch"
  build linux "$arch" bambu-mcp   "bambu-mcp-linux-$arch"
  build linux "$arch" printer-sim "printer-sim-linux-$arch"
done
say "binaires Windows"
build windows amd64 bambu-mcp   "bambu-mcp-windows-amd64.exe"
build windows amd64 bambu-agent "bambu-agent-windows-amd64.exe"

# --- 4. Worker CAO (sans .venv ni caches) -------------------------------------
say "worker CAO"
tar "${TAR_LOCAL[@]}" --exclude='.venv' --exclude='__pycache__' --exclude='.pytest_cache' --exclude='*.pyc' \
    --exclude='.mypy_cache' --exclude='.ruff_cache' \
    -czf "$OUT/atelier-cad-worker-$VERSION.tar.gz" -C workers geometry
echo "  atelier-cad-worker-$VERSION.tar.gz"

# --- 5. Déploiement et documentation utilisateur -------------------------------
say "archive de déploiement"
DEPLOY_ITEMS=()
for p in deploy README.md docs/INSTALL.md docs/BRANCHEMENT.md docs/UTILISATION.md docs/ETAT.md docs/SECURITY.md; do
  [ -e "$p" ] && DEPLOY_ITEMS+=("$p")
done
tar "${TAR_LOCAL[@]}" -czf "$OUT/atelier-deploy-$VERSION.tar.gz" "${DEPLOY_ITEMS[@]}"
echo "  atelier-deploy-$VERSION.tar.gz (${#DEPLOY_ITEMS[@]} éléments)"

# --- 6. Empreintes ------------------------------------------------------------
say "SHA256SUMS"
( cd "$OUT"
  if command -v sha256sum >/dev/null; then
    sha256sum --text -- * | grep -v ' SHA256SUMS$' > SHA256SUMS
  else
    shasum -a 256 --text -- * | grep -v ' SHA256SUMS$' > SHA256SUMS
  fi
  cat SHA256SUMS )

say "livraison prête : $OUT"
