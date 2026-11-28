#!/usr/bin/env bash
# s23.candidate-key — a candidate row is a unit, and the label chain has to key on it.
#
# doc/301 §9.1.3 一 says E2's denominator is the *row*: one claim can carry two frozen
# candidates, so the sample's unit is `(item_id, candidate_index)`. The manual has said so
# since v1.2 — as advice to the annotator ("按 `(item_id, candidate_index)` 对齐"). The tools
# did not: `labels --stub` wrote one line per `item_id`, `--check` and κ matched on
# `item_id`, `adjudicate --stub` derived one case per `item_id`. On 151's candidate
# worksheet that is 36 rows read as 30 claims: two judgements per claim collapse into one,
# which of the two survives depends on file order, every gate stays green, and the κ
# denominator is quietly 30. doc/304 §7 item 40.
#
# The fix is in Go, and `go test ./cmd/goldtool/` holds it — which is exactly the gap
# §4 E112 named: a rule held only by tests can be deleted test by test, and the suite still
# says "ok". So this row holds three things a `go test` cannot:
#
#   ① the tests: the six that pin the composite key still exist and still pass (the -run
#     pattern is *derived* from the named list, so the list cannot drift from the regex);
#   ② the manual: it names the refusal code and the key, in the sections an annotator reads,
#     and the candidate side has its own runbook step (a reader who only sees §2's table
#     would still hand-key the index);
#   ③ the code: `assemble` still refuses the candidate side by name. Without that sentence
#     the failure mode is not a crash, it is a *narrower* gold file.
#
# And when the real worksheet is in the tree (it is not, in a clean clone: `tmp/` is
# gitignored), ④ re-derives the two numbers the plan states — 36 rows, 30 claims — from the
# file itself rather than from a document.
#
# Usage:
#   bash scripts/release-gate/candidate-key.sh             # the check (hermetic when tmp/ is absent)
#   bash scripts/release-gate/candidate-key.sh --selftest  # prove the check can fail
#
# CAND_MANUAL, CAND_ASSEMBLE and CK_TEST_OVERLAY exist for `--selftest` only: proving a
# refusal must not require editing a tracked file.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

MANUAL="${CAND_MANUAL:-doc/operations/gold-labeling-manual.md}"
ASSEMBLE="${CAND_ASSEMBLE:-cmd/goldtool/assemble.go}"
OVERLAY="${CK_TEST_OVERLAY:-}"
CANDWS="${CAND_WORKSHEET:-tmp/gold/worksheet-cand-all-v1.jsonl}"

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# The six named tests. `-run` is built from this list below, so a test added here is a test
# run, and a test renamed there is a hole reported by name.
REQUIRED_TESTS=(
  TestSampleCandidateSideDrawsRows
  TestCandidateStubWritesOneRowPerCandidate
  TestCandidateLabelsRefuseARowWithoutAnIndex
  TestCandidateKappaCountsRowsNotClaims
  TestAssembleRefusesACandidatesSideLabelFile
  TestAdjudicateStubKeysTheCandidatesSide
)

# Derived, not typed twice: `TestSampleCandidateSideDrawsRows|TestCandidateStub…|…`
run_pattern="$(IFS='|'; printf '%s' "${REQUIRED_TESTS[*]}")"

winpath() { (cd "$1" && pwd -W) 2>/dev/null || printf '%s' "$1"; }

run_tests() {  # writes `--- PASS:` lines to stdout, returns go test's status
  local overlay_args=()
  [ -n "$OVERLAY" ] && overlay_args=("-overlay=$OVERLAY")
  go test ./cmd/goldtool/ -run "^($run_pattern)\$" -count=1 -v "${overlay_args[@]}" 2>&1
}

# ── selftest: three mutations, each of which must be refused ────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a test renamed, the manual's key deleted, assemble's refusal deleted)"

  # ① one test renamed. The suite still compiles, still passes, the summary still says "ok";
  # only the named list notices. That is the whole reason the list exists.
  cp cmd/goldtool/candchain_test.go "$tmp/candchain_test.go"
  sed 's/^func TestCandidateKappaCountsRowsNotClaims(/func TestCandidateKappaCountsClaims(/' \
    "$tmp/candchain_test.go" > "$tmp/renamed_test.go"
  printf '{"Replace":{"%s":"%s"}}\n' \
    "$(winpath cmd/goldtool)/candchain_test.go" "$(winpath "$tmp")/renamed_test.go" > "$tmp/overlay.json"
  out="$(CK_TEST_OVERLAY="$tmp/overlay.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'TestCandidateKappaCountsRowsNotClaims'; then
    say "SELFTEST FAIL: a renamed (i.e. deleted) test went unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      a test renamed out of the file -> refused"

  # ② the manual loses the key. It still documents `labels --stub`, still names every rule;
  # what goes away is the one sentence that says two rows of the same claim are two rows.
  sed '/candidate_index/d' "$MANUAL" > "$tmp/manual-nokey.md"
  out="$(CAND_MANUAL="$tmp/manual-nokey.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'does not name'; then
    say "SELFTEST FAIL: a manual with no candidate_index in it was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a manual whose candidate-side key was deleted -> refused"

  # ③ `assemble`'s refusal deleted. Nothing else changes: the merge would still drop both
  # candidate judgements, and every other check would still pass.
  sed '/candidateSideLabels/d' "$ASSEMBLE" > "$tmp/assemble.go"
  out="$(CAND_ASSEMBLE="$tmp/assemble.go" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'assemble no longer refuses'; then
    say "SELFTEST FAIL: an assemble that silently accepts the candidate side was accepted"
    say "$out"
    exit 1
  fi
  say "ok      an assemble that dropped the candidate-side refusal -> refused"

  say "SELFTEST OK"
  exit 0
fi

say "candidate row = (item_id, candidate_index) — code + manual, hermetic when tmp/ is absent"

# ── ① the six tests still exist, and still pass ────────────────────────────
if ! command -v go >/dev/null 2>&1; then
  bad "no go on PATH: 'I did not run the chain' is not 'the chain keys on the row'"
else
  testout="$(run_tests)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    good "go test ./cmd/goldtool/ -run '${run_pattern}' passes"
  else
    bad "the candidate-key tests fail (exit $rc)"
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

# ── ② assemble still refuses the candidate side, by name ───────────────────
if grep -qF -- 'candidateSideLabels(ls)' "$ASSEMBLE"; then
  if grep -qF -- 'is keyed by (item_id, candidate_index)' "$ASSEMBLE"; then
    good "assemble refuses the candidates side and names the key it cannot use"
  else
    bad "assemble calls candidateSideLabels but no longer says why: the operator reads a key name, not the reason"
  fi
else
  bad "assemble no longer refuses the candidates side: two candidate judgements would merge into one gold row"
fi

# ── ③ the manual teaches the key, in the places people read ────────────────
if [ ! -f "$MANUAL" ]; then
  # Not tracked (documents are admitted one at a time; .gitignore is the judge): a clean checkout — CI, or a fresh clone — does not
  # carry it. The comparison then did not happen, which is a different sentence from "the
  # two agree". Exit 3 says which.
  na=1
  say "N/A   $MANUAL is not in this tree: documents are admitted one at a time and this one is not tracked (see .gitignore),"
  say "      so the manual half did not run in this checkout — the code-side checks above did."
else
  if grep -qF -- 'label_without_candidate_index' "$MANUAL"; then
    good "the manual names label_without_candidate_index (the refusal a person will hit)"
  else
    bad "the manual does not name label_without_candidate_index: the refusal would be discovered at exit 1"
  fi
  n="$(grep -cF -- '(item_id, candidate_index)' "$MANUAL" || true)"
  if [ "${n:-0}" -ge 2 ]; then
    good "the manual names the composite key in ${n} places (alignment rule + tool behaviour)"
  else
    bad "the manual names (item_id, candidate_index) ${n:-0} time(s), want at least 2 — §2's table alone is advice, not a rule"
  fi
  if grep -qF -- 'worksheet-cand-all-v1.jsonl' "$MANUAL"; then
    good "the manual has a candidate-side runbook step (a reader who copies §6 gets the right file)"
  else
    bad "the manual's runbook never points at the candidate-side worksheet: copying §6 produces an items-side label file"
  fi
fi

# ── ④ the plan's two numbers, re-derived from the worksheet (N/A off-tree) ─
if [ -f "$CANDWS" ]; then
  tmpbin="$(mktemp -d)"
  trap 'rm -rf "$tmpbin"' EXIT
  if go build -o "$tmpbin/goldtool" ./cmd/goldtool 2>/dev/null; then
    "$tmpbin/goldtool" labels --stub --worksheet "$CANDWS" --annotator s23 \
      --out "$tmpbin/labels.jsonl" >"$tmpbin/out.txt" 2>"$tmpbin/err.txt"; rc=$?
    got_rows="$(grep -o '"items": [0-9]*' "$tmpbin/out.txt" | head -1 | tr -dc '0-9' || true)"
    got_ids="$(grep -o '"item_ids": [0-9]*' "$tmpbin/out.txt" | head -1 | tr -dc '0-9' || true)"
    want_rows="$(wc -l < "$CANDWS" | tr -dc '0-9')"
    if [ "$rc" -eq 0 ] && [ "$got_rows" = "$want_rows" ] && [ "${got_ids:-0}" -gt 0 ] \
       && [ "$got_rows" -gt "$got_ids" ]; then
      good "$CANDWS: $got_rows candidate rows over $got_ids claims (rows > claims, as §9.1.3 一 says)"
    else
      bad "$CANDWS: stub says ${got_rows:-?} rows / ${got_ids:-?} claims (exit $rc, file has $want_rows lines)"
      printf '%s\n' "$(cat "$tmpbin/err.txt")" | head -4
    fi
  else
    bad "cmd/goldtool does not build"
  fi
else
  say "N/A   $CANDWS is not in this tree (tmp/ is gitignored): the 36/30 re-derivation did not run here."
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "candidate-key: FAIL"
  exit 1
fi
if [ "${na:-0}" = "1" ]; then
  say "candidate-key: N/A — the code half held (the chain keys on the row and refuses"
  say "  to guess an index), but the manual that teaches the key is not in this checkout."
  say "  Exit 3: 「prompt 里有一个文件没人能打开」 is not 「文档齐了」."
  exit 3
fi
say "candidate-key: PASS (a candidate row is keyed by (item_id, candidate_index); a file without the index is refused, not defaulted)"
exit 0
