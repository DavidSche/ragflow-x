#!/usr/bin/env bash
# s13.assurance-gold — doc/301 §11.1 G-A1..A8, as a check that can go red.
#
# `internal/service/report_release_gate.go` is the gate *as a function*, and its own
# tests prove the function: twelve rows, every row able to fail, no denominator means
# `unverified` (never `pass`), and the Clopper-Pearson bound is the arithmetic 301
# quotes. What the Go tests structurally *cannot* check is the other half of the same
# promise:
#
#   - the table has to reach a human-readable log, with `n` on every row. A green
#     `go test` line says "it passed", not "how much it proved";
#   - `44117-44123` has to be registered in **five** places, and one of them
#     (`doc/specification/12`) is not Go at all. A code minted on four sides out of five
#     is exactly the drift that ships;
#   - `assurance_gate=required` has to stay unrepresentable in v0.7.0.
#
# Nothing here re-implements a decision the Go gate already made. It re-reads the gate's
# own output and refuses to summarise it into a shorter, greener sentence.
#
# What this script does NOT cover, said out loud because a green line invites the other
# reading: production `coverage >=85%` needs real traffic and lives in T16's live drill,
# not here.
#
# Usage:
#   bash scripts/release-gate/assurance.sh             # the check
#   bash scripts/release-gate/assurance.sh --selftest  # prove the check can fail
#
# The `ASSURANCE_*_FILE` / `ASSURANCE_SKIP_GO` overrides exist for `--selftest` only:
# a check that has never been observed refusing something is a check nobody should
# trust, and proving that must not require editing tracked files.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

CODES_FILE="${ASSURANCE_CODES_FILE:-internal/service/report_codes.go}"
CODESTEST_FILE="${ASSURANCE_CODESTEST_FILE:-internal/service/report_codes_test.go}"
SPEC_FILE="${ASSURANCE_SPEC_FILE:-doc/specification/12_前后端接口交互规范.md}"
FEMAP_FILE="${ASSURANCE_FEMAP_FILE:-web/src/resources/conversation-center/report/report-contract.ts}"
FETEST_FILE="${ASSURANCE_FETEST_FILE:-web/src/resources/conversation-center/report/report-contract.test.ts}"
SKIP_GO="${ASSURANCE_SKIP_GO:-0}"

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

CODES="44117 44118 44119 44120 44121 44122 44123"

# codes_in_role: every code must appear *in the position that carries meaning* in that
# file. A plain `grep 44117` would also match a comment that says "44117 is ..." — and
# the comments in these very files do say exactly that. The first version of this script
# was fooled by it: it deleted an assignment, kept the comment, and reported pass.
# So each place is asked in its own syntax:
#   Go constants  -> `codeX = 44117` on a non-comment line
#   specification -> a table row `| `44117` | 400 | ...`
#   frontend map  -> an object key `44117: { ... }`
codes_in_role() { # file, label, grep -E pattern with %s for the code
  local file="$1" label="$2" fmt="$3" missing="" pat
  if [ ! -f "$file" ]; then
    bad "$label: $file does not exist"
    return
  fi
  for code in $CODES; do
    pat="$(printf "$fmt" "$code")"
    grep -E "$pat" "$file" | grep -qvE '^[[:space:]]*(//|#|\*)' || missing="$missing $code"
  done
  if [ -n "$missing" ]; then
    bad "$label ($file) is missing:$missing"
  else
    good "$label"
  fi
}

if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (three mutations of the registry)"

  # ① a gap: one assignment deleted while its comment survives. A bare digit grep is
  # satisfied by the comment, which is why the real check reads syntax, not digits.
  grep -v '^[[:space:]]*codeReportAssuranceQuotaExhausted' "$CODES_FILE" >"$tmp/codes_gap.go"
  out="$(ASSURANCE_SKIP_GO=1 ASSURANCE_CODES_FILE="$tmp/codes_gap.go" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'missing: 44123'; then
    say "SELFTEST FAIL: an assignment deleted (comment kept) was accepted"
    say "$out"
    exit 1
  fi
  say "ok      assignment deleted, comment kept -> refused"

  # ② the copy-paste risk: a 24th code appended while all 23 stay put. The family is
  # closed, and this is the only case an "each of the seven is present" grep waves through.
  cp "$CODES_FILE" "$tmp/codes24.go"
  printf '\tcodeReportBogusExtra = 44124\n' >>"$tmp/codes24.go"
  out="$(ASSURANCE_SKIP_GO=1 ASSURANCE_CODES_FILE="$tmp/codes24.go" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'want 23'; then
    say "SELFTEST FAIL: a 24th report code was accepted"
    say "$out"
    exit 1
  fi
  say "ok      24th code appended -> refused"

  # ③ a spec row that only appears inside prose: the row-shape check must not be
  # satisfiable by the words "44123 is the only 429".
  grep -v '^|[[:space:]]*`44123`' "$SPEC_FILE" >"$tmp/spec_gap.md"
  if ! grep -q '44123' "$tmp/spec_gap.md"; then
    # The prose mentioning 44123 lived on the same line as the row; deleting the row
    # deleted the mention too, so inject a prose-only mention to keep the case honest.
    printf '\n44123 is mentioned in prose but has no table row.\n' >>"$tmp/spec_gap.md"
  fi
  out="$(ASSURANCE_SKIP_GO=1 ASSURANCE_SPEC_FILE="$tmp/spec_gap.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'missing: 44123'; then
    say "SELFTEST FAIL: a spec that mentions 44123 only in prose was accepted"
    say "$out"
    exit 1
  fi
  say "ok      specification row removed, prose mention kept -> refused"

  say "selftest: PASS"
  exit 0
fi

# ── ① the gate's own proof suite ────────────────────────────────────────────
say "G-A1..A8 offline replay (doc/301 §11.1)"
say "  scope: the committed gold-set fixture in internal/service"
say "  not in scope: production coverage (real traffic -> T16 live drill)"

if [ "$SKIP_GO" != "1" ]; then
  export GOCACHE="${GOCACHE:-$ROOT/tmp/gocache}"
  export GOTMPDIR="${GOTMPDIR:-$ROOT/tmp}"
  log="$(mktemp)"
  trap 'rm -f "$log"' EXIT
  if go test ./internal/service/ -count=1 -run '^(TestReleaseGatePassesOnAClaimedFixture|TestEveryReleaseGateRowCanFail|TestMissingDenominatorsAreUnverifiedNotPassing|TestTheFalseCertificationCeilingIsAnIntervalNotAPointEstimate|TestTheAccountingRowRefusesToBeVacuouslyGreen|TestTheCoverageRowCatchesARoundingHundredPercent|TestClopperPearsonUpperMatchesKnownValues)$' -v >"$log" 2>&1; then
    good "the gate can fail: 7 property tests green (12 rows, every row breakable)"
  else
    bad "the gate's proof suite is red (a row that cannot fail, or an interval standing in for a point estimate):"
    sed -n '1,80p' "$log"
  fi
  rm -f "$log"

  # ── ② the table, with its denominators, in the log ────────────────────────
  table="$(go test ./internal/service/ -count=1 -run '^TestReleaseGatePrintsTheTableForTheGateScript$' -v 2>&1)"
  rows="$(printf '%s\n' "$table" | grep -c 'gate-row: id=')"
  if [ "$rows" != "12" ]; then
    bad "the log carries $rows gate rows, want 12 — a shorter table is a greener-looking gate, not a better one"
  else
    good "12 rows printed, each with its own sample size"
  fi
  notpass="$(printf '%s\n' "$table" | grep 'gate-row: id=' | grep -vc 'verdict=pass' || true)"
  if [ "${notpass:-0}" != "0" ]; then
    bad "$notpass row(s) are not pass on the committed fixture:"
    printf '%s\n' "$table" | grep 'gate-row: id=' | grep -v 'verdict=pass'
  else
    good "every row is pass on the committed fixture (no unverified, no fail)"
  fi
  missing_n="$(printf '%s\n' "$table" | grep 'gate-row: id=' | grep -vc 'sample=' || true)"
  if [ "${missing_n:-0}" != "0" ]; then
    bad "$missing_n row(s) print no sample size"
  else
    good "every row prints its sample size"
  fi
  if printf '%s\n' "$table" | grep -q 'alpha=0.025'; then
    good "the interval alpha (0.025 = the one-sided half of a two-sided 95%) is printed"
  else
    bad "the Clopper-Pearson alpha is not printed: the two upper-bound rows would read as point estimates"
  fi
  # The two upper-bound rows claim an *interval*; their observed value must be the
  # bound, not the point estimate. A1-01 once printed 0.0000 and looked perfect because
  # the fixture had no false positives at all.
  for id in A1-01 A1-08; do
    line="$(printf '%s\n' "$table" | grep "gate-row: id=$id " || true)"
    if [ -z "$line" ]; then
      bad "$id is missing from the table"
    elif ! printf '%s\n' "$line" | grep -q 'point '; then
      bad "$id prints no Clopper-Pearson bound"
    else
      good "$id reports its interval bound ($(printf '%s\n' "$line" | sed -E 's/.*observed=//'))"
    fi
  done

  # ── ④ G-A8: the setting that must remain unrepresentable ──────────────────
  if go test ./internal/service/ -count=1 -run '^TestAssuranceGateRefusesRequiredBeforeCalibration$' >/dev/null 2>&1; then
    good "G-A8: the validator refuses assurance_gate=required"
  else
    bad "G-A8: nothing green proves assurance_gate=required is refused"
  fi
fi

# ── ③ G-A4: 44117-44123 across five places ─────────────────────────────────
# Five, because there are five readers of that closed set and they fail differently:
# the Go constants decide; the Go test pins the count and the absence of a 24th; the
# spec tells an integrator; the frontend map decides which message renders; its test
# pins the list. Any one of them alone looks fine.
say ""
say "G-A4 error-code closed set (44117-44123)"
codes_in_role "$CODES_FILE" "Go constants"                        '=[[:space:]]*%s([^0-9]|$)'
codes_in_role "$SPEC_FILE"  "API specification (specification/12)" '^\|[[:space:]]*`%s`'
codes_in_role "$FEMAP_FILE" "frontend error map (report-contract.ts)" '^[[:space:]]*%s:'

# The two *tests* pin the set structurally (a loop over 44101..44123, `codes[22]`,
# length 23) rather than by listing literals — which is better than what a literal grep
# can prove, so they are checked for the guard, not for the digits.
if [ -f "$CODESTEST_FILE" ] && grep -q '44124' "$CODESTEST_FILE" && grep -qE '!= 23|len\(reportErrorCodes\)' "$CODESTEST_FILE"; then
  good "Go closed-set test pins the count and forbids a 24th code"
else
  bad "Go closed-set test does not pin 23 codes / does not forbid 44124 ($CODESTEST_FILE)"
fi
if [ -f "$FETEST_FILE" ] && grep -q '44123' "$FETEST_FILE" && grep -q '23' "$FETEST_FILE"; then
  good "frontend map test pins the last code and the count"
else
  bad "frontend map test does not pin the closed set ($FETEST_FILE)"
fi

# Counting the assignments is the check a literal grep cannot fake: 22 means a gap, 24
# means a 24th code, and both would otherwise pass an "each of the seven is present"
# test while the registry quietly disagrees with the spec.
assigned="$(grep -cE '^[[:space:]]*code[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*441[0-9]{2}' "$CODES_FILE" || true)"
if [ "${assigned:-0}" = "23" ]; then
  good "the registry assigns exactly 23 codes (no gap, no 24th)"
else
  bad "the registry assigns ${assigned:-0} codes, want 23 (a gap and a 24th code both break the closed set)"
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "assurance-gold: FAIL"
  exit 1
fi
say "assurance-gold: PASS (offline fixture; production coverage is T16's live-drill row, not this script's)"
exit 0
