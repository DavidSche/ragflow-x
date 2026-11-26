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

# go:embed cannot reach outside internal/webfs: stage the built web assets
# there, same as the release.yml "Stage frontend assets for embedding" step.
rm -rf internal/webfs/dist
mkdir -p internal/webfs/dist
cp -r web/dist/. internal/webfs/dist/

echo "==> [2/6] Cross-compiled Go binaries (version stamped + webfs_embed): $VERSION"
LDFLAGS="-s -w -X main.version=$VERSION"
mkdir -p "$DIST"
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags=webfs_embed -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-linux-amd64"       ./cmd/server/
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -tags=webfs_embed -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-linux-arm64"       ./cmd/server/
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -tags=webfs_embed -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-darwin-amd64"      ./cmd/server/
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags=webfs_embed -ldflags="$LDFLAGS" -o "$DIST/ragflow-x-windows-amd64.exe" ./cmd/server/

echo "==> [3/6] Package binaries (standard layout) + frontend bundle + SHA256SUMS"
# Shared layout: binary at the root + config/docker/README companions,
# mirroring the release.yml "Package release artifacts" step.
mkdir -p "$DIST/bundle/config" "$DIST/bundle/docker"
cp config/config.example.yaml "$DIST/bundle/config/"
cp docker/docker-compose.yml docker/.env.example docker/ragflow-x.yaml "$DIST/bundle/docker/"
cp README.md "$DIST/bundle/"
# doc/310 §18 Phase 3 / doc/304 §4 E163: the package carries the contract the binaries
# compiled in, named for the version. Staged from the worktree; verify() below proves the
# worktree copy still equals the index (a dirty tree would otherwise ship an uncommitted
# contract) and that the bytes occur verbatim inside every shipped binary.
bash "$ROOT/scripts/release/spec-artifact.sh" stage "$VERSION" "$DIST/bundle"
# Git Bash on Windows often lacks `zip`; fall back to Python's zipfile.
# Argument is a directory; it is packed with paths relative to itself.
zip_one() {
  local src="$1" dst="$2"
  if command -v zip >/dev/null 2>&1; then
    ( cd "$src" && zip -qr "$OLDPWD/$dst" . )
  else
    python - "$src" "$dst" <<'PY'
import os, sys, zipfile
src, dst = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(dst, "w", zipfile.ZIP_DEFLATED) as z:
    for base, _dirs, files in os.walk(src):
        for name in files:
            full = os.path.join(base, name)
            z.write(full, os.path.relpath(full, src))
PY
  fi
}
(cd "$DIST" || exit 1
  # ② == ③ for every platform binary, before the binaries are moved into the stage dirs:
  # Go's //go:embed stores the spec raw, so this is an offline substring check, not a boot.
  for b in ragflow-x-linux-* ragflow-x-darwin-* ragflow-x-windows-*; do
    [ -f "$b" ] || continue
    bash "$ROOT/scripts/release/spec-artifact.sh" verify "$VERSION" "$DIST/bundle" "$DIST/$b" \
      || { echo "DRILL FAILED: $b does not carry the staged contract" >&2; exit 1; }
  done
  for f in ragflow-x-linux-* ragflow-x-darwin-* ragflow-x-windows-*; do
    [ -f "$f" ] || continue
    rm -rf stage; cp -r bundle stage
    case "$f" in
      *.exe)
        mv "$f" stage/ragflow-x.exe
        ( cd stage && zip_one . "../ragflow-x-${VERSION_NUM}-windows-amd64.zip" )
        ;;
      *)
        chmod +x "$f"
        platform="$(echo "$f" | sed 's/ragflow-x-//')"
        mv "$f" "stage/ragflow-x"
        tar czf "ragflow-x-${VERSION_NUM}-${platform}.tar.gz" -C stage .
        ;;
    esac
  done
  tar czf "ragflow-x-${VERSION_NUM}-web.tar.gz" -C "$ROOT/web" dist/
  # Standalone assets: a client should not have to untar a binary to read the contract.
  cp "bundle/api/openapi-${VERSION_NUM}.json" "bundle/api/openapi-${VERSION_NUM}.yaml" .
  # The human page rides along too (doc/304 §4 E164): it was rendered by `stage` from the
  # same spec, and spec-artifact.sh verify already proved it equals a fresh render of the
  # index. Here it only has to survive the copy and the checksum manifest.
  cp "bundle/api-reference-${VERSION_NUM}.html" .
  sha256sum *.tar.gz *.zip openapi-*.json openapi-*.yaml api-reference-*.html > SHA256SUMS

  # Layout guard (mirrors the release.yml packaging check): every platform
  # package must carry the binary + config + docker + README companions,
  # and the versioned contract (doc/310 §18 Phase 3 / T310-04).
  for pkgname in ragflow-x-*.tar.gz ragflow-x-*.zip; do
    case "$pkgname" in
      ragflow-x-${VERSION_NUM}-src.tar.gz|ragflow-x-${VERSION_NUM}-web.tar.gz) continue ;;
    esac
    listing=$(tar tzf "$pkgname" 2>/dev/null || unzip -Z1 "$pkgname")
    for required in "config/config.example.yaml" "docker/docker-compose.yml" "README.md" \
                    "api/openapi-${VERSION_NUM}.json" "api/openapi-${VERSION_NUM}.yaml" \
                    "api-reference-${VERSION_NUM}.html"; do
      echo "$listing" | grep -Eq "(^|/)${required}$" || {
        echo "DRILL FAILED: $pkgname is missing $required" >&2; exit 1;
      }
    done
    # ② after tar/zip == ② before: the packaging step must not rewrite or drop the contract.
    case "$pkgname" in
      *.zip) unzip -p "$pkgname" "api/openapi-${VERSION_NUM}.json" | cmp -s - "bundle/api/openapi-${VERSION_NUM}.json" ;;
      *)     tar xzf "$pkgname" -O "./api/openapi-${VERSION_NUM}.json" | cmp -s - "bundle/api/openapi-${VERSION_NUM}.json" ;;
    esac || { echo "DRILL FAILED: $pkgname carries a different contract than the bundle" >&2; exit 1; }
    # Same for the reading view: an unpacked page that differs from the staged one is a page
    # that was generated from something else.
    case "$pkgname" in
      *.zip) unzip -p "$pkgname" "api-reference-${VERSION_NUM}.html" | cmp -s - "bundle/api-reference-${VERSION_NUM}.html" ;;
      *)     tar xzf "$pkgname" -O "./api-reference-${VERSION_NUM}.html" | cmp -s - "bundle/api-reference-${VERSION_NUM}.html" ;;
    esac || { echo "DRILL FAILED: $pkgname carries a different doc page than the bundle" >&2; exit 1; }
  done
  echo "==> Layout guard passed: all platform packages carry config/docker/README + the versioned OpenAPI contract + its reading view"
)

echo "==> [4/6] Source snapshot (git archive HEAD)"
git archive --format=tar.gz --prefix="ragflow-x-${VERSION_NUM}/" -o "$DIST/ragflow-x-${VERSION_NUM}-src.tar.gz" HEAD
(cd "$DIST" && sha256sum "ragflow-x-${VERSION_NUM}-src.tar.gz" >> SHA256SUMS)

echo "==> [5/6] Integrity gate: sha256sum -c SHA256SUMS"
(cd "$DIST" && sha256sum -c SHA256SUMS)

echo "==> [6/6] Render release notes (logic inlined in release.yml)"
TAG="$VERSION"
PREV="$(git describe --tags --abbrev=0 "${TAG}^" 2>/dev/null || true)"
HIGHLIGHTS=".github/release-notes/${TAG}.md"
{
  echo "## ragflow-x ${TAG}"
  echo
  if [ -f "$HIGHLIGHTS" ]; then
    cat "$HIGHLIGHTS"
    echo
    echo "---"
    echo
  fi
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
echo "# Linux (amd64) — 二进制已嵌入前端 UI，包内含配置样例"
echo "tar xzf ragflow-x-${TAG}-linux-amd64.tar.gz"
echo "cp config/config.example.yaml config/config.yaml && ./ragflow-x"
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
tar xzf "$DIST/ragflow-x-${VERSION_NUM}-linux-amd64.tar.gz" -C "$DIST" -O ./ragflow-x | grep -a -o "v${VERSION_NUM}" | head -1 || echo "(string not found in stripped binary; verify with: strings ragflow-x-linux-amd64 | grep main.version)"
echo "==> Artifacts in $DIST (gitignored). Clean up with: rm -rf dist-drill/"
