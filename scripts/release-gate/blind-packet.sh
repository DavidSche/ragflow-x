#!/usr/bin/env bash
# s21.blind-packet — the annotator reads a file that has the model's opinion taken out of it.
#
# doc/301 §9.1 asks for two *independent* annotators. Until 2026-11-24 that independence was a
# sentence in the manual: "工作表里它就在那儿，请当作不存在". The worksheet is the only file an
# annotator opens, and it carries `prior_support_status` on every row (the stratification key,
# and the receipt's class mix) — so the rule asked a person to unsee a column. E71 had already
# closed the same hole one step earlier for the *skeleton* (`labels --stub` never echoed it);
# nothing looked at the file a person reads.
#
# What this row holds (doc/304 §4 E112, §7 item 36):
#
#   ① the code: every blind-packet test runs, and it is still the same eleven — a deleted test
#     leaves the suite green, which is how a rule becomes a habit again;
#   ② the surface: `goldtool --help` shows `--blind-out` and `--packet`. A flag only the author
#     knows about is not a flag; the tool's usage string is the page a tired operator reads;
#   ③ the manual: it documents both flags and no longer asks the reader to pretend.
#
# Why the end-to-end run is not here: building a pool in shell would be a third copy of the
# item schema, and a fixture no test validates is worse than no fixture. The chain
# (sample --blind-out -> labels --stub --packet -> refusal on a leak / on a different row set)
# is exercised in Go against the real `tmp/gold/pool-v5.jsonl` one layer up, and its readings
# are in doc/304 §5.3 beside E112.
#
# Usage:
#   bash scripts/release-gate/blind-packet.sh             # the check (hermetic, ~10s)
#   bash scripts/release-gate/blind-packet.sh --selftest  # prove the check can fail
#
# BP_MANUAL, BP_SRC and BP_TEST_OVERLAY exist for `--selftest` only: proving a refusal must
# not require editing a tracked file.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

MANUAL="${BP_MANUAL:-doc/operations/gold-labeling-manual.md}"
SRC="${BP_SRC:-cmd/goldtool/blind.go}"
OVERLAY="${BP_TEST_OVERLAY:-}"

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# The eleven tests that make up the packet contract. Named one by one because "the suite
# passed" is not the same claim as "these eleven still exist".
REQUIRED_TESTS=(
  TestThePacketCarriesNoPriorAndNoGold
  TestThePacketRemovesOnlyThoseKeys
  TestThePacketKeepsTheWorksheetIdsInOrder
  TestThePacketMayNotReplaceTheWorksheet
  TestALabelledRowIsRefusedNotStripped
  TestTheCandidateSideStripsTheSameKeys
  TestNoBlindFlagMeansNoBlindBlock
  TestStubFromAPacketChecksThePacketIsBlind
  TestStubRefusesAPacketThatLeakedThePrior
  TestStubRefusesAPacketThatIsNotTheSameRows
  TestThePacketKeepsTheFrameWhenItDropsTheScopeLabel
)

# Git Bash's /tmp is not a real filesystem root: `go` reads a path like /tmp/x as a
# drive-relative one, so an -overlay mapping must carry the Windows spelling of both sides.
winpath() { (cd "$1" && pwd -W) 2>/dev/null || printf '%s' "$1"; }

# The pattern is derived from the list, not written twice: `-run 'Blind|Packet'` looked right
# and silently skipped two of the eleven (`TestALabelledRowIsRefusedNotStripped`,
# `TestTheCandidateSideStripsTheSameKeys` — neither name contains "blind" or "packet").
run_pattern() {
  local IFS='|'
  printf '^(%s)$' "$*"
}

run_tests() {  # writes `--- PASS:` lines to stdout, returns go test's status
  local overlay_args=()
  [ -n "$OVERLAY" ] && overlay_args=("-overlay=$OVERLAY")
  GO_TEST_RUN_PATTERN="$(run_pattern "${REQUIRED_TESTS[@]}")"
  go test ./cmd/goldtool/ -run "$GO_TEST_RUN_PATTERN" -count=1 -v "${overlay_args[@]}" 2>&1
}

# ── selftest: four mutations, each of which must be refused ────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a manual that lost the flag, the sentence this round removed put back, a test that vanished, a strip that stopped stripping)"

  # ① the manual loses `--packet`. It still documents `--blind-out`, still describes the
  # packet, and reads as if nothing were missing — the exact way a released manual ages.
  sed 's/--packet/[--packet]/g' "$MANUAL" > "$tmp/manual-nopacket.md"
  out="$(BP_MANUAL="$tmp/manual-nopacket.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -qE 'does not document .*--packet'; then
    say "SELFTEST FAIL: a manual without --packet was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a manual that dropped --packet -> refused"

  # ② the sentence this round removed, put back. It was true until the packet existed, so
  # nothing about it looks wrong on the page; a check that only greps for the *presence* of
  # the new flags would pass a manual that still asks the reader to pretend.
  awk '/^3\. \*\*不看/ {print $0 "工作表里它就在那儿，请当作不存在。"; next} {print}' \
    "$MANUAL" > "$tmp/manual-back.md"
  if cmp -s "$MANUAL" "$tmp/manual-back.md"; then
    say "SELFTEST FAIL: mutation (2) did not change $MANUAL (the rule 3 anchor moved)"
    exit 1
  fi
  out="$(BP_MANUAL="$tmp/manual-back.md" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'rule 3 still asks the annotator to pretend'; then
    say "SELFTEST FAIL: '请当作不存在' came back unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      'the prior is there, treat it as absent' re-added -> refused"

  # ③ one test renamed. The suite still compiles, still passes, and the summary still says
  # "ok" — only the named list notices, which is why the list is the check.
  cp cmd/goldtool/blind_test.go "$tmp/blind_test.go"
  sed 's/^func TestThePacketMayNotReplaceTheWorksheet(/func TestThePacketReplacesNothing(/' \
    "$tmp/blind_test.go" > "$tmp/renamed_test.go"
  printf '{"Replace":{"%s":"%s"}}\n' \
    "$(winpath cmd/goldtool)/blind_test.go" "$(winpath "$tmp")/renamed_test.go" > "$tmp/overlay.json"
  out="$(BP_TEST_OVERLAY="$tmp/overlay.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'TestThePacketMayNotReplaceTheWorksheet'; then
    say "SELFTEST FAIL: a renamed (i.e. deleted) test went unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      a test renamed out of the file -> refused"

  # ④ the strip loop disabled. This is the mutation that matters most: the packet still gets
  # written, still names its sha256, still has the right rows — and hands the annotator the
  # prior back. A check that only reads the receipt cannot see it, which is why (1) reads the
  # tests and not an artefact.
  awk '/^func blindLine\(/ {inb=1} inb && /for k := range row \{/ {print "\tfor k := range map[string]json.RawMessage{} {"; next} {print}' \
    "$SRC" > "$tmp/blind-nostrip.go"
  if cmp -s "$SRC" "$tmp/blind-nostrip.go"; then
    say "SELFTEST FAIL: mutation (4) did not change $SRC (the anchor line moved); a mutation"
    say "  that cannot be applied proves nothing, so this selftest refuses to call itself green."
    exit 1
  fi
  printf '{"Replace":{"%s":"%s"}}\n' \
    "$(winpath cmd/goldtool)/blind.go" "$(winpath "$tmp")/blind-nostrip.go" > "$tmp/overlay-strip.json"
  out="$(BP_TEST_OVERLAY="$tmp/overlay-strip.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    say "SELFTEST FAIL: with the strip removed, the packet checks still passed"
    say "$out"
    exit 1
  fi
  if ! printf '%s' "$out" | grep -q 'TestThePacketCarriesNoPriorAndNoGold'; then
    say "SELFTEST FAIL: the strip mutation went red for another reason; the failure does not name"
    say "  the test that pins the strip, so nothing here proves the strip is what was checked."
    say "$out"
    exit 1
  fi
  say "ok      a packet that no longer strips prior_*/gold_* -> refused"

  say "SELFTEST OK"
  exit 0
fi

say "annotation packet (doc/304 §4 E112, §7 item 36) — code + surface + manual, hermetic"

# ── ① the tests still exist, and still pass ────────────────────────────────
if ! command -v go >/dev/null 2>&1; then
  bad "no go on PATH: 'I did not run the packet rules' is not 'the packet is blind'"
else
  testout="$(run_tests)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    good "go test ./cmd/goldtool/ -run 'Blind|Packet' passes"
  else
    bad "the blind-packet tests fail (exit $rc)"
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

# ── ② the flags are on the page, not only in the code ─────────────────────
tmpbin="$(mktemp -d)"
trap 'rm -rf "$tmpbin"' EXIT
if go build -o "$tmpbin/goldtool" ./cmd/goldtool 2>/dev/null; then
  help="$("$tmpbin/goldtool" --help 2>&1)"
  for flag in --blind-out --packet; do
    if printf '%s' "$help" | grep -qF -- "$flag"; then
      good "--help shows $flag (the annotation loop is discoverable, not folklore)"
    else
      bad "--help does not show $flag"
    fi
  done
  # A packet that is not the same rows must stay a usage-level refusal, not a crash: the
  # reader is the half that has to look at what is actually on disk.
  usage="$("$tmpbin/goldtool" labels --stub 2>&1)"; rc=$?
  if [ "$rc" -eq 2 ] && printf '%s' "$usage" | grep -q -- '--worksheet'; then
    good "labels --stub with no worksheet: exit 2 naming --worksheet"
  else
    bad "labels --stub with no worksheet: exit $rc (want 2), or it does not name --worksheet"
    printf '%s\n' "$usage" | head -3
  fi
else
  bad "cmd/goldtool does not build"
fi

# ── ③ the code still refuses the two ways a packet can be wrong ────────────
# The strings are the user-visible sentences, so they are also the contract: each names what
# was found (a leaked key with its line number, a row set that differs) rather than "invalid".
if [ -f "$SRC" ]; then
  for shape in "blindness is a habit again" 'are not the same rows'; do
    if grep -qF -- "$shape" "$SRC"; then
      good "blind.go still refuses: \"$shape…\""
    else
      bad "blind.go no longer says \"$shape…\": the refusal may have become a silent pass"
    fi
  done
else
  bad "$SRC is missing: the packet rules have no home"
fi

# ── ④ the manual documents the loop it asks people to run ────────────────
if [ ! -f "$MANUAL" ]; then
  # doc/ is gitignored (.gitignore:91): a clean checkout — CI, or a fresh clone — does not
  # carry it at all. The comparison then did not happen, and that is a different sentence
  # from "the two agree". Exit 3 says which; the gate records N/A under --ci and FAIL for
  # any run that could tag something.
  na=1
  say "N/A   $MANUAL (doc/operations/gold-labeling-manual.md) is not in this tree: doc/ is gitignored by policy (.gitignore:91),"
  say "      so this comparison did not run in this checkout — the code-side checks above did."
else
  for shape in 'sample --blind-out' 'labels --stub --packet'; do
    if grep -qF -- "$shape" "$MANUAL"; then
      good "the manual documents \`$shape\`"
    else
      bad "$MANUAL does not document \`$shape\`: the step exists in the tool and not in the instructions"
    fi
  done
  # Rule 3 used to be "the field is right there, treat it as absent". That is the sentence the
  # packet exists to make unnecessary; if it is back, a reader will do as it says. Scoped to
  # the rule itself: the v1.12 block at the top of the manual *quotes* the old sentence to
  # explain why it went, and a whole-file grep would refuse its own changelog.
  rule3="$(sed -n '/^3\. \*\*不看/,/^4\. /p' "$MANUAL")"
  if [ -z "$rule3" ]; then
    bad "$MANUAL has no §4 rule 3: the sentence that carries the blindness rule moved or went"
  elif printf '%s' "$rule3" | grep -qF -- '请当作不存在'; then
    bad "§4 rule 3 still asks the annotator to pretend the prior is absent: the packet is what makes it true"
  elif printf '%s' "$rule3" | grep -qF -- '标注包'; then
    good "§4 rule 3 tells the annotator what to read (the packet), not what to ignore"
  else
    bad "§4 rule 3 names neither the packet nor the old sentence: it was rewritten into something this check cannot read"
  fi
fi

say ""
if [ "$fail" -ne 0 ]; then
  say "blind-packet: FAIL"
  exit 1
fi
if [ "${na:-0}" = "1" ]; then
  say "blind-packet: N/A — the code half held (the packet is stripped and the stub checks it),"
  say "  but the manual that tells people to read the packet is not in this checkout."
  say "  Exit 3: 「prompt 里有一个文件没人能打开」 is not 「标注入读了正确的文件」."
  exit 3
fi
say "blind-packet: PASS (the packet carries no prior_*/gold_* key, the stub verifies it before writing a skeleton, the manual tells people to read it; T02's remaining blocker is still a human)"
exit 0
