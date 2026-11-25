#!/usr/bin/env bash
# s26.openapi-breaking — an API that people integrate against may gain, but not lose.
#
# doc/310 Phase 3 asks for "检查 API 破坏性变更" and §20 A21 records the state of the art as
# 「具备后续差异检查基础」: the spec is now generated, committed and freshness-checked (E138/
# E140), so for the first time there are two *comparable* documents — the one this commit
# would ship and the one a release already shipped. A21 stays a hope until something reads
# both and refuses the losses. That is this row.
#
# What it holds:
#
#   ① two sides, both real: the current side is the **index** (`git show :internal/docs/…`,
#     the E140 decision — the worktree is a reading about one machine) and the baseline side
#     is the newest tag whose tree carries the artifact. A comparison needs both, so both
#     are printed with their path/operation counts: "0 findings" and "0 operations read"
#     must not be able to look alike (doc/304 §4 E126/E133).
#   ② no baseline, said out loud: while no tag carries `internal/docs/swagger.json` there
#     are **zero released operations**. That is a known zero, not an unknown: nothing has
#     promised compatibility yet, so nothing can break it. The row passes *and prints the
#     tags it examined*. The day a tag ships the spec, the same code compares — this is a
#     cliff by design, and both sides of it are visible in the output.
#   ③ seven rules, each a loss a client can feel: a removed path (R1), a removed operation
#     (R2), a renamed/vanished operationId (R3), a newly required parameter (R4), a removed
#     documented response code (R5), a narrowed enum (R6), a changed schema type (R7).
#     Adding a path, an optional parameter, a response code or an enum value is *not*
#     breaking — a gate that reds on every diff is a gate people switch off.
#   ④ exemptions are written down and must be used: `openapi-breaking-allow.txt` carries
#     `RULE METHOD PATH  reason` lines. An exemption without a reason (≥12 chars) is
#     refused, and so is one that matches no finding — an exemption that exempts nothing is
#     a promise nobody keeps (doc/304 §4 E135: a row that is no longer a row is not counted).
#
# Usage:
#   bash scripts/release-gate/openapi-breaking.sh                 # check against the newest tag
#   bash scripts/release-gate/openapi-breaking.sh check <ref>     # check against a given ref
#   bash scripts/release-gate/openapi-breaking.sh --selftest      # prove each rule can fire
#
# Exit codes: 0 the index is a superset of the baseline (or nothing was ever released),
# 1 a breaking change (or a malformed/stale exemption), 3 nothing to read at all (no index
# copy, no python) — "could not look", never "looked and found nothing".
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SPEC="internal/docs/swagger.json"
ALLOW="${OAPI_BREAK_ALLOW:-scripts/release-gate/openapi-breaking-allow.txt}"
TMPROOT="${TMPDIR:-${TEMP:-/tmp}}"
WORK="$(mktemp -d "$TMPROOT/oapi-break-XXXXXX" 2>/dev/null)" || WORK=""
if [ -z "$WORK" ] || [ ! -d "$WORK" ]; then
  printf 'FAIL  no writable temp dir (TMPDIR/TEMP)\n' >&2
  exit 2
fi
trap 'rm -rf "$WORK"' EXIT

PY="$(command -v python || command -v python3 || true)"
if [ -z "$PY" ]; then
  printf 'FAIL  no python on PATH: the diff needs a JSON reader, and a grep-based diff would report schema-level changes as "no change"\n' >&2
  exit 3
fi

say()  { printf '%s\n' "$*"; }
fail=0
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# newest_spec_tag — the release this commit is being compared against.
#
# `git describe` is not enough: the tag has to *carry the artifact*, and tags before E140 do
# not. Trying them newest-first is what makes "the last released contract" a fact rather than
# a guess; the list is printed so a reader can see how far back the search went.
newest_spec_tag() {
  local tag
  for tag in $(git -C "$ROOT" tag --list --sort=-v:refname 2>/dev/null); do
    if git -C "$ROOT" cat-file -e "$tag:$SPEC" 2>/dev/null; then
      printf '%s\n' "$tag"
      return 0
    fi
  done
  return 1
}

# diff_specs <baseline.json> <current.json> — the seven rules. Prints one line per finding:
#
#   Rn METHOD PATH :: detail
#
# and a `READ` line whose counts both sides must have. Exemptions are applied here and
# reported separately, so the output says *why* a finding did not red the row.
diff_specs() {
  local allowp="${3-$ALLOW}"
  "$PY" - "$1" "$2" "$allowp" <<'PYEOF'
import io
import json
import sys

base_p, cur_p, allow_p = sys.argv[1], sys.argv[2], sys.argv[3]


def load(path):
    with io.open(path, encoding='utf-8') as fh:
        return json.load(fh)


METHODS = ('get', 'put', 'post', 'delete', 'patch', 'options', 'head', 'trace')


def ops(doc, path):
    raw = (doc.get('paths') or {}).get(path) or {}
    return {m: raw[m] for m in raw if m.lower() in METHODS}


def resolve(doc, node):
    """Follow one level of #/components/schemas — enough for the generated spec, and the
    alternative (a full resolver) is code that can be wrong in ways nobody reads."""
    if isinstance(node, dict) and isinstance(node.get('$ref'), str):
        ref = node['$ref']
        if ref.startswith('#/components/schemas/'):
            return (doc.get('components') or {}).get('schemas', {}).get(ref.split('/')[-1], {}) or {}
    return node if isinstance(node, dict) else {}


def collect_schema(doc, node, prefix, out):
    node = resolve(doc, node)
    t = node.get('type')
    if isinstance(t, str):
        out['type'][prefix] = t
    elif isinstance(t, list):
        out['type'][prefix] = ','.join(sorted(str(x) for x in t))
    enum = node.get('enum')
    if isinstance(enum, list):
        out['enum'][prefix] = [json.dumps(x, sort_keys=True) for x in enum]
    for name, child in (node.get('properties') or {}).items():
        collect_schema(doc, child, prefix + '.' + name, out)
    items = node.get('items')
    if isinstance(items, dict):
        collect_schema(doc, items, prefix + '[]', out)


def collect(doc):
    out = {'type': {}, 'enum': {}}
    for name, schema in ((doc.get('components') or {}).get('schemas') or {}).items():
        collect_schema(doc, schema, name, out)
    return out


findings = []  # (rule, method, path, detail)


def add(rule, method, path, detail):
    findings.append((rule, method, path, detail))


base = load(base_p)
cur = load(cur_p)

base_paths = base.get('paths') or {}
cur_paths = cur.get('paths') or {}
base_ops = sum(len(ops(base, p)) for p in base_paths)
cur_ops = sum(len(ops(cur, p)) for p in cur_paths)

print('READ baseline paths=%d operations=%d ; current paths=%d operations=%d'
      % (len(base_paths), base_ops, len(cur_paths), cur_ops))
if base_ops == 0 or cur_ops == 0:
    print('FAIL  one side documents no operation at all — a comparison against nothing is '
          'not a compatibility reading (a truncated generator looks exactly like this)')
    sys.exit(1)

for path in sorted(base_paths):
    if path not in cur_paths:
        for method in sorted(ops(base, path)):
            add('R1', method.upper(), path, 'path is no longer documented')
        continue
    for method in sorted(ops(base, path)):
        if method not in ops(cur, path):
            add('R2', method.upper(), path, 'operation is no longer documented')
            continue
        b_op = ops(base, path)[method]
        c_op = ops(cur, path)[method]
        b_id = b_op.get('operationId')
        c_id = c_op.get('operationId')
        if b_id and b_id != c_id:
            add('R3', method.upper(), path,
                'operationId %r became %r' % (b_id, c_id))

        # R4 — a parameter the client must now send. A parameter that disappeared, or one
        # that became optional, is a relaxation and stays quiet on purpose.
        def required_params(op):
            out = {}
            for prm in op.get('parameters') or []:
                prm = resolve(base if op is b_op else cur, prm)
                out[(prm.get('in'), prm.get('name'))] = bool(prm.get('required'))
            return out

        b_prm, c_prm = required_params(b_op), required_params(c_op)
        for key, req in c_prm.items():
            if req and not b_prm.get(key, False):
                add('R4', method.upper(), path,
                    'parameter %s in %s is now required' % (key[1], key[0]))

        # R5 — a documented status code that stopped being documented. An integrator reads
        # those codes to decide what to do; losing one is losing an answer.
        b_codes = {k for k in (b_op.get('responses') or {}) if not k.startswith('x-')}
        c_codes = {k for k in (c_op.get('responses') or {}) if not k.startswith('x-')}
        for code in sorted(b_codes - c_codes):
            add('R5', method.upper(), path, 'response %s is no longer documented' % code)

b_schemas, c_schemas = collect(base), collect(cur)
for kind, rule, label in (('type', 'R7', 'type'), ('enum', 'R6', 'enum value')):
    for site, value in b_schemas[kind].items():
        if site not in c_schemas[kind]:
            continue
        now = c_schemas[kind][site]
        if kind == 'type':
            if value != now:
                add(rule, '-', 'components.schemas', '%s changed %s -> %s' % (site, value, now))
        else:
            lost = [v for v in value if v not in now]
            if lost:
                add(rule, '-', 'components.schemas',
                    '%s lost %s %s' % (site, label, ', '.join(lost)))

# Exemptions: `RULE METHOD PATH  reason` (a comment line starts with #). The reason is not
# decoration — it is the sentence a reader in six months will need, and its absence is the
# difference between a governance record and a mute switch.
exempt = {}
if allow_p:
    try:
        with io.open(allow_p, encoding='utf-8') as fh:
            lines = fh.read().splitlines()
    except OSError:
        lines = []
    for n, line in enumerate(lines, 1):
        text = line.strip()
        if not text or text.startswith('#'):
            continue
        parts = text.split()
        if len(parts) < 4:
            print('FAIL  %s:%d has no reason — an exemption without a reason is a mute switch'
                  % (allow_p, n))
            sys.exit(1)
        rule, method, path = parts[0], parts[1], parts[2]
        reason = ' '.join(parts[3:])
        if len(reason) < 12:
            print('FAIL  %s:%d reason is too short to be a reason (%r) — write why this loss is '
                  'acceptable and for whom' % (allow_p, n, reason))
            sys.exit(1)
        if (rule, method, path) in exempt:
            # Two sentences about one loss: the second would win silently, and the reader
            # would see one reason while believing the other was never written. Refuse both
            # instead of picking one (the same shape as a stale exemption).
            print('FAIL  %s:%d is a second exemption for %s %s %s — two reasons for one loss, '
                  'one of which would silently disappear' % (allow_p, n, rule, method, path))
            sys.exit(1)
        exempt[(rule, method, path)] = reason

used = set()
real = []
for rule, method, path, detail in findings:
    key = (rule, method, path)
    if key in exempt:
        used.add(key)
        print('exempt %s %s %s :: %s (%s)' % (rule, method, path, detail, exempt[key]))
        continue
    print('%s %s %s :: %s' % (rule, method, path, detail))
    real.append((rule, method, path, detail))

stale = sorted(set(exempt) - used)
for key in stale:
    print('FAIL  exemption %s %s %s matches no finding — delete it (an exemption that exempts '
          'nothing is a promise nobody keeps)' % key)

if stale:
    sys.exit(1)
sys.exit(1 if real else 0)
PYEOF
}

# materialise <ref> <dest> — one side of the comparison, from the repo, never from a laptop.
materialise() {
  local ref="$1" dest="$2"
  if [ "$ref" = "INDEX" ]; then
    if ! git -C "$ROOT" show ":$SPEC" >"$dest" 2>/dev/null || [ ! -s "$dest" ]; then
      bad "the current side is not in the index ($SPEC) — add it, or the comparison would read this machine"
      return 1
    fi
    return 0
  fi
  if ! git -C "$ROOT" cat-file -e "$ref:$SPEC" 2>/dev/null; then
    bad "$ref does not carry $SPEC — there is no baseline spec in that ref"
    return 1
  fi
  git -C "$ROOT" show "$ref:$SPEC" >"$dest" 2>/dev/null || { bad "could not read $SPEC from $ref"; return 1; }
  [ -s "$dest" ] || { bad "$SPEC in $ref is empty"; return 1; }
  return 0
}

check_main() {
  local ref="${1:-}"
  local cur="$WORK/current.json" base="$WORK/baseline.json" out rc
  materialise INDEX "$cur" || return 1
  if [ -z "$ref" ]; then
    if ref="$(newest_spec_tag)"; then
      say "      baseline tag: $ref (newest tag carrying $SPEC)"
      materialise "$ref" "$base" || return 1
    else
      # ② zero released operations: a known zero, printed. Tags were examined; none carried
      # the artifact, so nothing in this repo has promised a client anything yet.
      say "      baseline tag: none — examined $(git -C "$ROOT" tag --list 2>/dev/null | wc -l | tr -d ' ') tag(s), none carries $SPEC"
      say "      0 released operations: no release has shipped a contract, so this commit cannot break one."
      say "      the first tag that ships the spec turns this row into a real comparison (doc/310 Phase 3, §20 A21)"
      good "no baseline: nothing released to break (0 operations)"
      return 0
    fi
  else
    say "      baseline ref: $ref"
    materialise "$ref" "$base" || return 1
  fi
  diff_specs "$base" "$cur"
  rc=$?
  if [ $rc -eq 0 ]; then
    good "no breaking change: the index keeps every path, operation, operationId, required-parameter set, response code, enum value and type the baseline documents"
    return 0
  fi
  bad "breaking change(s) above — keep the old shape, version the new one, or write an exemption with a reason in $ALLOW"
  return 1
}

# selftest — a rule that has never fired is a rule nobody knows the shape of. Each case is a
# fabricated pair in this run's temp dir; the tracked files and the allowlist are never
# edited to prove a refusal.
selftest() {
  say "selftest: each rule must fire on a fabricated pair and stay quiet on a clean one"
  local hits=0 rc d
  local d0="$WORK/c0" d1="$WORK/c1"
  mkdir -p "$d0" "$d1" || return 1

  write_pair "$d0/a.json" "$d1/a.json"
  diff_specs "$d0/a.json" "$d1/a.json" ""
  rc=$?
  if [ $rc -eq 0 ]; then say "  ok     an unchanged pair reads clean"; else say "  FAIL   an unchanged pair red"; return 1; fi

  # each case: base = d0/a.json, current = the mutation
  mutate() { # mutate <dir> <python-fragment-name>
    local dst="$1"
    "$PY" - "$d0/a.json" "$dst/x.json" "$2" <<'PYEOF'
import io, json, sys
src, dst, which = sys.argv[1], sys.argv[2], sys.argv[3]
doc = json.load(io.open(src, encoding='utf-8'))
paths = doc['paths']
op = paths['/api/v1/widgets']['get']
if which == 'r1':
    del paths['/api/v1/widgets/{id}']
elif which == 'r2':
    del paths['/api/v1/widgets']['delete']
elif which == 'r3':
    op['operationId'] = 'widgets.list.v2'
elif which == 'r4':
    op['parameters'].append({'in': 'query', 'name': 'tenant', 'required': True,
                             'schema': {'type': 'string'}})
elif which == 'r5':
    del op['responses']['404']
elif which == 'r6':
    doc['components']['schemas']['Widget']['properties']['state']['enum'] = ['on']
elif which == 'r7':
    doc['components']['schemas']['Widget']['properties']['name']['type'] = 'integer'
with io.open(dst, 'w', encoding='utf-8') as fh:
    json.dump(doc, fh, ensure_ascii=False)
PYEOF
  }

  expect_rule() { # expect_rule <rule> <name> <mutation>
    local rule="$1" name="$2" mut="$3" out line
    d="$WORK/case-$mut"; rm -rf "$d"; mkdir -p "$d"
    mutate "$d" "$mut" || { say "  FAIL   could not fabricate the $name case"; return 1; }
    out="$(diff_specs "$d0/a.json" "$d/x.json" "")"; rc=$?
    line="$(printf '%s\n' "$out" | grep -F "$rule " | head -1)"
    if [ $rc -ne 0 ] && [ -n "$line" ]; then
      say "  ok     $name fires ($rule) — $line"
      hits=$((hits + 1))
    else
      say "  FAIL   $name did not fire ($rule); output: $(printf '%s\n' "$out" | tr '\n' ' ' | cut -c1-200)"
    fi
  }

  expect_rule R1 "a removed path"            r1
  expect_rule R2 "a removed operation"       r2
  expect_rule R3 "a renamed operationId"     r3
  expect_rule R4 "a newly required parameter" r4
  expect_rule R5 "a removed response code"   r5
  expect_rule R6 "a narrowed enum"           r6
  expect_rule R7 "a changed schema type"     r7

  # relaxation must stay quiet: adding a path, an optional parameter and a response code is
  # what a healthy release looks like, and a gate that reds on it gets switched off.
  d="$WORK/case-growth"; rm -rf "$d"; mkdir -p "$d"
  "$PY" - "$d0/a.json" "$d/x.json" <<'PYEOF'
import io, json, sys
src, dst = sys.argv[1], sys.argv[2]
doc = json.load(io.open(src, encoding='utf-8'))
doc['paths']['/api/v1/gadgets'] = {'get': {'operationId': 'gadgets.list',
    'responses': {'200': {'description': 'ok'}}}}
op = doc['paths']['/api/v1/widgets']['get']
op['parameters'].append({'in': 'query', 'name': 'cursor', 'required': False,
                         'schema': {'type': 'string'}})
op['responses']['429'] = {'description': 'rate limited'}
doc['components']['schemas']['Widget']['properties']['state']['enum'] = ['on', 'off', 'paused']
with io.open(dst, 'w', encoding='utf-8') as fh:
    json.dump(doc, fh, ensure_ascii=False)
PYEOF
  diff_specs "$d0/a.json" "$d/x.json" "" >/dev/null
  rc=$?
  if [ $rc -eq 0 ]; then say "  ok     growth (new path, optional parameter, new code, wider enum) reads clean"; hits=$((hits + 1));
  else say "  FAIL   a compatible growth was refused"; fi

  # an exemption with a reason turns the finding into a note; without a reason it is a mute
  # switch, and one that matches nothing is a lie about the past.
  local allow="$WORK/allow.txt"
  printf 'R2 DELETE /api/v1/widgets  removed after the v0.6 deprecation window\n' >"$allow"
  d="$WORK/case-r2"; rm -rf "$d"; mkdir -p "$d"; mutate "$d" r2 || return 1
  diff_specs "$d0/a.json" "$d/x.json" "$allow" >/dev/null; rc=$?
  if [ $rc -eq 0 ]; then say "  ok     an exemption with a reason passes and prints the reason"; hits=$((hits + 1));
  else say "  FAIL   a written-down exemption was still refused"; fi

  printf 'R2 DELETE /api/v1/widgets  nope\n' >"$WORK/allow-short.txt"
  diff_specs "$d0/a.json" "$d/x.json" "$WORK/allow-short.txt" >/dev/null; rc=$?
  if [ $rc -ne 0 ]; then say "  ok     an exemption reason shorter than 12 chars is refused"; hits=$((hits + 1));
  else say "  FAIL   a reason-less exemption passed"; fi

  printf 'R5 GET /api/v1/gone  this endpoint never existed anywhere\n' >"$WORK/allow-stale.txt"
  diff_specs "$d0/a.json" "$d1/a.json" "$WORK/allow-stale.txt" >/dev/null; rc=$?
  if [ $rc -ne 0 ]; then say "  ok     an exemption that matches no finding is refused"; hits=$((hits + 1));
  else say "  FAIL   a stale exemption passed"; fi

  printf 'R2 DELETE /api/v1/widgets  duplicate attempt one: removed in v0.6
R2 DELETE /api/v1/widgets  duplicate attempt two: removed in v0.7
' >"$WORK/allow-dup.txt"
  diff_specs "$d0/a.json" "$d/x.json" "$WORK/allow-dup.txt" >/dev/null; rc=$?
  if [ $rc -ne 0 ]; then say "  ok     two exemptions for one loss are refused, not silently resolved"; hits=$((hits + 1));
  else say "  FAIL   a duplicated exemption passed"; fi

  # a truncated spec on either side is "could not look", not "no changes"
  printf '{"openapi":"3.1.0","paths":{}}' >"$WORK/empty.json"
  diff_specs "$d0/a.json" "$WORK/empty.json" "" >/dev/null; rc=$?
  if [ $rc -ne 0 ]; then say "  ok     a side with zero operations is refused, not read as compatible"; hits=$((hits + 1));
  else say "  FAIL   an empty spec read as compatible"; fi

  if [ "$hits" -eq 13 ]; then say "selftest: OK (13 cases; 7 rules fire, growth passes, 4 exemption shapes, 1 empty side)"; return 0; fi
  say "selftest: FAILED ($hits/13)"; return 1
}

# write_pair — the fabricated pair both the clean case and every mutation start from. A
# parameter whose *type* differs can only be seen through `schema`, and an enum only through
# a component, so the pair carries both shapes.
write_pair() {
  local base="$1" cur="$2"
  cat >"$base" <<'JSONEOF'
{
  "openapi": "3.1.0",
  "info": {"title": "fixture", "version": "1"},
  "paths": {
    "/api/v1/widgets": {
      "get": {
        "operationId": "widgets.list",
        "parameters": [
          {"in": "query", "name": "limit", "required": false, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "ok"}, "404": {"description": "not found"}}
      },
      "delete": {
        "operationId": "widgets.purge",
        "responses": {"204": {"description": "gone"}}
      }
    },
    "/api/v1/widgets/{id}": {
      "get": {
        "operationId": "widgets.get",
        "parameters": [
          {"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "components": {
    "schemas": {
      "Widget": {
        "type": "object",
        "properties": {
          "name": {"type": "string"},
          "state": {"type": "string", "enum": ["on", "off"]}
        }
      }
    }
  }
}
JSONEOF
  cp "$base" "$cur"
}

case "${1:-check}" in
  check)      shift || true; check_main "${1:-}"; rc=$? ;;
  --selftest) selftest; rc=$? ;;
  *)          printf 'usage: %s [check [REF]|--selftest]\n' "$(basename "$0")" >&2; rc=2 ;;
esac
exit "$rc"
