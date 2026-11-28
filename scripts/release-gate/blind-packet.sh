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
#   ③ the manual: it documents both flags and no longer asks the reader to pretend;
#   ④ the older half (doc/304 §7 item 37): a worksheet drawn *before* v1.12 gets a packet from
#     the new `goldtool packet`, and the stub accepts it — otherwise the rule covers every draw
#     made after the flag and nothing that T02's 120 annotations actually come from;
#   ⑤ the runbook's file names (doc/304 §4 E122): §5 on, every `--packet tmp/gold/…` input must
#     exist where `tmp/gold/` does. v1.13 derived `packet-*.jsonl` while §5.1/§6 still said
#     `worksheet-*.packet.jsonl`; the text agreed with itself, so only the filesystem could
#     tell — and the person who found out was the one who copied the command.
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

# The tests that make up the packet contract. Named one by one because "the suite passed" is
# not the same claim as "these still exist". The last five are the writer for worksheets that
# predate the flag (doc/304 §7 item 37): without them the rule covers only draws made after
# v1.12, which is exactly the gap item 37 was opened for.
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
  TestPacketIsDerivedFromAnOlderWorksheet
  TestThePacketCommandFeedsTheReaderThatChecksIt
  TestThePacketCommandRefusesToOverwriteItsWorksheet
  TestALabelledFileCannotBecomeAPacket
  TestThePacketCommandSaysWhenItStrippedNothing
)

# Git Bash's /tmp is not a real filesystem root: `go` reads a path like /tmp/x as a
# drive-relative one, so an -overlay mapping must carry the Windows spelling of both sides.
winpath() { (cd "$1" && pwd -W) 2>/dev/null || printf '%s' "$1"; }

# The pattern is derived from the list, not written twice: `-run 'Blind|Packet'` looked right
# and silently skipped two of them (`TestALabelledRowIsRefusedNotStripped`,
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

# ── selftest: five mutations, each of which must be refused ────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a manual that lost the flag, the sentence this round removed put back, a test that vanished, a strip that stopped stripping, a subcommand that left the usage page, a runbook that points at a file nobody has)"

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

  # ⑤ the older-worksheet route disappears from the usage page. Nothing else changes: the
  # command still exists, still works, and the operator with `tmp/gold/worksheet-primary-v1.jsonl`
  # in hand has no way to learn that. This is the mutation for doc/304 §7 item 37's *second*
  # half — the first was `sample --blind-out` existing at all.
  cp cmd/goldtool/main.go "$tmp/main.go"
  grep -v 'goldtool packet   --worksheet' "$tmp/main.go" > "$tmp/main-nopacket.go"
  if cmp -s cmd/goldtool/main.go "$tmp/main-nopacket.go"; then
    say "SELFTEST FAIL: mutation (5) did not change cmd/goldtool/main.go (the usage line moved)"
    exit 1
  fi
  printf '{"Replace":{"%s":"%s"}}
'     "$(winpath cmd/goldtool)/main.go" "$(winpath "$tmp")/main-nopacket.go" > "$tmp/overlay-usage.json"
  out="$(BP_TEST_OVERLAY="$tmp/overlay-usage.json" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'does not show .goldtool packet'; then
    say "SELFTEST FAIL: the older-worksheet route left the usage page unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      \`goldtool packet\` gone from --help -> refused"

  # ⑥ the runbook names a packet that is not on disk — the state this row was written in
  # (v1.13 derived `packet-primary-v1.jsonl` while §5.1/§6 still said
  # `worksheet-primary-v1.packet.jsonl`). Everything else is untouched and the text stays
  # internally consistent, so only a check that resolves the path against the filesystem can
  # see it. Needs `tmp/gold/` to exist: with no files there the check has no disk to read and
  # says so, exactly as the sample-side half above.
  if [ -d tmp/gold ]; then
    sed 's#--packet tmp/gold/packet-primary-v1.jsonl#--packet tmp/gold/worksheet-primary-v1.packet.jsonl#g' \
      "$MANUAL" > "$tmp/manual-stalepath.md"
    if cmp -s "$MANUAL" "$tmp/manual-stalepath.md"; then
      say "SELFTEST FAIL: mutation (6) did not change $MANUAL (the --packet path moved)"
      exit 1
    fi
    out="$(BP_MANUAL="$tmp/manual-stalepath.md" bash "$0" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'no such file exists on this tree'; then
      say "SELFTEST FAIL: a runbook that points at a packet nobody has was accepted"
      say "$out"
      exit 1
    fi
    say "ok      a \`--packet\` path with no file behind it -> refused"
  else
    say "      (mutation ⑥ not applied: tmp/gold/ is not in this tree, so no \`--packet\` path can resolve here)"
  fi

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
# The build honours the same overlay the test run does: `--selftest` mutates a tracked file
# without touching it, and a check whose *binary* came from the real tree would answer for a
# source it never read (mutation ⑤ left the usage page and the check still said ok).
build_args=()
[ -n "$OVERLAY" ] && build_args=("-overlay=$OVERLAY")
if go build -o "$tmpbin/goldtool" "${build_args[@]}" ./cmd/goldtool 2>/dev/null; then
  help="$("$tmpbin/goldtool" --help 2>&1)"
  for flag in --blind-out --packet; do
    if printf '%s' "$help" | grep -qF -- "$flag"; then
      good "--help shows $flag (the annotation loop is discoverable, not folklore)"
    else
      bad "--help does not show $flag"
    fi
  done
  # The subcommand for worksheets drawn before the flag is part of the surface too: an
  # operator with `tmp/gold/worksheet-primary-v1.jsonl` in hand reads this page first.
  if printf '%s' "$help" | grep -qF -- 'goldtool packet'; then
    good "--help shows \`goldtool packet\` (the older worksheets have a documented route)"
  else
    bad "--help does not show \`goldtool packet\`: the only worksheet nobody can blind is the one someone has already drawn"
  fi
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

# ── ③b the older half: a worksheet that predates the flag still gets a packet ──
# doc/304 §7 item 37 is about two files that were drawn before `--blind-out` existed. They are
# in tmp/ (gitignored), so this half runs only where they are: a clean checkout gets the code
# claim from the five named tests, not a silent green. The assertion is the chain the operator
# runs — derive, then read it in with the stub that checks blindness — so a packet the reader
# rejects counts as no packet at all.
for side in primary cand-all; do
  ws="tmp/gold/worksheet-$side-v1.jsonl"
  if [ ! -f "$ws" ]; then
    say "      ($ws not in this tree: the older-worksheet chain was not exercised here)"
    continue
  fi
  pk="$tmpbin/packet-$side.jsonl"
  lb="$tmpbin/labels-$side.jsonl"
  if ! "$tmpbin/goldtool" packet --worksheet "$ws" --out "$pk" --quiet 2>"$tmpbin/pkt-$side.err"; then
    bad "goldtool packet refused $ws: $(head -2 "$tmpbin/pkt-$side.err" | tr '
' ' ')"
    continue
  fi
  if grep -qoE '"(prior|gold)_[A-Za-z0-9_]*":' "$pk"; then
    bad "$(basename "$pk") still carries a prior_*/gold_* key: the file an annotator would read is not blind"
    continue
  fi
  if ! "$tmpbin/goldtool" labels --stub --worksheet "$ws" --annotator probe --out "$lb"       --packet "$pk" >"$tmpbin/stub-$side.out" 2>"$tmpbin/stub-$side.err"; then
    # The candidate side repeats `item_id` by construction (one claim, two frozen candidates).
    # Until E119 the label chain keyed on `item_id` alone, so the stub refused its own 36-row
    # worksheet; the chain now keys on `(item_id, candidate_index)`, so that refusal is gone —
    # and a refusal here is a real failure (doc/304 §7 item 40). The numbered note that used
    # to sit here was right while item 40 was open and would have been a branch that can
    # never fire once it closed, i.e. a green light nobody tests.
    bad "labels --stub --packet refused the packet derived from $ws: $(head -3 "$tmpbin/stub-$side.err" | tr '
' ' ')"
    continue
  fi
  good "$side: the worksheet drawn before v1.12 yields a packet the stub accepts (item 37's file, not a reshoot)"
done

# ── ④ the code still refuses the two ways a packet can be wrong ────────────
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

# ── ⑤ the manual documents the loop it asks people to run ────────────────
if [ ! -f "$MANUAL" ]; then
  # Not tracked (documents are admitted one at a time; .gitignore is the judge): a clean checkout — CI, or a fresh clone — does not
  # carry it at all. The comparison then did not happen, and that is a different sentence
  # from "the two agree". Exit 3 says which; the gate records N/A under --ci and FAIL for
  # any run that could tag something.
  na=1
  say "N/A   $MANUAL (doc/operations/gold-labeling-manual.md) is not in this tree: documents are admitted one at a time and this one is not tracked (see .gitignore),"
  say "      so this comparison did not run in this checkout — the code-side checks above did."
else
  for shape in 'sample --blind-out' 'labels --stub --packet' 'goldtool packet'; do
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
  # §5/§6 name the files the annotator has to have in hand. v1.13 *derived* the two packets
  # (`packet-primary-v1.jsonl`, `packet-cand-all-v1.jsonl`) while the runbook still said
  # `worksheet-primary-v1.packet.jsonl` — the name a fresh `--blind-out` would write, which on
  # this tree is nothing. The document was internally consistent (0a wrote that name, step 1
  # read it), so no amount of grepping the text could see it: the witness is the filesystem.
  # Inputs must exist; `--blind-out` is an output of a draw, so it is not checked (doc/304 §4
  # E122).
  pkts="$(sed -n '/^## 5\./,$p' "$MANUAL" | grep -oE -- '--packet +tmp/gold/[^ )\\]+' \
          | awk '{print $2}' | sed 's/[`"]*$//' | sort -u)"
  npkts="$(printf '%s\n' "$pkts" | grep -c . || true)"
  if [ "$npkts" -lt 2 ]; then
    bad "the manual shows $npkts \`--packet tmp/gold/…\` input(s) from §5 on: the runbook's file names were lost, so this check has nothing to read (want ≥2: the primary and the candidate side)"
  elif [ ! -d tmp/gold ]; then
    say "      (tmp/gold/ is not in this tree: the manual's \`--packet\` paths were not resolved against disk — a clean"
    say "       checkout has no annotation files at all; this half runs on the workstation that carries them)"
  else
    for p in $pkts; do
      if [ -f "$p" ]; then
        good "the manual's input \`$p\` exists (rule 3 tells people to read it; a path that is not there is not a packet)"
      else
        bad "the manual tells the annotator to read \`$p\`, and no such file exists on this tree: copying the runbook stops at the first command (the v1.13 packets are \`packet-<side>-v1.jsonl\`)"
      fi
    done
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
say "blind-packet: PASS (the packet carries no prior_*/gold_* key, the stub verifies it before writing a skeleton, the manual tells people to read it, and a worksheet drawn before the flag still gets one; T02's remaining blocker is still a human)"
exit 0
