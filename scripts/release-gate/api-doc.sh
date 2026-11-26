#!/usr/bin/env bash
# s28.api-doc — the hand-out-able API reference is a *rendering* of the served spec, and
# a rendering is only worth shipping if something refuses a stale one.
#
# The ask (2026-11-26): "用户需要接口开发文档的时候，可以直接从这里取，发给客户". The honest
# answer has two halves, and this row is both of them:
#
#   half ①  `doc/specification/api/api-reference.{md,html}` are produced from
#           `internal/docs/swagger.json` — the same bytes the binary serves (verified on
#           151: sha256 of `/docs/openapi.json` == sha256 of the committed file). One
#           source, two renderings, zero hand-maintained copies.
#   half ②  what the rendering may *say* is bounded by the annotations in the handlers.
#           Today 616/616 operations still carry the generator's placeholder summary and
#           0/616 have a request body, so the document leads with those numbers, computed
#           from the spec on every generation. It is a route contract, not a business
#           interface manual, and it says so in its first screen. Shipping the stub as
#           "the interface manual" would be a lie told in good typography.
#
# What this row holds (each item is a way the artifact can be wrong while looking fine):
#
#   ① freshness: regenerate into a temp dir and compare **byte-for-byte** against the
#     committed pair. A spec change without a regeneration is the failure mode that makes
#     a document describe last month's API (the same shape as E120/E126/E133: the ritual
#     exists, the reader does not).
#   ② coverage of the renderer, counted rather than trusted: the headings the renderer
#     emits must equal the operation count the spec declares (> 0), and every path must
#     appear literally. This is a separate failure from ① — a template that drops or
#     mangles one operation produces a product that "matches" itself, and "0 drift" must
#     not be able to look like "0 operations".
#   ③ the artifacts are **not ignored**: `git check-ignore` must miss both files, because
#     `doc/*` is ignored by policy (`.gitignore:91`) and an un-shippable deliverable is a
#     document with no reader. This is the one thing `docs-ledger.json` needed a snapshot
#     for (`doc/` is untracked there); here the artifact itself is tracked, so git *is*
#     the store and there is no second snapshot to drift.
#   ④ the check does not touch the worktree: mtimes of the two artifacts are unchanged
#     after a run, and `git status --porcelain -- doc/specification/api internal/docs`
#     stays empty. A check that "fixes" the artifact while testing it cannot tell drift
#     from a repair.
#   ⑤ a reading, not only a verdict: the PASS line prints annotations coverage
#     (summary/description/body/params) read out of the spec, so the next person can see
#     whether the document is still a stub without opening it.
#
# Usage:
#   bash scripts/release-gate/api-doc.sh                 # the check (~1s)
#   bash scripts/release-gate/api-doc.sh gen [DIR]       # write the pair (default: the repo path)
#   bash scripts/release-gate/api-doc.sh --selftest      # prove the check can fail (6 mutations)
#
# Env overrides exist for `--selftest` only; a tracked file is never edited to prove a refusal.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SPEC="${API_DOC_SPEC:-internal/docs/swagger.json}"
OUT="${API_DOC_OUT:-doc/specification/api}"
GEN="${API_DOC_GEN:-scripts/release-gate/api_doc_gen.py}"
PRODUCTS=(api-reference.md api-reference.html)

say() { printf '%s\n' "$*"; }

# Same trap as docs-ledger.sh: python's stdout is cp936 here while these strings are UTF-8,
# and `--selftest` greps a UTF-8 fragment, so the encoding must be pinned for the child.
export PYTHONIOENCODING=utf-8

pick_python() {
  local c
  for c in python3 python py; do
    if command -v "$c" >/dev/null 2>&1 && "$c" -c pass >/dev/null 2>&1; then
      printf '%s' "$c"; return 0
    fi
  done
  return 1
}
PYTHON="$(pick_python)" || { say "api-doc: FAIL — no python on PATH, so the renderer cannot run (no artifact is not 'fresh')"; exit 1; }

# ── shared: generate into a directory and print the readings ───────────────
# $1 = out dir. Prints the renderer's JSON line on success; returns its exit code.
render() {
  "$PYTHON" "$GEN" "$SPEC" "$1" 2>&1
}

count_heading() {  # count_heading <file> <prefix>
  grep -c "^$2" "$1" 2>/dev/null || true
}

# ── gen: write the pair where it ships ────────────────────────────────────
if [ "${1:-}" = "gen" ]; then
  target="${2:-$OUT}"
  say "api-doc: generating into $target from $SPEC"
  render "$target" || { say "api-doc: FAIL — the renderer refused (see above)"; exit 1; }
  ls -l "$target"/api-reference.* 2>/dev/null | sed 's/^/  /'
  exit 0
fi

# ── selftest: six mutations, each of which must be refused ────────────────
if [ "${1:-}" = "--selftest" ]; then
  say "selftest: can this check refuse? (a missing product, a one-byte edit, a renderer that drops an operation, a renderer that mangles a path, a byte-perfect pair on an ignored path, an untouched document after the spec lost a path, an untouched document after the spec gained a path, and an empty document)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  cp -r "$OUT" "$tmp/good" || { say "SELFTEST FAIL: cannot copy the committed pair from $OUT"; exit 1; }

  expect_refusal() {  # expect_refusal <sentence fragment> [env assignments...]
    local want="$1"; shift
    local out rc
    out="$(env "$@" bash "$0" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -qF -- "$want"; then
      say "SELFTEST FAIL: a broken artifact was accepted (or refused for the wrong reason)"
      say "  wanted: $want"
      printf '%s\n' "$out" | sed -n '1,14p'
      exit 1
    fi
    say "ok      $want"
  }

  # ① a product that is simply gone. Reads as "documentation pending", which is exactly why
  # it must be refused instead: the row would otherwise pass on a checkout that cannot ship.
  cp -r "$tmp/good" "$tmp/missing"
  rm -f "$tmp/missing/api-reference.html"
  expect_refusal "missing product" API_DOC_OUT="$tmp/missing"

  # ② one byte changed in the .md. This is the whole point of a byte comparison: a hand edit
  # that "just fixes a typo" is how a generated file becomes a hand-maintained file.
  cp -r "$tmp/good" "$tmp/edited"
  "$PYTHON" - "$tmp/edited/api-reference.md" <<'PY'
import io, sys
p = sys.argv[1]
s = io.open(p, encoding="utf-8", newline="").read()
old = "它由 OpenAPI 描述符渲染而来"
assert s.count(old) == 1, "selftest anchor missing"
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, "它由 OpenAPI 描述符渲染出来"))
PY
  expect_refusal "differs from a fresh generation" API_DOC_OUT="$tmp/edited"

  # ③ a *renderer* that drops an operation (a template that swallows one row). The product
  # on disk is untouched, so freshness cannot see it — coverage compares what the renderer
  # emitted against what the spec declares, which is why the two checks are not one.
  cp "$GEN" "$tmp/gen-drops.py"
  "$PYTHON" - "$tmp/gen-drops.py" <<'PYGEN'
import io, sys
p = sys.argv[1]
s = io.open(p, encoding="utf-8", newline="").read()
old = "ops = walk(spec)"
assert s.count(old) == 1, "selftest anchor missing"
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, "ops = walk(spec)[:-1]"))
PYGEN
  expect_refusal "renderer reports" API_DOC_GEN="$tmp/gen-drops.py"

  # ③b a renderer that mangles the path literal: the heading count still matches, so only the
  # "every path appears" join catches it. An operation list whose paths are wrong is worse
  # than a short one — it looks complete.
  cp "$GEN" "$tmp/gen-mangles.py"
  "$PYTHON" - "$tmp/gen-mangles.py" <<'PYGEN'
import io, sys
p = sys.argv[1]
lines = io.open(p, encoding="utf-8", newline="").readlines()
hits = [i for i, l in enumerate(lines) if l.lstrip().startswith('w("### %s')]
assert len(hits) == 1, "selftest anchor missing"
lines[hits[0]] = lines[hits[0]].replace("`%s`", "%s")
io.open(p, "w", encoding="utf-8", newline="").writelines(lines)
PYGEN
  expect_refusal "never appear in api-reference.md" API_DOC_GEN="$tmp/gen-mangles.py"

  # ④ drift, direction A: the spec lost a path and nobody regenerated. The document still
  # matches its own copy, so only a regeneration can see this.
  "$PYTHON" - "$SPEC" "$tmp/spec-shrunk.json" <<'PY'
import io, json, sys
src, out = sys.argv[1], sys.argv[2]
spec = json.load(io.open(src, encoding="utf-8"))
del spec["paths"]["/api/v1/usage/filter-options"]
io.open(out, "w", encoding="utf-8", newline="\n").write(json.dumps(spec, ensure_ascii=False))
PY
  expect_refusal "differs from a fresh generation" API_DOC_OUT="$tmp/good" API_DOC_SPEC="$tmp/spec-shrunk.json"

  # ⑤ drift, direction B: the spec gained an operation (a new handler) and the committed
  # document still describes yesterday's API. Same sentence, opposite cause — both must red.
  "$PYTHON" - "$SPEC" "$tmp/spec-grown.json" <<'PY'
import io, json, sys
src, out = sys.argv[1], sys.argv[2]
spec = json.load(io.open(src, encoding="utf-8"))
spec["paths"]["/api/v1/selftest-only"] = {
    "get": {"operationId": "SelftestOnly", "tags": ["auto"],
            "responses": {"200": {"description": "OK"}}}
}
io.open(out, "w", encoding="utf-8", newline="\n").write(json.dumps(spec, ensure_ascii=False))
PY
  expect_refusal "differs from a fresh generation" API_DOC_OUT="$tmp/good" API_DOC_SPEC="$tmp/spec-grown.json"

  # ⑦ the artifact becomes un-shippable: a byte-perfect pair on an ignored path. This is the
  # regression that would undo the whole row — the document exists, is fresh, and nobody who
  # clones the repo gets it (doc/* is ignored by policy).
  mkdir -p "$ROOT/doc/develop/api-doc-selftest"
  cp "$tmp/good"/api-reference.md "$tmp/good"/api-reference.html "$ROOT/doc/develop/api-doc-selftest"/
  expect_refusal "is gitignored" API_DOC_OUT="$ROOT/doc/develop/api-doc-selftest"     || { rm -rf "$ROOT/doc/develop/api-doc-selftest"; exit 1; }
  rm -rf "$ROOT/doc/develop/api-doc-selftest"

  # ⑥ a spec with no operations must never yield two empty files that "match": an empty
  # document is not a document, and a byte comparison of two empties would call it fresh.
  "$PYTHON" - "$tmp/spec-empty.json" <<'PY'
import io, sys
io.open(sys.argv[1], "w", encoding="utf-8", newline="\n").write('{"openapi":"3.1.0","info":{"title":"x","version":"0"},"paths":{}}')
PY
  expect_refusal "the renderer refused the committed spec" API_DOC_OUT="$tmp/good" API_DOC_SPEC="$tmp/spec-empty.json"

  printf '\napi-doc --selftest: PASS (8/8 mutations refused)\n'
  exit 0
fi

# ── check ────────────────────────────────────────────────────────────────
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

[ -f "$SPEC" ] || { say "api-doc: FAIL — $SPEC is missing; the artifact cannot be regenerated, so it cannot be called fresh"; exit 1; }
for f in "${PRODUCTS[@]}"; do
  [ -f "$OUT/$f" ] || {
    say "api-doc: FAIL — missing product $OUT/$f"
    say "  fix: bash scripts/release-gate/api-doc.sh gen"
    exit 1
  }
done

before_status="$(git status --porcelain -- "$OUT" "$(dirname "$SPEC")" 2>/dev/null | sort)"
before_mtimes="$(ls -l --time-style=full-iso "$OUT"/api-reference.md "$OUT"/api-reference.html 2>/dev/null | awk '{print $6, $7, $9}')"

fresh="$(render "$work")" || { say "api-doc: FAIL — the renderer refused the committed spec"; printf '%s\n' "$fresh"; exit 1; }

# ② coverage of the *renderer*, counted in its output rather than trusted
# The expected values come from the **spec**, not from the renderer's own report: taking
# them from the thing under test is how a renderer that drops an operation passes (it said
# 615, it emitted 615, self-consistent and wrong). Found by the selftest's ③, which is why
# ③ mutates the renderer and not the product.
readings="$(printf '%s' "$fresh" | tail -n 1)"
spec_counts="$(API_SPEC="$SPEC" "$PYTHON" - <<'PYSPEC'
import io, json, os
spec = json.load(io.open(os.environ["API_SPEC"], encoding="utf-8"))
# The same six methods the spec can carry. While this list and the renderer's both said five,
# a renderer that dropped HEAD looked self-consistent: the product and the check agreed with
# each other instead of with the spec (doc/304 §4 E173).
ops = [m for ops in (spec.get("paths") or {}).values() for m in ops if m in ("get","post","put","patch","delete","head")]
print(len(ops), len(spec.get("paths") or {}))
PYSPEC
)"
ops_in_spec="${spec_counts% *}"
paths_in_spec="${spec_counts#* }"
renderer_ops="$(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys; print(json.load(sys.stdin)["readings"]["operations"])')"
headings="$(count_heading "$work/api-reference.md" "### ")"
[ -n "$ops_in_spec" ] && [ "$ops_in_spec" -gt 0 ] || { say "api-doc: FAIL — the spec declares $ops_in_spec operations; an empty document is not a document"; exit 1; }
if [ "$renderer_ops" != "$ops_in_spec" ]; then
  say "api-doc: FAIL — the renderer reports $renderer_ops operations, the spec declares $ops_in_spec (it is describing its own output as if it were the input)"
  exit 1
fi
if [ "$headings" -ne "$ops_in_spec" ]; then
  say "api-doc: FAIL — operation headings in api-reference.md ($headings) != operations in the spec ($ops_in_spec)"
  exit 1
fi
articles="$(grep -c '^<h3 id=' "$work/api-reference.html" 2>/dev/null || true)"
if [ "$articles" -ne "$ops_in_spec" ]; then
  say "api-doc: FAIL — api-reference.html carries $articles operation sections, the spec declares $ops_in_spec"
  exit 1
fi
"$PYTHON" - "$SPEC" "$work/api-reference.md" <<'PY' || exit 1
import io, json, sys
spec = json.load(io.open(sys.argv[1], encoding="utf-8"))
md = io.open(sys.argv[2], encoding="utf-8").read()
missing = [p for p in sorted(spec.get("paths") or {}) if ("`%s`" % p) not in md]
if missing:
    sys.stdout.write("api-doc: FAIL — %d path(s) in the spec never appear in api-reference.md: %s\n"
                     % (len(missing), ", ".join(missing[:5])))
    sys.exit(1)
PY

# ① freshness: the committed pair == a fresh generation (a diff that names the file it is about)
drift=0
for f in "${PRODUCTS[@]}"; do
  if ! cmp -s "$work/$f" "$OUT/$f"; then
    say "api-doc: FAIL — $OUT/$f differs from a fresh generation (the document and the spec disagree)"
    cmp "$work/$f" "$OUT/$f" 2>&1 | sed 's/^/  /'
    drift=1
  fi
done
[ "$drift" -eq 0 ] || { say "  fix: bash scripts/release-gate/api-doc.sh gen   (then commit both files)"; exit 1; }

# ③ shippable: doc/* is ignored by policy, so an ignored product is a document no reader has
for f in "${PRODUCTS[@]}"; do
  if git check-ignore -q "$OUT/$f"; then
    say "api-doc: FAIL — $OUT/$f is gitignored (.gitignore line $(git check-ignore -v "$OUT/$f" | cut -d: -f2)); an un-shippable artifact is a document with no reader"
    exit 1
  fi
done

# ④ the check must not have touched what it is judging
after_status="$(git status --porcelain -- "$OUT" "$(dirname "$SPEC")" 2>/dev/null | sort)"
after_mtimes="$(ls -l --time-style=full-iso "$OUT"/api-reference.md "$OUT"/api-reference.html 2>/dev/null | awk '{print $6, $7, $9}')"
if [ "$before_status" != "$after_status" ] || [ "$before_mtimes" != "$after_mtimes" ]; then
  say "api-doc: FAIL — the check modified the worktree; a check that repairs what it tests cannot tell drift from a repair"
  exit 1
fi

# ⑤ a reading, not only a verdict
say "api-doc: PASS — ${paths_in_spec} paths / ${ops_in_spec} operations, ${headings} headings in the .md, ${articles} in the .html, byte-identical to a fresh generation"
say "  annotations: summary $(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys;d=json.load(sys.stdin)["readings"];print("%d/%d annotated" % (d["operations"]-d["summary_unannotated"], d["operations"]))'), description $(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys;d=json.load(sys.stdin)["readings"];print("%d/%d" % (d["with_description"], d["operations"]))'), requestBody $(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys;d=json.load(sys.stdin)["readings"];print("%d/%d" % (d["with_request_body"], d["operations"]))'), parameters $(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys;d=json.load(sys.stdin)["readings"];print("%d/%d" % (d["with_parameters"], d["operations"]))')"
if [ "$(printf '%s' "$readings" | "$PYTHON" -c 'import json,sys;d=json.load(sys.stdin)["readings"];print(d["with_request_body"])')" -eq 0 ]; then
  say "  note: the document is a route contract, not a business interface manual (0 request bodies annotated); it says so in its first screen"
fi
exit 0
