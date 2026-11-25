#!/usr/bin/env bash
# clean-clone.sh — one command for the sentence "it also passes in a fresh clone".
#
# Why this exists (doc/304 §7 item 38): every round that claimed "green in a clean clone" did
# it by hand — `git worktree`, copy `doc/` in, remember that `internal/engdocs` used to assert
# the directory *name*. Two of the three stumbles in the round that produced this script were
# about the method, not the code: a worktree at `E:/tmpgo/s21wt` turned `s07.go-test-all` red
# on a path-name assertion (fixed in E115), and `cp -r doc ./doc` nested the tree one level so
# `doc/` was "absent" and five doc rows answered N/A — a green reading of a run that had not
# checked them. A claim that needs three remembered tricks has no entry point, and without one
# "I verified it in a clean clone" is folklore.
#
# What the two halves of the claim actually are:
#
#   * a clean *checkout* (nothing but what git carries) — `.github/workflows/release.yml`
#     preflight already does this on every tag: a fresh `actions/checkout`, then
#     `scripts/release-gate.sh --tier static --ci`. In that tree `doc/` does not exist
#     (.gitignore:91), so the doc-reading rows answer N/A. Continuous, and it is the reader
#     for the code half.
#   * a clean checkout *with* the engineering docs — the case a human has to run, because
#     `doc/` is gitignored and therefore can never be in CI. Without it, "the doc rows pass"
#     is only ever tested in a working tree that has been accumulating edits.
#
# This script does the second one by default, and `--ci` reproduces the first so the two can
# be compared on the same machine. What it does NOT do, said out loud:
#
#   * no `npm install`: a worktree has no `node_modules`, so s08/s09 (frontend typecheck and
#     unit tests) are skipped. The skip is printed and repeated in the closing claim — a
#     skipped row is not a passing row. Run `make release-gate` for the full list.
#   * with `--ci` the doc rows are N/A by construction; that is the point of the flag, and
#     the closing claim says so instead of letting N/A read as green.
#   * it never claims `releasable`: the inner run is trimmed (`--skip` two rows), and the gate
#     itself refuses to call a trimmed run releasable.
#
# Usage:
#   bash scripts/release-gate/clean-clone.sh                 # copy doc/, run the static tier
#   bash scripts/release-gate/clean-clone.sh --ci            # no doc/: what release.yml sees
#   bash scripts/release-gate/clean-clone.sh --ref <rev>     # judge a commit other than HEAD
#   bash scripts/release-gate/clean-clone.sh --keep          # leave the worktree to look at
#   bash scripts/release-gate/clean-clone.sh --from-worktree # overlay what you would commit
#   bash scripts/release-gate/clean-clone.sh -- <gate args>  # caller owns the selection
#   bash scripts/release-gate/clean-clone.sh --selftest      # prove it reads the worktree
#
# CLEAN_CLONE_MUTATE exists for `--selftest` only: a bash snippet run with the worktree as its
# working directory, before the gate runs. It is the seam that makes "the reading comes from
# the worktree and not from your dirty tree" testable without corrupting the operator's own
# files — the same reason blind-packet's selftest uses `go test -overlay` instead of sed.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

REF="HEAD"
CI_DOCS=0
KEEP=0
SELFTEST=0
FROM_WT=0
GATE_ARGS=()      # empty means the default selection below
HAVE_GATE_ARGS=0

say() { printf '%s\n' "$*"; }
fail() { say "clean-clone: $*"; exit 1; }
usage() { sed -n '/^# Usage:/,/^# CLEAN_CLONE_MUTATE/p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --ref) REF="${2:-}"; shift 2 ;;
    --ref=*) REF="${1#*=}"; shift ;;
    --ci) CI_DOCS=1; shift ;;
    --keep) KEEP=1; shift ;;
    --from-worktree) FROM_WT=1; shift ;;
    --selftest) SELFTEST=1; shift ;;
    --help|-h) usage; exit 0 ;;
    --) shift; GATE_ARGS=("$@"); HAVE_GATE_ARGS=1; break ;;
    *) say "clean-clone: unknown argument: $1"; usage; exit 2 ;;
  esac
done

command -v git >/dev/null 2>&1 || fail "git is not on PATH"
git rev-parse --git-dir >/dev/null 2>&1 || fail "not a git checkout: $ROOT"

# One level only. The inner run is a real `release-gate.sh`, so if a future edit ever lets the
# full tier through here, this tool would create worktrees until the disk fills — the guard is
# cheaper than the incident.
DEPTH="${CLEAN_CLONE_DEPTH:-0}"
if [ "$DEPTH" -ge 1 ]; then
  fail "refusing to nest: CLEAN_CLONE_DEPTH=$DEPTH says this is already a clean-clone run"
fi

# A worktree cannot be created twice at the same path, and a stale one is the most likely
# reason a reader sees "already exists". Reclaim it loudly instead of dying with git's message.
WT_PARENT="${TMPDIR:-${TMP:-/tmp}}/clean-clone"
WT="$WT_PARENT/ragflow-x"
# The basename stays `ragflow-x` even though E115 removed the test that required it: a
# checkout under a different name is legitimate, but there is no reason to go looking for the
# next assumption of that shape in a tool that costs nothing to keep conventional.
mkdir -p "$WT_PARENT" || fail "cannot create $WT_PARENT"

reclaim() {
  if [ -e "$WT" ]; then
    say "clean-clone: reclaiming the stale worktree at $WT"
    git worktree remove --force "$WT" >/dev/null 2>&1 || rm -rf "$WT"
    git worktree prune >/dev/null 2>&1 || true
  fi
}

create_worktree() {
  reclaim
  git worktree add --detach --quiet "$WT" "$REF" || fail "cannot create a worktree at $WT"
  WT_SHA="$(git -C "$WT" rev-parse HEAD)" || fail "cannot read the worktree's HEAD"
  WT_SHORT="$(git -C "$WT" rev-parse --short=12 HEAD)"
  # `--from-worktree` judges "*this* working tree in a checkout shape" instead of the
  # commit. The content is every file git would carry if you committed now — `--cached`
  # plus new files, `--exclude-standard` so ignored paths (tmp/, node_modules/, doc/)
  # stay out. Without it a row added in an uncommitted round does not exist in the
  # worktree and the gate says "unknown id", which is a true statement about a commit
  # that is not the one the caller asked about (doc/304 §4 E121).
  if [ "$FROM_WT" -eq 1 ]; then
    nfiles="$(git ls-files -z --cached --others --exclude-standard | tr -cd '\000' | wc -c | tr -d ' ')"
    git ls-files -z --cached --others --exclude-standard \
      | tar -C "$ROOT" -cf - --null -T - | tar -C "$WT" -xf - \
      || fail "cannot overlay the working tree onto $WT"
    say "clean-clone: --from-worktree: overlaid $nfiles file(s) — uncommitted edits and"
    say "  new files included, ignored paths (tmp/, node_modules/, doc/) not"
  fi
  # The docs are the half CI cannot cover. `cp -a src/. dst/` and not `cp -r src dst`: the
  # latter nested doc/doc and made `doc/` "absent" for five rows (the bug item 38 records).
  if [ "$CI_DOCS" -eq 1 ]; then
    say "clean-clone: --ci: not copying the engineering docs (the published checkout shape)"
  elif [ -d "$ROOT/doc/develop" ]; then
    # `doc/` existing is NOT the predicate: `doc/user/**` ships with the repository
    # (.gitignore keeps `doc/` out but excepts `doc/user`), so a published checkout has a
    # `doc/` with nothing in it but `user/`. The first version tested `-d $ROOT/doc`, said
    # "copied doc/ from the working tree" in exactly that tree (it copied `doc/user` over
    # itself), and the six doc-reading rows then failed on files nobody had put there. The
    # predicate has to be the same one the code uses: `doc/develop` (engdocs.Present()).
    mkdir -p "$WT/doc" || fail "cannot create $WT/doc"
    for entry in "$ROOT"/doc/*; do
      base="$(basename "$entry")"
      [ "$base" = "user" ] && continue   # tracked, already in the worktree
      cp -a "$entry" "$WT/doc/$base" || fail "cannot copy doc/$base into $WT"
    done
    say "clean-clone: copied doc/ (minus doc/user, which git carries) from the working tree"
  else
    say "clean-clone: N/A — this working tree has no doc/develop, so the doc half cannot be"
    say "  verified here. Use --ci to say that out loud, or restore doc/develop and re-run."
    say "  Exit 3: a checkout without the engineering docs is the CI shape, not the case this"
    say "  script exists for."
    teardown
    exit 3
  fi
}

teardown() {
  if [ "$KEEP" -eq 1 ]; then
    say "clean-clone: left the worktree at $WT (--keep)"
    return 0
  fi
  git worktree remove --force "$WT" >/dev/null 2>&1 || rm -rf "$WT"
  git worktree prune >/dev/null 2>&1 || true
}

# run_gate <label>: the inner gate, in the worktree, with the same TEMP as this process.
run_gate() {
  local label="$1" rc
  local args=()
  if [ "$HAVE_GATE_ARGS" -eq 1 ]; then
    args=("${GATE_ARGS[@]}")
  else
    args=(--tier static --skip s08.fe-typecheck,s09.fe-test)
  fi
  # The frame of this tool is "the static tier", so a caller who only narrows (`--only s19`)
  # still gets that tier. Without it the gate answers INCOMPLETE (`l01.readyz: SKIP`, tier=all),
  # which reads like a failure of the code rather than of the invocation.
  case " ${args[*]} " in
    *" --tier "*) ;;
    *) args=(--tier static "${args[@]}") ;;
  esac
  [ "$CI_DOCS" -eq 1 ] && args+=(--ci)
  say "clean-clone: $label — bash scripts/release-gate.sh ${args[*]}"
  if [ -n "${CLEAN_CLONE_MUTATE:-}" ]; then
    ( cd "$WT" && bash -c "$CLEAN_CLONE_MUTATE" ) || fail "the mutation itself failed: $CLEAN_CLONE_MUTATE"
    say "clean-clone: applied CLEAN_CLONE_MUTATE inside the worktree"
  fi
  ( cd "$WT" && CLEAN_CLONE_DEPTH=$((DEPTH + 1)) bash scripts/release-gate.sh "${args[@]}" ) \
    >"$WT_PARENT/last-run.log" 2>&1
  rc=$?
  grep -E '^\[(FAIL|PASS)\]|^(RELEASE GATE|releasable):|^  - ' "$WT_PARENT/last-run.log" | tail -30 | sed 's/^/    /'
  # s19 compares the snapshot *in the commit* against the documents *copied from your tree*.
  # Those are two different vintages whenever someone edited doc/ and has not committed a new
  # snapshot yet, and the red is correct — but a reader who does not know that will read it as
  # "the commit is broken". Say it, only when it applies.
  if [ "$rc" -ne 0 ] && [ "$CI_DOCS" -eq 0 ] && grep -q '^  - s19.docs-ledger-snapshot: FAIL' "$WT_PARENT/last-run.log"; then
    say "clean-clone: note — s19 compares the snapshot in ${WT_SHORT} with the doc/ copied from"
    say "  your working tree. If you edited doc/ and have not committed a regenerated snapshot,"
    say "  this red is the freshness check working: run docs-ledger.sh --write-snapshot and commit."
  fi
  say "clean-clone: $label exit $rc (full log: $WT_PARENT/last-run.log)"
  return $rc
}

claim() {
  local verdict="$1"
  say ""
  if [ "$verdict" = "PASS" ]; then
    if [ "$CI_DOCS" -eq 1 ]; then
      say "clean-clone: PASS — checkout of ${WT_SHORT} at $WT, static tier minus the two"
      say "  frontend rows (a worktree has no node_modules), doc/ NOT copied: the doc-reading"
      say "  rows answered N/A. This is the shape .github/workflows/release.yml preflight runs."
    else
      say "clean-clone: PASS — checkout of ${WT_SHORT} at $WT, static tier minus the two"
      say "  frontend rows (no node_modules), doc/ copied from this working tree, so the doc"
      say "  rows really ran. Not releasable: the run was trimmed."
      [ "$FROM_WT" -eq 1 ] && say "  Content is --from-worktree: what you would commit now, not the commit."
    fi
  else
    say "clean-clone: FAIL — checkout of ${WT_SHORT} at $WT does not pass the static tier."
    say "  The log is the evidence: $WT_PARENT/last-run.log"
  fi
}

if [ "$SELFTEST" -eq 1 ]; then
  # Three runs, each answering a question the plain run cannot:
  #   ① a clean clone of an untouched commit passes (the tool works at all),
  #   ② corrupting a tracked file *inside the worktree* turns it red and names the row —
  #      if the script were reading the operator's tree, the corruption would be invisible,
  #   ③ deleting the copied `doc/` inside the worktree turns it red — the copy is load-bearing,
  #      so a green run cannot be a run that silently answered N/A.
  # A narrow selection keeps the selftest cheap. s21 is the packet reader (it compiles and runs
  # goldtool's tests) and s19 is a doc-reading row, so one run covers both halves of the claim.
  GATE_ARGS=(--only s21.blind-packet,s19.docs-ledger-snapshot); HAVE_GATE_ARGS=1
  say "selftest: can this tool be wrong in the three ways that matter?"
  bad=0
  have_doc=0
  [ -d "$ROOT/doc/develop" ] && have_doc=1   # the predicate engdocs.Present() uses

  # ① clean run
  CI_DOCS=1; create_worktree
  if run_gate "① clean checkout (--ci, narrow selection)"; then
    say "ok      ① a clean checkout of ${WT_SHORT} passes"
  else
    say "SELFTEST FAIL: ① a clean checkout did not pass; the tool cannot be trusted until this does"
    bad=1
  fi
  teardown

  # ② a tracked file corrupted inside the worktree
  CI_DOCS=1; create_worktree
  if CLEAN_CLONE_MUTATE='printf "package goldtool\n" > cmd/goldtool/blind.go' \
       run_gate "② tracked file clobbered in the worktree"; then
    say "SELFTEST FAIL: ② corrupting blind.go inside the worktree still passed — the run is not"
    say "  reading the worktree (or s21 stopped reading blind.go)"
    bad=1
  elif ! grep -q '^  - s21.blind-packet: FAIL' "$WT_PARENT/last-run.log"; then
    say "SELFTEST FAIL: ② went red for a reason that does not name s21.blind-packet"
    bad=1
  else
    say "ok      ② corrupting a tracked file inside the worktree -> refused, naming s21"
  fi
  teardown

  # ③ the copied doc/ removed (only meaningful when the source tree has docs)
  if [ "$have_doc" -eq 1 ]; then
    CI_DOCS=0; create_worktree
    if CLEAN_CLONE_MUTATE='rm -rf doc' run_gate "③ doc/ removed from the worktree"; then
      say "SELFTEST FAIL: ③ with doc/ deleted the run still passed — the doc rows answered N/A in"
      say "  a run that claims they ran"
      bad=1
    elif ! grep -q '^  - s21.blind-packet: FAIL' "$WT_PARENT/last-run.log"; then
      say "SELFTEST FAIL: ③ went red, but not on the row whose doc check needs doc/ (s21)"
      bad=1
    else
      say "ok      ③ deleting the copied doc/ inside the worktree -> refused, naming s21"
    fi
    teardown
  else
    say "skip    ③ this tree has no doc/develop, so the doc-copy half was NOT exercised"
  fi

  say ""
  if [ "$bad" -eq 0 ] && [ "$have_doc" -eq 1 ]; then
    say "SELFTEST OK — the tool reads the worktree, and the doc copy is load-bearing"
    exit 0
  fi
  if [ "$bad" -eq 0 ]; then
    # A green that quietly drops one of its three questions is the failure mode this whole
    # file is about, so the closing line names what was not asked.
    say "SELFTEST OK (partial) — the tool reads the worktree. The doc-copy mutation (③) did"
    say "  not run: no doc/develop in this tree. Partial is not the same as green."
    exit 0
  fi
  say "SELFTEST FAIL"
  exit 1
fi

create_worktree
run_gate "clean clone of ${WT_SHORT}"; rc=$?
claim "$([ "$rc" -eq 0 ] && echo PASS || echo FAIL)"
teardown
exit $rc
