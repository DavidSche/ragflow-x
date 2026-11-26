#!/usr/bin/env bash
# doc-present.sh — the half of "it also passes in a clean clone" that CI can never have.
#
# doc/304 §7 item 39. `s22.clean-clone` runs `clean-clone.sh --ci`: the published checkout
# shape, where `doc/` does not exist (.gitignore:91 is policy, not an oversight) and every
# doc-reading row answers N/A. That is the right shape for CI and the wrong shape for the
# sentence "in a tree that carries the engineering docs, the doc rows really ran". Until now
# the only automatic evidence for that sentence was `clean-clone.sh --selftest` mutation ③,
# which needs a human to type it — a check whose reader is a memory.
#
# This row asks one question, on every release run (`--tier all`), with no human:
#
#   on a checkout carrying `doc/` and nothing else untracked,
#   does every row that reads `doc/` print PASS (not N/A, not SKIP)?
#
# The checkout is `clean-clone.sh --from-worktree`: the commit's git metadata with the content
# of every file git would carry if you committed right now. That flag exists because of what
# the first version of this row found the hard way — `git worktree add HEAD` checks out the
# *commit*, and a row added in an uncommitted round is simply not in it (`--only` then answers
# "names no check in this file"), so the sentence could only ever be green after a commit.
# A row that is red for the whole of development is a row readers learn to ignore.
#
# "Nothing else untracked" is the part a working tree cannot give you: `tmp/gold/*.jsonl` is
# gitignored, so `s23`'s real-worksheet leg is *absent* in the worktree by construction. A doc
# row that quietly depends on an untracked file passes on a workstation forever and fails
# here — the only place it would ever be noticed.
#
# The selection is not written down here. Each doc-reading row carries the marker `doc` in its
# own declaration (`release-gate.sh`: `run_check "id" "tier" 1 "desc" doc cmd...`), and
# `--list=tsv` publishes it as a fourth column, so "the rows that read doc/" has one source —
# the row itself (doc/304 §4 E120 is what happens when a list gets written twice). A grep for
# `doc/` in the command would not do: `clean-clone.sh` mentions `doc/develop` and creates a
# worktree, and a row that creates worktrees cannot be part of a run that happens inside one.
#
# Three guards keep the marker honest, because a marker can be forgotten:
#   * every marked id must exist in the declarations (a renamed row is refused, not skipped);
#   * a selection of fewer than six rows is refused — it would PASS vacuously, which is the
#     failure mode of the round that produced this file (a `cp -r` that nested doc/ made five
#     rows answer N/A and read green);
#   * backwards: a script that reads a document file and answers exit 3 without doc/ must be
#     called by some row that declares doc, or the marker was dropped on that one row —
#     the likelier mistake, and one no forward check can see. "Reads a document file" is
#     "names a markdown path under doc/", and the path may be non-ASCII: the first
#     version allowed ASCII only and could not see `assurance.sh`, which reads
#     `doc/specification/12_前后端接口交互规范.md` (E123).
#
# Cost, measured on this machine 2026-11-24: 2m32s — one `git worktree add`, an ~850KB `doc/`
# copy, the working-tree overlay, and the eight doc rows. The rows are cheap in a warm tree
# (s13 10s) and not in a cold worktree (s13 95s: it compiles there), which is why this is a
# `build`-tier row and not a `static` one: item 39's own trigger is "before every release",
# and the pre-commit loop should not pay 2.5 minutes for a pre-release question.
# Item 39 declined this by guesswork; the measured number is in the 304 entry so the next
# round can argue with it instead of estimating again.
#
# Usage:
#   bash scripts/release-gate/doc-present.sh              # the row itself
#   bash scripts/release-gate/doc-present.sh --selftest   # prove it can be red
#   bash scripts/release-gate/doc-present.sh --keep       # leave the worktree behind
#   bash scripts/release-gate/doc-present.sh --check-selection  # only the selection + the marker
#                                                          guards, no worktree (selftest ④)
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SELFTEST=0
CHECK_SELECTION=0
KEEP_ARGS=()
say() { printf '%s\n' "$*"; }
bad() { say "doc-present: $*"; exit 1; }
usage() { sed -n '/^# Usage:/,/^set -uo/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --selftest) SELFTEST=1; shift ;;
    --check-selection) CHECK_SELECTION=1; shift ;;
    --keep) KEEP_ARGS=(--keep); shift ;;
    --help|-h) usage; exit 0 ;;
    *) say "doc-present: unknown argument: $1"; usage; exit 2 ;;
  esac
done

[ -d "$ROOT/doc/develop" ] || {
  say "doc-present: N/A — this tree has no doc/develop, so there is nothing whose rows could"
  say "  be seen to run. That is the CI shape (doc/ is gitignored); s22.clean-clone --ci is"
  say "  the row for it, and it records N/A out loud instead of counting it as green."
  exit 3
}

# ── the selection: the rows that declare `doc` ───────────────────────────────────────────────
# DOC_PRESENT_GATE exists for --selftest only: the mutation drops a marker from a copy of the
# gate and asks whether this row notices, without editing the operator's file.
GATE="${DOC_PRESENT_GATE:-scripts/release-gate.sh}"
LIST_TSV="$(bash "$GATE" --list=tsv)" || bad "cannot read the declarations"

DOC_IDS=()
while IFS=$'\t' read -r tier id cmd isdoc; do
  [ "${isdoc:-0}" = "1" ] || continue
  [ -n "${id:-}" ] || continue
  DOC_IDS+=("$id")
done < <(printf '%s\n' "$LIST_TSV")

report_selection() {
  say "doc-present: rows that declare \`doc\` (${#DOC_IDS[@]} rows, from --list=tsv):"
  if [ "${#DOC_IDS[@]}" -eq 0 ]; then
    say "  (none — the marker was dropped, or this gate has no doc rows)"
    return 0
  fi
  printf '  %s\n' "${DOC_IDS[@]}"
}

# A marked id that is not a declaration means the selection is describing a row that no
# longer exists — refuse instead of quietly running one row fewer. This is not decoration:
# `--list=tsv` is derived, but a derived list can still print a row id out of a stale
# comment (doc/304 §4 E120 was exactly that mistake in the other direction).
IDS_ONLY="$(printf '%s\n' "$LIST_TSV" | cut -f2)"
for id in "${DOC_IDS[@]}"; do
  grep -qxF "$id" <<<"$IDS_ONLY" || bad "marked id $id is not a declared row"
done

# A selection that is too small PASSES vacuously. Six is the number this round knows; the
# guard exists so that a future edit that drops the marker from most rows goes red rather
# than green. DOC_PRESENT_MIN_ROWS exists for --selftest only: mutation ④ walks two fixture
# rows through the same selection and would otherwise be refused for the wrong reason.
if [ "${#DOC_IDS[@]}" -lt "${DOC_PRESENT_MIN_ROWS:-6}" ]; then
  report_selection
  bad "only ${#DOC_IDS[@]} row(s) declare \`doc\`; the marker was dropped somewhere, not the docs"
fi


# The marker can be forgotten on a single row, and that is the likelier mistake, so the
# declaration is also checked backwards: a row whose command calls a script that reads a
# document file must declare doc. The predicate is "names a markdown path under doc/" — prose
# that mentions doc/304 §7 or doc/develop/313 §7 C8 does not count, which is why probes.sh and
# migration-window.sh (comments about where an item is recorded) stay out.
#
# E170 (2026-11-26): the predicate also accepted a bare `doc/develop`, so migration-window.sh —
# whose header says "doc/develop/313 §7 C8 requires 升级与回滚验证" and which answers exit 3 when
# it cannot compare two tags — read as a doc reader, and `s24` failed on a *comment*. The two
# ways to make it green were both wrong: delete the sentence from the header (removing the
# reason the script exists) or add `doc` to s32 (a marker meaning "needs doc/", which s32 does
# not). A guard that can only be satisfied by a lie teaches the next person to write the lie.
# The `.md` anchor is the honest discriminator: a reader names a file, prose names a section.
# Mutation ④ drives two fixture scripts through this predicate — one prose, one reader — and
# requires the refusal to name the reader only.
#
# E123: the predicate used to be `doc/[A-Za-z0-9._/-]+\.md`, and `assurance.sh` reads
# `doc/specification/12_前后端接口交互规范.md` — a file name with Chinese in it. The ASCII class
# stopped at `12_`, so the row that reads the *specification* was invisible to this backward
# check: drop its marker and the guard would still pass with seven rows (the "≥6" floor is not
# a substitute for looking). The character class is now "anything that is not whitespace",
# which is what a path is; the `.md` anchor is what keeps prose out.
#
# Two scripts are excluded by name: clean-clone.sh and this one *create worktrees*, and a row
# that creates worktrees cannot be part of a run that happens inside one. Their rows are
# s22/s24, and neither declares doc on purpose.
DOC_SCRIPTS=()
# DOC_PRESENT_SCRIPT_DIR exists for --selftest only: mutation ④ asks the backward predicate
# about two fixture scripts it wrote itself, instead of about whatever the repo happens to
# contain today.
for script in "${DOC_PRESENT_SCRIPT_DIR:-scripts/release-gate}"/*.sh; do
  case "$(basename "$script")" in
    clean-clone.sh|doc-present.sh) continue ;;
  esac
  grep -qE 'doc/[^[:space:]]+\.md' "$script" || continue
  grep -q 'exit 3' "$script" || continue
  DOC_SCRIPTS+=("$script")
done

unmarked=()
for script in "${DOC_SCRIPTS[@]}"; do
  base="$(basename "$script")"
  if ! printf '%s\n' "$LIST_TSV" | awk -F'\t' -v b="$base" '$4 == 1 && index($3, b) > 0 { found = 1 } END { exit !found }'; then
    unmarked+=("$script")
  fi
done
if [ "${#unmarked[@]}" -gt 0 ]; then
  report_selection
  say "doc-present: FAIL — these scripts read a document file, but no row that calls them"
  say "  declares doc, so this selection could not cover them (a doc-reading row without the"
  say "  marker would be silently left out of the run that exists to prove it ran):"
  printf '  %s\n' "${unmarked[@]}"
  exit 1
fi

if [ "$CHECK_SELECTION" -eq 1 ]; then
  report_selection
  say ""
  say "doc-present: selection OK — ${#DOC_IDS[@]} row(s) declare \`doc\`, and every script that"
  say "  names a markdown under doc/ (a reader, not a comment about a section) is called by one"
  exit 0
fi

ONLY="$(IFS=,; printf '%s' "${DOC_IDS[*]}")"

run_doc_leg() {
  local label="$1" out rc
  out="$(bash scripts/release-gate/clean-clone.sh --from-worktree "${KEEP_ARGS[@]}" -- --only "$ONLY")"
  rc=$?
  printf '%s\n' "$out"
  say ""
  if [ "$rc" -ne 0 ]; then
    say "doc-present: $label FAIL — clean-clone.sh exited $rc"
    return 1
  fi
  # PASS per row, not "the run was green": an N/A row prints no PASS line, and a run that
  # answered N/A for three of eight rows would otherwise read as success.
  local missing=() id
  for id in "${DOC_IDS[@]}"; do
    printf '%s\n' "$out" | grep -qF "[PASS] $id" || missing+=("$id")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    say "doc-present: $label FAIL — these declared-doc rows did not print PASS in the worktree:"
    printf '  %s\n' "${missing[@]}"
    return 1
  fi
  say "doc-present: $label PASS — all ${#DOC_IDS[@]} doc-reading rows printed PASS on a"
  say "  checkout that carries doc/ and no untracked files. Note what that means for s23: its"
  say "  real-worksheet leg (tmp/gold/, gitignored) did NOT run there, and the row still passed"
  say "  — so the check does not depend on a file only this workstation has."
  return 0
}

if [ "$SELFTEST" -eq 1 ]; then
  say "selftest: can this row be red in the ways that matter?"
  bad_count=0

  # ① the doc copy removed inside the worktree: the doc rows must go red and be named, or the
  #    green in the plain run means nothing. This is the question item 39 asked, asked by a
  #    row instead of by a person reading the script.
  out="$(CLEAN_CLONE_MUTATE='rm -rf doc' bash scripts/release-gate/clean-clone.sh --from-worktree -- --only "$ONLY" 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ]; then
    say "SELFTEST FAIL: ① with doc/ deleted from the worktree the run still passed — the doc"
    say "  rows answered N/A in a run that claims they ran"
    bad_count=1
  elif ! printf '%s\n' "$out" | grep -q 'RELEASE GATE:'; then
    # A leftover worktree (a killed earlier run) makes `git worktree add` refuse and the
    # clone never reaches the gate. That is an infrastructure sentence, not "the doc rows
    # stayed green" — saying so saves the next person from reading a defect into it.
    say "SELFTEST FAIL: ① the clone or the gate run did not complete (no 'RELEASE GATE:' line):"
    say "  a leftover worktree under /tmp/clean-clone, or a refusal before the rows ran. Not a"
    say "  verdict about the doc rows."
    bad_count=1
  elif ! printf '%s\n' "$out" | grep -qE '\[FAIL\] s(13|14|15|17|18|19|21|23)\.'; then
    say "SELFTEST FAIL: ① went red, but not on a row that declares \`doc\`"
    bad_count=1
  else
    say "ok      ① deleting the copied doc/ inside the worktree -> refused, naming a doc row"
  fi

  # ② the marker dropped from one row: something must refuse, and it must not need a
  #    worktree to notice. The mutation edits a *copy* of the gate script, so the operator's
  #    file is untouched (the same seam-avoidance as clean-clone's CLEAN_CLONE_MUTATE).
  tmp_gate="${TMPDIR:-${TMP:-/tmp}}/doc-present-gate.sh"
  mkdir -p "$(dirname "$tmp_gate")" || bad "cannot create $(dirname "$tmp_gate")"
  sed 's/^\(  run_check "s23\.candidate-key".*\)" doc bash/\1" bash/' \
    scripts/release-gate.sh > "$tmp_gate"
  if grep -q 'run_check "s23.candidate-key".* doc bash' "$tmp_gate"; then
    say "SELFTEST FAIL: ② the mutation did not remove the marker (the sed anchor moved)"
    bad_count=1
  elif out="$(DOC_PRESENT_GATE="$tmp_gate" bash scripts/release-gate/doc-present.sh 2>&1)"; then
    say "SELFTEST FAIL: ② dropping the marker from s23 was not noticed — the row would have"
    say "  run seven rows and reported green"
    bad_count=1
  elif ! printf '%s\n' "$out" | grep -q 'candidate-key.sh'; then
    say "SELFTEST FAIL: ② refused, but did not name the script whose marker went missing"
    bad_count=1
  else
    say "ok      ② dropping the marker from one row -> refused, naming the script, before any"
    say "         worktree was made"
  fi
  # ③ the marker dropped from the row that reads a *Chinese-named* document. E123: the
  #    backward predicate used to allow only ASCII in the path, so `assurance.sh` (which reads
  #    `doc/specification/12_前后端接口交互规范.md`) was invisible to it — this mutation passed
  #    before the class was widened, which is what makes it evidence and not decoration.
  sed 's/^\(  run_check "s13\.assurance-gold".*\)" doc bash/\1" bash/' \
    scripts/release-gate.sh > "$tmp_gate"
  if grep -q 'run_check "s13.assurance-gold".* doc bash' "$tmp_gate"; then
    say "SELFTEST FAIL: ③ the mutation did not remove the marker from s13 (the sed anchor moved)"
    bad_count=1
  elif out="$(DOC_PRESENT_GATE="$tmp_gate" bash scripts/release-gate/doc-present.sh 2>&1)"; then
    say "SELFTEST FAIL: ③ dropping the marker from s13 was not noticed — the backward check"
    say "  cannot see the script that reads a path with non-ASCII in it (E123)"
    bad_count=1
  elif ! printf '%s\n' "$out" | grep -q 'assurance\.sh'; then
    say "SELFTEST FAIL: ③ refused, but did not name assurance.sh"
    bad_count=1
  else
    say "ok      ③ dropping the marker from the row that reads doc/specification/12_…md -> refused,"
    say "         naming assurance.sh"
  fi
  rm -f "$tmp_gate"

  # ④ E170: the backward predicate must separate a *reader* from a *comment*. Two fixture
  #    scripts, both answering exit 3: `prose-only.sh` mentions `doc/develop/313 §7 C8` in a
  #    comment (exactly what migration-window.sh does), `reader.sh` names
  #    `doc/develop/313-x.md`. With neither row marked, the refusal must name reader.sh and
  #    must NOT name prose-only.sh. Before the `.md` anchor was made the only form, this test
  #    read red on the prose script too — which is how s24 failed on migration-window.sh
  #    while claiming to protect the doc rows.
  fixture_dir="${TMPDIR:-${TMP:-/tmp}}/doc-present-scripts"
  rm -rf "$fixture_dir"
  mkdir -p "$fixture_dir" || bad "cannot create $fixture_dir"
  printf '%s\n' '#!/usr/bin/env bash' \
    '# doc/develop/313 §7 C8 requires an upgrade/rollback drill before GA.' \
    'exit 3' > "$fixture_dir/prose-only.sh"
  printf '%s\n' '#!/usr/bin/env bash' \
    '# reads doc/develop/313-x.md to answer this' \
    'exit 3' > "$fixture_dir/reader.sh"
  cat > "$tmp_gate" <<'GATE'
set -uo pipefail
case "${1:-}" in
  --list=tsv)
    printf '%s\t%s\t%s\t%s\n' build s90.prose-only "bash ${DOC_PRESENT_SCRIPT_DIR}/prose-only.sh" 0
    printf '%s\t%s\t%s\t%s\n' build s91.reader "bash ${DOC_PRESENT_SCRIPT_DIR}/reader.sh" 0
    exit 0 ;;
esac
exit 0
GATE
  out="$(DOC_PRESENT_GATE="$tmp_gate" DOC_PRESENT_SCRIPT_DIR="$fixture_dir" DOC_PRESENT_MIN_ROWS=0 \
    bash scripts/release-gate/doc-present.sh --check-selection 2>&1)"
  rc=$?
  if [ "$rc" -eq 0 ]; then
    say "SELFTEST FAIL: ④ an unmarked row calling a real doc reader was accepted"
    bad_count=1
  elif ! printf '%s\n' "$out" | grep -q 'reader\.sh'; then
    say "SELFTEST FAIL: ④ refused, but did not name the script that reads a markdown under doc/"
    bad_count=1
  elif printf '%s\n' "$out" | grep -q 'prose-only\.sh'; then
    say "SELFTEST FAIL: ④ refused on a *comment* mentioning doc/develop — a guard that can only"
    say "  be satisfied by deleting the sentence from the header (E170)"
    bad_count=1
  else
    say "ok      ④ the backward predicate names the reader and not the prose -> a comment about"
    say "         doc/develop/313 §7 no longer conscripts a row (E170)"
  fi
  # The same two fixtures with the reader *marked* must pass: prove ④ is not red-by-default.
  sed 's/ 0$/ 1/' "$tmp_gate" > "$tmp_gate.marked"
  if out="$(DOC_PRESENT_GATE="$tmp_gate.marked" DOC_PRESENT_SCRIPT_DIR="$fixture_dir" \
      DOC_PRESENT_MIN_ROWS=0 bash scripts/release-gate/doc-present.sh --check-selection 2>&1)"; then
    say "ok      ④b the same two rows with the reader marked -> accepted (②'s mirror image)"
  else
    say "SELFTEST FAIL: ④b marking the reader still refused: $(printf '%s' "$out" | tail -3)"
    bad_count=1
  fi
  rm -rf "$fixture_dir" "$tmp_gate.marked"
  rm -f "$tmp_gate"

  say ""
  [ "$bad_count" -eq 0 ] && { say "SELFTEST OK (4 mutations)"; exit 0; }
  say "SELFTEST FAIL"
  exit 1
fi

report_selection
say ""
run_doc_leg "doc-present"
exit $?
