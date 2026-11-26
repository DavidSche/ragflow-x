#!/usr/bin/env python3
"""Put the two machine-readable contract fields into the generated spec (doc/310 T310-05/06).

Why this is a post-processing step and not an annotation:

  * swag v2 cannot carry them as annotations at all — `operationv3.go:163` files an
    operation-level `@x-` under `responses`, so the artifact ends up with
    `"responses": {"200": …, "x-ragflow-permission": "read:agent"}` (a string where an
    OpenAPI Response object must be). Re-adding the annotation would ship an illegal spec.
  * The facts still have a single source: `x-ragflow-permission` comes from
    `accessRules` (dumped by `cmd/apicontract -extensions`), `x-error-codes` from the
    `@Failure` descriptions the generator already derived from `httperr` return points.

The rewrite is line-based on purpose. Re-serialising 2.5 MB of JSON/YAML with a second
marshaller would make "the artifacts did not change" a claim about the marshaller; inserting
keys after the `operationId` line changes only the lines it means to. Every run drops the
keys it is about to write first, so applying twice is applying once.

Verification is not "the text contains the string": after writing, the JSON is parsed, the
`docs.go` template is parsed (after substituting the one `{{ marshal .Schemes }}` action) and
the YAML is scanned indentation-wise; each operation's two fields are compared with the
expectation derived from the spec itself. A rewrite that produced unparseable artifacts
fails loudly instead of shipping.

exit 0 = the artifacts carry the expectation; 1 = they do not; 2 = the question could not
be answered (missing file, bad sidecar) — never a pass.
"""

import io
import json
import os
import re
import shutil
import sys
import tempfile

FILES = ("docs.go", "swagger.json", "swagger.yaml")
PERM = "x-ragflow-permission"
CODES = "x-error-codes"

# A business code is five digits, not part of a longer number and not part of a version
# string (`0.6.5`) or a parameter name (`page_size`).
CODE_RE = re.compile(r"(?<![\w.])([1-5]\d{4})(?![\w.])")

JSON_OPID = re.compile(r'^(\s*)"operationId": "([A-Za-z0-9_.\-]+)",$')
JSON_DROP = re.compile(r'^\s*"(x-ragflow-permission|x-error-codes)":')
YAML_OPID = re.compile(r'^(\s*)operationId: ([A-Za-z0-9_.\-]+)$')
YAML_DROP = re.compile(r'^\s*(x-ragflow-permission|x-error-codes):')

GO_TEMPLATE_START = "const docTemplate = `"
GO_TEMPLATE_END = "\n`\n"
SCHEMES_ACTION = "{{ marshal .Schemes }}"
# swag writes a backtick inside the raw-string template as ascii-escaped below; the rendered
# document has the character itself, so a verifier that skipped this would call valid JSON
# invalid.
BACKTICK_ESCAPE = '` + "`" + `'


def die(msg, code=2):
    sys.stderr.write(msg + "\n")
    sys.exit(code)


def read(path):
    return io.open(path, encoding="utf-8").read()


def write(path, text):
    tmp = path + ".tmp"
    io.open(tmp, "w", encoding="utf-8", newline="").write(text)
    os.replace(tmp, path)


def load_sidecar(path):
    """`{"operations": {"GET /api/v1/agents": "read:agent"}}` — the accessRules dump.

    The check path prefers `--rules access.go`: comparing the artifact with the *writer's* own
    dump would only prove the writer is deterministic."""
    try:
        raw = json.loads(read(path))
    except (IOError, OSError, ValueError) as e:
        die("cannot read the permission sidecar %s: %s" % (path, e))
    ops = raw.get("operations")
    if not isinstance(ops, dict):
        die("the permission sidecar has no `operations` map — regenerate it with "
            "`go run ./cmd/apicontract -report -extensions <file>`")
    return ops


RULE_RE = re.compile(
    r'\{Method:\s*"([A-Z]+)",\s*Path:\s*"([^"]+)",\s*Action:\s*"([^"]+)",\s*Resource:\s*"([^"]+)"')


def normalise(path):
    """accessRules writes `:id`, the spec writes `{id}`. Both sides go through this, so a
    parameterised route cannot read as "no rule" (that bug hid 354 of 569 permissions)."""
    segs = [("{" + s[1:] + "}") if s[:1] in (":", "*") else s for s in path.strip("/").split("/")]
    return "/" + "/".join(segs)


def rules_from_go(path):
    """The gate's own reader for `accessRules`, so the artifact is compared with the source
    by something other than the writer (a writer checking itself is a tautology)."""
    try:
        text = read(path)
    except (IOError, OSError) as e:
        die("cannot read %s: %s" % (path, e))
    rules = {}
    for method, p, action, resource in RULE_RE.findall(text):
        rules["%s %s" % (method, normalise(p))] = "%s:%s" % (action, resource)
    if len(rules) < 100:
        die("%s declares only %d access rule(s) — the table's shape changed, refusing to "
            "call %d operation(s) 'public'" % (path, len(rules), 0))
    return rules


def load_spec(spec_dir):
    try:
        return json.loads(read(os.path.join(spec_dir, "swagger.json")))
    except (IOError, OSError, ValueError) as e:
        die("cannot read swagger.json: %s" % e)


def permissions(kind, path):
    """The (method, path) -> "action:resource" table, as a plain dict.

    Two readers, on purpose: `cmd/apicontract -extensions` is the writer (it is the analyser
    that knows the spec spelling), `--rules` is the gate's own regex over `access.go`. A gate
    that compared the artifact with the writer's dump would only prove the writer is
    deterministic."""
    if kind == "rules":
        return rules_from_go(path)
    return load_sidecar(path)


def expectation(spec, perms):
    """operationId -> (method+path, permission or None, sorted codes from its own failures)."""
    exp = {}
    for path, item in (spec.get("paths") or {}).items():
        for method, op in item.items():
            if not isinstance(op, dict) or "operationId" not in op:
                continue
            codes = set()
            for status, resp in (op.get("responses") or {}).items():
                if not isinstance(resp, dict) or status[:1] not in ("4", "5"):
                    continue
                codes |= set(CODE_RE.findall(resp.get("description") or ""))
            key = "%s %s" % (method.upper(), path)
            exp[op["operationId"]] = (key, perms.get(key), sorted(codes))
    return exp


def fields(perm, codes):
    out = []
    if perm:
        out.append((PERM, perm))
    out.append((CODES, codes))
    return out


def template_body(text):
    i = text.find(GO_TEMPLATE_START)
    if i < 0:
        return None
    i += len(GO_TEMPLATE_START)
    # The literal ends with "}`" — no newline before the closing backtick — so the end is the
    # last backtick before the SwaggerInfo declaration, not the next "\n`\n".
    k = text.find(GO_TEMPLATE_END, i)
    if k < 0:
        k = text.find("// SwaggerInfo", i)
    if k < 0:
        return None
    j = text.rfind("`", i, k)
    if j < 0:
        return None
    return text[i:j]


def renderable(body):
    """The template as the document it renders to: un-escape backticks, resolve the one
    template action. What is left has to parse as the spec, or the artifact is broken."""
    return body.replace(BACKTICK_ESCAPE, "`").replace(SCHEMES_ACTION, '["http"]')


def rewrite_json_like(text, exp, is_go):
    """Insert (or re-insert) the two keys after each operation's `operationId` line.

    Same routine for swagger.json and for the docs.go template: the template *is* the JSON
    document with one template action in it, so the operation objects are laid out the same
    way and the key has to be spelled identically in both."""
    out = []
    seen = set()
    for line in text.split("\n"):
        m = None if is_go and SCHEMES_ACTION in line else JSON_OPID.match(line)
        if JSON_DROP.match(line) and not line.lstrip().startswith('"operationId"'):
            continue  # drop the previous run's copy so a second run is not a second copy
        out.append(line)
        if not m:
            continue
        opid = m.group(2)
        if opid not in exp:
            continue
        seen.add(opid)
        _, perm, codes = exp[opid]
        for name, value in fields(perm, codes):
            v = json.dumps(value, ensure_ascii=False)
            out.append('%s"%s": %s,' % (m.group(1), name, v))
    return "\n".join(out), seen


def rewrite_yaml(text, exp):
    out = []
    seen = set()
    for line in text.split("\n"):
        if YAML_DROP.match(line) and not line.lstrip().startswith("operationId:"):
            continue
        out.append(line)
        m = YAML_OPID.match(line)
        if not m:
            continue
        opid = m.group(2)
        if opid not in exp:
            continue
        seen.add(opid)
        _, perm, codes = exp[opid]
        for name, value in fields(perm, codes):
            if isinstance(value, list):
                v = "[" + ", ".join(json.dumps(c) for c in value) + "]"
            else:
                v = value
            out.append("%s%s: %s" % (m.group(1), name, v))
    return "\n".join(out), seen


def apply(spec_dir, perms):
    spec = load_spec(spec_dir)
    exp = expectation(spec, perms)
    if not exp:
        die("the spec has no operations with an operationId — refusing to write nothing")
    unknown = sorted(set(perms) - {v[0] for v in exp.values()})
    if unknown:
        die("the sidecar names %d operation(s) the spec does not carry (accessRules and the "
            "route table disagree):\n  %s" % (len(unknown), "\n  ".join(unknown[:5])))
    for name in FILES:
        p = os.path.join(spec_dir, name)
        if not os.path.exists(p):
            die("%s is missing — no artifact is not 'nothing to do'" % name)
        text = read(p)
        if name == "swagger.yaml":
            new, seen = rewrite_yaml(text, exp)
        else:
            new, seen = rewrite_json_like(text, exp, is_go=(name == "docs.go"))
        missing = sorted(set(exp) - seen)
        if missing:
            die("%s: %d operation(s) have no `operationId` line to hang the extensions on "
                "(first: %s)" % (name, len(missing), missing[0]))
        write(p, new)
    return len(exp), sum(1 for v in exp.values() if v[1])


def verify(spec_dir, perms):
    """Re-read the three artifacts and compare what they *say* with the expectation."""
    spec = load_spec(spec_dir)
    exp = expectation(spec, perms)
    problems = []
    checked = 0

    # ── swagger.json: parse, because a key inserted at the wrong depth is not a key ──
    try:
        got = json.loads(read(os.path.join(spec_dir, "swagger.json")))
    except (IOError, OSError, ValueError) as e:
        return ["swagger.json does not parse: %s" % e], 0
    seen = set()
    for path, item in (got.get("paths") or {}).items():
        for method, op in item.items():
            if not isinstance(op, dict) or "operationId" not in op:
                continue
            opid = op["operationId"]
            seen.add(opid)
            checked += 1
            _, perm, codes = exp[opid]
            if perm:
                if op.get(PERM) != perm:
                    problems.append("swagger.json %s %s: %s is %r, accessRules says %r"
                                    % (method.upper(), path, PERM, op.get(PERM), perm))
            elif PERM in op:
                problems.append("swagger.json %s %s: carries %s=%r but no accessRules entry "
                                "covers it" % (method.upper(), path, PERM, op[PERM]))
            if op.get(CODES) != codes:
                problems.append("swagger.json %s %s: %s is %r, its @Failure descriptions say %r"
                                % (method.upper(), path, CODES, op.get(CODES), codes))
    for opid in sorted(set(exp) - seen):
        problems.append("swagger.json lost operation %s" % opid)

    # ── docs.go: substitute the one template action, then parse ──
    text = read(os.path.join(spec_dir, "docs.go"))
    body = template_body(text)
    if body is None:
        problems.append("docs.go has no docTemplate literal")
    else:
        body = renderable(body)
        try:
            dg = json.loads(body)
        except ValueError as e:
            problems.append("docs.go's template does not render to JSON: %s" % e)
            dg = None
        if dg is not None:
            n = 0
            for path, item in (dg.get("paths") or {}).items():
                for method, op in item.items():
                    if not isinstance(op, dict) or "operationId" not in op:
                        continue
                    opid = op["operationId"]
                    if opid not in exp:
                        continue
                    n += 1
                    _, perm, codes = exp[opid]
                    if perm and op.get(PERM) != perm:
                        problems.append("docs.go %s %s: %s is %r, accessRules says %r"
                                        % (method.upper(), path, PERM, op.get(PERM), perm))
                    if not perm and PERM in op:
                        problems.append("docs.go %s %s: carries %s but no rule covers it"
                                        % (method.upper(), path, PERM))
                    if op.get(CODES) != codes:
                        problems.append("docs.go %s %s: %s is %r, its @Failure descriptions "
                                        "say %r" % (method.upper(), path, CODES,
                                                    op.get(CODES), codes))
            if n != len(exp):
                problems.append("docs.go carries %d of the %d operations" % (n, len(exp)))

    # ── swagger.yaml: scanned by indentation (no PyYAML on the target hosts) ──
    path_cur, opid_y = None, None
    seen_y = set()
    yperm, ycodes = {}, {}
    for line in read(os.path.join(spec_dir, "swagger.yaml")).split("\n"):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        stripped = line.strip()
        if indent == 2 and stripped.startswith("/"):
            path_cur = stripped.rstrip(":")
        elif indent == 4 and stripped.endswith(":"):
            opid_y = None
        elif indent == 6:
            name, _, value = stripped.partition(": ")
            if name == "operationId":
                opid_y = value.strip()
                seen_y.add(opid_y)
            elif name == PERM:
                yperm[opid_y] = value.strip()
            elif name == CODES:
                ycodes[opid_y] = sorted(re.findall(r'"([^"]+)"', value))
        elif indent < 2:
            path_cur = None
    for opid in sorted(set(exp) - seen_y):
        problems.append("swagger.yaml lost operation %s" % opid)
    for opid in seen_y & set(exp):
        _, perm, codes = exp[opid]
        if perm and yperm.get(opid) != perm:
            problems.append("swagger.yaml %s: %s is %r, accessRules says %r"
                            % (opid, PERM, yperm.get(opid), perm))
        if not perm and opid in yperm:
            problems.append("swagger.yaml %s: carries %s but no rule covers it" % (opid, PERM))
        if ycodes.get(opid) != codes:
            problems.append("swagger.yaml %s: %s is %r, expected %r"
                            % (opid, CODES, ycodes.get(opid), codes))
    return problems, checked


def check(spec_dir, perms):
    problems, checked = verify(spec_dir, perms)
    if problems:
        for p in problems[:12]:
            print("FAIL  " + p)
        if len(problems) > 12:
            print("FAIL  … and %d more" % (len(problems) - 12))
        print("openapi-extensions: FAIL — %d problem(s); regenerate the artifacts with "
              "`bash scripts/release-gate/openapi.sh gen internal/docs`" % len(problems))
        return 1
    print("ok    %d operation(s) carry %s and %s in all three artifacts"
          % (checked, PERM, CODES))
    print("      %d operation(s) are covered by an accessRules entry, %d are public"
          % (len(perms), checked - len(perms)))
    print("openapi-extensions: PASS")
    return 0


def selftest():
    here = os.path.dirname(os.path.abspath(__file__))
    root = os.path.abspath(os.path.join(here, "..", ".."))
    src = os.path.join(root, "internal", "docs")
    sc = permissions("rules", os.path.join(root, "internal", "router", "access.go"))
    work = tempfile.mkdtemp(prefix="oapiext-")
    hits = 0
    try:
        base = os.path.join(work, "base")
        shutil.copytree(src, base)
        print("  baseline: the committed artifacts must read clean")
        rc = apply(base, sc)
        problems, _ = verify(base, sc)
        if problems:
            print("  FAIL   the rewrite of the committed artifacts does not verify: %s"
                  % problems[0])
            return 1
        print("  ok     baseline clean (%d operations)" % rc[0])

        cases = []

        def case(name, target, mutate):
            d = os.path.join(work, "case-%d" % len(cases))
            shutil.copytree(base, d)
            p = os.path.join(d, target)
            text = read(p)
            new = mutate(text)
            if new == text:
                cases.append((name, None))
                return
            write(p, new)
            cases.append((name, d))

        def drop_first_perm(t):
            for i, l in enumerate(t.split("\n")):
                if '"%s":' % PERM in l:
                    ls = t.split("\n")
                    return "\n".join(ls[:i] + ls[i + 1:])
            return t

        def drop_first_perm_yaml(t):
            for i, l in enumerate(t.split("\n")):
                if l.strip().startswith(PERM + ":"):
                    ls = t.split("\n")
                    return "\n".join(ls[:i] + ls[i + 1:])
            return t

        def edit_perm(t):
            m = re.search(r'"%s": "[a-z]+:[a-z\-]+"' % PERM, t)
            return t if not m else t.replace(m.group(0), '"%s": "read:nothing"' % PERM, 1)

        def drop_one_code(t):
            m = re.search(r'"%s": \["([1-5]\d{4})"' % CODES, t)
            return t if not m else t[:m.start()] + '"%s": [' % CODES + t[m.end():]

        def drop_codes_key(t):
            ls = [l for l in t.split("\n")]
            for i, l in enumerate(ls):
                if l.strip().startswith('"%s":' % CODES):
                    return "\n".join(ls[:i] + ls[i + 1:])
            return t

        case("a swagger.json that lost one permission", "swagger.json", drop_first_perm)
        case("a permission that disagrees with accessRules", "swagger.json", edit_perm)
        case("a swagger.json that lost one error code", "swagger.json", drop_one_code)
        case("a docs.go that disagrees with swagger.json", "docs.go", edit_perm)
        case("a docs.go that lost the error codes of one operation", "docs.go",
             drop_codes_key)
        case("a swagger.yaml that lost one permission", "swagger.yaml", drop_first_perm_yaml)

        for name, d in cases:
            if d is None:
                print("  FAIL   mutation %r did not apply — it proves nothing" % name)
                continue
            problems, _ = verify(d, sc)
            if problems:
                print("  ok     %s is refused" % name)
                hits += 1
            else:
                print("  FAIL   %s passed" % name)

        # a sidecar that names a path the spec does not carry must be refused, not ignored
        bad = dict(sc)
        bad["GET /api/v1/this/route/does/not/exist"] = "read:nothing"
        d = os.path.join(work, "case-sidecar")
        shutil.copytree(base, d)
        rc = 0
        try:
            apply(d, bad)
        except SystemExit as e:
            rc = e.code or 0
        if rc != 0:
            print("  ok     a sidecar entry the spec does not carry is refused")
            hits += 1
        else:
            print("  FAIL   a sidecar entry with no operation passed")
    finally:
        shutil.rmtree(work, ignore_errors=True)

    if hits == 7:
        print("selftest: OK (7 mutations refused, 1 baseline clean)")
        return 0
    print("selftest: FAILED (%d/7 mutations refused)" % hits)
    return 1


def main(argv):
    args = [a for a in argv[1:] if not a.startswith("--")]
    flags = [a for a in argv[1:] if a.startswith("--")]
    sidecar = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                           "openapi-extensions.json")
    if "--sidecar" in argv:
        sidecar = argv[argv.index("--sidecar") + 1]
        args = [a for a in args if a != sidecar]
    kind, path = "sidecar", sidecar
    if "--rules" in argv:
        kind, path = "rules", argv[argv.index("--rules") + 1]
        args = [a for a in args if a != path]
    if "--selftest" in flags:
        return selftest()
    perms = permissions(kind, path)
    if not args:
        die("usage: %s [apply|check] DIR [--sidecar FILE] | --selftest" % os.path.basename(argv[0]))
    mode, spec_dir = "apply", args[0]
    if args[0] in ("apply", "check"):
        mode = args[0]
        if len(args) < 2:
            die("%s needs a directory" % mode)
        spec_dir = args[1]
    if mode == "check":
        return check(spec_dir, perms)
    n, withperm = apply(spec_dir, perms)
    print("openapi-extensions: %d operation(s) rewritten, %d with %s" % (n, withperm, PERM))
    problems, _ = verify(spec_dir, perms)
    if problems:
        for p in problems[:8]:
            print("FAIL  " + p)
        print("openapi-extensions: FAIL — the rewrite did not verify")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
