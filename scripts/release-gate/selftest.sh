#!/usr/bin/env bash
# selftest.sh — the gate's enforcement tests itself.
#
# `scripts/release-gate.sh` writes a verdict; `scripts/git-hooks/pre-push` reads
# it back. That hook is the only place a release is actually *stopped*, so it is
# the one piece of this system that must never rot silently: a hook that always
# exits 0 looks exactly like a hook that always passes.
#
# Every case below builds a throwaway report, feeds a push line to the hook on
# stdin exactly as git would, and asserts the exit code. It runs in a temp
# directory (never touches the real tmp/ reports) and needs no network.
#
#   scripts/release-gate/selftest.sh        # exit 0 = the enforcement holds
set -uo pipefail

ROOT=$(cd -- "$(dirname -- "$0")/../.." && pwd)
HOOK="$ROOT/scripts/git-hooks/pre-push"
WORK="$ROOT/tmp/gate-selftest-$$"
ZERO=0000000000000000000000000000000000000000
SHA=1111111111111111111111111111111111111111
TAG=v9.9.9

PY=""
for cand in python3 python py; do
  p=$(command -v "$cand" 2>/dev/null) || continue
  if "$p" -c 'import sys' >/dev/null 2>&1; then PY="$p"; break; fi
done
if [ -z "$PY" ]; then echo "selftest: python is required" >&2; exit 2; fi
if [ ! -f "$HOOK" ]; then echo "selftest: $HOOK is missing" >&2; exit 2; fi

PASS=0
FAIL=0
CASE_DIRS=()

cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

# make_report <dir> <verdict> <releasable> <rev> <version> <finished_at> [check_status]
#
# `check_status` (optional) adds one required static row with that status. It exists so
# the hook can be asked about a status the gate itself only writes under `--ci` (N/A):
# a report is a JSON file, and the question "what if a partial run were dressed up as a
# release run" deserves an answer that is a test rather than an argument.
make_report() {
  local dir="$1" verdict="$2" releasable="$3" rev="$4" version="$5" finished="$6" check="${7:-}"
  mkdir -p "$dir/tmp/release-gate-selftest"
  "$PY" - "$dir/tmp/release-gate-selftest/summary.json" "$verdict" "$releasable" "$rev" "$version" "$finished" "$check" <<'PY'
import json, sys
path, verdict, releasable, rev, version, finished, check = sys.argv[1:8]
doc = {
    "schema": "rgx.release-gate.v1",
    "verdict": verdict,
    "releasable": releasable == "true",
    "tier": "all", "profile": "release",
    "version": version,
    "finished_at": finished,
    "git": {"rev": rev, "short": rev[:12], "branch": "main", "tag": version, "dirty": False, "dirty_files": 0},
    "artifacts": {"frontend_asset": "index-selftest.js", "served_asset": "index-selftest.js"},
    "checks": ([{"id": "s18.docs-ledger", "tier": "static", "required": True,
                 "status": check, "seconds": 0, "note": "selftest row"}] if check else []),
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(doc, fh)
PY
}

# expect <name> <expected 0|1> <dir-with-fake-tmp> <ref>
expect() {
  local name="$1" want="$2" dir="$3" ref="$4"
  local out rc
  out=$(cd "$dir" && printf '%s %s %s %s\n' "$ref" "$SHA" "$ref" "$ZERO" | sh "$HOOK" 2>&1)
  rc=$?
  local got=0
  [ "$rc" -ne 0 ] && got=1
  if [ "$got" = "$want" ]; then
    PASS=$((PASS + 1))
    printf 'PASS  %-46s (hook exit %s)\n' "$name" "$rc"
  else
    FAIL=$((FAIL + 1))
    printf 'FAIL  %-46s (hook exit %s, expected %s)\n' "$name" "$rc" "$([ "$want" = 1 ] && echo nonzero || echo zero)"
    printf '%s\n' "$out" | sed 's/^/      /'
  fi
}

NOW=$("$PY" -c 'import datetime;print(datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))')
STALE=$("$PY" -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(hours=100)).strftime("%Y-%m-%dT%H:%M:%SZ"))')

# 1. nothing has been run: a tag push must not leave.
d="$WORK/no-report"; mkdir -p "$d"
expect "no gate report: tag push refused" 1 "$d" "refs/tags/$TAG"

# 2. the gate ran and failed.
d="$WORK/verdict-fail"; make_report "$d" FAIL false "$SHA" "$TAG" "$NOW"
expect "verdict=FAIL: tag push refused" 1 "$d" "refs/tags/$TAG"

# 3. a green run, but for a different commit (the classic "verified A, shipped B").
d="$WORK/wrong-rev"; make_report "$d" PASS true "2222222222222222222222222222222222222222" "$TAG" "$NOW"
expect "report is for another commit: refused" 1 "$d" "refs/tags/$TAG"

# 4. a green run for another version.
d="$WORK/wrong-version"; make_report "$d" PASS true "$SHA" "v0.0.1" "$NOW"
expect "report is for another version: refused" 1 "$d" "refs/tags/$TAG"

# 5. a green run that is no longer allowed to speak for this tag.
d="$WORK/stale"; make_report "$d" PASS true "$SHA" "$TAG" "$STALE"
expect "report older than the max age: refused" 1 "$d" "refs/tags/$TAG"

# 6. a green run that is not releasable (live tier skipped, served != built).
d="$WORK/not-releasable"; make_report "$d" PASS false "$SHA" "$TAG" "$NOW"
expect "releasable=false: refused" 1 "$d" "refs/tags/$TAG"

# 7. deleting a release tag on the remote is a release action too.
d="$WORK/delete"; make_report "$d" PASS true "$SHA" "$TAG" "$NOW"
out=$(cd "$d" && printf '%s %s %s %s\n' "(delete)" "$ZERO" "refs/tags/$TAG" "$SHA" | sh "$HOOK" 2>&1)
if [ $? -ne 0 ]; then PASS=$((PASS + 1)); echo "PASS  tag deletion refused"; else FAIL=$((FAIL + 1)); echo "FAIL  tag deletion allowed"; echo "$out" | sed 's/^/      /'; fi

# 8. the honest case: a matching, fresh, releasable report lets the tag through.
d="$WORK/good"; make_report "$d" PASS true "$SHA" "$TAG" "$NOW"
expect "matching fresh releasable report: allowed" 0 "$d" "refs/tags/$TAG"

# 9. a report whose required row is N/A (the status a `--ci` run uses when the tree
# carries no doc/) must not become a release report by being renamed, hand-edited, or
# carried from CI: the hook requires every required row to say PASS.
d="$WORK/na-row"; make_report "$d" "PASS (ci: 1 not applicable)" true "$SHA" "$TAG" "$NOW" "N/A"
expect "a required row is N/A: refused" 1 "$d" "refs/tags/$TAG"

# 10. ordinary branches are never gated — this protects releases, not work.
d="$WORK/branch"; mkdir -p "$d"
expect "branch push is not gated" 0 "$d" "refs/heads/feature/x"

echo
echo "selftest: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
