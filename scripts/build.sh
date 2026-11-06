#!/usr/bin/env bash
# Build, tests et compilation croisée. Usage : scripts/build.sh [all|build|test|test-race|vet|cross|clean]
set -euo pipefail
cd "$(dirname "$0")/.."
export CGO_ENABLED=0   # SQLite pur Go : pas de cgo
LDFLAGS="-s -w"
CMDS=(bambud bambu-mcp)

build() {
  mkdir -p bin
  for c in "${CMDS[@]}"; do go build -ldflags "$LDFLAGS" -o "bin/$c" "./cmd/$c"; done
}

cross() {
  for arch in amd64 arm64; do
    mkdir -p "bin/linux-$arch"
    for c in "${CMDS[@]}"; do
      GOOS=linux GOARCH=$arch go build -ldflags "$LDFLAGS" -o "bin/linux-$arch/$c" "./cmd/$c"
    done
    GOOS=linux GOARCH=$arch go build ./...
  done
  echo "cross-compile OK (linux/amd64, linux/arm64)"
}

case "${1:-all}" in
  vet)       go vet ./... ;;
  test)      go test ./... -count=1 ;;
  test-race) CGO_ENABLED=1 go test ./... -race -count=1 ;;  # exige cgo + compilateur C
  build)     build ;;
  cross)     cross ;;
  clean)     rm -rf bin data ;;
  all)       go vet ./... && go test ./... -count=1 && build ;;
  *) echo "tâche inconnue : $1" >&2; exit 2 ;;
esac
