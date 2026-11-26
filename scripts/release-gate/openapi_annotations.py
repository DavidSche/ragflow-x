#!/usr/bin/env python3
"""openapi_annotations.py — refuse a new API endpoint that arrives without a contract.

Why this exists (doc/310 §22, doc/304 §4 E162)
---------------------------------------------
`doc/specification/11`(权限) has one rule that makes RBAC auditable: every route must appear in
`accessRules`, and a test derives the protected set **from the running router** instead of a
second hand-written list (E159). The API description needs the same shape: an endpoint whose
annotation is missing is an endpoint with no contract — no request body, no response schema,
no description of what it does — and until now nothing refused one.

The constraint (AGENTS.md 「API 契约（强制）」) says: 新增或修改接口时必须写 OpenAPI 注解。 A
sentence in a document is not a check (E129 「文字承诺 ≠ 实现」), so this file is the reader:

  R1  every operation in the spec is either **fully annotated** or listed in the frozen
      baseline. A new endpoint cannot inherit the exemption — it must be documented.
  R2  the baseline may not contain an operation the spec no longer declares: a removed route
      must leave a visible trace in the diff (`--write-baseline`), not linger as a lie.
  R3  the baseline may not contain an operation that **is** fully annotated: progress is
      recorded by deleting the line. Otherwise the file grows stale and "616 条待补" would
      mean nothing a year from now.
  R4  `--write-baseline` refuses to grow the set. Regenerating the baseline is how someone
      would otherwise silence a brand-new undocumented endpoint in one command; the ratchet
      only turns one way.
  R5  the coverage reading is printed on every run (annotated / total / exempted). A number
      nobody sees cannot fall.
  R6  a success response is documented by a 2xx with a schema, **or** by a redirect that
      names its destination, **or** by a non-JSON 2xx whose media type is the payload
      (CSV download, stream, HTML shell). The first batch of real annotations (doc/310
      T310-01) hit both shapes: `GET /auth/oidc/{start,callback}` only redirect, and
      `GET /tenants/export` returns CSV. Requiring a JSON body schema there would have
      forced a fake schema into the published contract — the opposite of the point.
  R8  a `204`/`205` with **no** content is a complete success: the RFC forbids a body, so
      demanding a schema would only produce a lie (`{type: string}` on an empty answer).
      `DELETE /reports/{id}/schedule` answers exactly that way, and before this rule the only
      way to satisfy the check was to invent a payload. 200 with an empty body is *not*
      accepted: there the client is entitled to a body.
  R7  "has a schema" is not "has a payload": the 2xx is resolved and refused when its
      `data` is `{}` (the shared `docs.Envelope`) or when the target was turned into an
      `allOf` by a field override. Both are green under R6 and useless to a client.

What counts as "fully annotated" (the definition is deliberately narrow and machine-checkable;
see doc/310 §8.2 — it is the minimum for a client to code against):
  * `summary` — present, not the generator placeholder, not the path repeated back;
  * `description` — non-empty, at least 20 characters (a restatement of the summary is not a
    description, and 20 chars is long enough that a placeholder fails it);
  * `tags` — at least one, and not the placeholder `auto`;
  * `operationId` — set, not `OpenAPIRouteNN`, and unique across the document (a client
    generates method names from it);
  * request body — required for POST/PUT/PATCH, with a `$ref` or an object schema (a body of
    `{type: string}` documents nothing a client can send);
  * success response — at least one 2xx carrying a `$ref` or object/array schema — **or**
    one of the two non-JSON shapes that are complete contracts by themselves:
      - a 3xx that names where it goes (`Location` header): a redirect has no body by
        design, and demanding a body schema would only produce a fake one;
      - a 2xx whose declared media type is not JSON (a CSV/filename download, an event
        stream): the payload is the media type, and `{type: string}` is the honest schema
        for it. A JSON 2xx with a string schema is still a gap — that is what this rule
        exists to refuse (`--selftest` mutant M10 keeps the two apart).

Usage:
  python scripts/release-gate/openapi_annotations.py --check
  python scripts/release-gate/openapi_annotations.py --write-baseline
  python scripts/release-gate/openapi_annotations.py --selftest
  python scripts/release-gate/openapi_annotations.py --check --spec X --baseline Y   (tests)
"""

import argparse
import io
import json
import os
import re
import shutil
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DEFAULT_SPEC = os.path.join(ROOT, "internal", "docs", "swagger.json")
DEFAULT_BASELINE = os.path.join(ROOT, "scripts", "release-gate", "openapi-annotations-baseline.txt")

# `head` is included: the router serves HEAD from the same handler as GET (r.Static), and a
# document that leaves HEAD out of the denominator would silently stop describing it.
METHODS = ("get", "post", "put", "patch", "delete", "head")
BODY_METHODS = ("post", "put", "patch")
PLACEHOLDER_SUMMARY = "Auto-generated API route"
PLACEHOLDER_TAG = "auto"
AUTO_ID = re.compile(r"^OpenAPIRoute\d+$")
MIN_DESCRIPTION = 20

HEADER = """# Frozen list of endpoints that are still allowed to have no OpenAPI annotation.
#
# One per line: `METHOD /path` (sorted). This file may only SHRINK.
#   * a NEW endpoint cannot inherit the exemption — it must be annotated (s29.openapi-annotations);
#   * annotating an endpoint means deleting its line here (`--write-baseline` does it, and
#     refuses to add lines, so the ratchet only turns one way);
#   * a route that disappears must also leave this file (same command).
#
# Regenerate: python scripts/release-gate/openapi_annotations.py --write-baseline
# Constraint: AGENTS.md 「API 契约（强制）」; design: doc/310 §8.2, §22.
"""


def load_spec(path):
    with io.open(path, encoding="utf-8") as fh:
        return json.load(fh)


def operations(spec):
    """[(method, path, op)] in a stable order."""
    out = []
    for path in sorted(spec.get("paths") or {}):
        for method in METHODS:
            op = (spec["paths"][path] or {}).get(method)
            if isinstance(op, dict):
                out.append((method.upper(), path, op))
    return out


def read_baseline(path):
    if not os.path.exists(path):
        return None
    keys = []
    with io.open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            keys.append(line)
    return keys


def schema_kind(schema):
    """'ref' | 'object' | 'array' | 'scalar' | None — what a client can hang a type on."""
    if not isinstance(schema, dict):
        return None
    if schema.get("$ref"):
        return "ref"
    kind = schema.get("type")
    if kind in ("object", "array"):
        return kind
    if kind:
        return "scalar"
    if schema.get("properties"):
        return "object"
    return None


def body_documented(op):
    body = op.get("requestBody") or {}
    content = body.get("content") or {}
    for media, spec in content.items():
        if "json" not in media:
            continue
        if schema_kind(spec.get("schema")) in ("ref", "object", "array"):
            return True
    return False


# Media types whose payload *is* the contract. A JSON response is deliberately absent: a
# JSON 200 with a bare-string schema is what the rule exists to reject.
NON_JSON_BODIES = (
    "text/csv",
    "text/plain",
    "text/html",  # the SPA shell (`GET /`) is a document, not a bare-string JSON answer
    "text/event-stream",
    "application/octet-stream",
    "application/zip",
    "application/pdf",
)


def redirect_documented(op):
    """A 3xx is complete when it names the destination in a Location header."""
    for code, resp in (op.get("responses") or {}).items():
        if not str(code).startswith("3"):
            continue
        headers = {str(h).lower() for h in ((resp or {}).get("headers") or {})}
        if "location" in headers:
            return True
    return False


# Status codes whose body is empty by definition. A documented one is complete.
NO_CONTENT = ("204", "205")


def success_documented(op):
    for code, resp in (op.get("responses") or {}).items():
        if not str(code).startswith("2"):
            continue
        if str(code) in NO_CONTENT and not ((resp or {}).get("content") or {}):
            return True
        for media, spec in ((resp or {}).get("content") or {}).items():
            base = media.split(";")[0].strip().lower()
            if "json" in base:
                if schema_kind(spec.get("schema")) in ("ref", "object", "array"):
                    return True
            elif base in NON_JSON_BODIES and schema_kind(spec.get("schema")):
                return True
    if redirect_documented(op):
        return True
    return False


def untyped_payload(op, schemas):
    """Why the 2xx that satisfied success_documented still names no payload type.

    A `$ref` to a component whose `data` is `{}` is what the shared `docs.Envelope` looks
    like: the response schema is an object, so it passes "带 schema 的 2xx", and the client
    still cannot generate the type it receives. That is the difference between an endpoint
    that *has* an annotation and one that *is* a contract, so it is a separate gap.
    """
    if not schemas:
        return None
    for code, resp in (op.get("responses") or {}).items():
        if not str(code).startswith("2"):
            continue
        for media, spec in ((resp or {}).get("content") or {}).items():
            if "json" not in media.split(";")[0].strip().lower():
                continue
            ref = ((spec.get("schema") or {}).get("$ref") or "").rstrip("/")
            if not ref:
                continue
            name = ref.rsplit("/", 1)[-1]
            target = schemas.get(name)
            if not isinstance(target, dict):
                continue
            if target.get("allOf"):
                return "%s 的 2xx 指向 %s，而它被 swag 拼成了 allOf（字段覆盖会污染共享定义）" % (code, name)
            props = target.get("properties") or {}
            if "data" in props and schema_kind(props["data"]) is None:
                return "%s 的 2xx 指向 %s，但其中的 data 没有类型（共享信封：客户端生成不出载荷）" % (code, name)
    return None


PLACEHOLDER_WORDS = ("TODO", "TBD", "WIP", "Auto-generated", "待补", "未命名", "Lorem")

# Verbs a summary can open with. The list is read as "a sentence about an action", not as
# English style policing: the generator humanises the handler name (`listAssistantReleases`
# → "list Assistant Releases"), and a summary that is a bare noun phrase tells an integrator
# nothing about what the call does.
# The question the reader has is "which credential do I present, and what must it be allowed
# to do". Three answers are legitimate here and they are not interchangeable: an RBAC pair,
# the gateway's API key (a different credential, E159), or "no rule, everyone logged in".
PERMISSION_WORDS = ("权限", "read:", "manage:", "execute:", "API Key", "公开路由")


def summary_is_a_sentence(summary):
    """Two words, or four CJK characters. `Get`/`角色` alone are not summaries."""
    words = [w for w in summary.split() if w]
    cjk = sum(1 for ch in summary if "\u4e00" <= ch <= "\u9fff")
    return len(words) >= 2 or cjk >= 4


def quality_gaps(method, path, op):
    """T310-07: the ratchet proves "there is an annotation", not "the annotation is right".
    These three rules are the part of "right" that can be decided by reading the descriptor."""
    gaps = []
    summary = (op.get("summary") or "").strip()
    desc = (op.get("description") or "").strip()
    if summary and not summary_is_a_sentence(summary):
        gaps.append("summary 不是一句动作描述（%r：少于两个词/四个汉字）" % summary)
    blob = summary + " " + desc
    hit = [w for w in PLACEHOLDER_WORDS if w.lower() in blob.lower()]
    if hit:
        gaps.append("summary/description 里还有占位词 %s" % "、".join(hit))
    if op.get("security"):
        # Either spelling of the same fact is enough: the machine-readable key T310-05 added,
        # or prose. A protected endpoint where a client cannot find out *which* permission it
        # needs is the one that produces a 403 nobody can explain (E159).
        if not op.get("x-ragflow-permission") and not any(w in desc for w in PERMISSION_WORDS):
            gaps.append("受保护端点没写所需权限（既无 x-ragflow-permission，description 也没提）")
    return gaps


def annotation_gaps(method, path, op, ids, schemas=None):
    """Every reason this endpoint is not a contract a client could code against."""
    gaps = []
    summary = (op.get("summary") or "").strip()
    if not summary:
        gaps.append("没有 summary")
    elif summary == PLACEHOLDER_SUMMARY:
        gaps.append("summary 仍是占位符 %r" % PLACEHOLDER_SUMMARY)
    elif summary.strip("`/ ") == path.strip("`/ "):
        gaps.append("summary 只是把路径抄了一遍")

    desc = (op.get("description") or "").strip()
    if not desc:
        gaps.append("没有 description")
    elif len(desc) < MIN_DESCRIPTION:
        gaps.append("description 过短（%d < %d 字符），像占位符" % (len(desc), MIN_DESCRIPTION))

    tags = [t for t in (op.get("tags") or []) if t]
    if not tags:
        gaps.append("没有 tags")
    elif tags == [PLACEHOLDER_TAG]:
        gaps.append("tags 只有占位符 %r" % PLACEHOLDER_TAG)

    op_id = (op.get("operationId") or "").strip()
    if not op_id:
        gaps.append("没有 operationId")
    elif AUTO_ID.match(op_id):
        gaps.append("operationId 是自动编号 %s" % op_id)
    elif ids.get(op_id, 0) > 1:
        gaps.append("operationId %s 在文档里出现 %d 次" % (op_id, ids[op_id]))

    if method.lower() in BODY_METHODS and not body_documented(op):
        gaps.append("写操作没有 requestBody（或它的 schema 不是 $ref/object）")
    if not success_documented(op):
        gaps.append("没有带 schema 的 2xx 响应（也没有带 Location 的 3xx 或非 JSON 的下载/流式 2xx）")
    else:
        untyped = untyped_payload(op, schemas)
        if untyped:
            gaps.append(untyped)

    gaps.extend(quality_gaps(method, path, op))
    return gaps


def analyse(spec, baseline_keys):
    ops = operations(spec)
    schemas = ((spec.get("components") or {}).get("schemas") or {})
    ids = {}
    for _, _, op in ops:
        key = (op.get("operationId") or "").strip()
        if key:
            ids[key] = ids.get(key, 0) + 1

    annotated, exempt, undecided, gaps_of = set(), set(), set(), {}
    baseline = set(baseline_keys or [])
    for method, path, op in ops:
        key = "%s %s" % (method, path)
        gaps = annotation_gaps(method, path, op, ids, schemas)
        if not gaps:
            annotated.add(key)
        elif key in baseline:
            exempt.add(key)
        else:
            undecided.add(key)
            gaps_of[key] = gaps

    present = {"%s %s" % (m, p) for m, p, _ in ops}
    removed = sorted(baseline - present)
    stale = sorted(baseline & annotated)

    return {
        "ops": ops,
        "total": len(ops),
        "annotated": annotated,
        "exempt": exempt,
        "undecided": undecided,
        "gaps": gaps_of,
        "baseline_present": present,
        "removed": removed,
        "stale": stale,
        "ids": ids,
    }


def report(res, n_lines=6):
    out = []
    if res["undecided"]:
        out.append("undecided: %d 个端点既不在基线里、也没有注解（新端点必须自带契约）" % len(res["undecided"]))
        for key in sorted(res["undecided"])[:n_lines]:
            out.append("  %-52s %s" % (key, "；".join(res["gaps"][key])))
        if len(res["undecided"]) > n_lines:
            out.append("  … 另有 %d 个" % (len(res["undecided"]) - n_lines))
    if res["removed"]:
        out.append("removed-but-still-listed: %d 条基线记录指向已不存在的路由（删路由要一起删基线）"
                   % len(res["removed"]))
        for key in res["removed"][:n_lines]:
            out.append("  %s" % key)
    if res["stale"]:
        out.append("annotated-but-still-listed: %d 条基线记录其实已经有注解了（进度要删行，不能只加注解）"
                   % len(res["stale"]))
        for key in res["stale"][:n_lines]:
            out.append("  %s" % key)
    return out


def check(spec_path, baseline_path, quiet=False):
    spec = load_spec(spec_path)
    baseline = read_baseline(baseline_path)
    res = analyse(spec, baseline)
    problems = report(res)
    total = res["total"]
    if total == 0:
        sys.stdout.write("openapi-annotations: FAIL — the spec declares no operations; an empty "
                         "document satisfies every rule and proves nothing\n")
        return 1
    if baseline is None:
        problems.insert(0, "baseline 文件不存在：%s（删文件不能变成豁免所有端点）" % baseline_path)
    if problems:
        sys.stdout.write("openapi-annotations: FAIL\n")
        for line in problems:
            sys.stdout.write("  " + line + "\n")
        sys.stdout.write("  fix: 给端点补 @Summary/@Description/@Tags/@ID/@Param…body/@Success，"
                         "再跑 `make swagger api-doc`，然后 `--write-baseline`（它只会删行）\n")
        return 1
    if not quiet:
        sys.stdout.write(
            "openapi-annotations: PASS — %d/%d 端点完全注解，%d 个在基线上（待补），基线文件 %d 行\n"
            % (res["total"] - len(res["exempt"]), total, len(res["exempt"]), len(baseline)))
        sys.stdout.write(
            "  ratchet: 新端点必须自带注解；基线的行数只能减少（当前 %d → 目标 0）\n" % len(baseline))
    return 0


def write_baseline(spec_path, baseline_path):
    spec = load_spec(spec_path)
    old = read_baseline(baseline_path)
    res = analyse(spec, old)
    new = sorted(set(res["exempt"]) | set(res["undecided"]))
    if old is None:
        sys.stdout.write("openapi-annotations: 首次生成基线（%d 行）。此后这个文件只能缩小。\n" % len(new))
    else:
        added = sorted(set(new) - set(old))
        dropped = sorted(set(old) - set(new))
        if added:
            sys.stdout.write("openapi-annotations: REFUSED — 新基线会多出 %d 条豁免；"
                             "新端点必须写注解，不能用重新生成基线来豁免它：\n" % len(added))
            for key in added[:10]:
                gaps = res["gaps"].get(key) or ["（已不再出现或已有注解）"]
                sys.stdout.write("  %-52s %s\n" % (key, "；".join(gaps)))
            return 1
        for key in dropped:
            sys.stdout.write("  - %s\n" % key)
        sys.stdout.write("openapi-annotations: 基线 %d → %d 行（%d 条已注解或已下线）\n"
                         % (len(old), len(new), len(dropped)))
    with io.open(baseline_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(HEADER)
        for key in new:
            fh.write(key + "\n")
    return 0


# ── selftest ────────────────────────────────────────────────────────────────

ANNOTATED_OP = {
    "summary": "列出用户可见的用量过滤维度",
    "description": "返回用量与成本归因页面六个下拉框的可选值，按当前账号的可见范围裁剪。",
    "tags": ["usage"],
    "operationId": "ListUsageFilterOptions",
    "responses": {"200": {"description": "OK",
                          "content": {"application/json": {"schema": {"type": "object"}}}}},
}


def run_check_quiet(spec_path, baseline_path):
    spec = load_spec(spec_path)
    baseline = read_baseline(baseline_path)
    res = analyse(spec, baseline)
    if baseline is None or res["total"] == 0:
        return 1
    _lines = report(res)
    return 1 if _lines else 0


def selftest():
    failures = 0

    def ok(label, cond, detail=""):
        nonlocal failures
        if cond:
            sys.stdout.write("ok      %s\n" % label)
        else:
            failures += 1
            sys.stdout.write("FAIL    %s %s\n" % (label, detail))

    tmp = tempfile.mkdtemp()
    try:
        real = load_spec(DEFAULT_SPEC)
        # The fixture is the real spec plus three endpoints that are *not* annotated: the
        # frozen baseline has to be produced by this test, because the shipped one is now
        # empty (0 lines) and a self-test that reads it would assert nothing.
        base = json.loads(json.dumps(real))
        for n in (1, 2, 3):
            base["paths"]["/api/v1/selftest-exempt-%d" % n] = {
                "get": {"operationId": "SelftestExempt%d" % n, "tags": ["auto"],
                        "summary": PLACEHOLDER_SUMMARY, "responses": {"200": {"description": "OK"}}}}
        fixture_path = os.path.join(tmp, "fixture-spec.json")
        with io.open(fixture_path, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(json.dumps(base, ensure_ascii=False))
        baseline_path = os.path.join(tmp, "baseline.txt")
        empty_baseline = os.path.join(tmp, "empty-baseline.txt")

        def save(spec, name="spec.json"):
            p = os.path.join(tmp, name)
            with io.open(p, "w", encoding="utf-8", newline="\n") as fh:
                fh.write(json.dumps(spec, ensure_ascii=False))
            return p

        # bootstrap a baseline from the fixture, then use it as the frozen exemption set
        assert write_baseline(fixture_path, baseline_path) == 0
        with io.open(baseline_path, encoding="utf-8") as fh:
            saved = fh.read()
        frozen_lines = [l for l in saved.splitlines() if l and not l.startswith("#")]
        ok("baseline 是冻结的豁免集（%d 行，只有 fixture 的 3 个待补端点）" % len(frozen_lines),
           len(frozen_lines) == 3)

        # ① a new endpoint with no annotation must not inherit the exemption
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-unannotated"] = {
            "post": {"operationId": "SelftestUnannotated", "tags": ["auto"],
                     "summary": PLACEHOLDER_SUMMARY,
                     "responses": {"200": {"description": "OK"}}}}
        p = save(spec, "spec-new-raw.json")
        ok("新端点没有注解 → 拒绝", run_check_quiet(p, baseline_path) == 1)

        # ② the same endpoint, fully annotated, must pass — otherwise the rule is "no new
        # endpoints", which nobody would keep
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-annotated"] = {
            "post": dict(ANNOTATED_OP, requestBody={"required": True, "content": {
                "application/json": {"schema": {"$ref": "#/components/schemas/docs.Envelope"}}}})}
        p = save(spec, "spec-new-annotated.json")
        ok("新端点注解齐全 → 通过", run_check_quiet(p, baseline_path) == 0)

        # ③ annotation added but the line kept: progress must be recorded by deleting
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-exempt-1"] = {"get": dict(ANNOTATED_OP, operationId="SelftestExempt1")}
        with io.open(baseline_path, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
        p = save(spec, "spec-annotated-kept.json")
        ok("已注解但仍留在基线上 → 拒绝", run_check_quiet(p, baseline_path) == 1)

        # ④ a baseline entry for a route that no longer exists
        spec = json.loads(json.dumps(base))
        del spec["paths"]["/api/v1/selftest-exempt-2"]
        p = save(spec, "spec-route-removed.json")
        ok("基线指向已删除的路由 → 拒绝", run_check_quiet(p, baseline_path) == 1)

        # ⑤ an empty baseline cannot become "everything is exempt"
        with io.open(empty_baseline, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(HEADER)
        ok("基线为空 → 拒绝（未注解端点无处豁免）", run_check_quiet(fixture_path, empty_baseline) == 1)

        # ⑥ a POST with prose but no request body is not a contract
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-nobody"] = {"post": dict(ANNOTATED_OP, operationId="SelftestNoBody")}
        p = save(spec, "spec-new-nobody.json")
        ok("写操作缺 requestBody → 拒绝", run_check_quiet(p, baseline_path) == 1)

        # ⑦ --write-baseline must not be usable as a silencer
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-unannotated"] = {
            "get": {"operationId": "SelftestUnannotated2", "tags": ["auto"],
                    "summary": PLACEHOLDER_SUMMARY,
                    "responses": {"200": {"description": "OK"}}}}
        p = save(spec, "spec-grow.json")
        ok("--write-baseline 拒绝长大", write_baseline(p, baseline_path) == 1)

        # ⑧ the positive half of the ratchet: annotate, rewrite, the file shrinks and the
        # check goes green — progress must be a diff in the baseline, not in someone's memory
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-exempt-3"] = {"get": dict(ANNOTATED_OP, operationId="SelftestExempt3")}
        p = save(spec, "spec-progress.json")
        wrote = write_baseline(p, baseline_path)
        with io.open(baseline_path, encoding="utf-8") as fh:
            after = [l for l in fh.read().splitlines() if l and not l.startswith("#")]
        ok("注解后重写基线：缩小 1 行且检查通过",
           wrote == 0 and len(after) == len(frozen_lines) - 1 and run_check_quiet(p, baseline_path) == 0,
           "(lines %d -> %d)" % (len(frozen_lines), len(after)))
        # ⑧ legitimately shrank the exemption set for its own spec; put the frozen copy back
        # byte for byte, otherwise the mutants below inherit a baseline that no longer covers
        # `base` and would fail for the wrong reason. Restored by hand, because write_baseline
        # refuses to grow the file — the ratchet has to keep turning one way.
        with io.open(baseline_path, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(saved)

        # ⑨ a redirect is a complete success response only when it names its destination
        redirect = dict(ANNOTATED_OP, operationId="SelftestRedirect",
                        responses={"302": {"description": "Found",
                                           "headers": {"Location": {"schema": {"type": "string"}}},
                                           "content": {"text/html": {"schema": {"type": "string"}}}}})
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-redirect"] = {"get": redirect}
        p = save(spec, "spec-redirect-ok.json")
        ok("带 Location 的 302 → 通过（跳转本身就是契约）", run_check_quiet(p, baseline_path) == 0)

        spec = json.loads(json.dumps(base))
        nameless = json.loads(json.dumps(redirect))
        del nameless["responses"]["302"]["headers"]
        spec["paths"]["/api/v1/selftest-redirect"] = {"get": nameless}
        p = save(spec, "spec-redirect-nameless.json")
        ok("不说明目的地的 302 → 拒绝", run_check_quiet(p, baseline_path) == 1)

        # ⑩ a non-JSON 2xx is complete; a JSON 2xx with only a string is not
        csv_op = dict(ANNOTATED_OP, operationId="SelftestCsv",
                      responses={"200": {"description": "OK",
                                         "content": {"text/csv": {"schema": {"type": "string"}}}}})
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-csv"] = {"get": csv_op}
        p = save(spec, "spec-csv-ok.json")
        ok("非 JSON 下载 2xx → 通过（载荷就是媒体类型）", run_check_quiet(p, baseline_path) == 0)

        spec = json.loads(json.dumps(base))
        json_scalar = dict(ANNOTATED_OP, operationId="SelftestJsonScalar",
                           responses={"200": {"description": "OK",
                                              "content": {"application/json": {"schema": {"type": "string"}}}}})
        spec["paths"]["/api/v1/selftest-json-scalar"] = {"get": json_scalar}
        p = save(spec, "spec-json-scalar.json")
        ok("JSON 2xx 只有 string schema → 仍然拒绝", run_check_quiet(p, baseline_path) == 1)

        # ⑪ "has an annotation" is not "has a contract": a 2xx that $refs the shared
        # docs.Envelope is an object, so it passes the schema check, but its `data` is {}
        spec = json.loads(json.dumps(base))
        shared = dict(ANNOTATED_OP, operationId="SelftestSharedEnvelope",
                      responses={"200": {"description": "OK", "content": {
                          "application/json": {"schema": {"$ref": "#/components/schemas/docs.Envelope"}}}}})
        spec["paths"]["/api/v1/selftest-shared-envelope"] = {"get": shared}
        p = save(spec, "spec-shared-envelope.json")
        ok("2xx 指向共享信封（data 为空）→ 拒绝", run_check_quiet(p, baseline_path) == 1)

        spec = json.loads(json.dumps(base))
        typed = dict(ANNOTATED_OP, operationId="SelftestTypedEnvelope",
                     responses={"200": {"description": "OK", "content": {
                         "application/json": {"schema": {"$ref": "#/components/schemas/internal_handler.MeEnvelope"}}}}})
        spec["paths"]["/api/v1/selftest-typed-envelope"] = {"get": typed}
        p = save(spec, "spec-typed-envelope.json")
        ok("2xx 指向具名信封（data 有类型）→ 通过", run_check_quiet(p, baseline_path) == 0)

        # ⑫ a documented 204 is a complete answer; a 200 with no body is not. The pair
        # keeps the rule from becoming "no content is always fine".
        spec = json.loads(json.dumps(base))
        no_content = dict(ANNOTATED_OP, operationId="SelftestNoContent",
                          responses={"204": {"description": "无响应体"}})
        spec["paths"]["/api/v1/selftest-no-content"] = {"delete": no_content}
        p = save(spec, "spec-no-content-204.json")
        ok("204 无响应体 → 通过（空答案也是契约）", run_check_quiet(p, baseline_path) == 0)

        spec = json.loads(json.dumps(base))
        empty_200 = dict(ANNOTATED_OP, operationId="SelftestEmpty200",
                         responses={"200": {"description": "无响应体"}})
        spec["paths"]["/api/v1/selftest-empty-200"] = {"get": empty_200}
        p = save(spec, "spec-empty-200.json")
        ok("200 无响应体 → 拒绝（客户端有权拿到载荷）", run_check_quiet(p, baseline_path) == 1)

        # ⑬⑭⑮ T310-07: the ratchet proves "there is an annotation". These three prove the
        # quality rules can fail — each one mutation of an otherwise-passing endpoint, so the
        # rule is what turns it red.
        bare = dict(ANNOTATED_OP, operationId="SelftestBareNoun", summary="用量")
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-bare-noun"] = {"get": bare}
        p = save(spec, "spec-bare-noun.json")
        ok("summary 只有名词（「用量」）→ 拒绝", run_check_quiet(p, baseline_path) == 1)

        todo = dict(ANNOTATED_OP, operationId="SelftestTodo",
                    description="返回用量过滤维度。TODO 补齐口径说明。")
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-todo"] = {"get": todo}
        p = save(spec, "spec-todo.json")
        ok("description 里留着 TODO → 拒绝", run_check_quiet(p, baseline_path) == 1)

        silent = dict(ANNOTATED_OP, operationId="SelftestSilentPerm",
                      description="返回当前账号可见的用量过滤维度，按可见范围裁剪。",
                      security=[{"BearerAuth": []}])
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-silent-perm"] = {"get": silent}
        p = save(spec, "spec-silent-perm.json")
        ok("受保护端点不说要哪个权限 → 拒绝", run_check_quiet(p, baseline_path) == 1)

        named = dict(silent, operationId="SelftestNamedPerm",
                     **{"x-ragflow-permission": "read:usage"})
        spec = json.loads(json.dumps(base))
        spec["paths"]["/api/v1/selftest-named-perm"] = {"get": named}
        p = save(spec, "spec-named-perm.json")
        ok("受保护端点带 x-ragflow-permission → 通过", run_check_quiet(p, baseline_path) == 0)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    if failures:
        sys.stdout.write("\nopenapi-annotations --selftest: FAIL (%d)\n" % failures)
        return 1
    sys.stdout.write("\nopenapi-annotations --selftest: PASS (20/20 checks)\n")
    return 0


def main(argv):
    ap = argparse.ArgumentParser(add_help=True)
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--write-baseline", action="store_true")
    ap.add_argument("--selftest", action="store_true")
    ap.add_argument("--spec", default=DEFAULT_SPEC)
    ap.add_argument("--baseline", default=DEFAULT_BASELINE)
    args = ap.parse_args(argv[1:])
    if args.selftest:
        return selftest()
    if args.write_baseline:
        return write_baseline(args.spec, args.baseline)
    return check(args.spec, args.baseline)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
