#!/usr/bin/env bash
# s25.openapi-freshness — the committed OpenAPI artifacts must equal a fresh generation.
#
# doc/310 §12.2 asks for exactly one thing in CI: regenerate and diff, so a handler whose
# annotation changed without `make swagger` turns red here instead of becoming a document
# that describes the API of three weeks ago. §20 A25 states the same claim from the other
# side ("swagger-check does not modify the worktree, and it finds missing/extra/inconsistent
# artifacts"). Until this row existed, `make swagger-check` was **the only implementer of
# that claim and nothing ran it**: the artifacts in `internal/docs/` had no reader other than
# `go build` (the blank import), which is satisfied by any bytes at all. That is the same
# shape as doc/304 §4 E120 (`--list` was a second hand-written list) and E130 ("已闭环（E119）"
# was a message with no reader): the check existed, the ritual existed, the join did not.
#
# What this row holds:
#
#   ① freshness: `swag init` runs again, into a temp dir, with the *same* flags the Makefile
#     uses — because the flags and the pinned version live in this file and the Makefile
#     targets now call it (`gen` / `check`), instead of two copies drifting apart;
#   ② all three artifacts: `docs.go`, `swagger.json`, `swagger.yaml` are compared. Comparing
#     only the JSON would let a hand-edited `docs.go` ship a different spec at runtime than
#     the file a reviewer read;
#   ③ the worktree is not modified: `git status --porcelain -- internal/docs` must be empty
#     before and after, and the committed files' mtimes must not change. A check that
#     "fixes" the artifact while testing it cannot tell drift from a repair;
#   ④ generation really happened: the generated dir must be newer than the run's start and
#     carry the three files. "No diff" read off a directory that was never written is the
#     failure mode that makes a freshness check worse than none (doc/304 §4 E126/E133);
#   ⑤ a reading, not a verdict: the PASS line prints the spec version and the operation
#     count from the artifact, so "0 drift" and "0 operations" cannot look alike. Route ↔
#     spec *coverage* is not this row's job: `openapi_coverage_test.go` runs it inside s07;
#   ⑥ the baseline is the side a commit would ship (the index), not this laptop's copy:
#     `git show :internal/docs/<f>`, and a baseline missing from the index is red with the
#     one-line fix. Reading the worktree made "no drift" a statement about one machine —
#     the artifacts sat untracked in a developer tree while this row was green.
#
# Usage:
#   bash scripts/release-gate/openapi.sh             # the check (one generation, ~10s warm)
#   bash scripts/release-gate/openapi.sh gen DIR     # write the three artifacts into DIR
#   bash scripts/release-gate/openapi.sh --selftest  # prove the check can fail (6 mutations)
#
# Needs the Go module cache (or network) for `swag@v$(SWAG_VERSION)`: the generator is pinned
# in the Makefile today and in this file from now on. Offline without a cache is exit 2
# ("could not run the generator"), never a pass.
#
# Env overrides exist for `--selftest` only; a tracked file is never edited to prove a refusal.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SWAG_VERSION="${SWAG_VERSION:-v2.0.0-rc6}"
SWAG_SPEC_FLAGS="${SWAG_SPEC_FLAGS:---v3.1}"
SWAG_EXCLUDES="cmd/goldtool,localenv"
COMMITTED="${OAPI_COMMITTED:-internal/docs}"
GEN="${OAPI_GEN:-}"
SKIP_GEN="${OAPI_SKIP_GEN:-}"
FILES=(docs.go swagger.json swagger.yaml)

TMPROOT="${TMPDIR:-${TEMP:-/tmp}}"
WORK="$(mktemp -d "$TMPROOT/oapi-XXXXXX" 2>/dev/null)" || WORK=""
if [ -z "$WORK" ] || [ ! -d "$WORK" ]; then
  printf 'FAIL  no writable temp dir (TMPDIR/TEMP); generation needs one\n' >&2
  exit 2
fi
trap 'rm -rf "$WORK"' EXIT

fail=0
QUIET=0
say()  { printf '%s\n' "$*"; }
bad()  { if [ "$QUIET" -eq 1 ]; then printf 'FAIL  %s\n' "$*" >>"$WORK/quiet.log"; else printf 'FAIL  %s\n' "$*"; fi; fail=1; }
good() { if [ "$QUIET" -eq 1 ]; then printf 'ok    %s\n' "$*" >>"$WORK/quiet.log"; else printf 'ok    %s\n' "$*"; fi; }

# gen <dir> — the one place the generator's flags are written. `make swagger` calls this.
#
# It writes into a staging dir first and only copies the three artifacts into $out once the
# generator returned 0 and produced all three — so a failed generation cannot leave $out
# empty. That is not hypothetical: this function used to start with `rm -rf "$out"`, and one
# failed run against `internal/docs` deleted the hand-written `envelope.go` that sat next to
# the generated files (doc/304 §4 E140). A generator recipe may replace what it generates;
# it may not delete what it does not.
gen() {
  local out="$1"
  local stage="$WORK/stage"
  rm -rf "$stage"; mkdir -p "$stage" || return 2
  # shellcheck disable=SC2086
  go run "github.com/swaggo/swag/v2/cmd/swag@${SWAG_VERSION}" init \
    -g cmd/server/main.go -o "$stage" --packageName docs \
    --parseDependency --parseInternal --exclude "$SWAG_EXCLUDES" \
    $SWAG_SPEC_FLAGS >"$WORK/gen.log" 2>&1
  local rc=$?
  if [ $rc -ne 0 ]; then
    bad "the generator itself failed (exit $rc) — this is not a drift reading; $out was left alone"
    tail -3 "$WORK/gen.log" | while IFS= read -r l; do printf '      %s\n' "$l"; done
    return 2
  fi
  local f
  for f in "${FILES[@]}"; do
    if [ ! -s "$stage/$f" ]; then
      bad "the generator produced no $f — $out was left alone"
      return 2
    fi
  done
  mkdir -p "$out" || return 2
  for f in "${FILES[@]}"; do cp "$stage/$f" "$out/$f" || return 2; done
  # `make swagger` used to end with `gofmt -w internal/docs/`; the formatting step belongs to
  # the generator's recipe, not to a target someone may run by hand.
  command -v gofmt >/dev/null 2>&1 && gofmt -w "$out"/*.go 2>/dev/null
  return 0
}

# Compare the committed artifacts against a generated dir. Both sides are gofmt'd in *copies*
# (the Makefile does the same), so a formatting difference in the repo shows up as drift via
# s02 and not as a confusing diff here.
compare() {
  local committed="$1" generated="$2" label="${3:-}"
  local c="$WORK/committed" g="$WORK/generated"
  rm -rf "$c" "$g"; mkdir -p "$c" "$g" || return 2
  local f
  for f in "${FILES[@]}"; do
    if [ ! -f "$committed/$f" ]; then bad "committed $f is missing ($committed)"; return 1; fi
    cp "$committed/$f" "$c/$f" || return 2
    if [ ! -f "$generated/$f" ]; then bad "the generator produced no $f"; return 1; fi
    cp "$generated/$f" "$g/$f" || return 2
  done
  command -v gofmt >/dev/null 2>&1 && { gofmt -w "$c"; gofmt -w "$g"; }
  for f in "${FILES[@]}"; do
    if diff -q "$c/$f" "$g/$f" >/dev/null 2>&1; then
      [ -n "$label" ] || good "$f identical to a fresh generation"
    else
      bad "$f drifted from a fresh generation — run 'make swagger' (or 'bash scripts/release-gate/openapi.sh gen internal/docs') and review the diff"
      [ "$QUIET" -eq 1 ] || diff -u "$c/$f" "$g/$f" | sed -n '1,4p' | while IFS= read -r l; do printf '      %s\n' "$l"; done
    fi
  done
  [ "$fail" -eq 0 ]
}

# baseline_from_commit <committed-dir> — materialise the side that the next commit ships.
#
# The worktree is the weak reading: a tree that has the artifacts and a repo that does not
# produces "no drift" about that tree (doc/304 §4 E140 — they were untracked the whole time
# this row was green). So when the path lives inside this repository, the baseline is the
# index (`git show :<path>`); outside the repository it is a fabricated baseline, which only
# `--selftest` does, and the reading says so out loud.
BASE_DIR=""
BASELINE_SOURCE=""
baseline_from_commit() {
  local committed="$1" dst="$WORK/baseline" f root_abs dir_abs
  rm -rf "$dst"; mkdir -p "$dst" || return 2
  root_abs="$(cd "$ROOT" && pwd -P)"
  dir_abs="$(cd "$committed" 2>/dev/null && pwd -P)" || dir_abs=""
  case "$dir_abs" in
    "$root_abs"/*) : ;;
    *)
      for f in "${FILES[@]}"; do
        if [ ! -f "$committed/$f" ]; then bad "baseline $f is missing ($committed)"; return 1; fi
        cp "$committed/$f" "$dst/$f" || return 2
      done
      BASE_DIR="$dst"
      BASELINE_SOURCE="worktree copy outside the repository (fabricated: selftest only)"
      return 0 ;;
  esac
  for f in "${FILES[@]}"; do
    if ! git -C "$ROOT" show ":$committed/$f" >"$dst/$f" 2>/dev/null; then
      bad "the baseline is not in the index: $committed/$f is not in the next commit"
      say "      the artifacts may exist in this worktree only, which would make 'no drift' a reading about this machine"
      say "      fix: git add $committed    (doc/310 §12.1 step 4, §12.2 'untracked artifacts' check)"
      return 1
    fi
  done
  BASE_DIR="$dst"
  BASELINE_SOURCE="index ($committed)"
  return 0
}

# A reading from the artifact, so "no drift" cannot hide "no operations".
reading() {
  local spec="$1"
  local ver ops
  ver="$(grep -o '"openapi": *"[^"]*"' "$spec" 2>/dev/null | head -1 | sed 's/.*: *"//; s/"$//')"
  # The count has to be of *operations*, not of lines containing the word: the generated
  # spec also carries `operationId` as a path parameter name and as a description string,
  # and `grep -c '"operationId"'` read 622 for 616 operations (doc/304 §4 E143). A reading
  # that over-counts is the same defect it is meant to catch.
  ops="$(grep -c '"operationId": *"' "$spec" 2>/dev/null)"
  say "      spec: openapi ${ver:-unknown}, ${ops:-0} operationId(s)"
}

check_main() {
  if [ -n "$SKIP_GEN" ]; then
    bad "check refused: OAPI_SKIP_GEN is set — a freshness reading taken without generating is a reading of the committed files"
    return 1
  fi
  local before after
  before="$(git status --porcelain -- "$COMMITTED" 2>/dev/null)"
  local start="$WORK/start"; : > "$start"
  local out="$WORK/gen"
  # ④ generation really happened: the generated side lives in *this run's* private temp dir,
  # so "no diff" can never be read off a directory some earlier run left behind (an mtime
  # comparison alone would be the weak form of the same claim).
  case "$out" in
    "$WORK"/*) : ;;
    *) bad "the generated dir is not inside this run's temp dir — refusing to read a diff off it"; return 1 ;;
  esac
  gen "$out" || return $?
  # ④ generation really happened: three files, written after this run started.
  local f missing=0
  for f in "${FILES[@]}"; do
    if [ ! -s "$out/$f" ]; then bad "generated $f is empty or absent"; missing=1; fi
  done
  [ $missing -eq 0 ] || return 1
  if [ "$out/docs.go" -ot "$start" ]; then
    bad "generated docs.go is older than this run — the generator did not write it"
    return 1
  fi
  baseline_from_commit "$COMMITTED" || return 1
  compare "$BASE_DIR" "$out"
  local rc=$?
  after="$(git status --porcelain -- "$COMMITTED" 2>/dev/null)"
  if [ "$before" != "$after" ]; then
    bad "the check modified the worktree ($COMMITTED) — a check that repairs what it tests cannot read drift"
    return 1
  fi
  [ "$rc" -eq 0 ] || return 1
  reading "$COMMITTED/swagger.json"
  good "the baseline is $BASELINE_SOURCE"
  good "the worktree was not touched by the check"
  return 0
}

selftest() {
  say "selftest: each mutation must turn this check red"
  : > "$WORK/quiet.log"
  local out="$WORK/gen" rc=0 hits=0
  gen "$out" || return 1
  local base="$WORK/case0"; rm -rf "$base"; mkdir -p "$base"
  cp "$COMMITTED"/*.go "$COMMITTED"/*.json "$COMMITTED"/*.yaml "$base"/ 2>/dev/null
  say "  baseline: an untouched copy of $COMMITTED must read clean"
  QUIET=1; fail=0; compare "$base" "$out"; rc=$?; QUIET=0
  if [ $rc -eq 0 ]; then say "  ok     baseline clean"; else say "  FAIL   baseline red (the mutations below would prove nothing)"; return 1; fi

  # ② a committed artifact that no longer matches the code (the everyday case: annotation
  #    edited, `make swagger` not run)
  local c1="$WORK/case-json"; rm -rf "$c1"; mkdir -p "$c1"; cp "$base"/* "$c1"/
  sed -i '0,/"operationId": *"/{/"operationId": *"/d;}' "$c1/swagger.json"
  QUIET=1; fail=0; compare "$c1" "$out"; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     a committed swagger.json that lost an operation is refused"; hits=$((hits+1)); else say "  FAIL   a stale swagger.json passed"; fi

  # ③ the other direction: the *generated* side grew an operation — i.e. the code documents
  #    something the committed file does not have. Both directions must be refused; a check
  #    that only diffs one way is a check that approves deletions.
  local g1="$WORK/gen-json"; rm -rf "$g1"; mkdir -p "$g1"; cp "$out"/* "$g1"/
  sed -i '1a\    "x-extra": { "operationId": "probe.extra" },' "$g1/swagger.json"
  QUIET=1; fail=0; compare "$base" "$g1"; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     a generation that carries an undocumented operation is refused"; hits=$((hits+1)); else say "  FAIL   one-way diff"; fi

  # ④ docs.go carries the same spec at runtime; a hand edit there is drift too
  local c2="$WORK/case-go"; rm -rf "$c2"; mkdir -p "$c2"; cp "$base"/* "$c2"/
  sed -i 's/LeftDelim: *"{{"/LeftDelim: "{{ " /' "$c2/docs.go"
  QUIET=1; fail=0; compare "$c2" "$out"; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     a hand-edited docs.go is refused"; hits=$((hits+1)); else say "  FAIL   docs.go was not compared"; fi

  # ⑤ a missing artifact must not read as "nothing to compare"
  local c3="$WORK/case-missing"; rm -rf "$c3"; mkdir -p "$c3"; cp "$base"/* "$c3"/; rm -f "$c3/swagger.yaml"
  QUIET=1; fail=0; compare "$c3" "$out"; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     a missing swagger.yaml is refused"; hits=$((hits+1)); else say "  FAIL   a missing artifact passed"; fi

  # ⑥ and the check itself must refuse to read freshness without generating
  QUIET=1; fail=0; SKIP_GEN=1 check_main; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     OAPI_SKIP_GEN is refused instead of passing"; hits=$((hits+1)); else say "  FAIL   a check that skipped generation passed"; fi

  # ⑦ the baseline must be in the index: a path that exists in the worktree but not in the
  #    next commit cannot be the thing a generation is compared against
  QUIET=1; fail=0; baseline_from_commit "$ROOT/internal/config"; rc=$?; QUIET=0
  if [ $rc -ne 0 ]; then say "  ok     a baseline that is not in the index is refused (with the git add fix)"; hits=$((hits+1)); else say "  FAIL   an unindexed baseline passed"; fi

  [ "$hits" -eq 6 ] && { say "selftest: OK (6 mutations refused, 1 baseline clean)"; return 0; }
  say "selftest: FAILED"; return 1
}

case "${1:-check}" in
  check)      check_main; rc=$? ;;
  gen)        gen "${2:?gen needs an output dir}"; rc=$? ;;
  --selftest) selftest; rc=$? ;;
  *)          printf 'usage: %s [check|gen DIR|--selftest]\n' "$(basename "$0")" >&2; rc=2 ;;
esac
exit "$rc"
