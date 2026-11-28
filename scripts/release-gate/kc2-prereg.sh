#!/usr/bin/env bash
# s15.kc2-prereg — doc/301 §5.7's KC-2 experiment, registered before it has numbers.
#
# T11 is a two-week, allowed-to-fail PoC, and §9.2's discipline is that an experiment's
# decision rule is written down before the numbers exist. `goldtool kc2 prereg` freezes
# §5.7's three numbers, the two arm definitions and the plan into
# `internal/service/testdata/gold/prereg-kc2-v1.json`; `kc2 prereg --check` compares that
# file against the compiled constants `internal/service` judges with, in both directions.
#
# A `go test` line says "ok". It cannot say *which* three numbers were frozen, and it
# cannot see the document. So this check holds the three things a green test structurally
# cannot:
#
#   ① the frozen file still agrees with the compiled rule — one place to edit a threshold
#     would otherwise be two (and the second one is invisible in review);
#   ② each of the three quotes is **§5.7's own sentence**, verbatim, in the file *and* in
#     `doc/develop/301-*.md`. Turning "成本增幅 ≤30%" into "成本可控" is a rule change made
#     by a formatter, and the file, the code, and the test would all stay green;
#   ③ the decision rule and the plan quantity are printed, so "每臂 30 节" is a number in a
#     log rather than a memory (30 is a *plan* quantity: a short sample means `unverified`,
#     never a relaxed gate).
#
# What this script does NOT cover, said out loud because a green line invites the other
# reading: it does not run the experiment. It cannot — on the drill database
# `rgx_artifact_projection` is empty, so no section carries a locator hint, and
# `goldtool kc2 eval` correctly returns `unverified` (`treatment_without_hints`, exit 3).
# That live reading is doc/305 §2.17, not this script's.
#
# Usage:
#   bash scripts/release-gate/kc2-prereg.sh             # the check
#   bash scripts/release-gate/kc2-prereg.sh --selftest  # prove the check can fail
#
# The KC2_* overrides exist for `--selftest` only: proving that a check refuses something
# must not require editing tracked files.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

PREREG_FILE="${KC2_PREREG_FILE:-internal/service/testdata/gold/prereg-kc2-v1.json}"
DOC_301="${KC2_301_FILE:-$(ls doc/develop/301-*.md 2>/dev/null | head -n 1)}"
SKIP_GO="${KC2_SKIP_GO:-0}"

# The Go test resolves paths against its own package directory, so an override is made
# absolute here rather than left to depend on who ran the script.
case "$PREREG_FILE" in
  /*) ;;
  *) PREREG_FILE="$ROOT/$PREREG_FILE" ;;
esac

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# ── selftest: three mutations, each of which must be refused ─────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a drifted threshold, a paraphrased quote, a deleted sentence)"

  # ① a threshold edited in the frozen file: 10pp becomes 8pp. The experiment becomes
  # easier without touching code, which is exactly what pre-registration exists to stop.
  sed 's/"value": 10,/"value": 8,/' "$PREREG_FILE" > "$tmp/easier.json"
  out="$(KC2_PREREG_FILE="$tmp/easier.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'kc2LiftFloorPP = 8'; then
    say "SELFTEST FAIL: a relaxed threshold in the frozen file was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a relaxed threshold in the frozen file -> refused"

  # ② a quote paraphrased on the frozen side: the number is unchanged, the sentence is no
  # longer §5.7's. A reader comparing "the same three numbers" would not notice.
  sed 's/"quote": "lift ≥10pp"/"quote": "lift 有明显提升"/' "$PREREG_FILE" > "$tmp/numb.json"
  out="$(KC2_PREREG_FILE="$tmp/numb.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'kc2LiftFloorPP quote'; then
    say "SELFTEST FAIL: a paraphrased quote in the frozen file was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a paraphrased quote in the frozen file -> refused"

  # ③ the document loses the sentence the file still states. The pair is checked from both
  # sides, because a doc that drifts is how "预注册" becomes a story.
  grep -v '成本增幅 ≤30%' "$DOC_301" > "$tmp/301.md"
  out="$(KC2_SKIP_GO=1 KC2_301_FILE="$tmp/301.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'not in 301 §5.7 verbatim: 成本增幅 ≤30%'; then
    say "SELFTEST FAIL: a sentence missing from the document was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a sentence removed from the document -> refused"

  say "SELFTEST OK"
  exit 0
fi

# ── ① the frozen file still agrees with the compiled rule ───────────────────
if [ "$SKIP_GO" != "1" ]; then
  say "KC-2 pre-registration (doc/301 §5.7) — frozen file <-> compiled constants"
  out="$(KC2_PREREG_FILE="$PREREG_FILE" go test ./cmd/goldtool -count=1 \
          -run '^TestTheFrozenKC2PreregPrintsForTheGateScript$' -v 2>&1)"
  if [ $? -ne 0 ]; then
    bad "the frozen pre-registration does not agree with the compiled rule"
    printf '%s\n' "$out" | grep -E 'kc2LiftFloorPP|kc2TraceabilityFloor|kc2CostDeltaCeiling|plan\.sections_per_arm|arm\[|not_claimed|author|schema|FAIL' | head -12
  else
    good "the frozen file agrees with internal/service's constants (bidirectional check)"
  fi

  thresholds="$(printf '%s\n' "$out" | grep -c 'kc2-threshold: key=' || true)"
  if [ "${thresholds:-0}" = "3" ]; then
    good "three thresholds are frozen (lift / traceability / cost); a fourth would be a rule nobody registered"
  else
    bad "printed ${thresholds:-0} threshold(s), want 3"
  fi
  for key in kc2LiftFloorPP=10 kc2TraceabilityFloor=1 kc2CostDeltaCeiling=0.3; do
    if printf '%s\n' "$out" | grep -q "kc2-threshold: key=${key%%=*} value=${key##*=} "; then
      good "${key%%=*} is frozen at ${key##*=}"
    else
      bad "${key%%=*} is not printed at ${key##*=}: the number would be un-auditable"
    fi
  done
  if printf '%s\n' "$out" | grep -q 'kc2-plan: sections_per_arm=30'; then
    good "the plan is 30 sections per arm (a plan quantity: short sample -> unverified, never a relaxed gate)"
  else
    bad "the plan quantity is not printed: §9.2's E2 precedent would be restated by hand"
  fi
  if printf '%s\n' "$out" | grep -q 'kc2-decision: lift ≥10pp 且回溯 100% 且成本增幅 ≤30%'; then
    good "the decision rule is §5.7's sentence"
  else
    bad "the decision rule is missing or paraphrased"
  fi

  # Red-ability, derived from the tests that ran: eleven ways to drift the file, three
  # verdicts that must map to three exit codes. Deleting a probe lowers the count. Note the
  # anchor is the subtest name, not `^---`: `go test -v` indents subtests.
  for pair in "TestKC2PreregCheckNamesEveryDrift:11:drift" \
              "TestKC2EvalExitCodes:3:verdict"; do
    name="${pair%%:*}"; rest="${pair#*:}"; want="${rest%%:*}"; kind="${rest##*:}"
    sub="$(go test ./cmd/goldtool -count=1 -run "^${name}$" -v 2>&1)"
    if [ $? -ne 0 ]; then
      bad "$name did not pass: the '$kind' direction is unproven"
      printf '%s\n' "$sub" | grep -E '^(---|    ---)' | head -12
    fi
    got="$(printf '%s\n' "$sub" | grep -c -- "--- PASS: ${name}/" || true)"
    if [ "${got:-0}" -ge "$want" ]; then
      good "$name: ${got} probe(s) (want >= ${want})"
    else
      bad "$name has ${got:-0} sub-probe(s), want >= ${want}: an unobserved direction is not a check"
    fi
  done
fi

# ── ② the three quotes and the decision rule are §5.7's, verbatim, on both sides ─
say ""
say "§5.7 sentence identity (frozen file <-> doc/301)"
if [ ! -f "$PREREG_FILE" ]; then
  bad "$PREREG_FILE does not exist"
elif [ ! -f "$DOC_301" ]; then
    # Not tracked (documents are admitted one at a time; .gitignore is the judge): a clean checkout — CI, or a fresh clone —
    # does not carry it at all. The comparison then did not happen, and that is a
    # different sentence from "the two agree". Exit 3 says which; the release gate
    # records it as N/A under --ci and as FAIL for any run that could tag.
    na=1
    say "N/A   doc/develop/301-*.md is not in this tree: documents are admitted one at a time and this one is not tracked (see .gitignore),"
  say "      so this comparison did not run in this checkout — the code-side checks above did."
else
  quotes="$(grep -o '"quote": "[^"]*"' "$PREREG_FILE" | sed 's/^"quote": "//; s/"$//' || true)"
  count="$(printf '%s\n' "$quotes" | grep -c . || true)"
  if [ "${count:-0}" = "3" ]; then
    good "the file declares three quotes"
  else
    bad "the file declares ${count:-0} quote(s), want 3"
  fi
  while IFS= read -r q; do
    [ -z "$q" ] && continue
    if grep -Fq -- "$q" "$DOC_301"; then
      good "in 301 §5.7 verbatim: $q"
    else
      bad "not in 301 §5.7 verbatim: $q"
    fi
  done <<< "$quotes"

  decision="$(grep -o '"decision": "[^"]*"' "$PREREG_FILE" | sed 's/^"decision": "//; s/"$//' || true)"
  if [ -n "$decision" ] && grep -Fq -- "$decision" "$DOC_301"; then
    good "the decision rule is in 301 §5.7 verbatim"
  else
    bad "the decision rule is not in 301 §5.7 verbatim: $decision"
  fi
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "kc2-prereg: FAIL"
  exit 1
fi
if [ "${na:-0}" = "1" ]; then
  say "kc2-prereg: N/A — every check that could run held, but 301 is not in this"
  say "  checkout, so the three quotes were not compared against it. Exit 3."
  exit 3
fi
say "kc2-prereg: PASS (offline instrument; the live reading is doc/305 §2.17, not this script's)"
exit 0
