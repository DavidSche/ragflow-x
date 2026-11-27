#!/usr/bin/env bash
# s17.adjudicate — the last hand-transcribed file in the two-annotator loop is gone.
#
# T02's chain is `sample -> labels --stub -> labels --check -> adjudicate --stub ->
# adjudicate --check -> assemble -> kappa -> eval`. Until v1.11 of the manual, the fourth
# arrow did not exist: the adjudicator read two label files, worked out which items the merge
# would drop, and *retyped* the item ids into `adjudication.jsonl`. That is the same defect
# `labels` closed in v1.6, one step later, and it failed quietly in both directions — a
# dropped row is one merge retry ("unadjudicated_disagreement, 1 of N"), and a row for an
# *agreed* item silently replaces a judgement nobody disputed, with gold crediting the
# adjudicator for it.
#
# What a `go test` cannot see is whether the *manual* still tells people to do it by hand.
# So this row holds two things:
#
#   ① the code: every `TestAdjudicate*` runs, and it is still the same set of tests — a test
#     deleted from the file is a hole this row would otherwise keep calling green;
#   ② the manual: it documents the two commands, names the refusals that make a skeleton
#     trustworthy, and no longer says `--adjudication` is 「唯一必须人手写」.
#
# The chain itself (stub -> fill -> check -> assemble -> gold) lives in Go
# (`TestAdjudicateFilledStubAssemblesWithoutProblems`), not here: writing a fourth copy of
# the item schema in shell would be a fixture no test validates.
#
# What this does NOT cover, said out loud: it never runs the 120-item annotation. T02's
# blocker is a human, not a command (doc/304 §2).
#
# Usage:
#   bash scripts/release-gate/adjudicate.sh             # the check (hermetic, ~10s)
#   bash scripts/release-gate/adjudicate.sh --selftest  # prove the check can fail
#
# ADJ_MANUAL and ADJ_TEST_OVERLAY exist for `--selftest` only: proving a refusal must not
# require editing a tracked file.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

MANUAL="${ADJ_MANUAL:-doc/operations/gold-labeling-manual.md}"
OVERLAY="${ADJ_TEST_OVERLAY:-}"

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# The 13 tests that make up the equivalence, the blindness rule and the four refusals. Named
# one by one because "the suite passed" is not the same claim as "these 13 still exist": a
# deleted test disappears from a summary line without moving anything else.
REQUIRED_TESTS=(
  TestAdjudicateStubMatchesTheRowsAssembleCannotMerge
  TestAdjudicateStubBlanksOnlyTheDecisions
  TestAdjudicateFilledStubAssemblesWithoutProblems
  TestAdjudicateCheckUnfinishedIsExit3
  TestAdjudicateCheckNamesRowsTheStubNeverSaw
  TestAdjudicateCheckRefusesEditingAnAgreedFact
  TestAdjudicateCheckRefusesARowForAnAgreedItem
  TestAdjudicateRefusesTheSameAnnotatorTwice
  TestAdjudicateStubRefusesToOverwrite
  TestAdjudicateCheckRefusesACandidateOutsideTheItem
  TestAdjudicateRefusesAGoldWorksheet
  TestAdjudicateNoDisagreementsIsAnEmptyStub
  TestAdjudicateUsageIsExit2
)

# Git Bash's /tmp is not a real filesystem root: `go` reads a path like /tmp/x as a
# drive-relative one, so an -overlay mapping must carry the Windows spelling of both sides.
winpath() { (cd "$1" && pwd -W) 2>/dev/null || printf '%s' "$1"; }

run_tests() {  # writes `--- PASS:` lines to stdout, returns go test's status
  local overlay_args=()
  [ -n "$OVERLAY" ] && overlay_args=("-overlay=$OVERLAY")
  go test ./cmd/goldtool/ -run 'TestAdjudicate' -count=1 -v "${overlay_args[@]}" 2>&1
}

# ── selftest: three mutations, each of which must be refused ────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a manual that forgot the command, a manual that took it back, a test that vanished)"

  # ① the manual loses the `--check` command. The file still documents `--stub`, still names
  # the refusals, and reads as if nothing were missing — which is exactly how a released
  # manual ages when the tool moves and the prose does not.
  sed '/goldtool.exe adjudicate --check/d' "$MANUAL" > "$tmp/manual-nocheck.md"
  out="$(ADJ_MANUAL="$tmp/manual-nocheck.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -qE 'does not document .*adjudicate --check'; then
    say "SELFTEST FAIL: a manual without \`adjudicate --check\` was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a manual that dropped the --check command -> refused"

  # ② the sentence this round removed, put back. It was true until the tool existed, so
  # nothing about it looks wrong on the page; a check that only greps for the *presence* of
  # the new commands would pass a manual that still says "hand-write this one".
  { cat "$MANUAL"; printf '\n#    --adjudication 是这块里**唯一**必须人手写的文件\n'; } > "$tmp/manual-back.md"
  out="$(ADJ_MANUAL="$tmp/manual-back.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'still says the adjudication file must be hand-written'; then
    say "SELFTEST FAIL: the 'only hand-written file' sentence came back unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      the 'only hand-written file' sentence re-added -> refused"

  # ③ one test renamed. The suite still compiles, still passes, and the summary still says
  # "ok" — only the named list notices, which is why the list is the check.
  cp cmd/goldtool/adjudicate_test.go "$tmp/adjudicate_test.go"
  sed 's/^func TestAdjudicateStubRefusesToOverwrite(/func TestAdjudicateStubOverwritesNothing(/' \
    "$tmp/adjudicate_test.go" > "$tmp/renamed_test.go"
  printf '{"Replace":{"%s":"%s"}}\n' \
    "$(winpath cmd/goldtool)/adjudicate_test.go" "$(winpath "$tmp")/renamed_test.go" > "$tmp/overlay.json"
  out="$(ADJ_TEST_OVERLAY="$tmp/overlay.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'TestAdjudicateStubRefusesToOverwrite'; then
    say "SELFTEST FAIL: a renamed (i.e. deleted) test went unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      a test renamed out of the file -> refused"

  say "SELFTEST OK"
  exit 0
fi

say "adjudication skeleton (doc/304 §7 item 25) — code + manual, hermetic"

# ── ① the tests still exist, and still pass ────────────────────────────────
if ! command -v go >/dev/null 2>&1; then
  bad "no go on PATH: 'I did not run the chain' is not 'the chain works'"
else
  testout="$(run_tests)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    good "go test ./cmd/goldtool/ -run TestAdjudicate passes"
  else
    bad "the adjudicate tests fail (exit $rc)"
    printf '%s\n' "$testout" | grep -E '^(--- FAIL|FAIL|    )' | head -8
  fi
  gone=0
  for t in "${REQUIRED_TESTS[@]}"; do
    if ! printf '%s\n' "$testout" | grep -qE -- "--- PASS: $t([[:space:]]|$)"; then
      bad "$t did not run: the claim it pins is unguarded now"
      gone=1
    fi
  done
  [ "$gone" -eq 0 ] && good "all ${#REQUIRED_TESTS[@]} named tests ran (a deleted test would leave the suite green)"
fi

# ── ② the tool can be found without the manual ────────────────────────────
tmpbin="$(mktemp -d)"
trap 'rm -rf "$tmpbin"' EXIT
if go build -o "$tmpbin/goldtool" ./cmd/goldtool 2>/dev/null; then
  help="$("$tmpbin/goldtool" --help 2>&1)"
  if printf '%s' "$help" | grep -qE '^[[:space:]]*goldtool adjudicate --stub'; then
    good "--help lists adjudicate (the command is discoverable, not folklore)"
  else
    bad "--help does not list adjudicate"
  fi
  # A usage error must stay a usage error: `adjudicate` with no mode is not "0 disagreements",
  # and it is not a verdict either. Same three sentences the rest of the tool uses.
  usage="$("$tmpbin/goldtool" adjudicate 2>&1)"; rc=$?
  if [ "$rc" -eq 2 ] && printf '%s' "$usage" | grep -q -- '--stub' \
     && printf '%s' "$usage" | grep -q -- '--check'; then
    good "adjudicate without a mode: exit 2 naming --stub and --check"
  else
    bad "adjudicate without a mode: exit $rc (want 2), or it does not name both modes"
    printf '%s\n' "$usage" | head -3
  fi
else
  bad "cmd/goldtool does not build"
fi

# ── ③ the manual documents the loop it asks people to run ────────────────
if [ ! -f "$MANUAL" ]; then
    # Not tracked (doc/321 tracks doc/specification + doc/develop only): a clean checkout — CI, or a fresh clone —
    # does not carry it at all. The comparison then did not happen, and that is a
    # different sentence from "the two agree". Exit 3 says which; the release gate
    # records it as N/A under --ci and as FAIL for any run that could tag.
    na=1
    say "N/A   $MANUAL (doc/operations/gold-labeling-manual.md) is not in this tree: only doc/specification/ and doc/develop/ are tracked (doc/321) and this document is not one of them,"
  say "      so this comparison did not run in this checkout — the code-side checks above did."
else
  for shape in 'goldtool.exe adjudicate --stub' 'goldtool.exe adjudicate --check'; do
    if grep -qF -- "$shape" "$MANUAL"; then
      good "the manual documents \`$shape\`"
    else
      bad "$MANUAL does not document \`$shape\`: the step exists in the tool and not in the instructions"
    fi
  done
  # A skeleton is only trustworthy if the two ways it can be wrong are named in the page the
  # adjudicator reads: deciding twice over one person's file, and editing a fact both
  # annotators already agreed on.
  for code in same_annotator_twice adjudication_changed_agreed_fact adjudication_without_disagreement; do
    if grep -qF -- "$code" "$MANUAL"; then
      good "the manual names $code"
    else
      bad "the manual does not name $code: the refusal would be discovered at exit 1"
    fi
  done
  if grep -qF -- '唯一**必须人手写的文件' "$MANUAL"; then
    bad "$MANUAL still says the adjudication file must be hand-written: that is the hole v1.11 closed"
  else
    good "the manual no longer calls --adjudication the only hand-written file"
  fi
  rows="$(grep -cE '^\| 2b \| ' "$MANUAL" || true)"
  if [ "${rows:-0}" -eq 2 ]; then
    good "the manual's exit-code table carries both 2b rows (--stub and --check)"
  else
    bad "the manual's exit-code table has ${rows:-0} 2b row(s), want 2"
  fi
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "adjudicate: FAIL"
  exit 1
fi
if [ "${na:-0}" = "1" ]; then
  say "adjudicate: N/A — the tool half held (it derives the skeleton from the two"
  say "  label files), but the manual that documents the loop is not in this"
  say "  checkout. Exit 3: 「prompt 里有一个文件没人能打开」 is not 「文档齐了」."
  exit 3
fi
say "adjudicate: PASS (skeleton derived from the two label files; the manual documents it; T02's remaining blocker is a human)"
exit 0
