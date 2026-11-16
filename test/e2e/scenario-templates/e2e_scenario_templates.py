#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""场景模板全量端到端验证：9 个内置模板 × 全部可实例化形态 × 真实 RAGFlow 引擎。

为什么要有这个脚本：单元测试证明不了「下载后一用就出问题」。本脚本在真实
环境（151 测试环境 + 真实引擎 v1.0.0）里，用 test/ 下的真实文档，把每个模板
的每种形态从建库、解析、能力体检、应用修复、实例化到真实问答走一遍，并把
每一步的 HTTP/业务码写进证据 JSON。

用法（在仓库根目录执行）：
    RGX_E2E_TAG=20261117 python test/e2e/scenario-templates/e2e_scenario_templates.py
    RGX_E2E_TAG=20261117 python test/e2e/scenario-templates/e2e_scenario_templates.py --phase data
    RGX_E2E_TAG=20261117 python test/e2e/scenario-templates/e2e_scenario_templates.py --only policy.chat
证据：test/e2e/scenario-templates/out/evidence-<tag>.json
      test/e2e/scenario-templates/out/state-<tag>.json（幂等续跑）
退出码：0 全绿；1 存在 FAIL（FAIL 一律代表产品或环境缺陷，不允许"已知问题"豁免）。

覆盖矩阵见同目录 matrix.md；断言设计原则见 README.md。
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

BASE = os.environ.get("RGX_E2E_BASE", "http://192.168.4.151:19193/api/v1")
PLATFORM_USER = os.environ.get("RGX_E2E_PLATFORM_USER", "admin")
PLATFORM_PASS = os.environ.get("RGX_E2E_PLATFORM_PASS", "RgxTest#2026a")
WS_NAME = os.environ.get("RGX_E2E_WS_NAME", "E2E-场景模板全量验证")
WS_USER = os.environ.get("RGX_E2E_WS_USER", "e2e-admin")
WS_PASS = os.environ.get("RGX_E2E_WS_PASS", "E2e#2026pass")
PROJECT_NAME = os.environ.get("RGX_E2E_PROJECT", "E2E 场景模板验证项目")
TAG = os.environ.get("RGX_E2E_TAG", datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S"))
OUT_DIR = os.environ.get("RGX_E2E_OUT", os.path.join(os.path.dirname(os.path.abspath(__file__)), "out"))
STATE_PATH = os.path.join(OUT_DIR, f"state-{TAG}.json")
EVIDENCE_PATH = os.path.join(OUT_DIR, f"evidence-{TAG}.json")
PARSE_TIMEOUT = int(os.environ.get("RGX_E2E_PARSE_TIMEOUT", "900"))
QA_TIMEOUT = int(os.environ.get("RGX_E2E_QA_TIMEOUT", "600"))

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
# Real documents, grouped by where they live. A dataset spec names a file by
# its bare name and the first root that has it wins, so moving a file between
# corpus folders does not break the matrix.
DOCS_ROOTS = [
    os.path.join(REPO_ROOT, "test", "安全生产政策法规-docs"),
    os.path.join(REPO_ROOT, "test", "山东省属企业及其下属企业安全生产风险点防控情况的报告-docs"),
    os.path.join(REPO_ROOT, "test", "隐患检查-docs"),
]

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

EVIDENCE = {"meta": {"base": BASE, "tag": TAG, "started_utc": datetime.now(timezone.utc).isoformat()},
            "steps": [], "lanes": {}, "negatives": {}, "env_notes": []}
STATE = {}
FAILS: list[str] = []
# Which invocation produced the step / lane record. `--phase lanes` replay after a
# full run must not wipe what the data phase learned (chunk counts, dataset ids),
# so evidence accumulates and every record carries its run number.
RUN = 1


def log(msg):
    print(msg, flush=True)


# ---------------------------------------------------------------- http plumbing
def call(path, method="GET", body=None, token=None, headers=None, raw=False, timeout=300):
    """One HTTP round-trip. A transport error is reported as status 0 and retried
    once: the 151 box drops idle keep-alives, and a silently swallowed status 0
    once turned a missing capability verdict into a "green" lane."""
    status, payload = _call_once(path, method, body, token, headers, raw, timeout)
    if status == 0:
        time.sleep(3)
        status, payload = _call_once(path, method, body, token, headers, raw, timeout)
    return status, payload


def _call_once(path, method, body, token, headers, raw, timeout):
    url = BASE + path
    data = None
    if raw:
        data = body
    elif body is not None:
        data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    if not raw:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            status, payload = r.status, r.read()
    except urllib.error.HTTPError as e:
        status, payload = e.code, (e.read() or b"{}")
    except Exception as exc:  # network flake: report as 0 so the caller can retry
        return 0, {"code": -1, "message": f"transport error: {exc}"}
    try:
        parsed = json.loads(payload or b"{}")
    except Exception:
        parsed = {"raw": (payload or b"")[:400].decode("utf-8", "replace")}
    return status, parsed


def step(stage, method, path, status, payload, summary=None, lane=None):
    entry = {
        "lane": lane, "stage": stage, "method": method, "path": path.split("?")[0],
        "run": RUN, "http": status, "code": payload.get("code"),
        "summary": (summary or str(payload.get("message")))[:200],
    }
    EVIDENCE["steps"].append(entry)
    ok = status < 400 and payload.get("code") == 0
    log(("  ok " if ok else "  !! ") + f"[{lane or '-':22}] {stage:12} {method:4} {entry['path']} "
        f"-> {status}/{payload.get('code')} {entry['summary'][:90]}")
    return ok


def fail(lane, reason):
    FAILS.append(f"{lane}: {reason}")
    log(f"  FAIL [{lane}] {reason}")
    target = EVIDENCE["lanes"] if lane in {l["lane"] for l in LANES} else EVIDENCE["negatives"]
    target.setdefault(lane, {})["fail"] = reason


def ok_or_fail(lane, cond, reason):
    if not cond:
        fail(lane, reason)
    return cond


# --------------------------------------------------------------------- auth
def login(username, password):
    st, pl = call("/auth/login", "POST", {"username": username, "password": password})
    data = pl.get("data") if isinstance(pl.get("data"), dict) else {}
    tok = data.get("token")
    step("auth", "POST", "/auth/login", st, pl, f"login {username}")
    if not tok:
        sys.exit(f"login failed for {username}: {st}/{pl.get('code')} {pl.get('message')}")
    return tok


def try_login(username, password):
    st, pl = call("/auth/login", "POST", {"username": username, "password": password})
    data = pl.get("data") or {}
    if st < 400 and pl.get("code") == 0 and isinstance(data, dict) and data.get("token"):
        return data["token"]
    return None


def workspace_tenant(token):
    st, pl = call("/auth/me", token=token)
    return (pl.get("data") or {}).get("tenant_id")


def ensure_tenant(ptok):
    st, pl = call("/tenants?page=1&page_size=100", token=ptok)
    items = (pl.get("data") or {}).get("items", []) if isinstance(pl.get("data"), dict) else []
    for it in items:
        if (it.get("name") or "") == WS_NAME:
            step("bootstrap", "GET", "/tenants", st, pl, f"tenant exists {it.get('id')}")
            return it.get("id")
    # The platform tenant list is not a reliable lookup (it answered `total: 0`
    # for a workspace that exists), so fall back to the workspace user's own
    # tenant_id before trying to create a duplicate.
    st, pl = call("/users?page=1&page_size=200", token=ptok)
    users = (pl.get("data") or {}).get("items", []) if isinstance(pl.get("data"), dict) else []
    for user in users:
        if (user.get("username") or "") == WS_USER and user.get("tenant_id"):
            step("bootstrap", "GET", "/users", st, pl, f"tenant via user {user.get('tenant_id')}")
            return user.get("tenant_id")
    st, pl = call("/tenants", "POST", {"name": WS_NAME}, token=ptok)
    tid = (pl.get("data") or {}).get("id")
    step("bootstrap", "POST", "/tenants", st, pl, f"tenant created {tid}")
    if st == 409 or pl.get("code") == 40911:
        # Already exists and platform scope cannot list it: the workspace user
        # created by the previous run is the only handle on that tenant.
        return None
    if not tid:
        sys.exit(f"create tenant failed: {st}/{pl.get('code')} {pl.get('message')}")
    return tid


def ensure_user(ptok, tenant_id):
    body = {"username": WS_USER, "password": WS_PASS, "role": "tenant_admin"}
    if tenant_id:
        body["tenant_id"] = tenant_id
    st, pl = call("/users", "POST", body, token=ptok)
    step("bootstrap", "POST", "/users", st, pl, f"user {WS_USER} in {tenant_id}")
    ok_or_fail("bootstrap", pl.get("code") in (0, 40022),
               f"create workspace user failed: {st}/{pl.get('code')} {pl.get('message')}")


def ensure_project(tok):
    st, pl = call("/projects?page=1&page_size=100", token=tok)
    items = pl.get("data") if isinstance(pl.get("data"), list) else (
        (pl.get("data") or {}).get("items", []) if isinstance(pl.get("data"), dict) else [])
    for it in items:
        if (it.get("name") or "") == PROJECT_NAME:
            return it.get("id")
    st, pl = call("/projects", "POST", {"name": PROJECT_NAME}, token=tok)
    pid = (pl.get("data") or {}).get("id")
    step("bootstrap", "POST", "/projects", st, pl, f"project {pid}")
    if not pid:
        sys.exit(f"create project failed: {st}/{pl.get('code')} {pl.get('message')}")
    return pid


# ------------------------------------------------------------------ datasets
def resolve_doc(name):
    for root in DOCS_ROOTS:
        path = os.path.join(root, name)
        if os.path.exists(path):
            return path
    return None


def upload(token, dataset_id, path, lane=None):
    boundary = "----rgxe2e7f31"
    with open(path, "rb") as f:
        content = f.read()
    name = os.path.basename(path)
    body = b"".join([
        (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{name}\"\r\n"
         f"Content-Type: application/octet-stream\r\n\r\n").encode("utf-8"),
        content, b"\r\n", (f"--{boundary}--\r\n").encode(),
    ])
    st, pl = call(f"/datasets/{dataset_id}/documents", "POST", body, token=token,
                  headers={"Content-Type": f"multipart/form-data; boundary={boundary}"}, raw=True)
    step("dataset", "POST", f"/datasets/{dataset_id}/documents", st, pl, name, lane=lane)
    return st < 400 and pl.get("code") == 0


def dataset_snapshot(token, dataset_ids):
    """chunk_method/pushdown live behind GET /datasets/:id/parse-profile, not the list."""
    out = {}
    for did in dataset_ids:
        st, pl = call(f"/datasets/{did}/parse-profile", token=token)
        data = pl.get("data") or {}
        out[did] = {"chunk_method": data.get("chunk_method"), "pushdown": data.get("pushdown_enabled")}
    return out


def find_dataset(token, name):
    st, pl = call("/datasets?page=1&page_size=200", token=token)
    data = pl.get("data")
    items = data.get("items", []) if isinstance(data, dict) else (data if isinstance(data, list) else [])
    for item in items:
        if (item.get("name") or "") == name:
            return item.get("id")
    return None


def dataset_document_ids(token, dataset_id):
    st, pl = call(f"/datasets/{dataset_id}/documents", token=token)
    docs = pl.get("data") if isinstance(pl.get("data"), list) else []
    return [d.get("id") for d in docs if d.get("id")]


def dataset_chunks(token, dataset_id):
    st, pl = call(f"/datasets/{dataset_id}/documents", token=token)
    docs = pl.get("data") if isinstance(pl.get("data"), list) else []
    return sum(int((d or {}).get("chunk_count") or 0) for d in docs)


def upload_and_parse(token, did, display, files, lane=None):
    for name in files:
        path = resolve_doc(name)
        if not path:
            fail(lane or "dataset", f"missing fixture {name} (looked in {DOCS_ROOTS})")
            return
        upload(token, did, path, lane=lane)
    # Exactly what the UI sends: the selected document ids. The engine has no
    # "parse all" form (`{}` / `[]` answers `code:102 document_ids is required`,
    # which the handler used to turn into 502/50203 — doc/285 §11.4).
    doc_ids = dataset_document_ids(token, did)
    st, pl = call(f"/datasets/{did}/parse", "POST", {"document_ids": doc_ids}, token=token)
    step("dataset", "POST", f"/datasets/{did}/parse", st, pl, f"parse {len(doc_ids)} docs", lane=lane)
    ok_or_fail(lane or "dataset", st < 400 and pl.get("code") == 0,
               f"parse request rejected: {st}/{pl.get('code')} {pl.get('message')}")
    deadline = time.time() + PARSE_TIMEOUT
    chunks = 0
    while time.time() < deadline:
        st, pl = call(f"/datasets/{did}/documents", token=token)
        docs = pl.get("data") if isinstance(pl.get("data"), list) else []
        chunks = sum(int((d or {}).get("chunk_count") or 0) for d in docs)
        if chunks > 0:
            break
        time.sleep(10)
    EVIDENCE.setdefault("dataset_chunks", {})[display] = chunks
    step("dataset", "GET", f"/datasets/{did}/documents", st, pl, f"parsed chunks={chunks}", lane=lane)
    ok_or_fail(lane or "dataset", chunks > 0, f"dataset {display} never produced chunks ({chunks})")


def ensure_dataset(token, key, lane=None):
    """Create (once) + upload + parse + wait for chunks. Returns dataset id."""
    spec = DATASETS[key]
    name = f"{spec['display']}-{TAG}"
    cache = STATE.setdefault("datasets", {})
    if key in cache:
        did = cache[key]
        chunks = dataset_chunks(token, did)
        if chunks > 0:
            EVIDENCE.setdefault("dataset_chunks", {})[spec["display"]] = chunks
            step("dataset", "GET", f"/datasets/{did}/documents", 200, {"code": 0},
                 f"reuse {spec['display']} chunks={chunks}", lane=lane)
            return did
        cache.pop(key, None)
    existing = find_dataset(token, name)
    if existing:
        step("dataset", "GET", "/datasets", 200, {"code": 0}, f"reuse {spec['display']} by name -> {existing}", lane=lane)
        cache[key] = existing
        if dataset_chunks(token, existing) <= 0:
            upload_and_parse(token, existing, spec["display"], spec["files"], lane)
        else:
            EVIDENCE.setdefault("dataset_chunks", {})[spec["display"]] = dataset_chunks(token, existing)
        return existing
    st, pl = call("/datasets", "POST", {"name": name}, token=token)
    did = (pl.get("data") or {}).get("id")
    step("dataset", "POST", "/datasets", st, pl, f"create {spec['display']} -> {did}", lane=lane)
    if not did:
        fail(lane or "dataset", f"create dataset {spec['display']} failed: {st}/{pl.get('code')} {pl.get('message')}")
        return None
    cache[key] = did
    upload_and_parse(token, did, spec["display"], spec["files"], lane)
    return did


# -------------------------------------------------------------- capability
def capability_check(token, template_id, dataset_id, app_type, lane):
    st, pl = call(f"/scenario-template-assets/{template_id}/capability-check", "POST",
                  {"app_type": app_type, "datasets": [{"role": "primary", "dataset_id": dataset_id}]},
                  token=token)
    view = pl.get("data") if isinstance(pl.get("data"), dict) else {}
    rows = view.get("datasets") or []
    row = rows[0] if rows else {}
    if st >= 400 or not row.get("verdict"):
        fail(lane, f"capability-check gave no verdict: http={st} code={pl.get('code')} "
                   f"{(pl.get('message') or '')[:80]}")
    step("check", "POST", f"/scenario-template-assets/{template_id}/capability-check", st, pl,
         f"verdict={view.get('verdict')} row={row.get('verdict')} "
         f"repairs={[r.get('action') for r in (row.get('repairs') or [])]}", lane=lane)
    return view, row


def wait_for_metadata_check(token, template_id, dataset_id, app_type, lane, timeout=240):
    """The pushdown repair only starts an async engine-side meta_fields backfill.

    A re-check fired immediately reads the pre-backfill documents and answers
    `repairable` again (the wizard deliberately keeps its old conclusion and asks
    for an explicit re-check — see `postRepairStep`). The API-level test polls so
    that a *stale* verdict is not mistaken for a failed repair.
    """
    deadline = time.time() + timeout
    view, row = capability_check(token, template_id, dataset_id, app_type, lane)
    while time.time() < deadline and row.get("verdict") == "repairable":
        pending = [r.get("action") for r in (row.get("repairs") or [])]
        if "enable_pushdown_and_sync_meta" not in pending:
            break
        time.sleep(15)
        view, row = capability_check(token, template_id, dataset_id, app_type, lane)
    return view, row


def apply_repairs(token, dataset_id, row, lane):
    """Drive exactly the channel each action names (doc/285 §10.1)."""
    applied = []
    before = dataset_snapshot(token, STATE.get("datasets", {}).values())
    for repair in row.get("repairs") or []:
        action = repair.get("action")
        if action == "apply_parse_profile":
            target = repair.get("chunk_method")
            if not target:
                fail(lane, "server gave no target chunk_method for apply_parse_profile")
                continue
            st, pl = call(f"/datasets/{dataset_id}/parse-profile", "POST",
                          {"chunk_method": target, "rewrite_existing": True,
                           "acknowledge_shared_impact": True}, token=token)
            data = pl.get("data") or {}
            got = data.get("chunk_method") if isinstance(data, dict) else None
            step("repair", "POST", f"/datasets/{dataset_id}/parse-profile", st, pl,
                 f"target={target} got={got} "
                 f"reparse={data.get('reparse_triggered') if isinstance(data, dict) else None}", lane=lane)
            ok_or_fail(lane, st < 400 and pl.get("code") == 0,
                       f"parse-profile repair failed: {st}/{pl.get('code')} {pl.get('message')}")
            ok_or_fail(lane, (got or target) == target, f"parse-profile wrote {got} instead of {target}")
            applied.append(action)
        elif action == "enable_pushdown_and_sync_meta":
            st, pl = call(f"/datasets/{dataset_id}", "PUT",
                          {"pushdown_enabled": True, "acknowledge_shared_impact": True}, token=token)
            pending = st == 202
            step("repair", "PUT", f"/datasets/{dataset_id}", st, pl,
                 "approval hold" if pending else "pushdown enabled", lane=lane)
            ok_or_fail(lane, st < 400 or pending,
                       f"pushdown repair failed: {st}/{pl.get('code')} {pl.get('message')}")
            applied.append(action)
        else:
            fail(lane, f"repair {action} has no wizard channel (manual/blocked): {repair.get('reason')}")
    after = dataset_snapshot(token, STATE.get("datasets", {}).values())
    touched = {k: (before.get(k, {}).get("chunk_method"), after.get(k, {}).get("chunk_method"))
               for k in after
               if (before.get(k, {}).get("chunk_method"), before.get(k, {}).get("pushdown"))
               != (after.get(k, {}).get("chunk_method"), after.get(k, {}).get("pushdown"))}
    EVIDENCE["lanes"].setdefault(lane, {})["dataset_writes"] = {"target": dataset_id, "changed": touched}
    others = {k: v for k, v in touched.items() if k != dataset_id}
    ok_or_fail(lane, not others, f"repair changed unrelated datasets: {others}")
    return applied


# ------------------------------------------------------------- instantiate
def instantiate(token, template_id, app_type, name, project_id, dataset_id, lane):
    body = {"app_type": app_type, "name": name, "project_id": project_id,
            "template_version": 1, "create_missing_datasets": False,
            "dataset_ids": [dataset_id],
            "authoring": {"description": "E2E 场景模板全量验证（自动化脚本，数据保留待复核）"}}
    st, pl = call(f"/scenario-template-assets/{template_id}/instantiate", "POST", body, token=token,
                  headers={"Idempotency-Key": f"e2e-{lane}-{TAG}"})
    data = pl.get("data") or {}
    step("instantiate", "POST", f"/scenario-template-assets/{template_id}/instantiate", st, pl,
         f"state={data.get('instantiation_state')} replayed={data.get('replayed')} "
         f"instance={data.get('instance_id')} chat={(data.get('chat') or {}).get('id')} "
         f"search={(data.get('search_app') or {}).get('id')}", lane=lane)
    ok_or_fail(lane, st < 400 and pl.get("code") == 0,
               f"instantiate failed: {st}/{pl.get('code')} {pl.get('message')}")
    return data


# --------------------------------------------------------------------- Q&A
def ask_chat(token, chat_id, questions, lane):
    st, pl = call(f"/chats/{chat_id}/sessions", "POST", {"name": f"e2e-{lane}"}, token=token)
    sid = (pl.get("data") or {}).get("id")
    step("qa", "POST", f"/chats/{chat_id}/sessions", st, pl, f"session={sid}", lane=lane)
    if not sid:
        fail(lane, f"create chat session failed: {st}/{pl.get('code')} {pl.get('message')}")
        return []
    out = []
    for q, expect in questions:
        t0 = time.time()
        st, pl = call("/chat/completions", "POST",
                      {"chat_id": chat_id, "session_id": sid,
                       "messages": [{"role": "user", "content": q}]}, token=token, timeout=QA_TIMEOUT)
        # `/chat/completions` proxies the engine body verbatim (OpenAI-ish:
        # answer/reference at top level, no {code,data} envelope).
        data = pl.get("data") if isinstance(pl.get("data"), dict) else pl
        answer = (data.get("answer") or "") if isinstance(data, dict) else ""
        refs = (data.get("reference") or []) if isinstance(data, dict) else []
        nref = len(refs) if isinstance(refs, list) else 0
        # `expect is None` marks the catalog's own evaluation question: it was
        # written for another corpus, so only "answered normally" is asserted.
        hit = (expect in answer) if expect else None
        ok_http = st < 400 and (pl.get("code") in (0, None))
        step("qa", "POST", "/chat/completions", st, {"code": 0 if ok_http else pl.get("code")},
             f"{q[:24]}… answer={len(answer)}c refs={nref} keyword={expect}:{hit} {time.time()-t0:.1f}s",
             lane=lane)
        ok_or_fail(lane, ok_http, f"chat completion http failed: {st}/{pl.get('code')} {str(pl.get('message'))[:80]}")
        ok_or_fail(lane, len(answer.strip()) > 0, f"empty answer for {q}")
        if expect:
            ok_or_fail(lane, hit, f"answer did not contain expected fact '{expect}' for {q}")
        out.append({"q": q, "expect": expect, "hit": hit, "answer_len": len(answer),
                    "refs": nref, "answer": answer[:600], "seconds": round(time.time() - t0, 1)})
    return out


def ask_search(token, search_app_id, questions, lane):
    out = []
    for q, expect in questions:
        t0 = time.time()
        st, pl = call(f"/search-apps/{search_app_id}/completion", "POST", {"question": q},
                      token=token, timeout=QA_TIMEOUT)
        data = pl.get("data") if isinstance(pl.get("data"), dict) else pl
        answer = (data.get("answer") or "") if isinstance(data, dict) else ""
        refs = (data.get("reference") or []) if isinstance(data, dict) else []
        nref = len(refs) if isinstance(refs, list) else 0
        hit = (expect in answer) if expect else None
        step("qa", "POST", f"/search-apps/{search_app_id}/completion", st, pl,
             f"{q[:24]}… answer={len(answer)}c refs={nref} keyword={expect}:{hit} {time.time()-t0:.1f}s",
             lane=lane)
        ok_or_fail(lane, st < 400 and pl.get("code") == 0,
                   f"search completion http failed: {st}/{pl.get('code')}")
        ok_or_fail(lane, len(answer.strip()) > 0, f"empty search answer for {q}")
        if expect:
            ok_or_fail(lane, hit, f"search answer did not contain expected fact '{expect}' for {q}")
        out.append({"q": q, "expect": expect, "hit": hit, "answer_len": len(answer),
                    "refs": nref, "answer": answer[:600], "seconds": round(time.time() - t0, 1)})
    return out


# --------------------------------------------------------------- the matrix
# One dataset per template, built from the real corpus under test/. Sharing a
# dataset between templates with different parse intents used to make one lane's
# repair rewrite another lane's chunking (doc/285 §11.1), so every template owns
# its own knowledge base here.
DATASETS = {
    "eq": {"display": "企业问答库", "chunk_hint": "book", "files": [
        "中央企业安全生产监督管理办法.md", "安全生产专项整治三年行动.md"]},
    "policy": {"display": "政策法规库", "chunk_hint": "book", "files": [
        "山东省安全生产行政责任制.md", "山东省安全生产工作任务分工.md", "省属企业安全生产事故应急预案.md"]},
    "training": {"display": "培训赋能库", "chunk_hint": "book", "files": [
        "山东省属企业安全生产专家遴选调用办法（试行）.md", "山东省属企业安全生产考核标准.md"]},
    "operations": {"display": "运维应急库", "chunk_hint": "book", "files": [
        "省属企业安全生产事故应急预案.md", "山东国投公司安全生产风险防控基本情况报告.docx"]},
    "customer-service": {"display": "客服问答库", "chunk_hint": "general", "files": [
        "山东省安全生产举报办法.md", "2025年上半年国资委安全生产督导检查隐患台账_处理后.xlsx"]},
    "product-manual": {"display": "产品手册库", "chunk_hint": "book", "files": [
        "南郊集团-山东大厦安全生产检查情况.docx", "山东健康集团安全风险管控工作总结.docx"]},
    "sales-support": {"display": "销售支持库", "chunk_hint": "general", "files": [
        "山东农业集团关于安全生产风险防控工作情况.docx", "南郊集团关于梳理安全生产风险点防控情况的报告.docx"]},
    "contract-compliance": {"display": "合同合规库", "chunk_hint": "paper", "files": [
        "安全生产社会化服务机构管理办法.md", "加强矿山安全生产工作的实施意见(不完整).md"]},
    "finance": {"display": "财务口径库", "chunk_hint": "book", "files": [
        "山东省属企业安全生产考核标准.md", "山东健康集团安全风险管控工作总结.docx"]},
}

Q_EQ = [("中央企业依据风险程度分为几类？", "三类"),
        ("第一类中央企业和涉矿中央企业集团总部应当配备什么？", "安全生产总监")]
Q_POLICY = [("本省落实安全生产行政责任制应当坚持哪些原则？", "谁主管谁负责"),
            ("安全生产工作实行什么制度？", "一岗双责"),
            ("生产安全事故分为哪几级？", "特别重大事故")]
Q_TRAINING = [("专家年度调用时间原则上不超过多少天？", "15"),
              ("专家遴选数量按什么标准掌握？", "两倍")]
Q_OPS = [("省属一级企业接到事发单位事故报告后多长时间内书面报送？", "半小时"),
         ("省属企业每年应开展什么活动？", "演练")]
Q_CS = [("安全生产举报工作所需经费如何保障？", "财政预算"),
        ("县级以上应急管理部门承担举报工作的哪些环节？", "核查")]
Q_PM = [("山东大厦排查出的燃气相关风险是什么？", "燃气"),
        ("山东健康集团按什么原则管控风险？", "分级管控")]
Q_SS = [("山东农业集团聚焦哪些重点领域？", "粮食加工"),
        ("南郊集团要求权属单位坚持什么理念？", "人民至上")]
Q_CC = [("社会化服务机构服务档案保管期限不得少于几年？", "5年"),
        ("矿山企业主要负责人对所属矿山每季度至少开展几次重大事故隐患排查？", "每季度")]
Q_FIN = [("一个考核年度内发生1起重大及以上事故扣至多少分？", "80"),
         ("山东健康集团如何管控风险？", "分级管控")]

LANES = [
    {"lane": "enterprise-qa.chat", "template": "enterprise-qa", "app_type": "chat", "dataset": "eq", "questions": Q_EQ},
    {"lane": "enterprise-qa.search", "template": "enterprise-qa", "app_type": "search", "dataset": "eq", "questions": Q_EQ[:1]},
    {"lane": "policy.chat", "template": "policy", "app_type": "chat", "dataset": "policy", "questions": Q_POLICY[:2]},
    {"lane": "policy.search", "template": "policy", "app_type": "search", "dataset": "policy", "questions": Q_POLICY[2:]},
    {"lane": "training.chat", "template": "training", "app_type": "chat", "dataset": "training", "questions": Q_TRAINING},
    {"lane": "operations.chat", "template": "operations", "app_type": "chat", "dataset": "operations", "questions": Q_OPS[:1]},
    {"lane": "operations.search", "template": "operations", "app_type": "search", "dataset": "operations", "questions": Q_OPS[1:]},
    {"lane": "customer-service.chat", "template": "customer-service", "app_type": "chat", "dataset": "customer-service", "questions": Q_CS[:1]},
    {"lane": "customer-service.search", "template": "customer-service", "app_type": "search", "dataset": "customer-service", "questions": Q_CS[1:]},
    {"lane": "product-manual.search", "template": "product-manual", "app_type": "search", "dataset": "product-manual", "questions": Q_PM},
    {"lane": "sales-support.chat", "template": "sales-support", "app_type": "chat", "dataset": "sales-support", "questions": Q_SS},
    {"lane": "contract-compliance.chat", "template": "contract-compliance", "app_type": "chat", "dataset": "contract-compliance", "questions": Q_CC},
    {"lane": "finance.chat", "template": "finance", "app_type": "chat", "dataset": "finance", "questions": Q_FIN[:1]},
    {"lane": "finance.search", "template": "finance", "app_type": "search", "dataset": "finance", "questions": Q_FIN[1:]},
]

# Forms a template does not declare must be refused, never half-built.
NEGATIVES = [
    {"lane": "neg.chat-on-search-only", "template": "product-manual", "app_type": "chat", "accept_codes": (40085,)},
    {"lane": "neg.search-on-chat-only", "template": "contract-compliance", "app_type": "search", "accept_codes": (40085,)},
    {"lane": "neg.search-on-chat-only-2", "template": "training", "app_type": "search", "accept_codes": (40085,)},
    # `agent` is declared by six templates but their packs carry no canvas DSL,
    # so the catalog reports them as not renderable. The API must say so
    # explicitly instead of creating an empty canvas.
    {"lane": "neg.agent-without-canvas", "template": "training", "app_type": "agent", "accept_codes": (40085,)},
    # 自动建库 creates empty knowledge bases, and the engine refuses to bind a
    # dataset without a parsed chunk. That refusal is legitimate, but its message
    # used to be the engine's English sentence about an internal dataset id, which
    # told the operator nothing. It must now instruct (doc/286 §4).
    {"lane": "neg.auto-datasets-empty-kb", "template": "enterprise-qa", "app_type": "chat",
     "accept_codes": (40085,), "auto_create": True,
     "expect_message": "no parsed content yet"},
]

# The catalog's own evaluation questions are written for another corpus. Ask the
# first one anyway: an off-domain question must still come back as a normal
# answer (no 5xx, no empty body), it simply is not keyword-asserted.
OFF_DOMAIN_PROBE = True


def run_lane(token, spec, catalog, dataset_ids, project_id):
    lane = spec["lane"]
    key = spec["template"]
    app_type = spec["app_type"]
    rec = EVIDENCE["lanes"].setdefault(lane, {"template": key, "app_type": app_type})
    rec["run"] = RUN
    row = next((it for it in catalog if it.get("key") == key), None)
    if not row:
        fail(lane, f"template {key} not in catalog")
        return
    rec["renderable"] = row.get("renderable_app_types")
    if app_type not in (row.get("renderable_app_types") or []):
        fail(lane, f"{app_type} is not renderable for {key} (renderable={row.get('renderable_app_types')})")
        return
    dataset_id = dataset_ids.get(spec["dataset"])
    if not dataset_id:
        fail(lane, f"dataset {spec['dataset']} missing")
        return

    view, ds_row = capability_check(token, row["id"], dataset_id, app_type, lane)
    rec.pop("repairs_applied", None)
    rec["check_before"] = {"verdict": ds_row.get("verdict"),
                           "repairs": [r.get("action") for r in (ds_row.get("repairs") or [])]}
    if ds_row.get("verdict") == "blocked":
        fail(lane, f"capability blocked: "
                   f"{[r.get('capability') for r in (ds_row.get('requirements') or []) if not r.get('satisfied')]}")
        return
    if ds_row.get("repairs"):
        rec["repairs_applied"] = apply_repairs(token, dataset_id, ds_row, lane)
        # Append-only: a later pass finds the dataset already satisfied and applies
        # nothing, which must not erase the evidence that this lane was repaired.
        rec.setdefault("repairs_history", []).append({"run": RUN, "actions": rec["repairs_applied"]})
        if "enable_pushdown_and_sync_meta" in rec["repairs_applied"]:
            view, ds_row = wait_for_metadata_check(token, row["id"], dataset_id, app_type, lane)
        else:
            view, ds_row = capability_check(token, row["id"], dataset_id, app_type, lane)
        rec["check_after"] = {"verdict": ds_row.get("verdict"),
                              "repairs": [r.get("action") for r in (ds_row.get("repairs") or [])]}
        ok_or_fail(lane, ds_row.get("verdict") in ("satisfied", "probe_limited"),
                   f"still {ds_row.get('verdict')} after repairs: "
                   f"{[(r.get('capability'), r.get('achieved')) for r in (ds_row.get('requirements') or []) if not r.get('satisfied')]}")

    data = instantiate(token, row["id"], app_type, f"E2E-{key}-{app_type}-{TAG}", project_id, dataset_id, lane)
    rec["instance"] = {"id": data.get("instance_id"), "state": data.get("instantiation_state")}
    questions = list(spec["questions"])
    if OFF_DOMAIN_PROBE and (row.get("evaluation_questions") or []):
        questions = questions + [(row["evaluation_questions"][0], None)]

    if app_type == "chat":
        chat_id = (data.get("chat") or {}).get("id")
        rec["app_id"] = chat_id
        if not chat_id:
            fail(lane, "instantiate returned no chat id")
            return
        rec["qa"] = ask_chat(token, chat_id, questions, lane)
    else:
        app_id = (data.get("search_app") or {}).get("id")
        rec["app_id"] = app_id
        if not app_id:
            fail(lane, "instantiate returned no search_app id")
            return
        rec["qa"] = ask_search(token, app_id, questions, lane)


def run_negative(token, spec, catalog, dataset_ids, project_id):
    lane = spec["lane"]
    row = next((it for it in catalog if it.get("key") == spec["template"]), None)
    rec = EVIDENCE["negatives"].setdefault(lane, {"template": spec["template"], "app_type": spec["app_type"]})
    rec["run"] = RUN
    if not row:
        fail(lane, f"template {spec['template']} not in catalog")
        return
    dataset_id = dataset_ids.get((next((l["dataset"] for l in LANES if l["template"] == spec["template"]), None)) or "eq")
    body = {"app_type": spec["app_type"], "name": f"E2E-{spec['template']}-{spec['app_type']}-neg-{TAG}",
            "project_id": project_id, "template_version": 1,
            "create_missing_datasets": False, "dataset_ids": [dataset_id]}
    if spec.get("auto_create"):
        # Empty `dataset_ids` + opt-in: the server materialises the template's own
        # knowledge-base suggestions, which arrive empty.
        body.update({"create_missing_datasets": True, "dataset_ids": []})
    st, pl = call(f"/scenario-template-assets/{row['id']}/instantiate", "POST", body, token=token,
                  headers={"Idempotency-Key": f"e2e-{lane}-{TAG}"})
    rec.update({"http": st, "code": pl.get("code"), "message": (pl.get("message") or "")[:200]})
    step("negative", "POST", f"/scenario-template-assets/{row['id']}/instantiate", st, pl,
         f"{spec['app_type']} on {spec['template']} -> {st}/{pl.get('code')} (want 4xx)", lane=lane)
    ok_or_fail(lane, st >= 400 and st < 500 and pl.get("code") in spec["accept_codes"],
               f"expected 4xx/{spec['accept_codes']}, got {st}/{pl.get('code')} {pl.get('message')}")
    ok_or_fail(lane, "agent_dsl" not in (pl.get("message") or ""),
               f"message leaks internal jargon to the operator: {pl.get('message')}")
    if spec.get("expect_message"):
        ok_or_fail(lane, spec["expect_message"] in (pl.get("message") or ""),
                   f"refusal must instruct the operator ('{spec['expect_message']}'), got: {pl.get('message')}")


def main():
    ap = argparse.ArgumentParser()
    # `all` = data + lanes; `data` = upload/parse only; `lanes` = everything that
    # needs an existing environment (catalog -> check -> repair -> instantiate -> QA).
    ap.add_argument("--phase", choices=["all", "data", "lanes"], default="all")
    ap.add_argument("--only", default="", help="comma separated lane keys")
    args = ap.parse_args()
    only = {s.strip() for s in args.only.split(",") if s.strip()}
    os.makedirs(OUT_DIR, exist_ok=True)
    global RUN
    if os.path.exists(STATE_PATH):
        STATE.update(json.load(open(STATE_PATH, encoding="utf-8")))
    if os.path.exists(EVIDENCE_PATH):
        previous = json.load(open(EVIDENCE_PATH, encoding="utf-8"))
        # Keep the facts that survive a partial re-run: dataset metadata, chunk
        # counts and the run history. Lanes/steps are appended, not reset, so the
        # file stays one auditable timeline for the tag.
        EVIDENCE["dataset_chunks"] = previous.get("dataset_chunks") or {}
        EVIDENCE["meta"]["datasets"] = previous.get("meta", {}).get("datasets") or {}
        EVIDENCE["meta"]["runs"] = previous.get("meta", {}).get("runs") or []
        # Lanes / negatives / steps accumulate: a `--only` smoke pass must not
        # erase the full run before it. Each record carries its run number and the
        # summary only reports what this invocation touched.
        EVIDENCE["lanes"] = previous.get("lanes") or {}
        EVIDENCE["negatives"] = previous.get("negatives") or {}
        EVIDENCE["steps"] = previous.get("steps") or []
        EVIDENCE["env_notes"] = previous.get("env_notes") or []
        RUN = len(EVIDENCE["meta"]["runs"]) + 1

    log(f"== target {BASE} tag={TAG} lanes={len(LANES)}")
    # The workspace user is the fixture; the platform session only exists to
    # create it on a virgin database. Platform-scope listings do not show
    # workspace tenants/users, so a duplicate-create 40911/40022 is the only
    # signal that the fixture is already there.
    token = try_login(WS_USER, WS_PASS)
    if token:
        tenant_id = workspace_tenant(token)
        step("bootstrap", "POST", "/auth/login", 200, {"code": 0}, f"workspace user {WS_USER} (existing)")
    else:
        ptok = login(PLATFORM_USER, PLATFORM_PASS)
        tenant_id = ensure_tenant(ptok)
        ensure_user(ptok, tenant_id)
        token = login(WS_USER, WS_PASS)
        tenant_id = tenant_id or workspace_tenant(token)
    STATE["tenant_id"] = tenant_id
    project_id = ensure_project(token)
    STATE["project_id"] = project_id
    EVIDENCE["meta"].update({"tenant_id": tenant_id, "project_id": project_id, "user": WS_USER})

    if args.phase in ("all", "data"):
        dataset_ids = {}
        needed = {spec["dataset"] for spec in LANES}
        for key in DATASETS:
            if key in needed:
                dataset_ids[key] = ensure_dataset(token, key)
        STATE["datasets"] = dataset_ids
        EVIDENCE["meta"]["datasets"] = dataset_ids

    if args.phase != "data":
        dataset_ids = STATE.get("datasets") or {}
        st, pl = call("/scenario-template-catalog", token=token)
        catalog = pl.get("data") if isinstance(pl.get("data"), list) else []
        step("catalog", "GET", "/scenario-template-catalog", st, pl, f"catalog n={len(catalog)}")
        ok_or_fail("catalog", len(catalog) >= 9, f"catalog returned {len(catalog)} templates (want >= 9)")
        for spec in LANES:
            if only and spec["lane"] not in only:
                continue
            log(f"-- lane {spec['lane']}")
            run_lane(token, spec, catalog, dataset_ids, project_id)
        if not only:
            for spec in NEGATIVES:
                run_negative(token, spec, catalog, dataset_ids, project_id)

    # A 5xx anywhere is a product/environment defect, never an acceptable answer.
    server_errors = [s for s in EVIDENCE["steps"] if (s.get("http") or 0) >= 500]
    step("assert", "GET", "no-5xx", 0 if not server_errors else 500, {"code": 0},
         f"5xx steps={len(server_errors)}", lane="assert")
    ok_or_fail("assert", not server_errors,
               f"{len(server_errors)} step(s) returned 5xx: {[s['path'] for s in server_errors][:5]}")

    json.dump(STATE, open(STATE_PATH, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    EVIDENCE["meta"].setdefault("runs", []).append({
        "run": RUN, "phase": args.phase, "only": sorted(only),
        "finished_utc": datetime.now(timezone.utc).isoformat(), "fails": list(FAILS)})
    EVIDENCE["meta"]["finished_utc"] = datetime.now(timezone.utc).isoformat()
    EVIDENCE["meta"]["fails"] = FAILS
    json.dump(EVIDENCE, open(EVIDENCE_PATH, "w", encoding="utf-8"), ensure_ascii=False, indent=1)

    log("\n== matrix")
    log(f"{'lane':24} {'check':14} {'repaired':34} {'inst':9} {'qa':7}")
    for spec in LANES:
        rec = EVIDENCE["lanes"].get(spec["lane"]) or {}
        if rec.get("run") != RUN:
            log(f"{spec['lane']:24} (not run this pass)")
            continue
        checked = ((rec.get("check_after") or rec.get("check_before") or {}).get("verdict")) or "-"
        qa = [x for x in (rec.get("qa") or []) if x.get("expect")]
        qa_ok = f"{sum(1 for x in qa if x.get('hit'))}/{len(qa)}" if qa else "-"
        log(f"{spec['lane']:24} {str(checked):14} "
            f"{','.join(rec.get('repairs_applied') or []) or '-':34} "
            f"{str(rec.get('instance', {}).get('state') or '-'):9} {qa_ok:7}"
            + ("  FAIL" if rec.get("fail") else ""))
    log("\n== negatives")
    for spec in NEGATIVES:
        rec = EVIDENCE["negatives"].get(spec["lane"]) or {}
        if rec.get("run") != RUN:
            continue
        log(f"{spec['lane']:24} {spec['template']:22} {spec['app_type']:7} "
            f"-> {rec.get('http')}/{rec.get('code')}" + ("  FAIL" if rec.get("fail") else ""))
    log(f"\nevidence -> {EVIDENCE_PATH}")
    if FAILS:
        log("\n-- failures")
        for f in FAILS:
            log("  * " + f)
        sys.exit(1)
    log("\nALL EXECUTED LANES GREEN")


if __name__ == "__main__":
    main()
