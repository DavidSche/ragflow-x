# syntax=docker/dockerfile:1
# RAGFlow-X API server image (control plane + gateway + embedded web UI).

# ---- frontend stage (first: no dependency on the Go stages) ----
# Produces the Vite production bundle that gets embedded into the Go binary
# via internal/webfs (go:embed) with the webfs_embed build tag.
# Both build stages run on the builder's own platform ($BUILDPLATFORM) and only
# the Go compile is retargeted: `npm ci` under QEMU emulation is the difference
# between a 3-minute and a 30-minute arm64 leg.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
# `npm ci` runs the `postinstall: patch-package` hook, so web/patches must be
# in place *before* the install. With the patches absent the hook finds nothing
# to apply and still exits 0, leaving query-string v10 unpatched (it is
# default-export only after the v9 ESM rewrite) — the failure then surfaces
# three steps later as `[MISSING_EXPORT] "stringify"/"parse" is not exported by
# node_modules/query-string/index.js`, thrown from ra-core.
COPY web/patches ./patches
RUN test -n "$(ls patches/*.patch 2>/dev/null)" || { echo 'web/patches is missing before npm ci: patch-package would skip the patch silently' >&2; exit 1; }
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- build stage ----
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src

# Release version stamped into the binary (set by release.yml / docker build --build-arg).
ARG VERSION=dev

# Target platform, filled in by buildx for multi-arch builds and defaulted for a
# plain `docker build`.
ARG TARGETOS
ARG TARGETARCH

# Module proxy. Defaults to the upstream one, which is what CI uses; networks
# that cannot reach proxy.golang.org (mainland China, some corp egress) build
# with `--build-arg GOPROXY=https://goproxy.cn,direct` instead of failing on
# `go mod download`.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# Cache module downloads first so dependency layers are reused.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Stage the web build where internal/webfs can embed it (go:embed cannot
# reach outside the package directory), then build with webfs_embed so the
# image serves API + UI from the single binary.
COPY --from=web /web/dist ./internal/webfs/dist
# CGO disabled + linux -> fully static binary, runs in the slim alpine runtime.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -tags=webfs_embed \
    -ldflags="-s -w -X main.version=${VERSION}" -o /out/ragflow-x ./cmd/server

# ---- runtime stage ----
FROM alpine:3.21

# tzdata for correct UTC/local time handling; ca-certificates for outbound
# TLS (RAGFlow / provider calls).
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=build /out/ragflow-x /usr/local/bin/ragflow-x
# Entrypoint decides whether a config was mounted. It is deliberately NOT the
# archive launcher (scripts/release/start.sh): inside a container the binary must
# stay PID 1 for signal handling, port-in-use refusal would turn a restart into a
# crash loop, and pid/port files have no meaning. Archives and images are two
# different launch contracts.
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
# Shipped as a reference; real configs are mounted read-only in compose.
COPY config/config.example.yaml /app/config.example.yaml
# Where the built-in "both" default puts its rotating file. Compose pins
# `output: stdout` (the container log transport); a bare `docker run` keeps the
# console stream AND a file here, so both contracts work out of the box.
RUN mkdir -p /app/logs

ENV RGX_MODE=release \
    RGX_DATA_DIR=/app/data \
    RGX_SETUP_SECRETS_FILE=/app/secrets/runtime.secrets.json \
    RGX_SETUP_RSA_KEY=/app/secrets/rsa_setup.pem \
    RGX_LOG_FORMAT=json

EXPOSE 9191

# Setup first-run files and state are persisted through named volumes.
VOLUME ["/app/data", "/app/secrets", "/app/config"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=25s --retries=3 \
  CMD wget -qO- http://127.0.0.1:9191/api/v1/readyz > /dev/null 2>&1 || exit 1

# No default CMD: the entrypoint adds `-config /app/config/config.yaml` only when
# that file exists (compose mounts it; a bare `docker run` does not) and passes
# any explicit arguments straight through.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD []
