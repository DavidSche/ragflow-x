#!/usr/bin/env bash
# s14.assurance-quality — doc/301 §9.4's five review-quality self-checks, as a check
# that can go red.
#
# `internal/service/report_quality_selfcheck.go` is §9.4 *as a function*: five rows,
# every row able to fail, no denominator means `unverified` (never `pass`), and the
# 15% ceiling is the share of abstentions rather than of claims. Its own tests prove
# the function. What a `go test` line structurally cannot show is the other half of the
# same promise:
#
#   - the table has to reach a log with `n` on every row. "ok 0.16s" says the check
#     passed, not how much it proved, and §9.4's heading is about *not being taken on
#     faith*;
#   - **the five sentences have to be §9.4's sentences.** Turning "`not_found` 为 0"
#     into "few `not_found`" is a rule change made by a formatter, and it is invisible
#     in a green test run. So each of the five rows is required to appear in the
#     document *verbatim*, as a whole table row, on both sides;
#   - each of the five has to be demonstrably able to fail, in both directions
#     (`fail` when violated, `unverified` when unmeasurable), and the count of those
#     probes is derived from the tests that actually ran rather than restated here.
#
# What this script does NOT cover, said out loud because a green line invites the other
# reading: the live numbers (Q-2 and Q-3 are `unverified` on the drill database, because
# no `cache_key` was ever replayed and no `supported` row exists there) come from
# `cmd/assurance-quality` against a real DSN and are recorded in doc/305 §2.16. This
# script runs offline; it proves the instrument, not the reading.
#
# Usage:
#   bash scripts/release-gate/assurance-quality.sh             # the check
#   bash scripts/release-gate/assurance-quality.sh --selftest  # prove the check can fail
#
# The `QUALITY_*_FILE` / `QUALITY_SKIP_GO` overrides exist for `--selftest` only: a check
# that has never been observed refusing something is a check nobody should trust, and
# proving that must not require editing tracked files.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SRC_FILE="${QUALITY_SELFCHECK_FILE:-internal/service/report_quality_selfcheck.go}"
TESTS_FILE="${QUALITY_SELFCHECK_TESTS:-internal/service/report_quality_selfcheck_test.go}"
DOC_301="${QUALITY_301_FILE:-$(ls doc/develop/301-*.md 2>/dev/null | head -n 1)}"
SKIP_GO="${QUALITY_SKIP_GO:-0}"

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# §9.4's table, extracted from the function as whole rows: `| check | expectation |
# failure means |`. Reading the *triple* matters — the expectation column alone is
# "0" and "100%", which a plain grep finds anywhere in a 150k-character document.
#
# The extractor reads Go *source*, so it undoes one level of Go escaping: the
# third row's failure-means quotes its own phrase (`有"无来源的结论"漏网`), and a
# comparison against the document has to see the compiled string, not the literal.
quality_rows() { # file
  awk '/^[[:space:]]*qualitySelfCheck[A-Za-z]+(Check|Expectation|Means)[[:space:]]*=/ {
    line=$0
    sub(/^[^"]*"/, "", line)
    sub(/"$/, "", line)
    gsub(/\\"/, "\"", line)
    n++
    slot=(n-1)%3
    buf[slot]=line
    if (n%3==0) printf "| %s | %s | %s |\n", buf[0], buf[1], buf[2]
  }' "$1"
}

# ── selftest: three mutations, each of which must be refused ─────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (two mutations of the function, one of the document)"

  # ① a paraphrase on the function side: the number is still 0, the sentence is not
  # §9.4's any more. This is the failure mode the whole second check exists for.
  sed 's/`not_found` 为 0/`not_found` 很少/' "$SRC_FILE" > "$tmp/paraphrase.go"
  out="$(QUALITY_SKIP_GO=1 QUALITY_SELFCHECK_FILE="$tmp/paraphrase.go" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'not in 301 §9.4 verbatim'; then
    say "SELFTEST FAIL: a paraphrase in the function was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a paraphrase in the function -> refused"

  # ② a triple deleted: four rows plus a green log would be a shorter, greener §9.4.
  grep -v 'qualitySelfCheckAbstain' "$SRC_FILE" > "$tmp/missing.go"
  out="$(QUALITY_SKIP_GO=1 QUALITY_SELFCHECK_FILE="$tmp/missing.go" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'declares 4 triple'; then
    say "SELFTEST FAIL: a deleted row was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a row deleted from the function -> refused"

  # ③ the document loses a row the function still states. The pair has to be checked
  # from both sides: a doc that drifts is how "跑在 CI 里" becomes a story.
  grep -v '^| `supported` 行' "$DOC_301" > "$tmp/301.md"
  out="$(QUALITY_SKIP_GO=1 QUALITY_301_FILE="$tmp/301.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'not in 301 §9.4 verbatim: | `supported` 行'; then
    say "SELFTEST FAIL: a row missing from the document was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a row removed from the document -> refused"

  say "SELFTEST OK"
  exit 0
fi

# ── ① the function's own table, on the committed fixture ────────────────────
if [ "$SKIP_GO" != "1" ]; then
  say "§9.4 Q-1..Q-5 as a function (offline fixture)"
  table="$(go test ./internal/service -count=1 -run '^TestTheQualitySelfCheckPrintsTheTableForTheGateScript$' -v 2>&1)"
  if [ $? -ne 0 ]; then
    bad "the table test did not pass"
    printf '%s\n' "$table" | tail -20
  fi
  rows="$(printf '%s\n' "$table" | grep -c 'quality-row: id=' || true)"
  if [ "${rows:-0}" = "5" ]; then
    good "the evaluator prints five rows (the script parses exactly five)"
  else
    bad "the evaluator printed ${rows:-0} row(s), want 5: §9.4 has five checks and a shorter table is greener for free"
  fi
  for id in Q-1 Q-2 Q-3 Q-4 Q-5; do
    if printf '%s\n' "$table" | grep -q "quality-row: id=$id "; then
      good "$id reaches the log"
    else
      bad "$id is missing from the log"
    fi
  done
  if printf '%s\n' "$table" | grep 'quality-row: id=' | grep -qv 'sample='; then
    bad "a row prints no sample size"
  else
    good "every row prints its sample size"
  fi
  notpass="$(printf '%s\n' "$table" | grep 'quality-row: id=' | grep -vc 'verdict=pass' || true)"
  if [ "${notpass:-0}" != "0" ]; then
    bad "$notpass row(s) are not pass on the committed fixture:"
    printf '%s\n' "$table" | grep 'quality-row: id=' | grep -v 'verdict=pass'
  else
    good "every row is pass on the committed fixture (no unverified, no fail)"
  fi
  if printf '%s\n' "$table" | grep -q 'quality-ceiling: abstain_model_uncertain=0.15'; then
    good "the abstain ceiling is printed as 0.15 (a share of abstentions, §9.4)"
  else
    bad "the 15% ceiling is not printed: the ceiling would be a constant nobody can audit"
  fi

  # Red-ability, derived from the tests that ran: six ways to fail and six ways to be
  # unmeasurable. The count is of `--- PASS:` children, so deleting a probe lowers it.
  for pair in "TestEveryQualitySelfCheckRowCanFail:6:fail" \
              "TestEveryQualitySelfCheckRowSaysUnverifiedWithoutADenominator:6:unverified"; do
    name="${pair%%:*}"; rest="${pair#*:}"; want="${rest%%:*}"; kind="${rest##*:}"
    out="$(go test ./internal/service -count=1 -run "^${name}$" -v 2>&1)"
    if [ $? -ne 0 ]; then
      bad "$name did not pass: the '$kind' direction is unproven"
      printf '%s\n' "$out" | grep -E '^(---|    ---)' | head -12
    fi
    # Subtests are indented in `go test -v` output, so the anchor is the name, not the
    # column: `--- PASS: parent/case`.
    got="$(printf '%s\n' "$out" | grep -c -- "--- PASS: ${name}/" || true)"
    if [ "${got:-0}" -ge "$want" ]; then
      good "$name: ${got} probe(s) that ${kind} (want >= ${want})"
    else
      bad "$name has ${got:-0} sub-probe(s), want >= ${want}: an unobserved direction is not a check"
    fi
  done
fi

# ── ② the five sentences are §9.4's, verbatim, on both sides ────────────────
say ""
say "§9.4 sentence identity (function <-> doc/301)"
if [ ! -f "$SRC_FILE" ]; then
  bad "$SRC_FILE does not exist"
else
  if [ ! -f "$DOC_301" ]; then
    # doc/ is gitignored (.gitignore:91): a clean checkout — CI, or a fresh clone —
    # does not carry it at all. The comparison then did not happen, and that is a
    # different sentence from "the two agree". Exit 3 says which; the release gate
    # records it as N/A under --ci and as FAIL for any run that could tag.
    na=1
    say "N/A   doc/develop/301-*.md is not in this tree: doc/ is gitignored by policy (.gitignore:91),"
  say "      so this comparison did not run in this checkout — the code-side checks above did."
  else
    rows="$(quality_rows "$SRC_FILE")"
    count="$(printf '%s\n' "$rows" | grep -c '^| ' || true)"
    if [ "${count:-0}" = "5" ]; then
      good "the function declares five (check, expectation, failure-means) triples"
    else
      bad "the function declares ${count:-0} triple(s), want 5 (a paraphrase here is a rule change)"
    fi
    while IFS= read -r row; do
      [ -z "$row" ] && continue
      if grep -Fq -- "$row" "$DOC_301"; then
        good "in 301 §9.4 verbatim: $(printf '%s' "$row" | cut -c1-46)…"
      else
        bad "not in 301 §9.4 verbatim: $row"
      fi
    done <<< "$rows"
  fi
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "assurance-quality: FAIL"
  exit 1
fi
if [ "${na:-0}" = "1" ]; then
  say "assurance-quality: N/A — every check that could run held, but 301 is not in"
  say "  this checkout, so the five sentences were not compared against it. Exit 3."
  exit 3
fi
say "assurance-quality: PASS (offline instrument; the live reading is doc/305 §2.16, not this script's)"
exit 0
