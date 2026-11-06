# Cibles principales. Sous Windows sans make, utiliser scripts/build.ps1.
# SQLite est pur Go (modernc) : CGO_ENABLED=0 partout, compilation croisée triviale.

export CGO_ENABLED=0
LDFLAGS := -s -w
CMDS := bambud bambu-mcp

.PHONY: all build web release test test-race vet cross clean

all: vet test build

build:
	@mkdir -p bin
	@for c in $(CMDS); do go build -ldflags "$(LDFLAGS)" -o bin/$$c ./cmd/$$c; done

# Interface web et vue MCP App : construites puis embarquees dans le binaire (go:embed).
web:
	cd web && (test -d node_modules || npm install) && node scripts/embed.mjs

# Construit web puis Go : interface + verifications + binaires.
release: web vet test build

vet:
	go vet ./...

test:
	go test ./... -count=1

# -race exige cgo + un compilateur C (indisponible sur ce poste Windows).
test-race:
	CGO_ENABLED=1 go test ./... -race -count=1

cross:
	@mkdir -p bin
	@for arch in amd64 arm64; do for c in $(CMDS); do \
	  GOOS=linux GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o bin/linux-$$arch/$$c ./cmd/$$c || exit 1; \
	done; done
	@GOOS=linux GOARCH=amd64 go build ./... && GOOS=linux GOARCH=arm64 go build ./... && echo "cross-compile OK"

clean:
	rm -rf bin data
