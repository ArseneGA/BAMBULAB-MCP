# syntax=docker/dockerfile:1.7
#
# Atelier Bambu Lab (bambud) : image tout-en-un.
#   - interface web + vue MCP App construites (Node), binaire Go statique ;
#   - Bambu Studio (AppImage officiel, version figée, SHA-256 vérifié, extrait au build : pas de FUSE) ;
#   - uv + worker CAO (CadQuery) pré-synchronisé ;
#   - utilisateur non root, volume /data, healthcheck.
#
# Construire :  docker build -t atelier-bambu .
# Voir docs/QUICKSTART.md. Bambu Studio est distribué par Bambu Lab sous licence AGPL-3.0 ; il est téléchargé
# depuis la page officielle des versions au moment du build (rien n'est publié par ce dépôt).
# Sans Studio dans l'image (build plus léger, découpe désactivée ou Studio monté depuis l'hôte) :
#   docker build --build-arg WITH_STUDIO=0 -t atelier-bambu:no-studio .

ARG NODE_VERSION=22
ARG GO_VERSION=1.27
ARG UBUNTU_VERSION=24.04
ARG UV_VERSION=0.9.30

FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uvimg

# ---------------------------------------------------------------- 1. interface web + vue MCP App
FROM node:${NODE_VERSION}-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# embed.mjs écrit dans ../internal/webui/assets et ../internal/mcpserver/ui/views
RUN mkdir -p /src/internal/webui/assets /src/internal/mcpserver/ui/views && npm run build

# ---------------------------------------------------------------- 2. binaires Go (statiques, sans cgo)
FROM golang:${GO_VERSION}-bookworm AS gobuild
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/assets/ internal/webui/assets/
COPY --from=web /src/internal/mcpserver/ui/views/ internal/mcpserver/ui/views/
ARG VERSION=docker
RUN go build -trimpath -ldflags "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=${VERSION}" -o /out/bambud ./cmd/bambud \
 && go build -trimpath -ldflags "-s -w -X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=${VERSION}" -o /out/bambu-mcp ./cmd/bambu-mcp

# ---------------------------------------------------------------- 3. Bambu Studio (AppImage extrait)
FROM ubuntu:${UBUNTU_VERSION} AS studio
ARG WITH_STUDIO=1
# Version figée (v02.09.01.70, build Ubuntu 24.04) : https://github.com/bambulab/BambuStudio/releases/tag/v02.09.01.70
ARG STUDIO_URL=https://github.com/bambulab/BambuStudio/releases/download/v02.09.01.70/BambuStudio_ubu24-v02.09.01.70-20261103183404.AppImage
ARG STUDIO_SHA256=668dabb1be53c758189ed6bc89935285dea5e0af46216546b1bafd6882e9aff0
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /opt/bambustudio && \
    if [ "$WITH_STUDIO" = "1" ]; then \
      curl -fsSL --retry 3 -o /tmp/studio.AppImage "$STUDIO_URL" && \
      echo "${STUDIO_SHA256}  /tmp/studio.AppImage" | sha256sum -c - && \
      chmod +x /tmp/studio.AppImage && cd /tmp && ./studio.AppImage --appimage-extract >/dev/null && \
      cp -a /tmp/squashfs-root/. /opt/bambustudio/ && rm -rf /tmp/squashfs-root /tmp/studio.AppImage ; \
    fi

# ---------------------------------------------------------------- 4. exécution
FROM ubuntu:${UBUNTU_VERSION} AS runtime

ARG WITH_STUDIO=1
ENV DEBIAN_FRONTEND=noninteractive
# Bibliothèques de Bambu Studio (lancé sans écran) et de CadQuery (OCP : GL/X11), curl pour le healthcheck.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl tini \
      libgtk-3-0t64 libglu1-mesa libgl1 libegl1 libosmesa6 libsecret-1-0 libdbus-1-3 libxkbcommon0 \
      libwebkit2gtk-4.1-0 libsoup-3.0-0 libxt6t64 libsm6 libxext6 libxrender1 libx11-6 libglib2.0-0t64 \
      fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*
COPY --from=uvimg /uv /usr/local/bin/uv

# Utilisateur non root ; tout ce qui est inscriptible vit sous /data (volume).
RUN useradd --uid 10001 --user-group --home-dir /data/home --no-create-home --shell /usr/sbin/nologin atelier \
 && mkdir -p /data /opt/atelier /opt/venv /opt/python && chown atelier:atelier /data

COPY --from=gobuild /out/bambud /out/bambu-mcp /usr/local/bin/
COPY --from=studio /opt/bambustudio /opt/bambustudio
COPY workers/geometry /opt/atelier/workers/geometry

# Worker CAO : Python géré par uv et CadQuery installés au build (le conteneur n'a besoin d'aucun accès réseau ensuite).
ENV UV_PYTHON_INSTALL_DIR=/opt/python UV_PROJECT_ENVIRONMENT=/opt/venv UV_FROZEN=1 UV_NO_DEV=1 UV_LINK_MODE=copy
RUN uv sync --frozen --no-dev --extra cad --project /opt/atelier/workers/geometry && chmod -R a+rX /opt/venv /opt/python /opt/atelier

COPY deploy/docker/entrypoint.sh /usr/local/bin/atelier-entrypoint
RUN chmod 0755 /usr/local/bin/atelier-entrypoint

ENV HOME=/data/home \
    TMPDIR=/data/tmp \
    BAMBU_DATA_DIR=/data/atelier \
    BAMBU_ADDR=0.0.0.0:7878 \
    BAMBU_IMPORT_ROOTS=/data/import \
    BAMBU_UV_PATH=/usr/local/bin/uv \
    BAMBU_GEOMETRY_DIR=/opt/atelier/workers/geometry \
    BAMBU_STUDIO_WRAPPER=none \
    BAMBU_REQUIRE_TOKEN=1

USER atelier
WORKDIR /data
VOLUME /data
EXPOSE 7878
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7878/health || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/atelier-entrypoint"]
CMD ["bambud"]
