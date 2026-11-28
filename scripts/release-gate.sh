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
#           vet/build, the shared-builder guard, RBAC route contracts,
#           append-only migrations, the whole
#           backend test suite, frontend typecheck + unit tests, changelog and
#           release notes for the version. Runs anywhere (CI included).
#   build   the artifact: production frontend build, the packaging rehearsal
#           (`scripts/release-drill.sh`), and the fingerprint of what was built.
#   live    the deployment: readyz, "is the served bundle the one I just built",
#           then the live API/browser suites listed in
#           `scripts/release-gate/suites.tsv` — and, twice around them, the
#           deployment log: a green run is not evidence that the run left nothing
#           behind (l03 before, l04 after; delta must be 0).
#
# Statuses
# --------
#   PASS     the check ran and held
#   FAIL     the check ran and did not hold
#   SKIP     a required check was not run (deselected, or its preconditions
#            are absent) — the run is INCOMPLETE, never silently green
#   MISSING  a live suite script is not on this machine — also INCOMPLETE
#   N/A      the check needs an input this tree does not carry, and this is a
#            `--ci` run. Since doc/321 the engineering archive *is* tracked
#            (doc/specification/, doc/develop/), so a clean checkout has 301/304/305
#            and specification/12 -- but still no doc/operations/ labeling manual and
#            no doc/environment/ note, because those stay machine-local. Whatever a
#            row needs, it compares code against documents and can only answer "not here"
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
#   scripts/release-gate.sh --list                          # show every check (person; marks doc rows)
#   scripts/release-gate.sh --list=tsv                      # same declarations, machine: tier/id/command/doc
#
# Live tier environment (never committed — this script holds no host or secret):
#   RGX_BASE                 platform base URL of the deployment under test
#   RGX_USER / RGX_PASSWORD  an admin account on that deployment
#   RGX_ENGINE               optional engine base URL (some suites read it)
#   RGX_GATE_DEPLOY_CMD      optional command that (re)deploys the artifact
#   RGX_GATE_LIVE_DIR        where the live suites live (default localenv/probes)
#   RGX_GATE_TMP_DIR         where the ad-hoc probes live (default tmp)
#   RGX_GATE_MAX_AGE_HOURS   how long a report stays valid for a tag (default 72)
#   RGX_GATE_LOG_CMD         command whose stdout is the deployment log, read before and
#                            after the suites (l03/l04). Without it a live run cannot say
#                            whether it left an ERROR behind, so the rows are INCOMPLETE —
#                            never a pass (doc/324 §7.3, doc/325).
#   RGX_GATE_LOG_ERROR_PATTERN  the fixed string counted as an error line
#                            (default `"level":"ERROR"`, this service's JSON logs)
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
LIST_FMT=""
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
    --only) ONLY="${ONLY:+$ONLY,}${2:-}"; shift 2 ;;
    --only=*) ONLY="${ONLY:+$ONLY,}${1#*=}"; shift ;;
    --skip) SKIP="${SKIP:+$SKIP,}${2:-}"; shift 2 ;;
    --skip=*) SKIP="${SKIP:+$SKIP,}${1#*=}"; shift ;;
    --ci) CI=1; shift ;;
    --list) LIST=1; shift ;;
    --list=tsv) LIST=1; LIST_FMT=tsv; shift ;;
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

# ── E98: a selection that selects nothing is a usage error, not a pass ──────
# `--only=s16,s19` used to match nothing (ids are `s16.probes-selfcheck`, not `s16`),
# every check was recorded as SKIP, `required` came out 0/0 and the gate printed PASS.
# `releasable` stayed false, so nothing could ship on it — but a report that says PASS
# without having run a check is exactly the reading this whole file exists to prevent.
#
# Three rules, and they are the *only* place selection is validated:
#   ① the id set is derived from this file (the `run_check`/`run_suite` calls), not typed
#      a second time, so a new check is known the moment it is declared;
#   ② an `--only` token that names no check is exit 2 and prints the whole id list — a
#      prefix is a typo, not a shorthand, and silently ignoring it is how s19 stopped
#      running;
#   ③ a selection that ends up empty (either way, including `--only X --skip X`) is exit 2
#      as well; and if the run somehow still reaches the verdict with RUNS=0, the summary
#      refuses to print PASS (the guard after the verdict closes that hole).
SELF="$(cd -- "$(dirname -- "$0")" 2>/dev/null && pwd)/$(basename -- "$0")"
known_ids() {
  grep -oE '^[[:space:]]*run_(check|suite)[[:space:]]+"[^"]+"' "$SELF" \
    | sed -E 's/.*"([^"]+)"$/\1/' \
    | grep -v '\$' | sort -u   # `run_suite "$sid"` in the manifest loop is not an id
}
split_ids() {
  printf '%s' "$1" | tr ',' '\n' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' | sed '/^$/d'
}
# list_decls prints the declarations as `<id> (<tier>) <command>`, one per line, grouped by
# tier. It is the *same* declarations the tiers run — see the --list branch below.
list_decls() {
  # `--list` prints what the tiers run, read out of the declarations themselves, so a row
  # cannot go missing from it (E120). Two formats: the default one is for a person (tier
  # headers, description, and `doc` for the rows that read the engineering docs), and
  # `--list=tsv` for a script: tier<TAB>id<TAB>command<TAB>doc=0|1. The command matters
  # there because a reader that wants "the rows that read doc/" needs to see which script
  # a row calls; the marker matters because a grep for `doc/` cannot tell a reader from
  # `clean-clone.sh`, which creates a worktree (doc/304 §4 E121).
  local bs tier
  bs=$(printf '\134')
  if [ "${LIST_FMT:-}" = "tsv" ]; then
    BS="$bs" awk '
      {
        cont = (substr($0, length($0)) == ENVIRON["BS"])
        buf = buf (cont ? substr($0, 1, length($0) - 1) : $0) " "
        if (cont) next
        # A comment can quote the call shape (`run_check "id" ...`) to explain it; the
        # declaration is the only thing that starts the line with the function name.
        if (buf ~ /^[[:space:]]*#/) { buf = ""; next }
        n = split(buf, f, "\"")
        if (n >= 7 && f[1] ~ /run_(check|suite)[[:space:]]+$/ && f[2] !~ /\$/) {
          isdoc = 0
          if (f[7] ~ /^[[:space:]]*doc([[:space:]]|$)/) { isdoc = 1; sub(/^[[:space:]]*doc[[:space:]]+/, "", f[7]) }
          cmd = f[7]
          for (i = 8; i <= n; i++) cmd = cmd "\"" f[i]
          gsub(/[[:space:]]+/, " ", cmd)
          sub(/^ /, "", cmd); sub(/ $/, "", cmd)
          printf "%s\t%s\t%s\t%d\n", f[4], f[2], cmd, isdoc
        }
        buf = ""
      }
    ' "$SELF"
    return 0
  fi
  for tier in static build live; do
    case "$tier" in
      static) echo "static tier (always required)" ;;
      build)  echo "build tier (required when selected)" ;;
      live)   echo "live tier (required for a releasable run)" ;;
    esac
    BS="$bs" awk -v want="$tier" '
      # A declaration may be wrapped with a trailing backslash; the backslash is dropped
      # from the joined line so the printed command is the command, not a backslash.
      {
        cont = (substr($0, length($0)) == ENVIRON["BS"])
        buf = buf (cont ? substr($0, 1, length($0) - 1) : $0) " "
        if (cont) next
        # A comment can quote the call shape (`run_check "id" ...`) to explain it; the
        # declaration is the only thing that starts the line with the function name.
        if (buf ~ /^[[:space:]]*#/) { buf = ""; next }
        n = split(buf, f, "\"")
        if (n >= 7 && f[1] ~ /run_(check|suite)[[:space:]]+$/ && f[4] == want) {
          # The description, not the command: a wrapped `bash -c ...` has quotes inside
          # its command, so the command field is a fragment for exactly the rows whose
          # command someone would most want to see. The description is one sentence.
          desc = f[6]
          gsub(/[[:space:]]+/, " ", desc)
          sub(/^ /, "", desc); sub(/ $/, "", desc)
          tag = f[4]
          if (f[7] ~ /^[[:space:]]*doc([[:space:]]|$)/) tag = tag ", doc"
          if (f[2] !~ /\$/) printf "  %-26s %-26s %s\n", f[2], "(" tag ")", desc
        }
        buf = ""
      }
    ' "$SELF"
  done
}

KNOWN_IDS="$(known_ids)"

if [ -n "$ONLY" ]; then
  UNKNOWN_ONLY=""
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    printf '%s
' "$KNOWN_IDS" | grep -qxF -- "$token" || UNKNOWN_ONLY="$UNKNOWN_ONLY $token"
  done <<< "$(split_ids "$ONLY")"
  if [ -n "$UNKNOWN_ONLY" ]; then
    {
      echo "release-gate: --only names no check in this file:$UNKNOWN_ONLY"
      echo "  ids are matched exactly and completely (no prefixes, no fuzzy match) — doc/304 §4 E98."
      echo "  known ids:"
      printf '%s
' "$KNOWN_IDS" | sed 's/^/    /'
    } >&2
    exit 2
  fi
  SELECTED=0
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    if should_run "$token"; then SELECTED=$((SELECTED + 1)); fi
  done <<< "$(split_ids "$ONLY")"
  if [ "$SELECTED" -eq 0 ]; then
    {
      echo "release-gate: --only/--skip selected 0 checks out of $(printf '%s
' "$KNOWN_IDS" | grep -c .)"
      echo "  every check --only named is also in --skip: that run would check nothing, so it is"
      echo "  a usage error (exit 2) rather than a PASS with required 0/0."
    } >&2
    exit 2
  fi
fi

# `--skip` gets the same treatment for the same reason, in the quieter direction: a skip
# naming a check that does not exist means the operator believed something was excluded
# when it was not. Say so instead of silently running it.
if [ -n "$SKIP" ]; then
  UNKNOWN_SKIP=""
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    printf '%s
' "$KNOWN_IDS" | grep -qxF -- "$token" || UNKNOWN_SKIP="$UNKNOWN_SKIP $token"
  done <<< "$(split_ids "$SKIP")"
  if [ -n "$UNKNOWN_SKIP" ]; then
    {
      echo "release-gate: --skip names no check in this file:$UNKNOWN_SKIP"
      echo "  (a skip that matches nothing excluded nothing — doc/304 §4 E98)"
      echo "  known ids:"
      printf '%s
' "$KNOWN_IDS" | sed 's/^/    /'
    } >&2
    exit 2
  fi
fi

# run_check <id> <tier> <required 1|0> <description> <command...>
run_check() {
  local id="$1" t="$2" req="$3" desc="$4"
  shift 4
  # A row that reads doc/ says so, as the fifth argument: `run_check "id" "tier" 1 "desc" doc cmd...`.
  # A tracked document can still be absent from a checkout (doc/operations/, doc/environment/ are
  # machine-local, doc/321), so such a row answers exit 3 rather than "consistent" and its whole point
  # is that it *ran* somewhere; `--list=tsv` publishes this marker as a fourth column and
  # `doc-present.sh` derives its selection from it (doc/304 §4 E121). The alternative —
  # grep the command for `doc/` — cannot tell a reader from `clean-clone.sh`, which creates a
  # worktree (and therefore cannot be selected by a row that runs inside one).
  local reads_doc=0
  if [ "${1:-}" = "doc" ]; then reads_doc=1; shift; fi
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
        # The marker has a second use here: an N/A on a doc-reading row should say *why*
        # (the document is not tracked) and name the row that proves it runs where it exists
        # exists, or a reader reads "N/A" as "this check does not matter".
        if [ "$reads_doc" = "1" ]; then
          note="$desc — N/A: this row reads a document the checkout does not carry (not tracked, doc/321); s24.doc-present is the row that proves it runs where the document exists"
        else
          note="$desc — not applicable in this tree (the check needs a document that is not in the checkout; see the log for which one)"
        fi
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
  # Every row below is read out of the `run_check`/`run_suite` declarations themselves, so a
  # check that is added, renamed or dropped appears here by construction. The heredoc this
  # replaced was a second hand-written list, and it went stale the first time a check was
  # added after it (s23) — the only thing that noticed was selftest.sh's "skip the whole
  # static tier" case, which reads this output (doc/304 §4 E120).
  list_decls
  # The suites line is prose, not a declaration: it is printed for the person reading the
  # default list, and left out of the machine format (a consumer that splits on tabs would
  # otherwise get a one-field line and have to special-case it).
  [ "${LIST_FMT:-}" = "tsv" ] || echo "  + suites from scripts/release-gate/suites.tsv"
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
  # s01a proves the scanner still sees what it claims to see. It exists because it did not:
  # every Chinese-named file was C-quoted by git and skipped by `[ -f ]` in silence, so a tree
  # of 305 Chinese-named documents scanned 7 and printed "clean". A scanner that can be blind
  # is worse than no scanner, because it is trusted (doc/321 §4).
  run_check "s01a.secret-scan-selftest" "static" 1 "the secret scanner still blocks a credential behind a non-ASCII filename" \
    bash scripts/secret-scan.sh --selftest

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

  # s07 runs the PostgreSQL contract tests, which catch this shape against a real
  # database — but only when RGX_TEST_POSTGRES_DSN is set, and only after the code
  # compiles. This is the same rule as a static check: the knowledge-ops 500 was a
  # paging read that inherited its count's GROUP BY, which SQLite accepts and
  # PostgreSQL rejects (42803). See cmd/gormguard.
  run_check "s20.gorm-builders" "static" 1 "no count/read builder shared with Group/Select/Distinct/Having/Unscoped" \
    go run ./cmd/gormguard ./internal

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
  run_check "s13.assurance-gold" "static" 1 "assurance gate G-A1..A8 (12 rows with n/CP, error-code closed set)" doc bash scripts/release-gate/assurance.sh

  # doc/301 §9.4 says its five self-checks run 「在 CI 里，不靠人」. s07 already runs their Go
  # tests; what s07 cannot see is whether the table reaches a log with `n` on every row,
  # whether each of the five has an observed failing direction, and whether the five
  # sentences are still §9.4's sentences rather than a paraphrase that asks for less.
  run_check "s14.assurance-quality" "static" 1 "doc/301 §9.4 self-checks (5 rows, fail+unverified probes, sentences verbatim)" doc bash scripts/release-gate/assurance-quality.sh

  run_check "s15.kc2-prereg" "static" 1 "doc/301 §5.7 KC-2 pre-registration (3 thresholds frozen, bidirectional check, quotes verbatim)" doc bash scripts/release-gate/kc2-prereg.sh

  # doc/304 §7 item 12: the two probes that produced T02's pool and T16's drill were in
  # `localenv/`, which .gitignore excludes — reproducible evidence, un-runnable instrument.
  # They are tracked now, and the price of tracking them is that no file may hold a
  # credential. That refusal, the `--offline` contract and the decoupling are all here.
  run_check "s16.probes-selfcheck" "static" 1 "live probes tracked + credential-free (doc/304 §7 item 12)"     bash scripts/release-gate/probes.sh

  # s16 checks the probes' *shape*; it never edits one. The `--selftest` is the other half:
  # 20 mutations planted in a copy of the tree, each of which must be refused. It ran only
  # when a person remembered to run it, which is the same hole s01a was created to close —
  # a check whose failing direction is never exercised is a check that can only say ok
  # (doc/321 §4). Nightly runs it too: the strings it mutates live in files that drift.
  run_check "s16a.probes-selftest" "static" 1 "the probe contract still refuses 20 planted violations (mutation selftest)"     bash scripts/release-gate/probes.sh --selftest

  # l03/l04 read the deployment log around the live suites. On the workstation the log lives
  # on the host, inside a container, or in a store — so their subject is a *command*, and a
  # command that counts the wrong thing (or counts nothing at all) would quietly make every
  # live run green. Seven hermetic cases pin its failing directions: a delta, a shrunken log,
  # a failing command, a missing command, a compare without a baseline, the shipped default
  # pattern (doc/325).
  run_check "s16b.log-error-delta-selftest" "static" 1 "the live log-delta row can fail (7 hermetic cases)"     bash scripts/release-gate/log-error-delta.sh --selftest

  # doc/304 §7 item 25: T02's chain had one file left that a human transcribed by hand, and the
  # manual said so in as many words (`--adjudication` 是这块里唯一必须人手写的文件). The tool
  # derives it now; this row holds the two halves a `go test` cannot: that the 13 named tests are
  # still there, and that the page people actually read documents the step it used to skip.
  run_check "s17.adjudicate" "static" 1 "adjudication skeleton derived from the label files (doc/304 §7 item 25)" doc bash scripts/release-gate/adjudicate.sh

  # doc/304 §7 items 2 and 3 are two *rituals* — «top up the evidence column at every
  # milestone», «check §4 once per review» — with no reader. This is the reader. It checks
  # shapes only (a cell is not empty, a status word is one of five, a number runs 1..N, a
  # version descends), never whether the evidence still supports the sentence beside it:
  # that is the review, and a gate that claimed it would be the exact lie §7 item 3 warns of.
  run_check "s18.docs-ledger" "static" 1 "tracking doc + plan appendix shapes (doc/304 §7 items 2/3, selftest refuses 12 mutations incl. a dangling E citation, a changelog row that lost its date cell in each document, and a governed 305 §5 row out of order or renumbered onto a taken number)" doc bash scripts/release-gate/docs-ledger.sh

  # s18 reads the documents themselves, so a clean checkout (no `doc/`, .gitignore:91) can only
  # answer "N/A". 304 §7 item 26 chose route ① on 2026-11-23: freeze the ledger facts into a
  # tracked snapshot and judge *that* in CI, with a sha256 freshness check against the
  # documents wherever they exist. Without this line, "CI is green" would keep meaning "the
  # ledger was not there to check", which is the gap item 26 named.
  run_check "s19.docs-ledger-snapshot" "static" 1 "tracked ledger snapshot: schema/closed sets/order + sha256 freshness (doc/304 §7 item 26)" doc bash scripts/release-gate/docs-ledger-snapshot.sh

  # doc/304 §4 E112 named its own gap: the packet rule was held by `go test` alone, and the
  # gate's only goldtool row (`s17.adjudicate`) runs `-run TestAdjudicate`, so eleven blind
  # tests could be deleted without turning anything red. This row is the reader. It holds the
  # tests, the two flags on `--help`, the two refusals in the code, and the manual sentence
  # that says what to read instead of what to ignore — and its selftest proves each of the
  # four can fail, including "the packet silently stops stripping".
  run_check "s21.blind-packet" "static" 1 "annotation packet: no prior_*/gold_* key, the stub verifies it before writing (doc/304 §4 E112, §7 item 36)" doc bash scripts/release-gate/blind-packet.sh

  # doc/301 §9.1.3 一 says E2's unit is the candidate *row*; the manual has told the annotator
  # to align on (item_id, candidate_index) since v1.2, and until v1.14 the tools keyed on
  # item_id alone — 36 rows read as 30 claims, every gate green (doc/304 §7 item 40). The fix
  # is a Go test set, which is the same gap E112 named: tests can be deleted one at a time and
  # the suite still says ok. So this row holds the named tests (regex derived from the list),
  # the two manual sections a person copies from, and `assemble`'s refusal by name — and, when
  # the real worksheet is on disk, re-derives 36/30 from the file instead of the document.
  run_check "s23.candidate-key" "static" 1 "candidate row = (item_id, candidate_index): stub writes it, check/kappa refuse to guess it, assemble refuses that side (doc/304 §7 item 40)" doc bash scripts/release-gate/candidate-key.sh

  # doc/310 §12.2 — 生成物新鲜度
  #
  # §12.2 asks CI to regenerate the OpenAPI artifacts and diff. That claim had exactly one
  # implementer (`make swagger-check`) and **no reader**: nothing in this script ran it, so the
  # three files in `internal/docs/` were only ever read by a blank import, which any bytes
  # satisfy. The same shape as E120 (`--list` was a second hand-written list) and E130 ("已闭环
  # （E119）" had no reader): the check existed, the ritual existed, the join did not. §20 A25
  # states the other half ("不修改工作区，能发现缺失/多余/不一致") and is now the row's own
  # `--selftest`, five mutations including the two directions of drift (committed lost an
  # operation / generation carries one the committed file lacks) and a missing artifact.
  #
  # The flags and the pinned version moved into that script, and the Makefile targets call it;
  # two copies of a generator invocation is how the artifacts and the recipe drift apart.
  run_check "s25.openapi-freshness" "static" 1 "the indexed OpenAPI artifacts == a fresh generation, three files, worktree untouched (doc/310 §12.2, §20 A25)" bash scripts/release-gate/openapi.sh

  # doc/310 Phase 3 lists 「检查 API 破坏性变更」 and §20 A21 records it as「具备后续差异检查基础」.
  # The basis arrived with E138/E140 — the spec is generated, committed and freshness-checked —
  # so two comparable documents exist for the first time: the one this commit would ship (the
  # index) and the one a release already shipped (the newest tag carrying the file). A21 stays
  # a hope until something reads both and refuses the losses; that is this row.
  #
  # Seven rules, each a loss a client feels (removed path / operation / operationId / response
  # code, newly required parameter, narrowed enum, changed schema type). Adding a path, an
  # optional parameter, a response code or an enum value is *not* breaking — a gate that reds
  # on a healthy release gets switched off, and then it protects nothing.
  run_check "s26.openapi-breaking" "static" 1 "API compatibility: the indexed spec against the newest released spec, seven rules + written exemptions (doc/310 Phase 3, §20 A21)" bash scripts/release-gate/openapi-breaking.sh

  # doc/301 §5.10 GW-2 (T14) has shipped a harness since E92 — `--selftest` with 28 assertions that
  # prove the numbers can go red — and **no reader**: nothing in this script ran it, so the only
  # thing standing between a narrowed assertion and a green perf report was habit. That is the same
  # shape as E120 (`--list` was a second hand-written list) and E130 (a "closed" row nothing read).
  #
  # E151 adds the reading T14 needed and this row is its reader: a cell short of samples now says
  # *why* (`blocked_by` = skipped_live / sample_plan / upstream_quota) instead of printing one
  # sentence for three different facts. The run that produced the six unmeasured c2/c4 cells was
  # quota-blocked, and the report could not say so — `samples 5 < 20` reads like our own plan.
  # The assertion that matters here is the negative one: those two shortfalls must not print the
  # same string, because the day they do, "upstream would not let us measure" is sold as "we chose
  # not to measure" (doc/304 §4 E146's three kinds of empty, applied to a latency table).
  run_check "s27.perf-selftest" "static" 1 "perf baseline harness still refuses to pass an unmeasured cell, and names which kind of unmeasured it is (doc/301 GW-2, doc/304 §4 E151)" "$PYTHON" scripts/gateway-perf-baseline.py --selftest

  # doc/304 §4 E161: "需要接口开发文档的时候，可以直接取，发给客户". The deliverable existed in
  # three places it could not be taken from — a 674 KB `swagger.json` full of `$ref`s, a running
  # 151 whose docs surface is admin-gated, and a hand-written PDF that would be stale by the next
  # route. This row makes the fourth place real: `doc/specification/api/api-reference.{md,html}`,
  # rendered from the same bytes the binary serves and tracked so a clone carries it.
  #
  # The row's second job matters more than its first. The rendering can only say what the
  # annotations say, and today 616/616 operations still carry the generator's placeholder summary
  # with 0 request bodies — so the document opens with those counts (computed on every run) and
  # calls itself a route contract, not a business interface manual. A gate that only checked
  # "file exists and is fresh" would let that stub ship as "the customer interface doc"; the
  # freshness check plus the printed annotation coverage is what keeps the claim honest, and the
  # day someone annotates a handler the numbers move and this row demands a regeneration.
  run_check "s28.api-doc" "static" 1 "the shipped API reference == a fresh render of the served spec, tracked (not gitignored), with annotation coverage printed (doc/304 §4 E161)" bash scripts/release-gate/api-doc.sh

  # doc/310 §22, doc/304 §4 E162: AGENTS.md 「API 契约（强制）」 says every new or changed
  # endpoint must carry OpenAPI annotations. A sentence in a document is not a check (E129),
  # and 616/616 endpoints today have none — so nothing would have refused the 617th.
  #
  # This row is the ratchet, not a wish: a frozen baseline lists the endpoints still allowed to
  # have no annotation, a **new** endpoint cannot inherit that exemption, and the baseline may
  # only shrink (`--write-baseline` refuses to add a line, so it cannot be used as a silencer).
  # Two rules keep the file from rotting the other way: a baseline entry whose route is gone,
  # or which has meanwhile been annotated, also fails — progress is a diff, not a memory.
  #
  # The PASS line prints `annotated/total` every run. Today it reads `0/616`, which is the
  # honest distance to a real interface manual (doc/304 §7 item 47 is the burn-down plan).
  run_check "s29.openapi-annotations" "static" 1 "no new endpoint ships without a contract: every operation is annotated or in a baseline that may only shrink (doc/310 §22, doc/304 §4 E162)" "$PYTHON" scripts/release-gate/openapi_annotations.py --check

  # doc/310 §18 Phase 3, §22.4 T310-04, doc/304 §4 E163: “发布时附带版本化 OpenAPI 文件”.
  #
  # The sentence is easy to satisfy with any file named right (E126: a path is not a reading).
  # The claim worth checking is three-way byte equality: index (`git show :internal/docs/…`) ==
  # the packaged `api/openapi-<version>.{json,yaml}` == the bytes that occur verbatim inside the
  # shipped binary (Go `//go:embed` stores the file raw). Two consequences matter:
  #   ① a dirty tree cannot ship an uncommitted contract (worktree vs index is asserted);
  #   ② docs.Enabled=false does not mean “the binary knows nothing” — the spec is compiled in,
  #      only the HTTP surface is gated (doc/310 §13.1a).
  # The row builds one native ./cmd/server (no webfs_embed) so the assertion runs on every push
  # rather than only when someone cuts a tag, and then runs 4 mutations + 1 control.
  run_check "s30.release-spec-artifact" "static" 1 "the release package carries the contract its binary compiled in: index bytes == packaged bytes == bytes embedded in the binary (doc/310 §18 Phase 3, T310-04; doc/304 §4 E163)" bash scripts/release/spec-artifact.sh gate

  # doc/develop/313 §7 C8 approved v0.7.0 on condition that upgrade *and* rollback are
  # verified. s06 covers the half where a table or column disappears. The half it cannot
  # see is a column that appears and the previous release cannot fill: AutoMigrate adds
  # `not null` verbatim, and every old insert then fails — on the rollback target, after
  # the fact. Cheap to check statically (a tag-to-worktree struct diff), impossible to
  # notice in review.
  run_check "s32.release-upgrade-rollback" "static" 1 "the previous release can still write: every column added to an existing table since the last release tag is nullable or defaulted (doc/develop/313 §7 C8; the half s06 cannot see) and every written exemption is used" \
    bash scripts/release-gate/migration-window.sh "$BASE"


  # doc/310 §22.5 T310-05 / T310-06, doc/304 §4 E173: the spec told a client *that* an
  # endpoint needs a bearer token, never *which* permission, and never which business codes a
  # failure carries. A client generator can only see machine-readable fields, and prose
  # (`@Description` "需要权限 read:agent") is not one.
  #
  # swag v2 cannot emit an operation-level `@x-` (it files it under `responses`,
  # operationv3.go:163), so the fields are added after generation by
  # `openapi_extensions.py` from two sources that already exist: `accessRules` (the table the
  # middleware enforces) and the `@Failure` descriptions the generator derives from the
  # `httperr` return points.
  #
  # What is checked is *equality with the source*, not presence:
  #   · every operation's `x-ragflow-permission` equals what `accessRules` says for its route,
  #     and an operation with no rule must not carry the key (default-deny is a reading too);
  #   · every operation's `x-error-codes` equals the codes its own failure descriptions name;
  #   · and all three artifacts agree — the JSON is parsed, the `docs.go` template is rendered
  #     and parsed, the YAML is scanned — because "the key is in swagger.json" says nothing
  #     about what `/docs/openapi.json` serves (`docs.go` is the runtime copy, E164's lesson).
  # The check reads `access.go` itself (`--rules`) rather than the writer's dump: comparing an
  # artifact with the tool that produced it is a tautology.
  #
  # The mutations run in the same row (1.6 s) rather than in a ritual: a reader that has never
  # been seen to fail is a reader whose exponent nobody has measured.
  run_check "s33.openapi-contract-extensions" "static" 1 "each operation carries the permission accessRules enforces and the business codes its failures name, in all three artifacts, and 7 mutations of that claim are refused (doc/310 §22.5 T310-05/T310-06; doc/304 §4 E173)" \
    bash -c "\"$PYTHON\" scripts/release-gate/openapi_extensions.py check internal/docs --rules internal/router/access.go && \"$PYTHON\" scripts/release-gate/openapi_extensions.py --selftest"

  # T310-10 / doc/304 §7 item 48: "how do I turn the docs surface on" was answerable on exactly
  # one machine, because the recipe lived in `localenv/151-test/deploy.sh` — a path `.gitignore`
  # excludes. A prose runbook rots silently, so the recipe is now a script whose selftest reads
  # the variable names out of `internal/config/config.go`: if the product renames
  # `RGX_DOCS_ENABLED`, this row goes red instead of exporting a name nobody reads (E159 shape,
  # which is exactly how the docs surface was dark on 151 for two releases).
  run_check "s34.docs-surface-recipe" "static" 1 "the recipe that enables the /docs surface names only variables the product actually reads, is idempotent, and refuses a stale recipe or a missing env file (doc/304 §4 E174, §7 item 48)" \
    bash -c "bash scripts/enable-docs-surface.sh --selftest"

  run_check "s08.fe-typecheck" "static" 1 "frontend typecheck" bash -c 'cd web && npx tsc -b'
  run_check "s09.fe-test" "static" 1 "frontend unit tests" bash -c 'cd web && npx vitest run --maxWorkers=2'

  # doc/304 §7 item 38: "it also passes in a fresh clone" had no entry point — three stumbles
  # in one round, two of them about the method rather than the code. This row runs the entry
  # point that now exists: a worktree of HEAD, one cheap row inside it, `--ci` so the doc
  # half is N/A (which is what release.yml preflight sees). That is deliberately a *shape*
  # claim — the tool still works — not "this tree is clean": the commit claim belongs to
  # release.yml preflight (fresh checkout, every tag) and to `clean-clone.sh --selftest`
  # (three mutations, including deleting the copied doc/ so a silent N/A turns red).
  run_check "s22.clean-clone" "static" 1 "clean-clone.sh still builds a worktree and runs a row in it (doc/304 §7 item 38)"     bash scripts/release-gate/clean-clone.sh --ci -- --only s19.docs-ledger-snapshot

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

  # The other half of "it also passes in a clean clone" (doc/304 §7 item 39): a checkout that
  # carries doc/ and nothing untracked, where the doc rows must print PASS. It is in the build
  # tier, not static, because of what the item itself says about the trigger — it answers a
  # question that is asked before a release, and it costs ~2.5 minutes (a worktree, an 850KB
  # doc copy, and s13 recompiling cold in it). A loop that runs before every commit should not
  # pay that; `--tier all` does, and that is what a release run is.
  run_check "s24.doc-present" "build" 1 "the doc rows really run in a checkout that carries doc/ and nothing untracked (doc/304 §7 item 39, needs the docs)" \
    bash scripts/release-gate/doc-present.sh

  # The fourth equation (doc/304 §4 E164, doc/310 §22.4 T310-12): the tarballs carry the
  # committed contract, and so must the *image*, which `docker/Dockerfile` compiles a second
  # time with no earlier check looking at the result. This row builds the image from this tree
  # and asks it over HTTP; `release.yml` asks the *published* per-platform digests the same
  # way, because a local build proves the recipe, not the artifact that was pushed.
  run_check "s31.release-image-spec" "build" 1 "the image serves the committed contract: /docs/openapi.json bytes == git index bytes (doc/310 §22.4 T310-12; doc/304 §4 E164)" \
    bash scripts/release/image-spec.sh gate "${VERSION:-v0.0.0-image}"
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

    # A green live run is not evidence that it left nothing behind: on 2026-11-28 a 9/9
    # run had logged one `level=ERROR`, a refusal filed as a delivery failure (doc/324).
    # Every other live row is HTTP-shaped, so a recovered panic, a swallowed bookkeeping
    # failure or a refusal logs and passes. These two rows read the deployment's own log
    # and assert the *delta* around the suites — a deployment that has been up for weeks
    # carries old errors, and "this run added none" is the claim worth making.
    if [ -z "${RGX_GATE_LOG_CMD:-}" ]; then
      # Reported as SKIP, not as PASS: the rows are required, so the run comes out
      # INCOMPLETE and cannot be `releasable`. Zero measurement is not a pass.
      record "l03.errors-baseline" "live" "1" "SKIP" "0" "" "RGX_GATE_LOG_CMD not set: the deployment log was never read"
      record "l04.no-new-errors" "live" "1" "SKIP" "0" "" "RGX_GATE_LOG_CMD not set: no delta could be measured"
      printf '[SKIP] %-30s no RGX_GATE_LOG_CMD (the log is not reachable from here)
' "l03.errors-baseline / l04.no-new-errors"
    else
      run_check "l03.errors-baseline" "live" 1 "count the deployment's error lines before the suites run"         bash "$ROOT/scripts/release-gate/log-error-delta.sh" baseline "$OUT"
    fi

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

    if [ -n "${RGX_GATE_LOG_CMD:-}" ]; then
      run_check "l04.no-new-errors" "live" 1 "the suites added no error line to the deployment log"         bash "$ROOT/scripts/release-gate/log-error-delta.sh" compare "$OUT"
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

# The last line of defence for E98: a run that recorded no check at all is not a PASS,
# whatever route got it here (an empty `--skip` set, a tier with nothing under it).
if [ "$RUNS" -eq 0 ]; then
  echo "release-gate: 0 checks ran (tier=$TIER profile=$PROFILE only='$ONLY' skip='$SKIP')" >&2
  echo "  a report with no checks is a usage error (exit 2), not a PASS — nothing was verified" >&2
  exit 2
fi

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
