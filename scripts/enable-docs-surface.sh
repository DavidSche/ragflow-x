#!/usr/bin/env bash
# ① enable-docs-surface.sh turns the API documentation surface on for one deployment, and
# ② proves that this recipe and the product agree on the variable names.
#
# Why this file exists (doc/304 §7 "151 的文档面开关无法提交" / T310-10): the recipe that made
# `/docs` reachable on the 151 test host lived in `localenv/151-test/deploy.sh`, which is
# ignored by `.gitignore:151`. So the answer to "how do I turn the docs surface on" existed on
# exactly one machine. A runbook can rot silently; a script that reads the variables out of
# `internal/config/config.go` cannot — if the product renames `RGX_DOCS_ENABLED`, the selftest
# below goes red instead of quietly exporting a variable nobody reads again (E159 shape).
#
# Usage:
#   bash scripts/enable-docs-surface.sh <env-file> [--require-admin]
#   bash scripts/enable-docs-surface.sh --selftest
#
# The file is edited in place and is idempotent: running twice leaves one `RGX_DOCS_ENABLED=`
# line. `--require-admin` (the product default) keeps `/docs` behind RequireAdmin; omit it when
# the deployment authenticates at the edge and anonymous readers are intended.
set -uo pipefail

CONFIG="${DOCS_SURFACE_CONFIG:-internal/config/config.go}"

say() { printf '%s\n' "$*" >&2; }
die() { printf 'enable-docs-surface: %s\n' "$*" >&2; exit 2; }

# The variables this recipe may touch. Read from the product, not from memory: a recipe that
# exports `RGX_DOCS_ON` is indistinguishable from a working one until somebody visits the URL.
declared_vars() {
  local f="${DOCS_SURFACE_CONFIG:-$CONFIG}"
  [ -f "$f" ] || return 3
  grep -o 'RGX_DOCS_[A-Z_]*' "$f" | sort -u
}

set_line() { # file key value
  local f="$1" k="$2" v="$3"
  if grep -q "^${k}=" "$f" 2>/dev/null; then
    sed -i "s|^${k}=.*$|${k}=${v}|" "$f"
  else
    printf '%s=%s\n' "$k" "$v" >> "$f"
  fi
}

apply() { # file require_admin
  local f="$1" ra="${2:-}"
  [ -f "$f" ] || { say "enable-docs-surface: no such env file: $f"; return 3; }
  local src="${DOCS_SURFACE_CONFIG:-$CONFIG}"
  local declared; declared="$(declared_vars)" || { say "enable-docs-surface: cannot read $src"; return 3; }
  for v in RGX_DOCS_ENABLED RGX_DOCS_REQUIRE_ADMIN; do
    printf '%s\n' "$declared" | grep -qx "$v" \
      || { say "enable-docs-surface: the product does not read $v any more (see $CONFIG); this recipe is stale"; return 1; }
  done
  set_line "$f" RGX_DOCS_ENABLED true
  if [ -n "$ra" ]; then set_line "$f" RGX_DOCS_REQUIRE_ADMIN true; fi
  say "enable-docs-surface: $f now says RGX_DOCS_ENABLED=true${ra:+ RGX_DOCS_REQUIRE_ADMIN=true}"
  say "  next: recreate (not restart) the container — env is read at process start;"
  say "  then check /docs/openapi.json returns 200 application/json, and /docs/ returns 200 text/html."
  say "  A 200 text/html on /docs/openapi.json means the route is missing and r.NoRoute's SPA shell"
  say "  answered instead — look at Content-Type, not at the status code (E159)."
  return 0
}

selftest() {
  local tmp; tmp="$(mktemp -d)" || die "no temp dir"
  trap 'rm -rf "$tmp"' RETURN
  local fails=0
  ok() { if [ "$1" = 0 ]; then echo "ok      $2"; else echo "FAIL    $2"; fails=$((fails + 1)); fi; }

  # ① the happy path writes exactly one line per variable
  printf 'PORT=1\n' > "$tmp/env1"
  apply "$tmp/env1" >/dev/null 2>&1
  ok $? "写入一个 env 文件"
  [ "$(grep -c '^RGX_DOCS_ENABLED=true$' "$tmp/env1")" = 1 ]
  ok $? "RGX_DOCS_ENABLED=true 恰好一行"

  # ② idempotent: a second run must not append a second line (an append-only recipe grows one
  #    line per deploy, and the last one wins by accident)
  apply "$tmp/env1" >/dev/null 2>&1
  [ "$(grep -c '^RGX_DOCS_ENABLED' "$tmp/env1")" = 1 ]
  ok $? "第二次运行不新增行（幂等）"

  # ③ it rewrites a previous value instead of leaving two disagreeing lines
  printf 'RGX_DOCS_ENABLED=false\n' > "$tmp/env2"
  apply "$tmp/env2" >/dev/null 2>&1
  [ "$(grep -c '^RGX_DOCS_ENABLED' "$tmp/env2")" = 1 ]
  ok $? "旧值被改写，不留两行"

  # ④ a variable the product no longer reads is refused, not exported into the void
  printf 'X=1\n' > "$tmp/env3"
  printf 'setBool(&cfg.Docs.Enabled, "RGX_DOCS_ENABLED")\n' > "$tmp/config-stale.go"
  DOCS_SURFACE_CONFIG="$tmp/config-stale.go" apply "$tmp/env3" >/dev/null 2>&1
  [ $? = 1 ]
  ok $? "产品不再读取的变量 → 拒绝（这份配方过期了）"

  # ⑤ a missing env file is exit 3 ("could not look"), not a success
  apply "$tmp/nope" >/dev/null 2>&1
  [ $? = 3 ]
  ok $? "env 文件不存在 → exit 3（看不出来不是通过）"

  if [ "$fails" = 0 ]; then echo "enable-docs-surface --selftest: PASS (5/5)"; return 0; fi
  echo "enable-docs-surface --selftest: FAIL ($fails)"; return 1
}

case "${1:-}" in
  --selftest) selftest; exit $? ;;
  ""|-h|--help) sed -n '2,30p' "$0"; exit 2 ;;
esac

target="$1"; shift
require_admin=""
while [ $# -gt 0 ]; do
  case "$1" in
    --require-admin) require_admin=1; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done
apply "$target" "$require_admin"
