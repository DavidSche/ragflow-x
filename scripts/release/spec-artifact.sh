#!/usr/bin/env bash
# spec-artifact.sh — a release ships the contract it actually compiled in.
#
# doc/310 §18 Phase 3 says "发布时附带版本化 OpenAPI 文件" and §22.2 P-310-8 records why it
# matters: `s26.openapi-breaking` can only answer "这次发版破坏了谁的集成" if a release leaves
# behind a document to compare against. The tag already carries `internal/docs/swagger.json`
# (that is what `s26` reads), but a *client* cannot open a git tag; the release page is where
# they look. So every platform package now carries `api/openapi-<version>.{json,yaml}`.
#
# The interesting half is not the copy, it is the claim. "The package contains the spec" is
# satisfied by any file with that name (doc/304 §4 E126: a path is not a reading). The claim
# worth a check is three-way byte equality:
#
# The same package also carries `api-reference-<version>.html`, the reading view for humans.
# It is **rendered from the spec being shipped**, never copied from a file committed at some
# earlier point: a page that says "v0.7.0" while showing v0.6.9's endpoints is the worst of the
# three failures, because it looks authoritative. So `stage` renders it and `verify` re-renders
# from the *index* spec and compares bytes. That is a generation-input proof, not a fourth
# independent contract (doc/310 §22.3, decision 2 in 304 §4 E164).
#
#   ① index bytes   = `git show :internal/docs/swagger.json`   (what the commit says, E140:
#                     the worktree is a reading about one machine, the index is the claim)
#   ② packaged bytes= the file inside `api/` of the bundle that gets tarred
#   ③ binary bytes  = the same bytes, verbatim, inside the shipped binary — Go's `//go:embed`
#                     stores the file raw, so the file's bytes are a literal substring of the
#                     executable. No database, no admin token, no server boot needed.
#
# ① == ② makes "the tarball carries the committed contract" checkable offline; ② ∈ ③ makes
# "the tarball carries the contract this binary serves" checkable too (docs.Enabled only
# decides whether an HTTP surface exposes it — doc/310 §13.1a — not what was compiled in).
#
# Usage:
#   bash scripts/release/spec-artifact.sh stage  <version> <dir>          # fill <dir>/api/ + the doc page
#   bash scripts/release/spec-artifact.sh verify <version> <dir> <binary> # ① ② ③ + filename
#   bash scripts/release/spec-artifact.sh verify <version> <dir> --no-binary   # ①② only
#   bash scripts/release/spec-artifact.sh --selftest                     # 5 mutations
#
# Exit codes: 0 every comparison held; 1 a mismatch (printed with both sides' size+sha256);
# 3 nothing to read (missing spec/index/binary, or no python) — "could not look", never
# "looked and it was fine".
set -uo pipefail

# Windows consoles default to GBK. Without this the circled-digit markers the
# verdict lines are grepped on arrive as '??', so the check would fail for a reason
# that has nothing to do with the artifact.
export PYTHONIOENCODING=utf-8

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

INDEX_PATH="internal/docs/swagger.json"
INDEX_YAML="internal/docs/swagger.yaml"
PY="$(command -v python || command -v python3 || true)"

say()  { printf '%s\n' "$*"; }
good() { printf 'ok    %s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
unread() { printf 'N/A   %s\n' "$*"; exit 3; }
fail=0

normalize_version() { # v0.6.5 -> 0.6.5 ; 0.6.5 -> 0.6.5
  local v="$1"
  printf '%s\n' "${v#v}"
}

# stage <version> <dir> — the bytes come from the *worktree* file, because that is what the
# compilers in release-drill.sh / release.yml just read. verify() then proves the worktree file
# still equals the index, so "stage from the worktree" cannot silently ship an uncommitted edit.
cmd_stage() {
  local version dir
  version="$(normalize_version "$1")"; dir="$2"
  [ -f "$INDEX_PATH" ] || unread "no $INDEX_PATH to stage (run openapi.sh gen)"
  [ -f "$INDEX_YAML" ] || unread "no $INDEX_YAML to stage (run openapi.sh gen)"
  mkdir -p "$dir/api" || unread "cannot create $dir/api"
  cp "$INDEX_PATH" "$dir/api/openapi-${version}.json" || unread "copy json failed"
  cp "$INDEX_YAML" "$dir/api/openapi-${version}.yaml" || unread "copy yaml failed"
  # The reading view is rendered here, from the same file the two copies came from.
  local tmp err
  tmp="$(mktemp -d "${TMPDIR:-${TEMP:-/tmp}}/spec-doc-XXXXXX" 2>/dev/null)"
  [ -n "$tmp" ] && [ -d "$tmp" ] || unread "no writable temp dir to render the doc page"
  err="$tmp/err"
  if ! "$PY" scripts/release-gate/api_doc_gen.py "$INDEX_PATH" "$tmp/out" >/dev/null 2>"$err"; then
    say "$(tail -3 "$err" 2>/dev/null)"
    rm -rf "$tmp"
    unread "cannot render api-reference.html from $INDEX_PATH (doc/310 §12: no hand-written page)"
  fi
  mv "$tmp/out/api-reference.html" "$dir/api-reference-${version}.html" || unread "render produced no api-reference.html"
  rm -rf "$tmp"
  good "staged api/openapi-${version}.{json,yaml} ($(wc -c <"$INDEX_PATH" | tr -d ' ') B / $(wc -c <"$INDEX_YAML" | tr -d ' ') B) + api-reference-${version}.html ($(wc -c <"$dir/api-reference-${version}.html" | tr -d ' ') B)"
}

# render_html_from_index <out-file> — the same renderer, fed the *index* bytes. Determinism is
# what makes byte comparison meaningful: api_doc_gen.py has no timestamps and sorts explicitly
# (doc/310 §12), so a difference means the spec changed, never "it ran on another day".
render_html_from_index() {
  local out="$1" tmp err
  tmp="$(mktemp -d "${TMPDIR:-${TEMP:-/tmp}}/spec-doc-XXXXXX" 2>/dev/null)"
  [ -n "$tmp" ] && [ -d "$tmp" ] || return 3
  err="$tmp/err"
  # The renderer names its input in the page header, so the temp copy must keep the
  # canonical basename: determinism may not depend on where the caller put the file
  # (first attempt used `$tmp/spec.json` and differed by exactly those 7 bytes).
  git show ":$INDEX_PATH" >"$tmp/swagger.json" 2>/dev/null || { rm -rf "$tmp"; return 3; }
  if ! "$PY" scripts/release-gate/api_doc_gen.py "$tmp/swagger.json" "$tmp/out" >/dev/null 2>"$err"; then
    say "$(tail -3 "$err" 2>/dev/null)"
    rm -rf "$tmp"
    return 1
  fi
  mv "$tmp/out/api-reference.html" "$out" || { rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
}

# compare_via_python <json|yaml> <version> <dir> <binary|-> [index-mode]
#
# One python process for all three comparisons: byte equality of the index copy vs the packaged
# copy, and `packaged bytes in binary`. A shell version would need `cmp` plus a substring search
# that coreutils does not have, and `grep -F` on a 674 KB binary with a NUL-bearing pattern is
# exactly the kind of "it looked fine" that E126 warns about.
cmd_verify_python() {
  "$PY" - "$@" <<'PY'
import hashlib, io, os, subprocess, sys

kind, version, pkgdir, binary = sys.argv[1:5]
repo = "internal/docs/swagger.json" if kind == "json" else "internal/docs/swagger.yaml"
pkg = os.path.join(pkgdir, "api", "openapi-%s.%s" % (version, kind))

def read(path):
    with open(path, "rb") as fh:
        return fh.read()

def sha(b):
    return hashlib.sha256(b).hexdigest()

if not os.path.exists(repo):
    print("N/A   no %s (run openapi.sh gen)" % repo)
    sys.exit(3)
if not os.path.exists(pkg):
    print("FAIL  missing packaged %s" % pkg)
    sys.exit(1)

work = read(repo)
packaged = read(pkg)

# ① index bytes: what the commit says. The worktree copy is what the build read, so the
# comparison is worktree == index — a dirty tree would otherwise ship an uncommitted contract.
try:
    index = subprocess.run(["git", "show", ":" + repo], capture_output=True, check=False)
except OSError:
    print("N/A   no git available, cannot read the index")
    sys.exit(3)
if index.returncode != 0:
    print("N/A   %s is not in the index (untracked): the release claim needs a committed contract" % repo)
    sys.exit(3)
index_bytes = index.stdout

if index_bytes != work:
    print("FAIL  %s in the worktree differs from the index (sha256 %s vs %s, %d B vs %d B)"
          % (repo, sha(work)[:16], sha(index_bytes)[:16], len(work), len(index_bytes)))
    sys.exit(1)

# ② packaged bytes == index bytes
if packaged != index_bytes:
    print("FAIL  ① != ②: api/openapi-%s.%s is %d B sha256 %s, index is %d B sha256 %s"
          % (version, kind, len(packaged), sha(packaged)[:16], len(index_bytes), sha(index_bytes)[:16]))
    sys.exit(1)
print("ok    ① == ②  %-34s %7d B  sha256 %s"
      % ("api/openapi-%s.%s" % (version, kind), len(packaged), sha(packaged)[:16]))

# ③ those bytes occur verbatim inside the shipped binary (Go //go:embed stores the file raw).
if binary != "-":
    if not os.path.exists(binary):
        print("N/A   no binary at %s" % binary)
        sys.exit(3)
    blob = read(binary)
    if packaged not in blob:
        print("FAIL  ② != ③: %d B (sha256 %s) do not occur inside %s (%d B)"
              % (len(packaged), sha(packaged)[:16], os.path.basename(binary), len(blob)))
        sys.exit(1)
    print("ok    ② == ③  %-34s found verbatim inside %s (%d B)"
          % ("(embedded copy)", os.path.basename(binary), len(blob)))
sys.exit(0)
PY
}

cmd_verify() {
  local raw="$1" dir="$2" binary="${3:--}" version
  # `--no-binary` is documented, so it must not be read as a path named "--no-binary"
  # (that would exit 3, i.e. "could not look" — a silent downgrade of the claim).
  case "$binary" in --no-binary) binary="-" ;; esac
  version="$(normalize_version "$raw")"
  [ -n "$PY" ] || unread "no python on PATH: byte equality and an embed substring need a reader"
  [ -d "$dir" ] || unread "no bundle dir $dir"
  say "index:   $INDEX_PATH (git show :$INDEX_PATH)"
  say "package: $dir/api/openapi-${version}.{json,yaml}"
  say "binary:  $binary"
  cmd_verify_python json "$version" "$dir" "$binary" || fail=1
  cmd_verify_python yaml "$version" "$dir" "$binary" || fail=1
  # A mis-named file is the same class of bug as a missing one: the version in the name is the
  # only thing telling a client which contract they downloaded (E126: the path is the claim).
  local stray
  stray="$(find "$dir/api" -maxdepth 1 -type f 2>/dev/null \
           | sed 's#.*/##' | grep -Ev "^openapi-${version//./\\.}\.(json|yaml)$" || true)"
  if [ -n "$stray" ]; then
    bad "api/ carries files for another version: $(printf '%s ' $stray)"
  else
    good "api/ holds exactly the two files named for ${version}"
  fi

  # ④ the human page: re-render from the index spec and compare bytes. A page rendered from
  # anything else (an older spec, a hand edit, a "quick fix" typo) shows up here.
  local page="$dir/api-reference-${version}.html" rendered
  stray="$(find "$dir" -maxdepth 1 -type f -name 'api-reference-*.html' 2>/dev/null            | sed 's#.*/##' | grep -Ev "^api-reference-${version//./\.}\.html$" || true)"
  if [ -n "$stray" ]; then
    bad "the package root carries doc pages for another version: $(printf '%s ' $stray)"
  fi
  if [ ! -f "$page" ]; then
    bad "no api-reference-${version}.html in $dir (the reading view is a release asset, not a repository file)"
  else
    rendered="$({ mktemp -d "${TMPDIR:-${TEMP:-/tmp}}/spec-page-XXXXXX"; } 2>/dev/null)/api-reference.html"
    if render_html_from_index "$rendered"; then
      if cmp -s "$rendered" "$page"; then
        good "④ doc page == a fresh render of the index spec ($(wc -c <"$page" | tr -d ' ') B sha256 $(sha256sum "$page" 2>/dev/null | cut -c1-16))"
      else
        bad "④ doc page != fresh render of the index spec (packaged $(wc -c <"$page" | tr -d ' ') B sha256 $(sha256sum "$page" 2>/dev/null | cut -c1-16), rendered $(wc -c <"$rendered" | tr -d ' ') B)"
      fi
      rm -f "$rendered"
      rmdir "$(dirname "$rendered")" 2>/dev/null || true
    else
      unread "cannot re-render the doc page from the index (nothing to compare against)"
    fi
  fi
  [ "$fail" -eq 0 ] || exit 1
  if [ "$binary" = "-" ]; then
    good "release package $dir carries the index bytes (binary not examined: --no-binary)"
  else
    good "release package $dir carries the contract its binary compiled in"
  fi
}

# ---------------------------------------------------------------- selftest
# Five mutations, each a way this check could be a decoration. M0 is the control: without it,
# "always red" would pass four of five.
cmd_selftest() {
  work="$(mktemp -d "${TMPDIR:-${TEMP:-/tmp}}/spec-artifact-XXXXXX" 2>/dev/null)"
  [ -n "$work" ] && [ -d "$work" ] || unread "no writable temp dir"
  # trap bodies run after the function's locals are gone under `set -u`, so this one is global.
  trap 'rm -rf "${work:-}"' EXIT
  local bin="${RGX_SPEC_SELFTEST_BINARY:-}"
  if [ -z "$bin" ]; then
    say "building a native ./cmd/server to embed the spec (one time)…"
    bin="$work/ragflow-x-native"
    if ! go build -o "$bin" ./cmd/server/ >"$work/build.log" 2>&1; then
      unread "go build ./cmd/server failed: $(tail -3 "$work/build.log" | tr '\n' ' ')"
    fi
  fi
  local version="9.9.9-selftest" d="$work/bundle" rc out

  run() { out="$(cmd_verify "$version" "$d" "$bin" 2>&1)"; rc=$?; }

  # M0 control: a correct bundle passes.
  cmd_stage "$version" "$d" >/dev/null
  run
  if [ $rc -eq 0 ]; then good "M0 control: a correct bundle passes"; else bad "M0 control failed (rc=$rc): $out"; fi

  # M1 one flipped byte in the packaged copy: ① != ②.
  cmd_stage "$version" "$d" >/dev/null
  "$PY" - "$d/api/openapi-${version}.json" <<'PY'
import sys
p = sys.argv[1]
b = bytearray(open(p, "rb").read())
b[len(b) // 2] ^= 0x01
open(p, "wb").write(bytes(b))
PY
  run
  if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "① != ②"; then good "M1 flipped byte in api/ is refused"; else bad "M1 not refused (rc=$rc): $out"; fi

  # M2 a stale packaged copy (an empty spec stands in for "generated before the last change").
  cmd_stage "$version" "$d" >/dev/null
  printf '{}' >"$d/api/openapi-${version}.json"
  run
  if [ $rc -eq 1 ]; then good "M2 stale packaged spec is refused"; else bad "M2 not refused (rc=$rc): $out"; fi

  # M3 a binary that does not carry the spec: ② != ③. Uses a text file as the "binary" — the
  # substring search is over raw bytes, so any file proves the rule can fire.
  cmd_stage "$version" "$d" >/dev/null
  printf 'not a binary\n' >"$work/fake-binary"
  out="$(cmd_verify "$version" "$d" "$work/fake-binary" 2>&1)"; rc=$?
  if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "② != ③"; then good "M3 spec absent from the binary is refused"; else bad "M3 not refused (rc=$rc): $out"; fi

  # M4 a file named for another version: the name is the claim a client reads.
  cmd_stage "$version" "$d" >/dev/null
  cp "$d/api/openapi-${version}.json" "$d/api/openapi-0.0.1.json"
  run
  if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "another version"; then good "M4 mis-versioned file name is refused"; else bad "M4 not refused (rc=$rc): $out"; fi

  say ""
  if [ "$fail" -ne 0 ]; then printf 'SELFTEST FAILED\n' >&2; exit 1; fi
  # M5 the documented `--no-binary` form: it must still check ① == ②, not become "skip
  # everything". A flag that silently disables the claim is worse than a missing flag.
  d="$work/bundle-nb"; rm -rf "$d"; cmd_stage "$version" "$d" >/dev/null
  out="$(cmd_verify "$version" "$d" --no-binary 2>&1)"; rc=$?
  if [ $rc -eq 0 ] && printf '%s' "$out" | grep -q "① == ②"; then
    good "M5 --no-binary still checks ① == ② instead of turning the flag into a skip"
  else
    bad "M5 --no-binary did not behave (rc=$rc): $out"
  fi

  # M6 the human page doctored after staging: ④ must catch it. This is the failure that
  # reads as "the docs say something slightly different from the spec", which is the one
  # nobody reports.
  d="$work/bundle-page"; rm -rf "$d"; cmd_stage "$version" "$d" >/dev/null
  "$PY" - "$d/api-reference-${version}.html" <<'PYPAGE'
import io, sys
p = sys.argv[1]
b = io.open(p, encoding="utf-8", newline="").read()
b = b.replace("</body>", "<!-- edited by hand --></body>", 1)
io.open(p, "w", encoding="utf-8", newline="").write(b)
PYPAGE
  out="$(cmd_verify "$version" "$d" --no-binary 2>&1)"; rc=$?
  if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "④ doc page !="; then
    good "M6 doctored doc page is refused (④)"
  else
    bad "M6 doctored doc page was accepted (rc=$rc)"
  fi

  say ""
  if [ "$fail" -ne 0 ]; then printf 'SELFTEST FAILED\n' >&2; exit 1; fi
  good "7/7 mutations handled (1 control + 5 refusals + 1 flag form)"
}

# ---------------------------------------------------------------- gate
# The release-gate row (s30). It is the same three-way claim the release job makes, executed
# on a binary this machine just built — so the assertion is exercised on every push, not only
# when someone cuts a tag. The selftest reuses the binary (no second build).
cmd_gate() {
  work="$(mktemp -d "${TMPDIR:-${TEMP:-/tmp}}/spec-gate-XXXXXX" 2>/dev/null)"
  [ -n "$work" ] && [ -d "$work" ] || unread "no writable temp dir"
  trap 'rm -rf "${work:-}"' EXIT
  local version="0.0.0-gate" bin="$work/ragflow-x"
  say "building ./cmd/server (native, no webfs_embed: the spec lives in internal/docs)…"
  if ! go build -o "$bin" ./cmd/server/ >"$work/build.log" 2>&1; then
    unread "go build ./cmd/server failed: $(tail -3 "$work/build.log" | tr '
' ' ')"
  fi
  cmd_stage "$version" "$work/bundle" || exit 3
  cmd_verify "$version" "$work/bundle" "$bin" || exit 1
  say ""
  say "selftest (reusing the binary above):"
  RGX_SPEC_SELFTEST_BINARY="$bin" cmd_selftest
}

case "${1:-}" in
  stage)    cmd_stage  "${2:?version}" "${3:?dir}" ;;
  verify)   cmd_verify "${2:?version}" "${3:?dir}" "${4:---}" ;;
  --selftest) cmd_selftest ;;
  gate)     cmd_gate ;;
  ""|-h|--help)
    sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 0 ;;
  *) printf 'unknown command %s\n' "$1" >&2; exit 2 ;;
esac
