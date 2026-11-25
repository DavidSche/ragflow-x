#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""W-4 pilot driver: put ONE wiki-compiled dataset in front of the W-4 probe.

W-4 (doc/304 §7 item 23) is four states, and none of them can be faked by a write
that returns 200:

    A  the target dataset has a confirmed run with a hash-bound artifact
    B  the independent artifacts query returns ready, self-consistent rows
    C  locator hints are effective and B holds
    D  the *target* revision carries location_hints with a traceable source

This file is the **write** half of that work: it is the operator tool that creates
the upstream compilation pipeline, binds it to the documents, ingests them, and
then triggers the ragflow-x compilation run. It is deliberately *not* the gate —
`w4_artifact_landing.py` is, because it reads with paths the writer never touches.
A driver that graded its own writes would be measuring its optimism.

Two rules this tool inherits from doc/304 and enforces in code:

* **a started compile is not a landed artifact.** Every step has a postcondition
  that is read back from a different endpoint than the one that wrote it, and the
  run stops at the first unmet postcondition instead of pressing on.
* **W-4 passing does not authorise T11.** This file never generates a report
  revision and never touches the treatment arm; D is the operator's call after
  A/B/C are read.
* **a terminal document is not a writable one, and a repair is not a wider permission.**
  The pre-write read comes from `w4_wait_state` (idle / busy / failed / unknown). A
  FAILED document refuses the *bind* write, and `safe_to_redispatch` — a narrower
  question that requires a FAILED document and still refuses busy/unknown — is the
  only thing that may authorise the engine's own repair (PATCH + ingest again).
  doc/304 §7 item 34: before this, a failed pilot dataset could only be repaired by
  deleting documents, which is how the 27 products disappeared on 2026-11-23.

The engine keys used here follow doc/environment §3: the deployment's file is
`internal/provider/ragflow/http.go` @ v1.0.0
  * `GET  /system/version`                     -> engine band (>=1.0.0 ⇒ wiki only)
  * `GET  /compilation-template-groups`        -> which group has a real config
  * `POST /agents` + `POST /agents/{id}/publish`
  * `POST /datasets/{id}/documents`            -> upload
  * `PATCH /datasets/{id}/documents/{doc}`     -> parse_type=2 + pipeline binding
  * `POST /documents/ingest`                   -> run the pipeline
  * `GET  /datasets/{id}/documents`            -> progress / ingestion_status

Usage:

    python scripts/probes/w4_wiki_pilot.py --status          # read-only
    python scripts/probes/w4_wiki_pilot.py --dry-run         # print the plan
    python scripts/probes/w4_wiki_pilot.py --apply           # do the writes

Exit codes (same sentences as the rest of the probe family, doc/305 §4.3):

    0 = the requested mode finished and every postcondition it checked held
    1 = a postcondition failed (the run stopped there; the reading is in the output)
    2 = environment unusable / usage (missing credential, host down)
    3 = mode ran, but skipped a step whose input an earlier step did not produce
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
import sys
import time

import requests

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)

import probe_common as pc  # noqa: E402
# The wait decision is a separate, hostless module: it has no client, it cannot write, and
# `probes.sh` may run its tests without gaining a path to this writer (doc/304 E97).
import w4_wait_state as wait_state  # noqa: E402

# A hostname is configuration; a credential is not (doc/304 §7 item 12).
DEFAULT_RGX_BASE = "http://192.168.4.151:19193"
DEFAULT_ENGINE_BASE = "http://192.168.4.151:9380"
DEFAULT_USER = "admin"

# The pilot's identity. Fixed strings, because a name that changes per run cannot be
# re-read later: "did the pilot land?" must be answerable by looking at the artefact,
# not by finding the shell that created it.
DATASET_NAME = "rgx-w4-wiki-pilot-20261123"
PIPELINE_TITLE = "rgx-w4-wiki-compiler-20261123"
# 1caa89e72c7a4c78944994ce19982a56 is the only wiki group on 4.151 whose template
# config is non-empty; the eight rgx-wiki-ab-* groups carry `{}` and compile to zero
# entities, which binds no artifact (live test comment, knowledge_compilation_live_test.go).
TEMPLATE_GROUP_ID = "1caa89e72c7a4c78944994ce19982a56"
TEMPLATE_KIND = "wiki"

# The smallest three documents of test/安全生产政策法规-docs that are complete files
# (the corpus also holds a file whose name says 不完整; a pilot should not start by
# compiling a truncated source and then argue about why entities look thin).
# The pilot's corpus, minus the one document that did not survive its own compile.
#
# Three files were attempted on 2026-11-23. Two reached COMPLETED (9 and 7 chunks);
# `山东省属企业安全生产考核标准.md` (10280 bytes) ran 42 minutes, reached progress 0.72
# and came back FAILED with an **empty** `progress_msg`.
#
# Corrected 2026-11-23 (doc/304 E103): "empty `progress_msg`" was read as "the engine never
# said why" and that was wrong. `progress_msg` is legacy and is deliberately not rewritten on
# failure (`markTerminalProgress` in internal/ingestion/service/ingestion_service.go says so),
# while the reason goes to `pipeline_operation_log` + the ingestion event stream — which the
# engine serves read-only:
#
#   GET /datasets/{id}/ingestions?log_type=file&document_id={doc}
#   GET /datasets/{id}/ingestions/{log_id}/messages
#
# `w4_compiler_evidence.py` reads them. The answer (proxy `504 proxy queue wait exceeded` and
# `502 proxy upstream error: hard deadline (absolute)`, after 5 retries) is our own routing
# decision, not a defect in the corpus: this pilot pinned `llm_id` to
# `deepseek-v4-flash-0731`, which lives on `gpustack-local` — the single machine at
# 192.168.31.211 that the `:18081` gate protects. A rewrite-type batch of chat calls is exactly
# the load §304 §7 item 32 (and the red line) says must not be pointed there.
#
# Fixed 2026-11-23 (doc/304 E107, on the operator's decision): the Compiler now points at
# AMD's `Qwen3.8-27B` on `radeon-external-proxy-155` (`:18082`, cap 2 / 1500ms stagger), so the
# batch leaves the single machine alone. The embedding model stays on `gpustack-local`: it is
# one small call per chunk, and moving it would invalidate every vector already indexed.
# The two documents still in CORPUS failed with this routing in place; nothing about the
# corpus or the compiler template changed. It produced 0 chunks and 0
# products, so the third document was deleted rather than left in the manifest: a run that includes it can
# never satisfy the source-terminal gate (`progress >= 1 and ingestion_status == COMPLETED`
# for every document in the run), and dropping it from `document_ids` while it stays a dataset
# document would make the manifest hash depend on a document the run quietly ignores.
#
# The failure is recorded here instead of retried, because one artifact landing is what
# W-4 has to prove; "compile quality of an 10KB regulation" is a different question and
# is not what this pilot was authorised to answer. Naming the cause did not authorise a
# re-run either: a retry against the same saturated proxy would spend the same 20–37 minutes
# and fail the same way, while destroying the evidence that named it.
CORPUS = [
    "安全生产社会化服务机构管理办法.md",
    "山东省安全生产举报办法.md",
]
CORPUS_DIR = os.path.join(REPO, "test", "安全生产政策法规-docs")

# The Compiler's chat model must NOT resolve to `gpustack-local` (doc/304 E107): that instance
# is the single machine behind the `:18081` gate, and knowledge compilation issues a batch of
# rewrite-type chat calls (the 2026-11-23 failure window logged 179 kills/queue-expiries on
# `gpustack` chat, 2 on embeddings). AMD carries the batch; see §304 §7 item 32.
#
# 2026-11-24 (doc/304 E125): AMD stopped carrying it either. Measured on the live engine, a
# 131-character answer through `Qwen3.8-27B` took 40.1s, and three real prompts on the
# `gpustack` route hit the proxy's absolute deadline at 120s apiece — a wiki rewrite needs
# minutes at that rate, and every call is capped at 120s by `model_proxy.mjs`.
# `Qwen/Qwen3.8-27B-FP8` on the ainet gateway (registered on the engine as
# `OpenAI-API-Compatible/default`, ~54 tok/s per doc/environment §1.4) answers in ~1s, and it
# is *external*, so it does not touch the single machine. That is the routing this pilot uses
# from now on; `EMBEDDING_*` deliberately stays on `gpustack-local` (one small call per chunk,
# and moving it would invalidate every vector already indexed).
CHAT_MODEL_NAME = "Qwen/Qwen3.8-27B-FP8"
CHAT_INSTANCE_NAME = "default"
EMBEDDING_MODEL_NAME = "qwen3-embedding-0.6b"
EMBEDDING_INSTANCE_NAME = "gpustack-local"
CHUNK_TOKEN_SIZE = 512

INGEST_TIMEOUT = 3600.0   # one wiki compile of ~28KB of markdown, measured generously
COMPILE_TIMEOUT = 5400.0  # reconcile budget for the ragflow-x run (upstream merges 75+ pages)
POLL_SECONDS = 10.0

TERMINAL_RUN_STATES = ("confirmed", "failed", "cancelled", "unknown")


# ── pure pieces (the selftest asserts these without a network) ─────────────

def pipeline_dsl(group_id, embedding_model, chat_model, suffix):
    """The File→Parser→TokenChunker→Compiler→Tokenizer canvas, doc/262 §C shape.

    The compiler node is the only place the pilot can be wrong in a way that looks
    right: a group with an empty config still parses, chunks and indexes — it just
    produces no entities, and an artifact with no entities binds to nothing.
    """
    comp = lambda name: "%s:%s" % (name, suffix)  # noqa: E731
    components = {
        "File": {
            "downstream": [comp("Parser")],
            "obj": {"component_name": "File", "params": {}},
            "upstream": [],
        },
        comp("Parser"): {
            "downstream": [comp("TokenChunker")],
            "obj": {
                "component_name": "Parser",
                "params": {"outputs": {
                    "html": {"type": "string", "value": ""},
                    "json": {"type": "Array<object>", "value": ""},
                    "markdown": {"type": "string", "value": ""},
                    "text": {"type": "string", "value": ""},
                }},
            },
            "upstream": ["File"],
        },
        comp("TokenChunker"): {
            "downstream": [comp("Compiler")],
            "obj": {
                "component_name": "TokenChunker",
                "params": {
                    "chunk_token_size": CHUNK_TOKEN_SIZE,
                    "delimiters": ["\n", "!", "?", ";", "。", "；", "！", "？"],
                },
            },
            "upstream": [comp("Parser")],
        },
        comp("Compiler"): {
            "downstream": [comp("Tokenizer")],
            "obj": {
                "component_name": "Compiler",
                "params": {
                    "compilation_template_group_id": group_id,
                    "embedding_model": embedding_model,
                    "llm_id": chat_model,
                    "plan": False,
                },
            },
            "upstream": [comp("TokenChunker")],
        },
        comp("Tokenizer"): {
            "downstream": [],
            "obj": {
                "component_name": "Tokenizer",
                "params": {
                    "fields": "text",
                    "filename_embd_weight": 0.1,
                    "search_method": ["embedding", "full_text"],
                },
            },
            "upstream": [comp("Compiler")],
        },
    }
    return {
        "components": components,
        "graph": {
            "nodes": [
                {"id": "File", "type": "fileNode"},
                {"id": comp("Parser"), "type": "parserNode"},
                {"id": comp("TokenChunker"), "type": "chunkerNode"},
                {"id": comp("Compiler"), "type": "compilationNode"},
                {"id": comp("Tokenizer"), "type": "tokenizerNode"},
            ],
            "edges": [
                {"source": "File", "target": comp("Parser")},
                {"source": comp("Parser"), "target": comp("TokenChunker")},
                {"source": comp("TokenChunker"), "target": comp("Compiler")},
                {"source": comp("Compiler"), "target": comp("Tokenizer")},
            ],
        },
    }


def dsl_contract(dsl):
    """What the engine must find in the canvas for compilation to be possible."""
    if not isinstance(dsl, dict):
        return {"parsed": False}
    components = dsl.get("components") or {}
    names = []
    for value in components.values():
        if isinstance(value, dict):
            names.append(((value.get("obj") or {}).get("component_name") or "").strip())
    graph = dsl.get("graph") or {}
    nodes = graph.get("nodes") or []
    edges = graph.get("edges") or []
    return {
        "parsed": bool(components),
        "has_parser": "Parser" in names,
        "has_chunker": "TokenChunker" in names,
        "has_compiler": "Compiler" in names,
        "has_indexer": "Tokenizer" in names,
        "path_valid": bool(nodes) and bool(edges),
        "components": names,
        "nodes": len(nodes),
        "edges": len(edges),
        "compiler_llm_id": compiler_param(dsl, "llm_id"),
        "compiler_embedding_model": compiler_param(dsl, "embedding_model"),
        "compiler_template_group_id": compiler_param(dsl, "compilation_template_group_id"),
    }


def repoint_dsl(dsl, llm_id, template_group_id):
    """Return (dsl', changes): a copy of `dsl` with the Compiler's pins corrected.

    Wholesale-replacing the canvas with a freshly built DSL would also "work", and it is
    exactly the mistake this function exists to avoid: it would silently discard whatever a
    human had added to that canvas. The reconciliation has to be a *field-level* patch so the
    diff stays auditable — the same standard the live change of 2026-11-23 was held to.
    """
    import copy
    out = copy.deepcopy(dsl)
    changes = []
    for value in (out.get("components") or {}).values():
        obj = (value or {}).get("obj") or {}
        if (obj.get("component_name") or "").strip() != "Compiler":
            continue
        params = obj.setdefault("params", {})
        for key, want in (("llm_id", llm_id),
                          ("compilation_template_group_id", template_group_id)):
            if want and params.get(key) != want:
                changes.append("%s: %r -> %r" % (key, params.get(key), want))
                params[key] = want
    return out, changes


def compiler_param(dsl, key):
    """One `params` value off the Compiler node, or "" when it is absent.

    The per-call param wins over the compilation template's own config
    (`overlayTemplateConfig`: "the caller's per-call override always wins"), so this
    value — not `compilation_template.config.llm_id` — is what a run actually uses.
    That distinction is the whole of E107: the template's `llm_id` can be stale and
    still look harmless while this one sends the batch to a single machine.
    """
    if not isinstance(dsl, dict):
        return ""
    for value in (dsl.get("components") or {}).values():
        if not isinstance(value, dict):
            continue
        obj = value.get("obj") or {}
        if (obj.get("component_name") or "").strip() != "Compiler":
            continue
        params = obj.get("params") or {}
        token = params.get(key)
        return token.strip() if isinstance(token, str) else ""
    return ""


def document_terminal(doc):
    """A document is terminal when its own progress says so.

    `progress` is a float on 1.0.0; `ingestion_status` carries FAILED/COMPLETED and is
    the only field that distinguishes "done" from "gave up" — a timed-out task also
    reaches progress 1.0 with an error message attached.
    """
    try:
        progress = float(doc.get("progress") or 0)
    except (TypeError, ValueError):
        progress = 0.0
    status = str(doc.get("ingestion_status") or "").upper()
    return progress >= 1.0 or status in ("COMPLETED", "FAILED", "CANCELED", "CANCELLED")


def document_running(doc):
    """Is a parse/merge task still on this document?

    Kept as the name the log lines use, but the decision is `w4_wait_state.classify`, not
    `run in ("1", "RUNNING")`. That one-line version is what let the 2026-11-23 write run
    under a document the engine still owned: `run` is optional on 1.0.0 (see
    `NormalizeParseStatus`), and a FAILED document reports `run=0` with progress 1.
    """
    return wait_state.classify(doc).state == wait_state.STATE_BUSY


def fetch_documents(engine, dataset_id, page_size=100):
    data = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=%d"
                     % (dataset_id, page_size)) or {}
    rows = data.get("docs") if isinstance(data, dict) else data
    out = {}
    for row in rows or []:
        if isinstance(row, dict):
            out.setdefault(str(row.get("name") or ""), row)
    return out


def wait_documents_idle(engine, dataset_id, names, timeout=600.0, defer_failed=False):
    """Wait until every named document is *idle* — four states, and unknown is not idle.

    A config write is refused with code 102 while a task runs. Waiting is the honest
    move: the task was started by this pilot's own upload, so it is not a foreign actor to
    cancel, and binding under it would leave the run reading a half-parsed document.

    The decision lives in `w4_wait_state` (idle / busy / failed / unknown). This function
    only supplies the read and turns a refusal into the pilot's failure sentence:

    * a document the engine gave up on stops the wait immediately — a longer wait cannot
      turn FAILED into a safe write, and 2026-11-23 is what treating it as "not running"
      cost (a FAILED document with 0 chunks, then 27 products deleted by the merge pass);
    * a row that does not say (`document_missing`, `no_state_fields`, `done_without_status`)
      keeps waiting, and then fails closed rather than proceeding;
    * running out of patience is a refusal, not an "ok, go".

    `defer_failed=True` is for callers that can *repair* a failed set (doc/304 §7 item 34):
    the terminal reading is returned instead of raised, because the next question is
    `wait_state.safe_to_redispatch` (a redispatch write), not "wait longer". It does **not**
    make a FAILED document idle — `assess_before_write().safe` still refuses it, and S6 still
    refuses to dispatch a run over it while upstream claims `completed`.
    """
    def report(assessment, elapsed):
        print("     waiting %5.0fs for the upload's own parse: %s"
              % (elapsed, assessment.describe()))

    outcome = wait_state.wait_for_idle(
        lambda: fetch_documents(engine, dataset_id), names,
        timeout=timeout, poll=POLL_SECONDS, on_poll=report)
    if not outcome.safe:
        if defer_failed and outcome.reason == "documents_failed":
            print("     %s" % outcome.why())
            print("     deferring: whether a repair re-dispatch is allowed is a different "
                  "question (w4_wait_state.safe_to_redispatch), asked by S4 below")
            return fetch_documents(engine, dataset_id)
        raise pc.CaseFail("refusing to write under a document that is not idle: %s" % outcome.why())
    print("     %s" % outcome.why())
    return fetch_documents(engine, dataset_id)


def file_fingerprint(path):
    with open(path, "rb") as handle:
        data = handle.read()
    return {
        "file": os.path.basename(path),
        "bytes": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
    }


def detail_counts(run):
    """The dispatcher's own numbers, out of the run's `detail` JSON.

    `CompilationRun` has no artifact columns; the counts live in `detail`
    (knowledge_compilation_dispatch.go writes artifact_count / ready_artifact_count /
    hash_bound_artifact_count there). A driver that read a field which does not exist
    would print 0 and blame the engine.
    """
    raw = run.get("detail")
    if isinstance(raw, dict):
        parsed = raw
    elif isinstance(raw, str) and raw.strip():
        try:
            parsed = json.loads(raw)
        except ValueError:
            parsed = {}
    else:
        parsed = {}
    if not isinstance(parsed, dict):
        parsed = {}
    out = {}
    for key in ("artifact_count", "ready_artifact_count", "bound_artifact_count",
                "hash_bound_artifact_count", "binding_incomplete_count"):
        try:
            out[key] = int(parsed.get(key) or 0)
        except (TypeError, ValueError):
            out[key] = 0
    return out


def engine_artifact_total(engine, dataset_id):
    """How many products the engine currently holds for this dataset.

    `total` is read from the artifact listing itself, not from the compilation status:
    the status reports what the last merge pass *intended*, and a pass that deletes and
    rewrites nothing still reports `state: completed`.
    """
    data = engine.ok("GET", "/datasets/%s/artifacts?page=1&page_size=1" % dataset_id) or {}
    try:
        return int((data or {}).get("total") or 0)
    except (TypeError, ValueError):
        return 0


def engine_compilation_status(engine, dataset_id):
    data = engine.ok("GET", "/datasets/%s/compilation/status" % dataset_id) or {}
    return data if isinstance(data, dict) else {}


def pick(rows, key, value):
    for row in rows or []:
        if isinstance(row, dict) and str(row.get(key) or "") == value:
            return row
    return None


def canvas_rows(data):
    """`GET /agents` answers `{canvas:[...], total:N}`; a bare list is also accepted.

    The engine lists agent canvases and dataflow canvases through the same endpoint and
    distinguishes them by `canvas_category`, so the pilot filters on that instead of
    trusting the title alone.
    """
    rows = []
    if isinstance(data, dict):
        rows = data.get("canvas") or data.get("agents") or []
    elif isinstance(data, list):
        rows = data
    return [row for row in rows if isinstance(row, dict)]


def pipeline_rows(data):
    rows = canvas_rows(data)
    dataflow = [row for row in rows if str(row.get("canvas_category") or "") == "dataflow_canvas"]
    return dataflow or rows


# ── the engine session (Bearer key, no cookies) ────────────────────────────

class Engine:
    def __init__(self, base, api_key, timeout=120.0):
        self.base = base.rstrip("/") + "/api/v1"
        self.timeout = timeout
        self.session = requests.Session()
        self.session.headers["Authorization"] = "Bearer " + api_key

    def api(self, method, path, **kw):
        kw.setdefault("timeout", self.timeout)
        if "json_body" in kw:
            kw["json"] = kw.pop("json_body")
        resp = self.session.request(method, self.base + path, **kw)
        try:
            body = resp.json()
        except ValueError:
            body = {"_raw": resp.text[:2000]}
        return resp.status_code, body

    def ok(self, method, path, **kw):
        status, body = self.api(method, path, **kw)
        if status != 200 or body.get("code") not in (0, None):
            raise pc.CaseFail("engine %s %s -> %s code=%s %s"
                              % (method, path, status, body.get("code"),
                                 str(body.get("message"))[:300]))
        return body.get("data")


def engine_models(engine, model_type):
    rows = engine.ok("GET", "/models?type=" + model_type) or []
    return rows


def model_row(rows, name):
    for row in rows:
        if row.get("name") == name:
            return row
    return None


# ── modes ─────────────────────────────────────────────────────────────────

def mode_status(engine, rgx, args):
    """Read-only: where the pilot stands, without writing anything."""
    version = engine.ok("GET", "/system/version")
    print("engine version        : %s" % (version or {}).get("version"))
    groups = (engine.ok("GET", "/compilation-template-groups?page=1&page_size=100")
              or {}).get("groups") or []
    real = []
    for group in groups:
        for template in group.get("templates") or []:
            config = template.get("config")
            if str(template.get("kind") or "").lower() == TEMPLATE_KIND and isinstance(config, dict) and config:
                real.append((group.get("id"), template.get("id"), sorted(config.keys())))
    print("wiki groups with config: %d" % len(real))
    for row in real:
        print("   %s/%s keys=%s" % (row[0][:12], row[1][:12], ",".join(row[2])[:80]))

    datasets = engine.ok("GET", "/datasets?page=1&page_size=100") or []
    target = pick(datasets, "name", DATASET_NAME)
    if not target:
        print("dataset %-28s : absent" % DATASET_NAME)
        return 0, {"dataset": None}
    print("dataset %-28s : %s docs=%s chunks=%s"
          % (DATASET_NAME, target.get("id"), target.get("document_count"), target.get("chunk_count")))
    docs = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=100" % target.get("id")) or {}
    rows = docs.get("docs") if isinstance(docs, dict) else docs
    for doc in rows or []:
        print("   doc %-40s progress=%s status=%s pipeline=%s run=%s"
              % (str(doc.get("name"))[:40], doc.get("progress"),
                 doc.get("ingestion_status"), str(doc.get("pipeline_id") or "-")[:10],
                 doc.get("run")))
    rgx_dataset = pick(rgx.ok("GET", "/api/v1/datasets?page=1&page_size=100") or [],
                       "name", DATASET_NAME)
    if rgx_dataset:
        runs = rgx.ok("GET", "/api/v1/datasets/%s/compilation/runs?page=1&page_size=20"
                      % rgx_dataset.get("id")) or {}
        for run in (runs.get("items") if isinstance(runs, dict) else runs) or []:
            counts = detail_counts(run)
            print("   run %s state=%s manifest_complete=%s artifacts=%s hash_bound=%s"
                  % (run.get("id"), run.get("state"), run.get("manifest_complete"),
                     counts["artifact_count"], counts["hash_bound_artifact_count"]))
    return 0, {"dataset": target.get("id")}


def mode_dry_run(engine, rgx, args):
    """Print the plan, including every postcondition, without writing."""
    print_plan()
    return 0, {}


def print_plan():
    print("plan (write steps; each postcondition is read from a different endpoint)")
    print("  S1 rgx POST /api/v1/datasets {name:%s} -> upstream dataset + link" % DATASET_NAME)
    print("     post: GET /api/v1/datasets returns the name once")
    print("  S2 engine POST /agents (canvas_category=dataflow_canvas, compiler group %s,"
          " Compiler llm_id=%s@%s)" % (TEMPLATE_GROUP_ID[:12], CHAT_MODEL_NAME, CHAT_INSTANCE_NAME))
    print("     if the canvas exists with a different Compiler llm_id:")
    print("     engine PUT /agents/{id} {dsl,release:true} -> canvas row + latest version in one tx")
    print("     engine POST /agents/{id}/publish -> version id (only when no version pins the model)")
    print("     post: GET /agents/{id} DSL has parser/chunker/compiler/indexer and a path;"
          " GET /agents/{id}/versions/{vid} pins the same llm_id")
    print("     why: the per-call llm_id beats compilation_template.config.llm_id"
          " (overlayTemplateConfig), so a stale canvas silently sends the batch to 31.211")
    print("  S3 rgx POST /api/v1/datasets/{id}/documents x%d (multipart)" % len(CORPUS))
    print("     post: GET /datasets/{upstream}/documents lists each name once")
    print("  S4 engine PATCH /datasets/{id}/documents/{doc} {parse_type:2,pipeline_id,pipeline_version_id}")
    print("     engine POST /documents/ingest {doc_ids,run:1,delete:true}")
    print("     repair path (doc/304 §7 item 34): when the pre-write read is not idle because a")
    print("       document is FAILED, w4_wait_state.safe_to_redispatch may permit exactly this")
    print("       PATCH+ingest and nothing else; busy/unknown/failed-with-nothing-to-repair is a")
    print("       refusal. A FAILED document never becomes 'safe' for any other write.")
    print("     post: doc.pipeline_id == the canvas (a doc left in builtin mode has no compiler)")
    print("  S5 wait: every doc idle (idle/busy/failed/unknown; unknown is not idle, and a")
    print("           timeout is a refusal) then doc progress>=1 and ingestion_status terminal")
    print("     post: status COMPLETED, not FAILED (a timed-out task also reaches progress 1)")
    print("  S6 rgx POST /api/v1/knowledge-profiles {profile_type:wiki,status:active}")
    print("     rgx POST /api/v1/datasets/{id}/compilation/runs {kind:wiki,idempotency_key,document_ids}")
    print("     post: run state moves to confirmed via the reconcile worker / reconcile endpoint")
    print("  S7 STOP. Read A/B/C/D with w4_artifact_landing.py. Generating the target")
    print("     revision (D) and T11 treatment are separate, separately authorised steps.")


def mode_apply(engine, rgx, args):
    started = time.time()
    reingest = bool(getattr(args, "reingest", False))
    report = {"dataset": None, "pipeline": None, "documents": [], "run": None,
              "upstream_compile": None, "steps": []}

    def step(name, detail):
        report["steps"].append({"step": name, "detail": detail})
        print("  [%s] %s" % (name, detail))

    # ── S1 the dataset, through ragflow-x so the link exists ──────────────
    datasets = rgx.ok("GET", "/api/v1/datasets?page=1&page_size=100")
    rows = datasets.get("items") if isinstance(datasets, dict) else datasets
    existing = None
    for row in rows or []:
        if row.get("name") == DATASET_NAME:
            existing = row
            break
    if existing:
        dataset = existing
        step("S1", "dataset already present id=%s upstream=%s"
             % (dataset.get("id"), dataset.get("ragflow_dataset_id") or dataset.get("external_id")))
    else:
        dataset = rgx.ok("POST", "/api/v1/datasets", json_body={"name": DATASET_NAME})
        step("S1", "created dataset id=%s" % dataset.get("id"))
    upstream_id = (dataset.get("ragflow_dataset_id") or dataset.get("external_id") or "")
    pc.expect(upstream_id, "S1 dataset has no upstream id in the answer: %r" % (dataset,))
    report["dataset"] = {"rgx_id": dataset.get("id"), "upstream_id": upstream_id}

    # ── S2 the canvas and its published version ───────────────────────────
    models = engine.ok("GET", "/models") or []
    chat = model_row(models, CHAT_MODEL_NAME)
    embed = model_row(models, EMBEDDING_MODEL_NAME)
    pc.expect(chat, "S2 chat model %s is not registered on the engine" % CHAT_MODEL_NAME)
    pc.expect(embed, "S2 embedding model %s is not registered on the engine" % EMBEDDING_MODEL_NAME)
    # The pin is by *instance*, not by name: a same-named model can be republished onto another
    # instance, and the whole point of E107 is that the batch must not land on 31.211.
    pc.expect(chat.get("instance_name") == CHAT_INSTANCE_NAME,
              "S2 chat model %s resolves to instance %r, not %r: knowledge compilation issues a"
              " batch of rewrite-type chat calls and must not be pointed at the single machine"
              " behind the :18081 gate (doc/304 E107, §7 item 32)"
              % (CHAT_MODEL_NAME, chat.get("instance_name"), CHAT_INSTANCE_NAME))
    pc.expect(embed.get("instance_name") == EMBEDDING_INSTANCE_NAME,
              "S2 embedding model %s resolves to instance %r, not %r"
              % (EMBEDDING_MODEL_NAME, embed.get("instance_name"), EMBEDDING_INSTANCE_NAME))
    step("S2", "models chat=%s@%s embedding=%s@%s (batch on AMD, small calls on gpustack)"
         % (chat.get("model_id"), chat.get("instance_name"),
            embed.get("model_id"), embed.get("instance_name")))

    desired_dsl = pipeline_dsl(TEMPLATE_GROUP_ID, embed.get("model_id"),
                               chat.get("model_id"), "w4pilot")
    agents = pipeline_rows(engine.ok("GET", "/agents?page=1&page_size=100") or [])
    canvas = pick(agents, "title", PIPELINE_TITLE)
    if canvas:
        pipeline_id = canvas.get("id")
        step("S2", "canvas already present id=%s" % pipeline_id)
    else:
        canvas = engine.ok("POST", "/agents", json_body={
            "title": PIPELINE_TITLE,
            "description": "ragflow-x W-4 wiki compilation pilot (doc/304 §7 item 23)",
            "canvas_category": "dataflow_canvas",
            "dsl": desired_dsl,
        })
        pipeline_id = canvas.get("id")
        step("S2", "created canvas id=%s" % pipeline_id)

    detail = engine.ok("GET", "/agents/%s" % pipeline_id) or {}
    contract = dsl_contract(detail.get("dsl"))
    step("S2", "canvas contract %s" % json.dumps(contract, ensure_ascii=False))
    pc.expect(contract.get("has_compiler"), "S2 canvas has no Compiler node: %s" % contract)
    pc.expect(contract.get("has_indexer"), "S2 canvas has no indexer node: %s" % contract)
    pc.expect(contract.get("path_valid"), "S2 canvas graph is not connected: %s" % contract)

    # A canvas created before E107 still pins the old model, and reusing it silently would
    # reproduce the failure this change exists to remove. `PUT` writes the canvas row and its
    # latest version in one transaction (`UpdateAgent` -> `SaveOrReplaceLatestTx`), which is
    # why this is a config correction and not a re-ingest.
    if (contract.get("compiler_llm_id") != chat.get("model_id")
            or contract.get("compiler_template_group_id") != TEMPLATE_GROUP_ID):
        patched, changes = repoint_dsl(detail.get("dsl"), chat.get("model_id"),
                                       TEMPLATE_GROUP_ID)
        step("S2", "repointing canvas %s" % json.dumps(changes, ensure_ascii=False))
        pc.expect(changes, "S2 the canvas looks stale but no Compiler field differs: %s"
                  % contract)
        engine.ok("PUT", "/agents/%s" % pipeline_id,
                  json_body={"dsl": patched, "release": True})
        detail = engine.ok("GET", "/agents/%s" % pipeline_id) or {}
        contract = dsl_contract(detail.get("dsl"))
    pc.expect(contract.get("compiler_llm_id") == chat.get("model_id"),
              "S2 canvas Compiler llm_id is %r after the update, want %r"
              % (contract.get("compiler_llm_id"), chat.get("model_id")))

    # Order of `GET /versions` is not part of the contract, so pick by content: the version is
    # usable only if its own Compiler pin matches the model this run intends to use.
    versions = engine.api("GET", "/agents/%s/versions" % pipeline_id)[1].get("data") or []
    usable = [v for v in versions
              if dsl_contract(v.get("dsl")).get("compiler_llm_id") == chat.get("model_id")]
    if usable:
        usable.sort(key=lambda v: (str(v.get("create_time") or ""), str(v.get("id") or "")),
                    reverse=True)
        version_id = usable[0].get("id")
        step("S2", "reusing published version %s (its Compiler pin already matches)" % version_id)
    else:
        version_id = (engine.ok("POST", "/agents/%s/publish" % pipeline_id, json_body={}) or {}).get("id")
        step("S2", "published version %s" % version_id)
    pc.expect(version_id, "S2 publish returned no version id; 1.0.0 refuses pipeline datasets without one")
    own = engine.api("GET", "/agents/%s/versions/%s" % (pipeline_id, version_id))[1].get("data") or {}
    published = dsl_contract(own.get("dsl"))
    pc.expect(published.get("compiler_llm_id") == chat.get("model_id"),
              "S2 published version %s pins Compiler llm_id %r, want %r (a version recorded"
              " before the repoint would send the batch back to the single machine)"
              % (version_id, published.get("compiler_llm_id"), chat.get("model_id")))
    report["pipeline"] = {"id": pipeline_id, "version_id": version_id,
                          "template_group_id": TEMPLATE_GROUP_ID,
                          "chat_model_id": chat.get("model_id"),
                          "embedding_model_id": embed.get("model_id")}

    # ── S3 upload the corpus through ragflow-x ────────────────────────────
    listing = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=100" % upstream_id) or {}
    docs = (listing.get("docs") if isinstance(listing, dict) else listing) or []
    by_name = {}
    for row in docs:
        by_name.setdefault(str(row.get("name") or ""), row)

    fingerprints = []
    for name in CORPUS:
        path = os.path.join(CORPUS_DIR, name)
        pc.expect(os.path.exists(path), "S3 corpus file missing: %s" % path)
        fingerprints.append(file_fingerprint(path))
        if name in by_name:
            step("S3", "document already uploaded: %s" % name)
            continue
        with io.open(path, "rb") as handle:
            body = handle.read()
        status, answer = rgx.api("POST", "/api/v1/datasets/%s/documents" % dataset.get("id"),
                                 files={"file": (name, body, "text/markdown")})
        pc.expect(200 <= status < 300 and answer.get("code") in (0, None),
                  "S3 upload %s -> %s %s" % (name, status, str(answer)[:300]))
        step("S3", "uploaded %s (%d bytes)" % (name, len(body)))
    report["corpus"] = fingerprints

    listing = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=100" % upstream_id) or {}
    docs = (listing.get("docs") if isinstance(listing, dict) else listing) or []
    by_name = {}
    for row in docs:
        by_name.setdefault(str(row.get("name") or ""), row)
    missing = [name for name in CORPUS if name not in by_name]
    pc.expect(not missing, "S3 postcondition failed, engine does not list: %s" % missing)

    # ── S4 bind the canvas, or repair a set the engine gave up on ─────────
    # Uploading through ragflow-x starts a parse immediately (naive, no compiler).
    # RAGFlow refuses a config write while a task is RUNNING (code 102), and the
    # honest response to that is to wait for the task this run already owns — not to
    # stop it, and not to bind underneath it.
    #
    # A FAILED document is deferred rather than fatal: the engine's own repair is to
    # dispatch parsing again, and refusing that left the 2026-11-23 dataset unrepairable
    # (the only way back was deleting documents, which is what destroyed the products).
    # Whether a repair is allowed is asked below, by `safe_to_redispatch`.
    wait_documents_idle(engine, upstream_id, CORPUS, timeout=600.0, defer_failed=True)

    doc_ids = [by_name[name].get("id") for name in CORPUS]
    pc.expect(all(doc_ids), "S4 engine listed a corpus document without an id")
    bound_rows = fetch_documents(engine, upstream_id)
    # The gate the 2026-11-23 run did not have: an upstream that reports `completed` while
    # its own documents are FAILED or still parsing contradicts itself, and neither reading
    # gets to win by preference. Note this reads the *documents* too — `state: completed`
    # alone is exactly the sentence that shipped 0 products (E97).
    try:
        upstream_status = engine_compilation_status(engine, upstream_id)
    except pc.CaseFail as exc:
        upstream_status = None
        step("S4", "compilation/status unreadable (%s) — treated as absent, never as completed"
             % str(exc)[:80])
    gate = wait_state.assess_before_write(bound_rows, CORPUS, upstream_status)
    step("S4", "pre-write read: compilation=%s %s"
         % (wait_state.compilation_state(upstream_status), gate.describe()))
    already_bound = all((bound_rows.get(name) or {}).get("pipeline_id") == pipeline_id
                        and int((bound_rows.get(name) or {}).get("chunk_count") or 0) > 0
                        for name in CORPUS)
    # Two questions, asked in order, and neither may answer for the other (doc/304 §7
    # item 34): the bind write needs every document idle; a set the engine gave up on can
    # only be written by the repair action, and that permission names the documents it
    # covers. `gate.safe` is unchanged, so a contradiction (upstream `completed` over
    # FAILED documents) still refuses the bind and still refuses S6's dispatch below.
    repair = None
    if not gate.safe:
        repair = wait_state.safe_to_redispatch(gate)
        pc.expect(repair.allowed,
                  "S4 refused before any config write: bind=%s repair=%s"
                  % (gate.describe(), repair.describe()))
        step("S4", "repair re-dispatch permitted: %s" % repair.describe())
        # A repair is a write even when the old chunk_count looks healthy: the products that
        # `already_bound` would trust were written by the run that failed.
        already_bound = False
    if already_bound and not reingest:
        step("S4", "documents already bound to %s and compiled; not re-ingesting" % pipeline_id)
    else:
        for name, doc_id in zip(CORPUS, doc_ids):
            engine.ok("PATCH", "/datasets/%s/documents/%s" % (upstream_id, doc_id), json_body={
                "parse_type": 2,
                "pipeline_id": pipeline_id,
                "pipeline_version_id": version_id,
            })
        step("S4", "bound %d documents to parse_type=2 + canvas %s" % (len(doc_ids), pipeline_id))
        engine.ok("POST", "/documents/ingest",
                  json_body={"doc_ids": doc_ids, "run": "1", "delete": True})
        step("S4", "ingest submitted for %d documents%s"
             % (len(doc_ids), " (repair of a failed set)" if repair is not None else ""))

    listing = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=100" % upstream_id) or {}
    docs = (listing.get("docs") if isinstance(listing, dict) else listing) or []
    bound = {str(row.get("name")): row for row in docs}
    unbound = [name for name in CORPUS if (bound.get(name) or {}).get("pipeline_id") != pipeline_id]
    pc.expect(not unbound, "S4 postcondition failed, these documents are not on the canvas: %s"
              % unbound)

    # ── S5 wait for a terminal document state ─────────────────────────────
    deadline = time.time() + INGEST_TIMEOUT
    final = {}
    while time.time() < deadline:
        listing = engine.ok("GET", "/datasets/%s/documents?page=1&page_size=100" % upstream_id) or {}
        docs = (listing.get("docs") if isinstance(listing, dict) else listing) or []
        final = {str(row.get("name")): row for row in docs}
        readable = [final.get(name) or {} for name in CORPUS]
        if all(document_terminal(doc) for doc in readable):
            break
        print("     waiting %5.0fs  %s" % (
            time.time() - (deadline - INGEST_TIMEOUT),
            " ".join("%s=%s/%s" % (str(d.get("name"))[:6], d.get("progress"),
                                   d.get("ingestion_status")) for d in readable)))
        time.sleep(POLL_SECONDS)
    else:
        raise pc.CaseFail("S5 documents did not reach a terminal state in %.0fs" % INGEST_TIMEOUT)

    for name in CORPUS:
        doc = final.get(name) or {}
        report["documents"].append({
            "name": name, "id": doc.get("id"),
            "progress": doc.get("progress"),
            "ingestion_status": doc.get("ingestion_status"),
            "chunk_count": doc.get("chunk_count"),
            "error": doc.get("progress_msg") or "",
        })
        step("S5", "%s progress=%s status=%s chunks=%s %s"
             % (str(name)[:28], doc.get("progress"), doc.get("ingestion_status"),
                doc.get("chunk_count"), str(doc.get("progress_msg") or "")[:80]))
    failed = [row for row in report["documents"]
              if str(row.get("ingestion_status") or "").upper() != "COMPLETED"]
    pc.expect(not failed, "S5 postcondition failed: %s did not COMPLETE"
              % [row["name"] for row in failed])

    # ── S5b wait for products, not for a status that says "completed" ──────────
    # 2026-11-23 reading: deleting the failed third document made the merge pass treat
    # both surviving documents as changed, so it deleted their 27 products and rewrote
    # 0, and still reported `state: completed`. A dispatch against that state came back
    # `unknown` / `upstream_idle_after_dispatch` — which is the honest answer to "compile
    # finished with nothing to bind", and is why products are a precondition here.
    deadline = time.time() + COMPILE_TIMEOUT
    total = 0
    while time.time() < deadline:
        total = engine_artifact_total(engine, upstream_id)
        status = engine_compilation_status(engine, upstream_id)
        report["upstream_compile"] = {
            "state": status.get("state"),
            "phase": status.get("current_phase"),
            "progress": status.get("progress"),
            "artifact_total": total,
            "last_completed_at": status.get("last_completed_at"),
        }
        step("S5b", "products=%d compile state=%s phase=%s progress=%s"
             % (total, status.get("state"), status.get("current_phase"), status.get("progress")))
        if total > 0 and str(status.get("state")) != "running":
            break
        time.sleep(POLL_SECONDS)
    pc.expect(total > 0,
              "S5b postcondition failed: the engine holds 0 products for this dataset after "
              "%.0fs, so there is nothing for a run to bind and dispatching would only "
              "reproduce upstream_idle_after_dispatch" % COMPILE_TIMEOUT)

    # ── S6 the ragflow-x side: profile + run ──────────────────────────────
    # Same rule before the dispatch, which is the write with the longest shadow: a run
    # started over a non-idle or self-contradicting dataset lands `unknown` and cannot be
    # un-started. Fail closed here, not after the POST.
    pre_dispatch = wait_state.assess_before_write(
        fetch_documents(engine, upstream_id), CORPUS,
        engine_compilation_status(engine, upstream_id))
    step("S6", "pre-dispatch read: %s" % pre_dispatch.describe())
    pc.expect(pre_dispatch.safe, "S6 refused before dispatching: %s" % pre_dispatch.describe())

    profiles = rgx.ok("GET", "/api/v1/knowledge-profiles?page=1&page_size=100") or {}
    profile_rows = profiles.get("items") if isinstance(profiles, dict) else profiles
    profile = None
    for row in profile_rows or []:
        if row.get("dataset_id") == dataset.get("id") and row.get("profile_type") == TEMPLATE_KIND:
            profile = row
            break
    if profile:
        step("S6", "profile already present id=%s status=%s" % (profile.get("id"), profile.get("status")))
    else:
        profile = rgx.ok("POST", "/api/v1/knowledge-profiles", json_body={
            "dataset_id": dataset.get("id"),
            "name": "w4-wiki-pilot",
            "description": "W-4 controlled single-dataset pilot (doc/304 §7 item 23)",
            "profile_type": TEMPLATE_KIND,
            "config": json.dumps({"template_group_id": TEMPLATE_GROUP_ID}),
            "status": "active",
        })
        step("S6", "created profile id=%s status=%s" % (profile.get("id"), profile.get("status")))
    pc.expect(profile.get("status") == "active", "S6 profile is not active: %r" % profile.get("status"))

    idem = "w4-pilot-%d" % int(started)
    run = rgx.ok("POST", "/api/v1/datasets/%s/compilation/runs" % dataset.get("id"), json_body={
        "profile_id": profile.get("id"),
        "idempotency_key": idem,
        "kind": TEMPLATE_KIND,
        "document_ids": doc_ids,
        "config": json.dumps({"template_group_id": TEMPLATE_GROUP_ID}),
    })
    step("S6", "triggered run id=%s state=%s" % (run.get("id"), run.get("state")))

    deadline = time.time() + COMPILE_TIMEOUT
    state = run.get("state")
    while time.time() < deadline:
        runs = rgx.ok("GET", "/api/v1/datasets/%s/compilation/runs?page=1&page_size=20"
                      % dataset.get("id")) or {}
        rows = runs.get("items") if isinstance(runs, dict) else runs
        current = None
        for row in rows or []:
            if row.get("id") == run.get("id"):
                current = row
                break
        if current:
            state = current.get("state")
            run = current
            if state in TERMINAL_RUN_STATES:
                break
        print("     run %s state=%s (%.0fs)" % (run.get("id"), state, time.time() - (deadline - COMPILE_TIMEOUT)))
        time.sleep(POLL_SECONDS)
    report["run"] = {"id": run.get("id"), "state": state,
                     "manifest_complete": run.get("manifest_complete"),
                     "source_consistency": run.get("source_consistency"),
                     "error": run.get("error") or "",
                     "counts": detail_counts(run)}
    counts = report["run"]["counts"]
    step("S6", "run final state=%s manifest_complete=%s artifacts=%s hash_bound=%s source_consistency=%s %s"
         % (state, run.get("manifest_complete"), counts["artifact_count"],
            counts["hash_bound_artifact_count"], run.get("source_consistency"),
            str(run.get("error") or "")[:120]))
    pc.expect(state == "confirmed",
              "S6 postcondition failed: run ended in %r, not confirmed (this is a reading, "
              "not a retry instruction)" % (state,))
    pc.expect(counts["hash_bound_artifact_count"] > 0,
              "S6 postcondition failed: confirmed but hash_bound_artifact_count=0 — 'confirmed' "
              "would then mean something weaker than W-4 A reads it as")

    print()
    print("NEXT: read A/B/C/D with the read-only gate — this tool cannot certify its own writes:")
    # Print the *ragflow-x* dataset id: the read-only gate resolves ids among the
    # console datasets, and printing the upstream id here sent one reader to
    # "--dataset 1f65... matches no dataset on this host" (doc/304 §4 E126).
    print("  python scripts/probes/w4_artifact_landing.py --dataset %s  "
          "# ragflow-x id, not the upstream one (=%s)" % (rgx_id, upstream_id))
    print("Generating the T11 target revision (D) and any treatment arm stay unauthorised here.")
    return 0, report


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="W-4 controlled wiki pilot (write half; the gate is w4_artifact_landing.py)")
    parser.add_argument("--status", action="store_true", help="read-only reading")
    parser.add_argument("--dry-run", action="store_true", help="print the plan")
    parser.add_argument("--apply", action="store_true", help="perform the writes")
    parser.add_argument("--rgx-base", default="")
    parser.add_argument("--engine-base", default="")
    parser.add_argument("--user", default="")
    parser.add_argument("--password", default="")
    parser.add_argument("--engine-key", default="")
    parser.add_argument("--out", default="", help="write the JSON report here too")
    parser.add_argument("--reingest", action="store_true",
                        help="re-submit the parse even if the documents already have chunks "
                             "(a merge pass that deleted products leaves chunks but no products)")
    args = parser.parse_args(argv)

    if not (args.status or args.dry_run or args.apply):
        print_plan()
        print()
        print("one of --status / --dry-run / --apply is required")
        return 2
    if args.dry_run:
        # A plan needs no credential: nothing here has touched the network yet, and a
        # dry run that demanded a password would train the operator to pass one around.
        print_plan()
        return 0

    # Same credential contract as the other probes: a host may have a default, a
    # credential may not, and a missing one is a usage error that names its variable.
    try:
        password = pc.Requirement("--password", "RGX_VERIFY_PASS", "console password",
                                  secret=True).resolve(args)
        engine_key = pc.Requirement("--engine-key", "RGX_ENGINE_KEY",
                                    "RAGFlow engine API key (doc/environment §3.2)",
                                    secret=True).resolve(args)
        args.rgx_base = pc.Requirement("--rgx-base", "RGX_BASE", "ragflow-x console base URL",
                                       default=DEFAULT_RGX_BASE).resolve(args)
        args.engine_base = pc.Requirement("--engine-base", "RGX_ENGINE_BASE",
                                          "RAGFlow engine API base URL",
                                          default=DEFAULT_ENGINE_BASE).resolve(args)
        args.user = pc.Requirement("--user", "RGX_USER", "console account",
                                   default=DEFAULT_USER).resolve(args)
    except pc.Usage as exc:
        print(pc.safe("usage: %s" % exc))
        return 2

    try:
        rgx = pc.Client(args.rgx_base)
        rgx.login(args.user, password)
        engine = Engine(args.engine_base, engine_key)
    except pc.EnvUnavailable as exc:
        print("environment unusable: %s" % exc)
        return 2
    except requests.RequestException as exc:
        print("environment unusable: %s" % exc)
        return 2

    mode = mode_status if args.status else (mode_dry_run if args.dry_run else mode_apply)
    try:
        code, report = mode(engine, rgx, args)
    except pc.CaseFail as exc:
        print("FAIL: %s" % exc)
        return 1
    except pc.EnvUnavailable as exc:
        print("environment unusable: %s" % exc)
        return 2
    except requests.RequestException as exc:
        print("environment unusable: %s" % exc)
        return 2

    if args.out and report:
        with io.open(args.out, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
        print("report: %s" % args.out)
    return code


if __name__ == "__main__":
    sys.exit(main())
