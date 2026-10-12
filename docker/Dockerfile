# syntax=docker/dockerfile:1
# RAGFlow-X API server image (control plane + gateway).

# ---- build stage ----
FROM golang:1.25-alpine AS build
WORKDIR /src

# Release version stamped into the binary (set by release.yml / docker build --build-arg).
ARG VERSION=dev

# Cache module downloads first so dependency layers are reused.
COPY go.mod go.sum ./
RUN go mod download

# Frontend build stage: produces the Vite production bundle embedded into
# the Go binary (go:embed via internal/webfs, tag webfs_embed).
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

COPY . .
# Stage the web build where internal/webfs can embed it (go:embed cannot
# reach outside the package directory), then build with webfs_embed so the
# image serves API + UI from the single binary.
COPY --from=web /web/dist ./internal/webfs/dist
# CGO disabled + linux -> fully static binary, runs in the slim alpine runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -tags=webfs_embed \
    -ldflags="-s -w -X main.version=${VERSION}" -o /out/ragflow-x ./cmd/server

# ---- runtime stage ----
FROM alpine:3.21

# tzdata for correct UTC/local time handling; ca-certificates for outbound
# TLS (RAGFlow / provider calls).
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=build /out/ragflow-x /usr/local/bin/ragflow-x
# Shipped as a reference; real configs are mounted read-only in compose.
COPY config/config.example.yaml /app/config.example.yaml

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

ENTRYPOINT ["ragflow-x"]
CMD ["-config", "/app/config/config.yaml"]
