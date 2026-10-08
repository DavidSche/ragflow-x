#!/usr/bin/env bash
# release-drill.sh — local rehearsal of .github/workflows/release.yml packaging.
#
# Mirrors the release workflow step by step so the release pipeline is verified
# BEFORE pushing a tag (the workflow itself has never run against a real tag):
#   1. frontend production build
#   2. cross-compiled Go binaries with the version stamped into main.version
#   3. tarballs / zip packaging + SHA256SUMS generation
#   4. source snapshot via git archive
#   5. SHA256SUMS verification (the release job's integrity gate)
#   6. release-notes rendering (the logic inlined in release.yml)
#
# Usage: scripts/release-drill.sh [version]
#   version defaults to v0.2.0-drill. Artifacts land in dist-drill/ (gitignored
#   via /dist-drill/); remove the directory afterwards to keep the tree clean.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)
VERSION="${1:-v0.2.0-drill}"
VERSION_NUM="${VERSION#v}"
DIST="$ROOT/dist-drill"

rm -rf "$DIST"
mkdir -p "$DIST"

echo "==> [1/6] Frontend production build"
(cd web && npm run build)

echo "==> [2/6] Cross-compiled Go binaries (version stamped: $VERSION)"
LDFLAGS="-s -w -X main.version=$VERSION"
mkdir -p "$DIST"
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-linux-amd64"       ./cmd/server/
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-linux-arm64"       ./cmd/server/
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-darwin-amd64"      ./cmd/server/
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-windows-amd64.exe" ./cmd/server/

echo "==> [3/6] Package binaries + frontend bundle + SHA256SUMS"
# Git Bash on Windows often lacks `zip`; fall back to Python's zipfile.
zip_one() {
  local src="$1" dst="$2"
  if command -v zip >/dev/null 2>&1; then
    zip -j -q "$dst" "$src"
  else
    python - "$src" "$dst" <<'PY'
import os, sys, zipfile
src, dst = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(dst, "w", zipfile.ZIP_DEFLATED) as z:
    z.write(src, arcname=os.path.basename(src))
PY
  fi
}
(cd "$DIST" || exit 1
  for f in ragflow-x-*; do
    case "$f" in
      *.exe) zip_one "$f" "ragflow-x-${VERSION_NUM}-$(echo "$f" | sed 's/ragflow-x-//').zip" ;;
      *)     chmod +x "$f"; tar czf "ragflow-x-${VERSION_NUM}-$(echo "$f" | sed 's/ragflow-x-//').tar.gz" "$f" ;;
    esac
  done
  tar czf "ragflow-x-${VERSION_NUM}-web.tar.gz" -C "$ROOT/web" dist/
  sha256sum *.tar.gz *.zip > SHA256SUMS
)

echo "==> [4/6] Source snapshot (git archive HEAD)"
git archive --format=tar.gz --prefix="ragflow-x-${VERSION_NUM}/" -o "$DIST/ragflow-x-${VERSION_NUM}-src.tar.gz" HEAD
(cd "$DIST" && sha256sum "ragflow-x-${VERSION_NUM}-src.tar.gz" >> SHA256SUMS)

echo "==> [5/6] Integrity gate: sha256sum -c SHA256SUMS"
(cd "$DIST" && sha256sum -c SHA256SUMS)

echo "==> [6/6] Render release notes (logic inlined in release.yml)"
TAG="$VERSION"
PREV="$(git describe --tags --abbrev=0 "${TAG}^" 2>/dev/null || true)"
{
  echo "## ragflow-x ${TAG}"
  echo
  if [ -n "$PREV" ]; then
    echo "自 **${PREV}** 以来的变更（共 $(git rev-list --count "${PREV}..${TAG}") 个提交）："
    echo
    git log --no-merges --format='- %s' "${PREV}..${TAG}"
  else
    echo "🎉 首次发版"
  fi
  echo
  echo "---"
  echo
  echo "### 安装"
  echo
  echo '```bash'
  echo "# Linux (amd64)"
  echo "tar xzf ragflow-x-${TAG}-linux-amd64.tar.gz"
  echo "./ragflow-x-linux-amd64"
  echo
  echo "# Docker"
  echo "docker pull ghcr.io/davidsche/ragflow-x:${TAG}"
  echo '```'
  echo
  echo "### 校验"
  echo
  echo '```bash'
  echo "sha256sum -c SHA256SUMS"
  echo '```'
} > "$DIST/release-notes.md"
cat "$DIST/release-notes.md"

echo
echo "==> Drill PASSED. Version stamp check:"
tar xzf "$DIST/ragflow-x-${VERSION_NUM}-linux-amd64.tar.gz" -C "$DIST" -O ragflow-x-linux-amd64 | grep -a -o "v${VERSION_NUM}" | head -1 || echo "(string not found in stripped binary; verify with: strings ragflow-x-linux-amd64 | grep main.version)"
echo "==> Artifacts in $DIST (gitignored). Clean up with: rm -rf dist-drill/"
