#!/usr/bin/env bash
# verify-and-clean.sh -- web verification (typecheck + tests + build) then
# garbage-collect reproducible verification artifacts under tmp/:.
#
# Rationale: local verification (and the probes it runs) writes disposable
# artifacts -- diagnostic scripts, baseline snapshots, vitest reports -- that
# can regrow to hundreds of MB and have already once filled the E: drive
# (ENOSPC truncated a doc/ file). This script is the single entry point to run
# the full web gate AND remove everything it produced, so the drive returns to
# its pre-run size instead of growing without bound.
#
# Cleanup is intentionally whitelisted: it only removes artifacts that the
# committed scripts under scripts/ can regenerate (perf-probe.py,
# variance-watch.py, d1-anchor-patch.py) plus one-off diagnostic scripts and
# the vitest JSON report. Evidence-like dirs (tmp/arm-compare, tmp/ci_repro,
# tmp/fresh29_run) are never removed by this script; they are only reported so
# a human can decide.
#
# Usage:
#   bash scripts/verify-and-clean.sh                # full gate + cleanup
#   bash scripts/verify-and-clean.sh --skip-build   # gate without vite build
#   bash scripts/verify-and-clean.sh --only-clean   # cleanup only, no gate
#   bash scripts/verify-and-clean.sh --keep-artifacts  # gate only, no cleanup
#
# Exit code: 0 = green; 1 = a gate step failed (cleanup still runs); 2 = usage
# error. Non-zero propagates so it can gate CI / merge.
set -u

cd "$(dirname "$0")/.." || exit 2
ROOT="$(pwd)"
WEB="$ROOT/web"

SKIP_BUILD=0
ONLY_CLEAN=0
KEEP_ARTIFACTS=0
for arg in "$@"; do
  case "$arg" in
    --skip-build) SKIP_BUILD=1 ;;
    --only-clean) ONLY_CLEAN=1 ;;
    --keep-artifacts) KEEP_ARTIFACTS=1 ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "verify-and-clean: unknown flag: $arg (see header for usage)"; exit 2 ;;
  esac
done

# --- disk free (NTFS under MINGW: `df` is unreliable, use python) ---
disk_free_gb() {
  # convert /e/... to E:/... for Windows python
  local win
  win="$(cd "$ROOT" && pwd -W 2>/dev/null)"
  win="${win:-$ROOT}"
  PYTHONIOENCODING=utf-8 python -c "import shutil;print(round(shutil.disk_usage(r'$win').free/2**30,1))" 2>/dev/null || echo "?"
}
echo "== verify-and-clean =="
echo "  root: $ROOT"
echo "  E: free before: $(disk_free_gb) GB"

FAIL=0
step() { echo; echo "== $1 =="; }

if [ "$ONLY_CLEAN" -eq 0 ]; then
  if [ ! -d "$WEB/node_modules" ]; then
    echo "verify-and-clean: web/node_modules missing -- run \`cd web && npm ci\` first"
    exit 2
  fi
  step "typecheck (tsc -b)"
  ( cd "$WEB" && PYTHONIOENCODING=utf-8 npx tsc -b --pretty false ) || FAIL=1

  step "vitest (full suite)"
  ( cd "$WEB" && PYTHONIOENCODING=utf-8 npx vitest run ) || FAIL=1

  if [ "$SKIP_BUILD" -eq 0 ]; then
    step "build (vite)"
    ( cd "$WEB" && PYTHONIOENCODING=utf-8 npm run build ) || FAIL=1
  else
    echo "  (skipped per --skip-build)"
  fi
fi

if [ "$KEEP_ARTIFACTS" -eq 0 ]; then
  step "cleanup (reproducible verification artifacts)"
  # Whitelist only. Each entry is either a committed script's output or a
  # one-off diagnostic; all regenerate trivially and their key numbers are
  # already recorded in doc/266.
  rm -rf "$ROOT/tmp/perf"
  rm -f  "$ROOT/tmp/variance-watch.log"
  rm -f  "$ROOT/tmp/d1-anchor-patch.json"
  rm -f  "$ROOT/tmp/verify_ab.py" "$ROOT/tmp/verify_local_improvement.py"
  rm -f  "$ROOT/tmp/verify-"*.py
  rm -rf "$ROOT/tmp/verify-20"*   # expired verification-run dirs
  rm -f  "$ROOT/test-results/vitest.json"
  echo "  removed: tmp/perf, variance-watch.log, d1-anchor-patch.json, verify_*.py, verify-20*/ dirs, test-results/vitest.json"

  echo "  left for human decision (never auto-removed):"
  du -s "$ROOT/tmp/arm-compare" "$ROOT/tmp/ci_repro" "$ROOT/tmp/fresh29_run" 2>/dev/null \
    | awk '{printf "    %6.1f MB  %s\n", $1/1024, $2}'
else
  echo "== cleanup =="
  echo "  (skipped per --keep-artifacts)"
fi

echo
echo "== result =="
echo "  E: free after: $(disk_free_gb) GB"
if [ "$FAIL" -eq 0 ]; then
  echo "  verify-and-clean: OK (gate green, artifacts cleaned)"
else
  echo "  verify-and-clean: FAILED (see logs above; cleanup still applied)"
fi
exit "$FAIL"
