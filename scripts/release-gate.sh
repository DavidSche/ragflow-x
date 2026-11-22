#!/usr/bin/env bash
# release-gate.sh — the one command that answers "may this tree become a release?"
#
# Why this exists
# ---------------
# Every regression that reached a user in this project so far passed every
# check we had at the time: the code compiled, the unit tests were green, and
# the bug lived in the seam nobody connected — a route missing from
# `accessRules` (403 in production), a canvas that saved but could never run,
# a rebuilt binary that was never actually served. Those are not "testing"
# problems, they are *release* problems: the checks that would have caught them
# existed, but nothing forced them to run before a tag.
#
# So the gate has three jobs:
#   1. run the tiers in a fixed order, so a failure is found as cheaply as
#      possible (static -> build -> live);
#   2. write a machine-readable verdict (`summary.json`) that a git hook reads
#      back, so "I ran it, honestly" is not the enforcement;
#   3. refuse to call a run releasable when a required check was skipped,
#      blocked, or run against a different artifact than the one being shipped.
#
# Tiers
# -----
#   static  code-level contracts: secret scan, formatting of touched files,
#           vet/build, RBAC route contracts, append-only migrations, the whole
#           backend test suite, frontend typecheck + unit tests, changelog and
#           release notes for the version. Runs anywhere (CI included).
#   build   the artifact: production frontend build, the packaging rehearsal
#           (`scripts/release-drill.sh`), and the fingerprint of what was built.
#   live    the deployment: readyz, "is the served bundle the one I just built",
#           then the live API/browser suites listed in
#           `scripts/release-gate/suites.tsv`.
#
# Statuses
# --------
#   PASS     the check ran and held
#   FAIL     the check ran and did not hold
#   SKIP     a required check was not run (deselected, or its preconditions
#            are absent) — the run is INCOMPLETE, never silently green
#   MISSING  a live suite script is not on this machine — also INCOMPLETE
#   N/A      the check needs an input this tree does not carry, and this is a
#            `--ci` run. `doc/` is gitignored by policy (.gitignore:91), so a
#            clean checkout has no 301/304/305, no specification/12 and no
#            labeling manual; five static checks compare code against those
#            documents and can only answer "not here". A check script says so
#            with exit 3, and the gate is the one place that decides what that
#            means: a `--ci` run records `N/A` and keeps the row visible; every
#            other run turns the same exit code into **FAIL**. CI cannot run the
#            live tier and therefore can never be `releasable`, so "CI green"
#            and "may be tagged" stay two different sentences. This is the only
#            status whose meaning depends on the profile, and it is written down
#            here rather than discovered in a log.
#
# Exit codes: 0 = PASS (and releasable when a version was given), 1 = FAIL,
# 2 = INCOMPLETE (something required did not run, or the report could not be
# written). These are the only three outcomes; there is no "warn".
#
# Usage
# -----
#   scripts/release-gate.sh --tier all --version v0.6.5     # what a release needs
#   scripts/release-gate.sh --tier static                   # pre-commit-ish loop
#   scripts/release-gate.sh --tier live --profile smoke     # fast live rehearsal
#   scripts/release-gate.sh --tier static --ci              # CI mode (no live)
#   scripts/release-gate.sh --list                          # show every check
#
# Live tier environment (never committed — this script holds no host or secret):
#   RGX_BASE                 platform base URL of the deployment under test
#   RGX_USER / RGX_PASSWORD  an admin account on that deployment
#   RGX_ENGINE               optional engine base URL (some suites read it)
#   RGX_GATE_DEPLOY_CMD      optional command that (re)deploys the artifact
#   RGX_GATE_LIVE_DIR        where the live suites live (default localenv/probes)
#   RGX_GATE_TMP_DIR         where the ad-hoc probes live (default tmp)
#   RGX_GATE_MAX_AGE_HOURS   how long a report stays valid for a tag (default 72)
#
# Enforcement lives in `scripts/git-hooks/pre-push`: a `v*` tag will not leave
# this machine without a PASS/A-passing report whose `git.rev` is the tagged
# commit, whose version matches the tag, and whose served fingerprint equals the
# built one. See `doc/environment/verification/46_*.md` for the full rationale.
set -uo pipefail

ROOT=$(cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT" || exit 2

GATE_SCHEMA="rgx.release-gate.v1"
GATE_VERSION="1.0.0"
STAMP=$(date +%Y%m%d-%H%M%S)

TIER="all"
PROFILE="release"
VERSION=""
BASE=""
LABEL=""
CI=0
OUT=""
ONLY=""
SKIP=""
LIST=0
ABORTED=0

usage() {
  sed -n '3,66p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tier) TIER="${2:-}"; shift 2 ;;
    --tier=*) TIER="${1#*=}"; shift ;;
    --profile) PROFILE="${2:-}"; shift 2 ;;
    --profile=*) PROFILE="${1#*=}"; shift ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --version=*) VERSION="${1#*=}"; shift ;;
    --base) BASE="${2:-}"; shift 2 ;;
    --base=*) BASE="${1#*=}"; shift ;;
    --label) LABEL="${2:-}"; shift 2 ;;
    --label=*) LABEL="${1#*=}"; shift ;;
    --out) OUT="${2:-}"; shift 2 ;;
    --out=*) OUT="${1#*=}"; shift ;;
    --only) ONLY="${2:-}"; shift 2 ;;
    --only=*) ONLY="${1#*=}"; shift ;;
    --skip) SKIP="${2:-}"; shift 2 ;;
    --skip=*) SKIP="${1#*=}"; shift ;;
    --ci) CI=1; shift ;;
    --list) LIST=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "release-gate: unknown argument: $1" >&2; usage 2 ;;
  esac
done

case "$TIER" in static|build|live|all) : ;; *) echo "release-gate: --tier must be static|build|live|all" >&2; exit 2 ;; esac
case "$PROFILE" in release|smoke) : ;; *) echo "release-gate: --profile must be release|smoke" >&2; exit 2 ;; esac
if [ "$CI" = "1" ]; then
  case "$TIER" in live) echo "release-gate: --ci cannot run the live tier (no deployment, no browsers)" >&2; exit 2 ;; esac
fi
if [ -n "$VERSION" ] && [ "${VERSION#v}" = "$VERSION" ]; then
  VERSION="v$VERSION"
fi

OUT="${OUT:-tmp/release-gate-$STAMP}"
mkdir -p "$OUT/checks" || { echo "release-gate: cannot create $OUT" >&2; exit 2; }

GATE_LIVE="${RGX_GATE_LIVE_DIR:-localenv/probes}"
GATE_TMP="${RGX_GATE_TMP_DIR:-tmp}"
export GATE_LIVE GATE_TMP

# `command -v python3` on Windows can resolve to the Store alias, which exits
# 49 and prints nothing; probe each candidate instead of trusting the name.
pick_python() {
  local c p
  for c in python3 python py; do
    p=$(command -v "$c" 2>/dev/null) || continue
    if "$p" -c 'import sys' >/dev/null 2>&1; then
      printf '%s\n' "$p"
      return 0
    fi
  done
  return 0
}
PYTHON=$(pick_python)

# ── records ──────────────────────────────────────────────────────────────
R_ID=(); R_TIER=(); R_REQ=(); R_STATUS=(); R_SECS=(); R_LOG=(); R_NOTE=()
RUNS=0

record() { # id tier required status seconds log note
  R_ID+=("$1"); R_TIER+=("$2"); R_REQ+=("$3"); R_STATUS+=("$4")
  R_SECS+=("$5"); R_LOG+=("$6"); R_NOTE+=("${7:-}")
}

note_ok() { printf '      %s\n' "$1"; }

tier_selected() {
  case "$TIER" in all) return 0 ;; *) [ "$TIER" = "$1" ] ;; esac
}

should_run() { # id
  local id="$1"
  if [ -n "$ONLY" ]; then
    case ",$ONLY," in *",$id,"*) : ;; *) return 1 ;; esac
  fi
  if [ -n "$SKIP" ]; then
    case ",$SKIP," in *",$id,"*) return 1 ;; esac
  fi
  return 0
}

# run_check <id> <tier> <required 1|0> <description> <command...>
run_check() {
  local id="$1" t="$2" req="$3" desc="$4"
  shift 4
  local status note="" log="" t0=$SECONDS rc=0
  if should_run "$id"; then
    log="$OUT/checks/$id.log"
    printf '$ %s\n\n' "$*" > "$log"
    printf '[run ] %-30s %s\n' "$id" "$desc"
    RUNS=$((RUNS + 1))
    if "$@" >> "$log" 2>&1; then
      status="PASS"
    else
      rc=$?
      # Exit 3 = "this tree cannot answer this question" (a document the check
      # compares against is not in the checkout). Only a `--ci` run is allowed
      # to record that as N/A; anywhere else it is a failure, because a run that
      # could push a tag must not be able to lose a check by losing its input.
      if [ "$rc" = "3" ] && [ "$CI" = "1" ]; then
        status="N/A"
        note="$desc — not applicable in this tree (the check needs a document that is not in the checkout; see the log for which one)"
      else
        status="FAIL"
        note="$desc"
      fi
    fi
    [ -n "$note" ] || note="$desc"
  else
    status="SKIP"
    req=0
    note="$desc — deselected (--only/--skip)"
  fi
  local secs=$((SECONDS - t0))
  record "$id" "$t" "$req" "$status" "$secs" "$log" "$note"
  printf '[%s] %-30s %3ss\n' "$status" "$id" "$secs"
  # N/A gets its tail shown too: "not applicable" without "which document" is a
  # status nobody can check.
  if [ "$status" = "FAIL" ] || [ "$status" = "N/A" ]; then
    echo "      ---- tail of $log ----"
    tail -n 25 "$log" | sed 's/^/      /'
  fi
}

# run_suite <id> <required> <script> <description> <command>
run_suite() {
  local id="$1" req="$2" script="$3" desc="$4" cmd="$5"
  if ! should_run "$id"; then
    record "$id" "live" "0" "SKIP" "0" "" "$desc — deselected (--only/--skip)"
    printf '[SKIP] %-30s %s\n' "$id" "$desc"
    return
  fi
  if [ ! -f "$script" ]; then
    record "$id" "live" "$req" "MISSING" "0" "" "$desc — suite script not found: $script"
    printf '[MISS] %-30s missing %s\n' "$id" "$script"
    return
  fi
  local ev="$OUT/evidence/$id"
  mkdir -p "$ev"
  local log="$OUT/checks/$id.log" t0=$SECONDS
  printf '$ %s\n\n' "$cmd" > "$log"
  printf '[run ] %-30s %s\n' "$id" "$desc"
  RUNS=$((RUNS + 1))
  local status="PASS"
  EVIDENCE_DIR="$ev" bash -c "$cmd" >> "$log" 2>&1 || status="FAIL"
  local secs=$((SECONDS - t0))
  record "$id" "live" "$req" "$status" "$secs" "$log" "$desc"
  printf '[%s] %-30s %3ss\n' "$status" "$id" "$secs"
  if [ "$status" = "FAIL" ]; then
    echo "      ---- tail of $log ----"
    tail -n 25 "$log" | sed 's/^/      /'
  fi
}

# ── git facts ────────────────────────────────────────────────────────────
GIT_REV=$(git rev-parse HEAD 2>/dev/null || echo "")
GIT_SHORT=$(git rev-parse --short HEAD 2>/dev/null || echo "")
GIT_BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
GIT_TAG=$(git describe --tags --exact-match 2>/dev/null || echo "")
DIRTY_FILES=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
GIT_DIRTY=0
[ "$DIRTY_FILES" != "0" ] && GIT_DIRTY=1
if [ -z "$BASE" ]; then
  BASE=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
fi

if [ "$LIST" = "1" ]; then
  cat <<'EOF'
static tier (always required)
  s01.secret-scan            scripts/secret-scan.sh --all
  s02.gofmt-touched          gofmt on *.go touched since --base
  s03.go-vet                go vet ./cmd/... ./internal/...
  s04.go-build              go build ./cmd/... ./internal/...
  s05.route-contracts       RBAC route registry + role lattice + permission matrix
  s06.migrations-append-only  no destructive DDL added since --base
  s07.go-test-all           go test ./cmd/... ./internal/... -count=1
  s08.fe-typecheck          cd web && npx tsc -b
  s09.fe-test               cd web && npx vitest run --maxWorkers=2
  s12.gate-selftest         scripts/release-gate/selftest.sh (the hook refuses what it should)
  s13.assurance-gold        scripts/release-gate/assurance.sh (doc/301 G-A1..A8: 12 rows with n + CP, 44117-44123 in 5 places)
  s14.assurance-quality     scripts/release-gate/assurance-quality.sh (doc/301 §9.4: 5 self-checks, each able to fail, sentences verbatim in 301)
  s15.kc2-prereg            scripts/release-gate/kc2-prereg.sh (doc/301 §5.7: KC-2 thresholds frozen, bidirectional check, quotes verbatim in 301)
  s16.probes-selfcheck      scripts/release-gate/probes.sh (doc/304 §7 item 12: T02/T16 probes tracked, no credential stored, --offline contract)
  s17.adjudicate            scripts/release-gate/adjudicate.sh (doc/304 §7 item 25: the adjudication skeleton is derived, the manual documents it, 13 tests named)
  s18.docs-ledger           scripts/release-gate/docs-ledger.sh (doc/304 §7 items 2 and 3: §2/§3/§6/§7 shapes, 301 §14.J tail + derived footer, 305 §2 order)
  s10.changelog             CHANGELOG.md carries the --version section
  s11.release-notes         .github/release-notes/<version>.md (optional, reported)
build tier (required when selected)
  b01.fe-build              cd web && npm run build
  b02.packaging-drill       scripts/release-drill.sh <version-or-drill>
  b03.build-fingerprint     record the index-*.js of web/dist
  b04.webfs-not-committed   internal/webfs/dist/ stays gitignored
live tier (required for a releasable run)
  l00.deploy                RGX_GATE_DEPLOY_CMD (optional, reported)
  l01.readyz                GET /api/v1/readyz -> ready
  l02.served-fingerprint    served index-*.js == web/dist index-*.js
  + suites from scripts/release-gate/suites.tsv
EOF
  exit 0
fi

echo "release-gate v$GATE_VERSION"
echo "  tree      ${GIT_SHORT:-<none>}${GIT_DIRTY:+} (branch ${GIT_BRANCH:-?}, dirty files: $DIRTY_FILES)"
echo "  tier      $TIER   profile: $PROFILE${VERSION:+   version: $VERSION}"
echo "  base      ${BASE:-<no tag>}"
echo "  output    $OUT"
echo

# ── s00: a release run must start from a committed tree ──────────────────
if [ -n "$VERSION" ]; then
  if [ "$GIT_DIRTY" = "1" ]; then
    record "s00.clean-tree" "static" "1" "FAIL" "0" "" "release runs must start from a clean tree ($DIRTY_FILES files changed)"
    cat <<EOF
[s00] FAIL a release run ($VERSION) must start from a committed tree.
      $DIRTY_FILES path(s) differ from HEAD. Commit or stash, then rerun:
        git status --short
      (Dirty-tree runs are allowed without --version: they answer "does this
       tree work", not "may this commit be tagged".)
EOF
    ABORTED=1
  else
    record "s00.clean-tree" "static" "1" "PASS" "0" "" "clean tree"
    echo "[PASS] s00.clean-tree"
  fi
fi

if [ "$ABORTED" = "1" ]; then
  echo
  echo "release gate FAILED before running any check (clean-tree precondition)."
  exit 1
fi

# ── static tier ──────────────────────────────────────────────────────────
if tier_selected static; then
  run_check "s01.secret-scan" "static" 1 "no credential material or local-env fingerprints in the tree" \
    bash scripts/secret-scan.sh --all

  run_check "s02.gofmt-touched" "static" 1 "touched Go files are gofmt-clean (base: ${BASE:-none})" \
    bash -c '
      root=$(pwd)
      if [ -z "$1" ]; then echo "no base ref: cannot scope the formatting check"; exit 3; fi
      files=$(git diff --name-only "$1" -- "*.go" | while IFS= read -r f; do [ -f "$root/$f" ] && printf "%s\n" "$f"; done)
      [ -z "$files" ] && { echo "no Go files touched since $1"; exit 0; }
      out=$(printf "%s\n" "$files" | tr "\n" "\0" | xargs -0 gofmt -l 2>/dev/null)
      if [ -n "$out" ]; then echo "gofmt -l:"; echo "$out"; exit 1; fi
      echo "clean: $(printf "%s\n" "$files" | wc -l | tr -d " ") file(s)"
    ' _ "$BASE"

  run_check "s03.go-vet" "static" 1 "go vet (product packages)" go vet ./cmd/... ./internal/...
  run_check "s04.go-build" "static" 1 "go build (product packages)" go build ./cmd/... ./internal/...

  # The invariants that were historically broken by *forgetting a line*, not by
  # writing bad code: a protected route with no RBAC rule, a new permission the
  # matrix never granted, a viewer that grew a write action.
  run_check "s05.route-contracts" "static" 1 "route <-> RBAC registry contracts, role lattice, permission matrix" \
    bash -c 'go test ./internal/router/ -run "Route|Registry|Permission|Access" -count=1 &&
             go test ./internal/db/ -run "PermissionMatrix|RoleLattice|BusinessUserReadIntent|Viewer|RegistryDeclares" -count=1'

  run_check "s06.migrations-append-only" "static" 1 "no destructive DDL added since ${BASE:-none}" \
    bash -c '
      if [ -z "$1" ]; then echo "no base ref: cannot scope the migration check"; exit 3; fi
      diff_out=$(git diff -U0 "$1" -- internal/db/migrate.go internal/db/migrations 2>/dev/null || true)
      bad=$(printf "%s\n" "$diff_out" | grep -E "^\+" | grep -vE "^\+\+\+" \
            | grep -E "DropTable|DropColumn|RenameColumn|DROP TABLE|DROP COLUMN|RENAME COLUMN" || true)
      if [ -n "$bad" ]; then
        echo "destructive DDL added (needs an explicit migration decision, not a silent release):"
        echo "$bad"
        exit 1
      fi
      echo "no destructive DDL added"
    ' _ "$BASE"

  # Full suite. Local `go test ./...` picks up stray non-tracked directories
  # (test-results/... has shipped Go files in extracted toolchains); the product
  # surface is spelled out so the gate means the same thing everywhere.
  run_check "s07.go-test-all" "static" 1 "backend test suite (cmd + internal)" \
    go test ./cmd/... ./internal/... -count=1 -timeout 30m

  # The guard itself is a piece of software: prove it still refuses, and still
  # allows, before trusting a green report from it.
  run_check "s12.gate-selftest" "static" 1 "the tag-push hook still blocks what it should"     bash scripts/release-gate/selftest.sh

  # doc/301 §11.1: the assurance gate has to be a check that can go red, not a promise in
  # a document. G-A2/G-A3/G-A5/G-A6/G-A7 are Go tests and are already inside s07; what
  # s07 cannot see is (a) whether the G-A1 table prints its `n` and Clopper-Pearson bound
  # in a human-readable log, and (b) whether 44117-44123 is registered in all five places,
  # one of which is documentation. Both live here.
  run_check "s13.assurance-gold" "static" 1 "assurance gate G-A1..A8 (12 rows with n/CP, error-code closed set)"     bash scripts/release-gate/assurance.sh

  # doc/301 §9.4 says its five self-checks run 「在 CI 里，不靠人」. s07 already runs their Go
  # tests; what s07 cannot see is whether the table reaches a log with `n` on every row,
  # whether each of the five has an observed failing direction, and whether the five
  # sentences are still §9.4's sentences rather than a paraphrase that asks for less.
  run_check "s14.assurance-quality" "static" 1 "doc/301 §9.4 self-checks (5 rows, fail+unverified probes, sentences verbatim)"     bash scripts/release-gate/assurance-quality.sh

  run_check "s15.kc2-prereg" "static" 1 "doc/301 §5.7 KC-2 pre-registration (3 thresholds frozen, bidirectional check, quotes verbatim)"     bash scripts/release-gate/kc2-prereg.sh

  # doc/304 §7 item 12: the two probes that produced T02's pool and T16's drill were in
  # `localenv/`, which .gitignore excludes — reproducible evidence, un-runnable instrument.
  # They are tracked now, and the price of tracking them is that no file may hold a
  # credential. That refusal, the `--offline` contract and the decoupling are all here.
  run_check "s16.probes-selfcheck" "static" 1 "live probes tracked + credential-free (doc/304 §7 item 12)"     bash scripts/release-gate/probes.sh

  # doc/304 §7 item 25: T02's chain had one file left that a human transcribed by hand, and the
  # manual said so in as many words (`--adjudication` 是这块里唯一必须人手写的文件). The tool
  # derives it now; this row holds the two halves a `go test` cannot: that the 13 named tests are
  # still there, and that the page people actually read documents the step it used to skip.
  run_check "s17.adjudicate" "static" 1 "adjudication skeleton derived from the label files (doc/304 §7 item 25)"     bash scripts/release-gate/adjudicate.sh

  # doc/304 §7 items 2 and 3 are two *rituals* — «top up the evidence column at every
  # milestone», «check §4 once per review» — with no reader. This is the reader. It checks
  # shapes only (a cell is not empty, a status word is one of five, a number runs 1..N, a
  # version descends), never whether the evidence still supports the sentence beside it:
  # that is the review, and a gate that claimed it would be the exact lie §7 item 3 warns of.
  run_check "s18.docs-ledger" "static" 1 "tracking doc + plan appendix shapes (doc/304 §7 items 2/3, selftest refuses 6 mutations)"     bash scripts/release-gate/docs-ledger.sh

  run_check "s08.fe-typecheck" "static" 1 "frontend typecheck" bash -c 'cd web && npx tsc -b'
  run_check "s09.fe-test" "static" 1 "frontend unit tests" bash -c 'cd web && npx vitest run --maxWorkers=2'

  if [ -n "$VERSION" ]; then
    run_check "s10.changelog" "static" 1 "CHANGELOG.md carries the $VERSION section" \
      bash -c 'git show HEAD:CHANGELOG.md | grep -qF "## [$1]" || { echo "CHANGELOG.md has no \"## [$1]\" section"; exit 1; }; echo ok' _ "${VERSION#v}"
    run_check "s11.release-notes" "static" 0 "curated release notes for $VERSION (optional)" \
      bash -c '[ -f "$1" ] && { echo "notes: $1"; exit 0; }; echo "no $1: the workflow renders notes from git history"; exit 0' _ ".github/release-notes/$VERSION.md"
  fi
fi

# ── build tier ───────────────────────────────────────────────────────────
if tier_selected build; then
  run_check "b01.fe-build" "build" 1 "frontend production build" bash -c 'cd web && npm run build'

  DRILL_VERSION="${VERSION:-v0.0.0-drill}"
  run_check "b02.packaging-drill" "build" 1 "packaging rehearsal (scripts/release-drill.sh $DRILL_VERSION)" \
    bash scripts/release-drill.sh "$DRILL_VERSION"
  # The drill is a rehearsal: its artifacts are throwaway and must not linger in
  # the tree a later tier inspects.
  rm -rf dist-drill

  run_check "b03.build-fingerprint" "build" 1 "record the bundle the build produced" \
    bash -c '
      asset=$(sed -n "s/.*src=\"\/assets\/\(index-[^\"]*\.js\)\".*/\1/p" web/dist/index.html | head -1)
      if [ -z "$asset" ]; then echo "web/dist/index.html names no /assets/index-*.js"; exit 1; fi
      printf "%s\n" "$asset" > "$1/frontend_asset"
      echo "built bundle: $asset"
    ' _ "$OUT"

  run_check "b04.webfs-not-committed" "build" 1 "embedded asset staging stays out of git" \
    bash -c '
      n=$(git ls-files internal/webfs/dist | wc -l | tr -d " ")
      if [ "$n" != "0" ]; then echo "internal/webfs/dist has $n tracked file(s)"; exit 1; fi
      echo "internal/webfs/dist: untracked (gitignored)"
    '
fi

# ── live tier ────────────────────────────────────────────────────────────
if tier_selected live; then
  # Only export what was actually provided: an empty RGX_ENGINE (or empty
  # credentials) would clobber the suite's own default instead of leaving it.
  [ -n "${RGX_BASE:-}" ] && export RGX_VERIFY_BASE="$RGX_BASE"
  [ -n "${RGX_USER:-}" ] && export RGX_VERIFY_USER="$RGX_USER"
  [ -n "${RGX_PASSWORD:-}" ] && export RGX_VERIFY_PASSWORD="$RGX_PASSWORD"
  [ -n "${RGX_ENGINE:-}" ] && export RGX_ENGINE

  if [ -z "${RGX_BASE:-}" ]; then
    record "l01.readyz" "live" "1" "SKIP" "0" "" "RGX_BASE is not set: no deployment to verify"
    record "l02.served-fingerprint" "live" "1" "SKIP" "0" "" "RGX_BASE is not set: no deployment to verify"
    printf '[SKIP] %-30s RGX_BASE not set\n' "l01.readyz / l02.served-fingerprint"
  else
    if [ -n "${RGX_GATE_DEPLOY_CMD:-}" ]; then
      run_check "l00.deploy" "live" 0 "deploy the artifact under test (RGX_GATE_DEPLOY_CMD)" \
        bash -c "$RGX_GATE_DEPLOY_CMD"
    else
      record "l00.deploy" "live" "0" "SKIP" "0" "" "RGX_GATE_DEPLOY_CMD not set: verifying the deployment as it stands"
      printf '[SKIP] %-30s no RGX_GATE_DEPLOY_CMD (verifying the running deployment)\n' "l00.deploy"
    fi

    run_check "l01.readyz" "live" 1 "GET /api/v1/readyz answers ready" \
      bash -c '
        body=$(curl -sf -m 15 "$RGX_BASE/api/v1/readyz") || { echo "readyz did not answer at $RGX_BASE"; exit 1; }
        case "$body" in
          *'"status"'*ready*) echo "readyz: $body"; exit 0 ;;
          *) echo "unexpected readyz body: $body"; exit 1 ;;
        esac
      '

    # The single most valuable live assertion: the browser gets the bundle this
    # gate just built. A rebuild that was never redeployed looks identical in
    # every other check.
    run_check "l02.served-fingerprint" "live" 1 "served bundle == the bundle this gate built" \
      bash -c '
        built_file="$1/frontend_asset"
        built=""
        [ -f "$built_file" ] && built=$(cat "$built_file")
        if [ -z "$built" ]; then
          built=$(sed -n "s/.*src=\"\/assets\/\(index-[^\"]*\.js\)\".*/\1/p" web/dist/index.html | head -1)
        fi
        served=$(curl -s -m 20 "$RGX_BASE/" | sed -n "s/.*src=\"\/assets\/\(index-[^\"]*\.js\)\".*/\1/p" | head -1)
        printf "%s\n" "$served" > "$1/served_asset"
        if [ -z "$built" ]; then echo "no built bundle to compare against"; exit 1; fi
        if [ "$built" != "$served" ]; then
          echo "served=${served:-<none>} built=$built"
          echo "the deployment is not running the bundle this gate built; redeploy before tagging"
          exit 1
        fi
        echo "served = built = $served"
      ' _ "$OUT"

    MANIFEST="$ROOT/scripts/release-gate/suites.tsv"
    if [ ! -f "$MANIFEST" ]; then
      record "l03.suites" "live" "1" "MISSING" "0" "" "suite manifest missing: scripts/release-gate/suites.tsv"
    else
      while IFS=$'\t' read -r sid sprofile sscript sdesc scmd; do
        case "$sid" in ''|\#*) continue ;; esac
        case "$sprofile" in
          always|release) req=1 ;;
          *) req=0 ;;
        esac
        if [ "$PROFILE" != "release" ] && [ "$sprofile" = "release" ]; then continue; fi
        run_suite "$sid" "$req" "$sscript" "$sdesc" "$scmd"
      done < "$MANIFEST"
    fi
  fi
fi

# ── verdict ──────────────────────────────────────────────────────────────
FAILED=0
INCOMPLETE=0
REQ_TOTAL=0
REQ_PASS=0
NA_COUNT=0
NA_LIST=()
REASONS=()
for i in "${!R_ID[@]}"; do
  [ "${R_REQ[$i]}" = "1" ] || continue
  REQ_TOTAL=$((REQ_TOTAL + 1))
  case "${R_STATUS[$i]}" in
    PASS) REQ_PASS=$((REQ_PASS + 1)) ;;
    FAIL) FAILED=$((FAILED + 1)); REASONS+=("${R_ID[$i]}: FAIL") ;;
    # N/A is counted apart from both: it is not a pass (the numerator stays
    # honest), and it is not a blocking reason *in a CI run*, which is the only
    # profile that can produce it.
    N/A) NA_COUNT=$((NA_COUNT + 1)); NA_LIST+=("${R_ID[$i]}") ;;
    *) INCOMPLETE=$((INCOMPLETE + 1)); REASONS+=("${R_ID[$i]}: ${R_STATUS[$i]}") ;;
  esac
done

VERDICT="PASS"
EXIT_CODE=0
if [ "$FAILED" -gt 0 ]; then
  VERDICT="FAIL"; EXIT_CODE=1
elif [ "$INCOMPLETE" -gt 0 ]; then
  VERDICT="INCOMPLETE"; EXIT_CODE=2
elif [ "$NA_COUNT" -gt 0 ]; then
  # The word itself carries the caveat: a report that says `PASS` must be one
  # where every required check had an input and held. `PASS (ci: ...)` cannot be
  # grepped as PASS, cannot satisfy `releasable`, and prints its own shortfall in
  # the verdict line rather than in a footnote below it.
  VERDICT="PASS (ci: $NA_COUNT not applicable)"
  EXIT_CODE=0
fi

FRONTEND_ASSET=""
[ -f "$OUT/frontend_asset" ] && FRONTEND_ASSET=$(cat "$OUT/frontend_asset")
SERVED_ASSET=""
[ -f "$OUT/served_asset" ] && SERVED_ASSET=$(cat "$OUT/served_asset")

RELEASABLE="false"
# A trimmed run (--only/--skip) is a debugging tool: it can be PASS, it can never
# speak for a release, because it did not run the list a release is gated on.
if [ "$VERDICT" = "PASS" ] && [ "$TIER" = "all" ] && [ -n "$VERSION" ] && [ "$GIT_DIRTY" = "0" ] \
   && [ -z "$ONLY" ] && [ -z "$SKIP" ] \
   && [ -n "$FRONTEND_ASSET" ] && [ "$FRONTEND_ASSET" = "$SERVED_ASSET" ]; then
  RELEASABLE="true"
fi

FINISHED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
# meta.tsv + checks.tsv are the only interface to the report writer (tabs, no
# quoting rules to get wrong); python turns them into summary.json / summary.md.
{
  printf 'schema\t%s\n' "$GATE_SCHEMA"
  printf 'gate_version\t%s\n' "$GATE_VERSION"
  printf 'finished_at\t%s\n' "$FINISHED_AT"
  printf 'tier\t%s\n' "$TIER"
  printf 'profile\t%s\n' "$PROFILE"
  printf 'label\t%s\n' "${LABEL//$'\t'/ }"
  printf 'version\t%s\n' "$VERSION"
  printf 'base\t%s\n' "$BASE"
  printf 'ci\t%s\n' "$CI"
  printf 'git_rev\t%s\n' "$GIT_REV"
  printf 'git_short\t%s\n' "$GIT_SHORT"
  printf 'git_branch\t%s\n' "$GIT_BRANCH"
  printf 'git_tag\t%s\n' "$GIT_TAG"
  printf 'git_dirty\t%s\n' "$GIT_DIRTY"
  printf 'git_dirty_files\t%s\n' "$DIRTY_FILES"
  printf 'frontend_asset\t%s\n' "$FRONTEND_ASSET"
  printf 'served_asset\t%s\n' "$SERVED_ASSET"
  printf 'live_base\t%s\n' "${RGX_BASE:-}"
  printf 'runs\t%s\n' "$RUNS"
  printf 'required_total\t%s\n' "$REQ_TOTAL"
  printf 'required_passed\t%s\n' "$REQ_PASS"
  printf 'verdict\t%s\n' "$VERDICT"
  printf 'releasable\t%s\n' "$RELEASABLE"
  printf 'exit_code\t%s\n' "$EXIT_CODE"
} > "$OUT/meta.tsv"
: > "$OUT/checks.tsv"
for i in "${!R_ID[@]}"; do
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "${R_ID[$i]}" "${R_TIER[$i]}" "${R_REQ[$i]}" "${R_STATUS[$i]}" "${R_SECS[$i]}" \
    "${R_LOG[$i]}" "$(printf '%s' "${R_NOTE[$i]}" | tr '\t' ' ')" >> "$OUT/checks.tsv"
done

if [ -n "${PYTHON:-}" ]; then
  "$PYTHON" - "$OUT" <<'PY'
import json, os, sys
out = sys.argv[1]
meta = {}
with open(os.path.join(out, "meta.tsv"), encoding="utf-8") as fh:
    for line in fh:
        line = line.rstrip("\n")
        if not line:
            continue
        k, _, v = line.partition("\t")
        meta[k] = v
checks = []
with open(os.path.join(out, "checks.tsv"), encoding="utf-8") as fh:
    for line in fh:
        line = line.rstrip("\n")
        if not line:
            continue
        cid, tier, req, status, secs, log, note = (line.split("\t") + [""] * 7)[:7]
        checks.append({
            "id": cid, "tier": tier, "required": req == "1", "status": status,
            "seconds": int(secs or 0), "log": log, "note": note,
        })
doc = {
    "schema": meta.get("schema"),
    "gate_version": meta.get("gate_version"),
    "finished_at": meta.get("finished_at"),
    "tier": meta.get("tier"),
    "profile": meta.get("profile"),
    "label": meta.get("label"),
    "version": meta.get("version"),
    "base": meta.get("base"),
    "ci": meta.get("ci") == "1",
    "git": {
        "rev": meta.get("git_rev"), "short": meta.get("git_short"),
        "branch": meta.get("git_branch"), "tag": meta.get("git_tag"),
        "dirty": meta.get("git_dirty") == "1",
        "dirty_files": int(meta.get("git_dirty_files") or 0),
    },
    "artifacts": {
        "frontend_asset": meta.get("frontend_asset") or None,
        "served_asset": meta.get("served_asset") or None,
    },
    "live_base": meta.get("live_base") or None,
    "required_total": int(meta.get("required_total") or 0),
    "required_passed": int(meta.get("required_passed") or 0),
    "checks": checks,
    "verdict": meta.get("verdict"),
    "releasable": meta.get("releasable") == "true",
    "exit_code": int(meta.get("exit_code") or 0),
}
with open(os.path.join(out, "summary.json"), "w", encoding="utf-8") as fh:
    json.dump(doc, fh, ensure_ascii=False, indent=2)
    fh.write("\n")

def esc(v):
    return str(v).replace("|", "\\|")

lines = []
lines.append("# 发版门禁报告 — %s" % doc["verdict"])
lines.append("")
lines.append("- 结论：**%s**（releasable=%s，exit=%s）" % (doc["verdict"], str(doc["releasable"]).lower(), doc["exit_code"]))
lines.append("- 层级：%s；画像：%s；版本：%s" % (doc["tier"], doc["profile"], doc["version"] or "—"))
lines.append("- 提交：%s（分支 %s，tag %s，脏文件 %s）" % (
    doc["git"]["short"] or "—", doc["git"]["branch"] or "—", doc["git"]["tag"] or "—", doc["git"]["dirty_files"]))
lines.append("- 对比基线：%s" % (doc["base"] or "—"))
lines.append("- 构建产物：%s；线上产物：%s" % (
    doc["artifacts"]["frontend_asset"] or "—", doc["artifacts"]["served_asset"] or "—"))
lines.append("- 用时/必过项：%s/%s 必过项通过；%s 项实际执行" % (
    doc["required_passed"], doc["required_total"], len(doc["checks"])))
lines.append("- 结束时间（UTC）：%s" % doc["finished_at"])
if doc["label"]:
    lines.append("- 备注：%s" % doc["label"])
lines.append("")
lines.append("| 检查 | 层 | 必过 | 结果 | 秒 | 说明 |")
lines.append("| --- | --- | --- | --- | --- | --- |")
for c in doc["checks"]:
    lines.append("| `%s` | %s | %s | **%s** | %d | %s |" % (
        esc(c["id"]), esc(c["tier"]), "是" if c["required"] else "否",
        esc(c["status"]), c["seconds"], esc(c["note"])))
lines.append("")
lines.append("日志：`%s/checks/`；证据：`%s/evidence/`。" % (out, out))
with open(os.path.join(out, "summary.md"), "w", encoding="utf-8") as fh:
    fh.write("\n".join(lines) + "\n")
print("report: %s/summary.md" % out)
PY
  REPORT_RC=$?
  if [ "$REPORT_RC" != "0" ]; then
    echo "release-gate: report writer failed (exit $REPORT_RC)" >&2
  fi
else
  echo "release-gate: no python on PATH — summary.json not written (a tag push will refuse this run)" >&2
fi

echo
echo "──────────────────────────────────────────────────────────────"
printf 'RELEASE GATE: %s   required %s/%s passed   (tier=%s profile=%s)\n' \
  "$VERDICT" "$REQ_PASS" "$REQ_TOTAL" "$TIER" "$PROFILE"
if [ "${#REASONS[@]}" -gt 0 ]; then
  echo "blocking:"
  for r in "${REASONS[@]}"; do echo "  - $r"; done
fi
if [ "$NA_COUNT" -gt 0 ]; then
  printf 'not applicable: %s (this tree carries no doc/ — .gitignore:91 — so these compared code against a document they could not open)\n' "$NA_COUNT"
  for r in "${NA_LIST[@]}"; do echo "  - $r"; done
  echo "  CI may report them as N/A; a release-intent run may not, and would have called them FAIL."
fi
if [ "$RELEASABLE" = "true" ]; then
  echo "releasable: yes  (tag $VERSION on $GIT_SHORT may be pushed)"
else
  echo "releasable: no   ($(
    if [ "$VERDICT" != "PASS" ]; then echo "verdict is $VERDICT";
    elif [ "$TIER" != "all" ]; then echo "tier is $TIER, a release needs --tier all";
    elif [ -z "$VERSION" ]; then echo "no --version given";
    elif [ "$GIT_DIRTY" = "1" ]; then echo "tree is dirty";
    elif [ -n "$ONLY" ] || [ -n "$SKIP" ]; then echo "run was trimmed with --only/--skip";
    elif [ -z "$FRONTEND_ASSET" ] || [ "$FRONTEND_ASSET" != "$SERVED_ASSET" ]; then echo "served bundle != built bundle";
    else echo "unknown"; fi) )"
fi
echo "report: $OUT/summary.md"
exit "$EXIT_CODE"
