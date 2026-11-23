#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""W-4 — why the upstream Compiler stage failed, read from the engine's own log API.

doc/304 §7 item 23 has been blocked since 2026-11-23 on one sentence: "the Compiler
failed and the engine never said why". That sentence was true of the *document list*
(`document.progress_msg` is empty by design — see `markTerminalProgress` in
`internal/ingestion/service/ingestion_service.go`, which explicitly does not rewrite the
legacy column), and it was false of the engine: the reason was written to
`pipeline_operation_log` + the ingestion event stream, and the engine serves both:

    GET /api/v1/datasets/{ds}/ingestions?log_type=file&document_id={doc}
    GET /api/v1/datasets/{ds}/ingestions/{log_id}/messages
    GET /api/v1/datasets/{ds}/ingestions/summary

This probe reads those three (plus documents, compilation status and artifacts) and
classifies each terminal failure message into a named cause. It exists because the
difference between "we do not know" and "we know, and the owner is not us" is the whole
point of a blocker:

    NAMED          every FAIL log carries a message this file can classify, and the
                   evidence substring is printed so a reader can check the call
    UNKNOWN        FAIL logs exist and no message was classified — stays unknown, exit 3
    NO_FAILURE     nothing failed here, so there is nothing to diagnose — exit 3
    CONTRADICTED   two readings disagree (summary says 2 failed, logs say none did), or a
                   request other than GET reached the transport — exit 1

What this probe is not:

* it does **not** re-ingest, re-trigger a run, delete a document, or change a dataset.
  The transport is `GET`-only and `_get()` is the only function that touches it
  (`assert_read_only` refuses anything else; `--selftest` proves the refusal fires).
* it **does not** advance W-4's four states (A 已生成 / B 已持久化 / C 可被读取 /
  D 落在目标修订上). Reading *why* a compile failed is orthogonal to whether an artifact
  landed: after a successful diagnosis the four states are exactly where they were. The
  gate for those is `w4_artifact_landing.py`, which is not this file.
* it does **not** decide anything. Naming the cause is an input to a decision (whose
  resource, which knob), and that decision is not this script's to take.

Usage (Git Bash on Windows, from the repo root). The file is tracked, so it carries no
credential (doc/304 §7 item 12):

    export RGX_ENGINE_KEY=…                      # engine API key (no default exists)

    python scripts/probes/w4_compiler_evidence.py --offline        # the contract, exit 0
    python scripts/probes/w4_compiler_evidence.py --dataset <id>   # collect + classify
    python scripts/probes/w4_compiler_evidence.py --replay tmp/w4diag/compiler-evidence.json
    python scripts/probes/w4_compiler_evidence.py --selftest       # the verdicts can fail

Exit codes (the family's sentences, doc/305 §4.3), with 0 re-read as the one thing this
probe can actually claim:

    0 = the cause is NAMED (every FAIL log classified; evidence printed)
    1 = two readings contradict each other, or a non-GET request was attempted
    2 = environment unusable / usage (missing credential, host down, bad arguments)
    3 = ran, and the honest answer is "still unknown" or "nothing failed here"
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
import sys
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)

from probe_common import (  # noqa: E402  (path insertion has to come first)
    CaseFail, Client, EnvUnavailable, Requirement, Usage, expect, print_contract,
    safe,
)

# A hostname is configuration; a credential is not (doc/304 §7 item 12).
DEFAULT_ENGINE_BASE = "http://192.168.4.151:9380"
STEP_ORDER = ("S1", "S2", "S3", "S4", "S5", "S6", "S7")
MAX_PAGES = 5
PAGE_SIZE = 100                      # the engine refuses page_size > 100 (code 101)

# The four verdicts. Strings, not booleans, because "unknown" has to survive a `if`.
NAMED = "named"
UNKNOWN = "unknown"
NO_FAILURE = "no_failure"
CONTRADICTED = "contradicted"

# ── the cause table ────────────────────────────────────────────────────────
#
# Ordered, first match wins, and each entry names the exact substring it matched. The
# order matters: an embedding call that dies in the proxy queue matches both
# `proxy queue wait exceeded` and `gpustack embeddings API error`, and the *useful*
# reading is the narrower one (which subsystem was waiting), not the generic one.
#
# Every needle is a string this family of hosts actually printed on 2026-11-23 or a
# near neighbour of one. A needle nobody has seen would classify nothing and would sit
# here looking like coverage.
CAUSES = (
    ("embedding_proxy_queue", "the embedding call queued at the model proxy and timed out",
     ("gpustack embeddings API error",)),
    ("proxy_queue_wait", "the request waited in the model proxy's queue past its limit",
     ("proxy queue wait exceeded",)),
    ("proxy_hard_deadline", "the model proxy's absolute deadline cut the request off",
     ("proxy upstream error: hard deadline",)),
    ("model_not_found", "the configured model/llm id does not resolve on the provider",
     ("model not found", "llm not found", "no such model", "model not exist")),
    ("provider_auth", "the provider rejected the credential",
     ("invalid api key", "unauthorized", "401")),
    ("pipeline_dsl_error", "the pipeline DSL did not compile into a runnable graph",
     ("compile agent", "component factory", "component not found", "NodeRunError")),
    ("parse_error", "the file itself did not parse",
     ("failed to parse", "parser error", "unsupported file type", "mineru")),
    ("upstream_timeout", "an upstream call ran out of time",
     ("context deadline exceeded", "timeout", "timed out")),
)

# Terminal-status spellings the engine uses for a failed run. They are not the same on the
# two log types served by the same endpoint: dataset-level (Wiki) rows say
# `operation_status: "FAIL"` with `status: "1"`, while file-level rows say
# `operation_status: "4"` (the document status code) with `status: "1"`. So failure is read
# from `operation_status` alone, and both spellings are accepted — the first live run of this
# probe read a failed file log as healthy precisely because it only knew the word "FAIL".
# The `status` field is *not* a run status in either shape and is never used to decide.
FAIL_STATUS = ("FAIL", "FAILED", "4")


# ── pure classification ────────────────────────────────────────────────────

def classify_message(message):
    """Map one failure message to (cause, why, evidence). Never guesses.

    Returns `unknown` with an empty evidence when nothing matched: the caller reports
    "still unknown", which is a different sentence from "no cause exists".
    """
    text = (message or "").lower()
    if not text.strip():
        return "unknown", "the message is empty", ""
    for cause, why, needles in CAUSES:
        for needle in needles:
            if needle.lower() in text:
                return cause, why, needle
    return "unknown", "no known cause matched this message", ""


def is_failure(log):
    """A log is a failure when `operation_status` says so, in either spelling it uses."""
    if not isinstance(log, dict):
        return False
    return str(log.get("operation_status") or "").strip().upper() in FAIL_STATUS


def message_of(log):
    """The terminal message, from whichever field this band puts it in."""
    if not isinstance(log, dict):
        return ""
    event = log.get("latest_ingestion_event") or {}
    message = event.get("message") or ""
    return message if isinstance(message, str) else ""


def classify_bundle(bundle):
    """The verdict, from a collected bundle. Pure: `--replay` and `--selftest` use it.

    `bundle` shape (everything `collect()` records):
        {"dataset": str, "requests": [{...}], "documents": [...], "summary": {...},
         "wiki_logs": [...], "file_logs": {doc_id: [...]}, "compilation": {...},
         "artifacts": {...}}
    """
    findings = []
    contradictions = []

    for req in bundle.get("requests") or []:
        method = str((req or {}).get("method") or "").upper()
        if method != "GET":
            contradictions.append("a %s request reached the transport (%s %s)"
                                  % (method or "?", req.get("step"), req.get("path")))

    summary = bundle.get("summary") or {}
    status = summary.get("status") or {}
    fail_count = status.get("fail_count")
    if not isinstance(fail_count, int):
        fail_count = None

    # Every failure reading in the bundle, tagged with where it came from. The two stages
    # are kept apart on purpose: a Wiki compile failure is a *run*, a file-level failure is
    # a *document*, and the dataset summary's `fail_count` counts documents. Comparing one
    # against the other is how a probe manufactures a contradiction it then reports as
    # someone else's bug.
    wiki_fail_logs = [log for log in (bundle.get("wiki_logs") or []) if is_failure(log)]
    file_fail_logs = []
    for doc_id, logs in (bundle.get("file_logs") or {}).items():
        for log in (logs or []):
            if is_failure(log):
                file_fail_logs.append((str(doc_id), log))
    fail_logs = [("wiki", log) for log in wiki_fail_logs]
    fail_logs += [("file:" + doc_id[:8], log) for doc_id, log in file_fail_logs]

    for stage, log in fail_logs:
        message = message_of(log)
        cause, why, evidence = classify_message(message)
        findings.append({
            "stage": stage,
            "log_id": log.get("id"),
            "task_type": log.get("task_type") or "",
            "document_name": log.get("document_name") or "",
            "component": (log.get("latest_ingestion_event") or {}).get("component") or "",
            "duration_s": log.get("process_duration"),
            "message": message,
            "cause": cause,
            "why": why,
            "evidence": evidence,
        })

    # Contradictions are checked *after* the readings exist, so a bundle that is merely
    # empty reports `no_failure` instead of accusing itself of lying. Both rules below are
    # about documents, so both use the file stage only.
    if fail_count and fail_count > 0 and not file_fail_logs:
        contradictions.append(
            "the dataset summary says %d document(s) failed but no failed file-level "
            "ingestion log was returned: the two readings cannot both be true" % fail_count)
    if file_fail_logs and fail_count == 0:
        contradictions.append(
            "file-level ingestion logs report %d failed document(s) but the dataset "
            "summary says fail_count=0" % len(file_fail_logs))
    for doc in (bundle.get("documents") or []):
        status_code = str(doc.get("status") or "")
        if status_code != "4":
            continue
        failed_ids = {log.get("document_id") for _, log in file_fail_logs}
        if str(doc.get("id")) not in {str(x) for x in failed_ids}:
            contradictions.append(
                "document %s is status 4 (FAILED) with no failed file-level log"
                % str(doc.get("id"))[:8])

    causes = sorted({f["cause"] for f in findings})
    if contradictions:
        verdict, reason = CONTRADICTED, contradictions[0]
    elif not fail_logs:
        verdict, reason = NO_FAILURE, "no failed run in this dataset"
    elif causes == ["unknown"]:
        verdict, reason = UNKNOWN, "failed runs exist but no message matched a known cause"
    elif "unknown" in causes:
        verdict, reason = UNKNOWN, ("some failed runs are classified, at least one is not "
                                    "(%d of %d)" % (sum(1 for f in findings
                                                        if f["cause"] == "unknown"),
                                                    len(findings)))
    else:
        verdict, reason = NAMED, ", ".join(causes)

    return {
        "verdict": verdict,
        "reason": reason,
        "causes": causes,
        "findings": findings,
        "contradictions": contradictions,
        "fail_runs": len(findings),
        "chunks": summary.get("chunk_num"),
        "tokens": summary.get("token_num"),
        "docs": summary.get("doc_num"),
    }


def exit_code_for(report):
    verdict = report.get("verdict")
    if verdict == CONTRADICTED:
        return 1
    if verdict == NAMED:
        return 0
    return 3            # UNKNOWN and NO_FAILURE: nothing was named, so nothing is claimed


# ── the transport: GET and nothing else ────────────────────────────────────

READ_ONLY_METHODS = ("GET",)


def assert_read_only(method):
    """The whole write surface of this probe, in one place, refusing by construction."""
    if str(method).upper() not in READ_ONLY_METHODS:
        raise CaseFail("this probe reads; %s is not one of %s"
                       % (str(method).upper(), "/".join(READ_ONLY_METHODS)))


def sha256_of(obj):
    raw = json.dumps(obj, ensure_ascii=False, sort_keys=True).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


class Reader:
    """The only thing that touches the network: a GET, recorded with its payload hash.

    Recording every request (method, path, params, status, payload sha256) is what lets
    `--replay` re-derive the verdict without the host, and what lets a later reader check
    that nothing but a GET ever happened.
    """

    def __init__(self, client, bucket):
        self.client = client
        self.bucket = bucket

    def send(self, method, step, path, **params):
        """The single seam between this probe and a host. Guarded first, recorded second.

        The guard sits *above* `client.api` so a refused method never reaches the socket,
        which is what makes "this probe reads" a property of the code instead of a promise
        in a docstring. `--selftest` exercises this seam with a fake client and asserts the
        fake recorded no call.
        """
        assert_read_only(method)                      # before the socket, not after
        status, body = self.client.api(method, path, params=params or None)
        self.bucket.append({
            "step": step,
            "method": str(method).upper(),
            "path": path,
            "params": params or {},
            "status": status,
            "sha256": sha256_of(body),
        })
        if not (200 <= status < 300):
            raise CaseFail("%s %s -> HTTP %s" % (method, path, status))
        return body

    def get(self, step, path, **params):
        body = self.send("GET", step, path, **params)
        if body.get("code") not in (0, None):
            raise CaseFail("GET %s -> code=%s %s"
                           % (path, body.get("code"), str(body.get("message"))[:200]))
        return body.get("data")


def collect(client, dataset):
    """S1–S6. Every step reads a different question; none of them writes anything."""
    requests = []
    r = Reader(client, requests)
    bundle = {"dataset": dataset, "requests": requests}

    # S1 — the engine band. Not fatal: an older band has no /system/version, and the
    # rest of the readings still mean what they mean.
    try:
        bundle["version"] = r.get("S1", "/api/v1/system/version")
    except CaseFail as exc:
        bundle["version"] = {"_unavailable": str(exc)}

    # S2 — documents, as the document list reports them. `progress_msg` is expected to be
    # empty on failure; recording it is how that expectation is checked, not assumed.
    docs = r.get("S2", "/api/v1/datasets/%s/documents" % dataset,
                 page=1, page_size=PAGE_SIZE) or {}
    bundle["documents"] = docs.get("docs") or []

    # S3 — the dataset's own tally. `fail_count`/`chunk_num` are the engine's statement
    # about the parse stage, independent of any single log row.
    bundle["summary"] = r.get("S3", "/api/v1/datasets/%s/ingestions/summary" % dataset) or {}

    # S4 — the compile stage: dataset-level (Wiki) logs. Paged, because a run history that
    # silently stopped at 100 rows would hide the failure it was collected to find.
    wiki_logs = []
    for page in range(1, MAX_PAGES + 1):
        chunk = r.get("S4", "/api/v1/datasets/%s/ingestions" % dataset,
                      page=page, page_size=PAGE_SIZE) or {}
        rows = chunk.get("logs") or []
        wiki_logs.extend(rows)
        if len(rows) < PAGE_SIZE:
            break
    bundle["wiki_logs"] = wiki_logs

    # S5 — the parse stage: file-level logs, filtered per document, because the default
    # list answers "what did the compiler do" and not "why did this file fail".
    file_logs = {}
    for doc in bundle["documents"]:
        doc_id = str(doc.get("id") or "")
        if not doc_id:
            continue
        chunk = r.get("S5", "/api/v1/datasets/%s/ingestions" % dataset,
                      log_type="file", document_id=doc_id,
                      page=1, page_size=PAGE_SIZE) or {}
        file_logs[doc_id] = chunk.get("logs") or []
    bundle["file_logs"] = file_logs

    # S6 — the downstream question (did anything land). Recorded here so the bundle can
    # answer "the compile failed *and* nothing landed" without a second tool; the four
    # W-4 states themselves are `w4_artifact_landing.py`'s call, not this file's.
    bundle["compilation"] = r.get("S6", "/api/v1/datasets/%s/compilation/status" % dataset) or {}
    bundle["artifacts"] = r.get("S6", "/api/v1/datasets/%s/artifacts" % dataset,
                                page=1, page_size=PAGE_SIZE) or {}
    return bundle


# ── reporting ──────────────────────────────────────────────────────────────

def print_report(bundle, report):
    print("")
    print(safe("documents: %d   failed runs: %d   chunks: %s   tokens: %s"
               % (len(bundle.get("documents") or []), report["fail_runs"],
                  report["chunks"], report["tokens"])))
    for finding in report["findings"]:
        print(safe("  FAIL [%s] %s %s  %.0fs"
                   % (finding["stage"], finding["task_type"],
                      (finding["document_name"] or finding["log_id"] or "")[:40],
                      finding["duration_s"] or 0)))
        print(safe("    cause: %s  (%s)" % (finding["cause"], finding["why"])))
        if finding["evidence"]:
            print(safe("    matched: %r" % finding["evidence"]))
        print(safe("    message: %s" % finding["message"][:400]))
    for contradiction in report["contradictions"]:
        print(safe("  CONTRADICTION: %s" % contradiction))
    print("")
    print(safe("verdict: %s — %s" % (report["verdict"].upper(), report["reason"])))
    print(safe("artifacts reported by the engine: %s"
               % ((bundle.get("artifacts") or {}).get("total"))))
    print("")
    print(safe("this probe reads. It did not re-ingest, re-trigger a run, delete a "
               "document or change a dataset."))
    print(safe("naming a cause does NOT advance W-4's four states (A/B/C/D): an artifact "
               "either landed on a target revision or it did not, and that is "
               "w4_artifact_landing.py's reading."))


def write_bundle(path, bundle):
    directory = os.path.dirname(os.path.abspath(path))
    if directory and not os.path.isdir(directory):
        os.makedirs(directory)
    with io.open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(json.dumps(bundle, ensure_ascii=False, indent=1, sort_keys=True))
        handle.write("\n")


REQUIREMENTS = [
    Requirement("--base", "RGX_ENGINE_BASE",
                "engine base URL (default %s)" % DEFAULT_ENGINE_BASE,
                default=DEFAULT_ENGINE_BASE),
    Requirement("--api-key", "RGX_ENGINE_KEY", "engine API key (Bearer)", secret=True),
]

STEPS = [
    ("S1", "GET /system/version — the engine band (not fatal when absent)"),
    ("S2", "GET /datasets/:id/documents — per-document status and the (legacy) progress_msg"),
    ("S3", "GET /datasets/:id/ingestions/summary — the engine's own fail/chunk tally"),
    ("S4", "GET /datasets/:id/ingestions — compile-stage (Wiki) run history, paged"),
    ("S5", "GET /datasets/:id/ingestions?log_type=file&document_id= — parse-stage history"),
    ("S6", "GET compilation/status + /artifacts — did anything land downstream"),
    ("S7", "classify every FAIL message, or say 'still unknown' out loud"),
]


# ── the selftest: can this probe be wrong? ─────────────────────────────────

def _log(message, status="FAIL", duration=1.0, task_type="Wiki", doc_id="d1",
         document_name="doc.md", log_id="lg1"):
    return {
        "id": log_id,
        "document_id": doc_id,
        "document_name": document_name,
        "task_type": task_type,
        "operation_status": status,
        "status": "4" if status.upper() in FAIL_STATUS else "3",
        "process_duration": duration,
        "latest_ingestion_event": {"id": 1, "message": message, "component": "",
                                   "phase": 0, "event_type": 2},
    }


def _doc(doc_id="d1", status="4"):
    return {"id": doc_id, "name": doc_id + ".md", "status": status,
            "chunk_count": 0, "progress": 0.72, "progress_msg": ""}


def selftest():
    """Cases that would each be a distinct operational lie if the probe said otherwise.

    Every case asserts an exact verdict *and* an exact exit code, so a mutation that makes
    the classifier permissive (or the contradiction check silent) turns this red.
    """
    cases = []

    def case(name, expected_verdict, expected_exit, bundle):
        cases.append((name, expected_verdict, expected_exit, bundle))

    # 1. the real shape from 2026-11-23: a 504 in the proxy queue, at parse time.
    case("proxy queue wait (504) at parse time", NAMED, 0, {
        "requests": [{"method": "GET", "step": "S5", "path": "/x"}],
        "documents": [_doc()],
        "summary": {"status": {"fail_count": 1}, "chunk_num": 0, "token_num": 0, "doc_num": 1},
        "wiki_logs": [],
        "file_logs": {"d1": [_log('Task failed: ... component "Compiler:w4pilot" invoke: '
                                  'Compiler: failed after 5 retries: API request failed '
                                  'with status 504: {"error":"proxy queue wait exceeded"}')]},
    })

    # 2. the embedding variant: the narrower needle must win, because the useful reading
    #    is "which subsystem was waiting", not the generic proxy sentence.
    case("embedding queued at the proxy", NAMED, 0, {
        "requests": [{"method": "GET", "step": "S5", "path": "/x"}],
        "documents": [_doc()],
        "summary": {"status": {"fail_count": 1}},
        "wiki_logs": [],
        "file_logs": {"d1": [_log("Compiler: knowledge_compiler: embed: gpustack embeddings "
                                  'API error: 504 Gateway Timeout, body: {"error":"proxy queue '
                                  'wait exceeded"}')]},
    })

    # 3. the compile-stage 502: same owner, different sentence.
    case("proxy hard deadline (502) at compile time", NAMED, 0, {
        "requests": [{"method": "GET", "step": "S4", "path": "/x"}],
        "documents": [_doc("d1", status="1")],
        "summary": {"status": {"fail_count": 0}},
        "wiki_logs": [_log('rewrite Wiki page "entity/org/x": failed after 5 retries: API '
                           'request failed with status 502: {"error":"proxy upstream error: '
                           'hard deadline (absolute)"}')],
        "file_logs": {},
    })

    # 4. a message nobody has seen yet. If the classifier answered `named` here, the
    #    probe would be inventing certainty — the exact failure mode doc/304 §7 item 23
    #    spent two days in.
    case("an unclassified message stays unknown", UNKNOWN, 3, {
        "requests": [{"method": "GET", "step": "S4", "path": "/x"}],
        "documents": [_doc("d1", status="1")],
        "summary": {"status": {"fail_count": 0}},
        "wiki_logs": [_log("something nobody has seen before")],
        "file_logs": {},
    })

    # 5. nothing failed: there is nothing to diagnose, and that is not a success.
    case("no failure at all", NO_FAILURE, 3, {
        "requests": [{"method": "GET", "step": "S4", "path": "/x"}],
        "documents": [_doc("d1", status="3")],
        "summary": {"status": {"fail_count": 0, "done_count": 1}},
        "wiki_logs": [_log("Task completed.", status="DONE")],
        "file_logs": {"d1": [_log("Task completed.", status="DONE", doc_id="d1")]},
    })

    # 6. the tally and the logs disagree. Someone must look; a probe that picks one of
    #    them is choosing which lie to believe.
    case("summary says failed, logs say none", CONTRADICTED, 1, {
        "requests": [{"method": "GET", "step": "S3", "path": "/x"}],
        "documents": [_doc("d1", status="3")],
        "summary": {"status": {"fail_count": 2}},
        "wiki_logs": [],
        "file_logs": {"d1": []},
    })

    # 7. a write reached the transport. Impossible by construction (there is one `_get`),
    #    and still asserted: "impossible by construction" is the sentence every writer
    #    says right before it writes.
    case("a non-GET request is refused", CONTRADICTED, 1, {
        "requests": [{"method": "POST", "step": "S5", "path": "/x"}],
        "documents": [_doc()],
        "summary": {"status": {"fail_count": 1}},
        "wiki_logs": [],
        "file_logs": {"d1": [_log('API request failed with status 504: {"error":"proxy queue '
                                  'wait exceeded"}')]},
    })

    # 8. a document is FAILED but its file log is missing entirely: the reading is
    #    incomplete, not clean.
    case("FAILED document with no file log", CONTRADICTED, 1, {
        "requests": [{"method": "GET", "step": "S2", "path": "/x"}],
        "documents": [_doc("d1", status="4")],
        "summary": {"status": {"fail_count": 1}},
        "wiki_logs": [_log('rewrite failed: API request failed with status 502: {"error":'
                           '"proxy upstream error: hard deadline (absolute)"}')],
        "file_logs": {},
    })

    # 9. the numeric spelling, which the first live run of this probe read as healthy: a
    #    file-level row says `operation_status: "4"` and `status: "1"`. A probe that knows
    #    only the word "FAIL" reports a failed dataset as having no failure at all.
    case("file log spells failure as 4", NAMED, 0, {
        "requests": [{"method": "GET", "step": "S5", "path": "/x"}],
        "documents": [_doc()],
        "summary": {"status": {"fail_count": 1}, "chunk_num": 0},
        "wiki_logs": [],
        "file_logs": {"d1": [dict(_log("Compiler: knowledge_compiler: embed: gpustack "
                                        'embeddings API error: 504 Gateway Timeout, body: '
                                        '{"error":"proxy queue wait exceeded"}',
                                        status="DONE"),
                                operation_status="4", status="1")]},
    })

    # 10. the transport guard itself, at the seam it guards: `send("POST", …)` must raise
    #     *and* the client beneath it must never be called. Asserting only the exception
    #     would pass on a guard that raises after the socket.
    class _FakeClient:
        def __init__(self):
            self.calls = []

        def api(self, method, path, **kw):          # noqa: ARG002 - the shape is the point
            self.calls.append(method)
            return 200, {"code": 0, "data": {}}

    def guard_case():
        fake = _FakeClient()
        reader = Reader(fake, [])
        refused = False
        try:
            reader.send("POST", "S9", "/x")
        except CaseFail:
            refused = True
        if not refused:
            return False, "a POST was not refused"
        if fake.calls:
            return False, "the client was called (%s) before the refusal" % fake.calls
        if reader.bucket:
            return False, "a refused request was recorded as a reading"
        return True, ""

    failed = 0
    for name, expected_verdict, expected_exit, bundle in cases:
        report = classify_bundle(bundle)
        got_exit = exit_code_for(report)
        if report["verdict"] != expected_verdict or got_exit != expected_exit:
            failed += 1
            print(safe("FAIL  %s -> %s/%d, want %s/%d"
                       % (name, report["verdict"], got_exit, expected_verdict, expected_exit)))
        else:
            print(safe("ok    %s -> %s/%d" % (name, report["verdict"], got_exit)))
    if not guard_case()[0]:
        failed += 1
        print(safe("FAIL  send('POST') -> %s" % guard_case()[1]))
    else:
        print(safe("ok    send('POST') is refused before the socket, and nothing is recorded"))

    print("")
    print(safe("%d case(s), %d failed" % (len(cases) + 1, failed)))
    if failed:
        print(safe("SELFTEST FAILED"))
        return 1
    print(safe("SELFTEST OK: named / unknown / no-failure / contradicted are four "
               "different answers, and a write is refused"))
    return 0


# ── main ───────────────────────────────────────────────────────────────────

def build_parser():
    parser = argparse.ArgumentParser(add_help=True, description=__doc__.splitlines()[0])
    parser.add_argument("--base", default="", help="engine base URL")
    parser.add_argument("--api-key", default="", help="engine API key (or RGX_ENGINE_KEY)")
    parser.add_argument("--dataset", default="", help="engine dataset id")
    parser.add_argument("--out", default="", help="where to write the evidence bundle")
    parser.add_argument("--offline", action="store_true", help="print the contract, exit 0")
    parser.add_argument("--selftest", action="store_true", help="prove the verdicts can fail")
    parser.add_argument("--replay", default="", help="re-classify a saved bundle, no network")
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)

    if args.selftest:
        return selftest()

    if args.offline:
        return print_contract(
            "w4_compiler_evidence.py — why the Compiler stage failed (doc/304 §7 item 23)",
            REQUIREMENTS, STEPS,
            notes=("read-only: the transport is GET-only and nothing here writes",
                   "exit 0 = the cause is NAMED; 3 = still unknown or nothing failed; "
                   "1 = two readings contradict",
                   "naming a cause does not advance W-4's A/B/C/D"))

    if args.replay:
        try:
            with io.open(args.replay, encoding="utf-8") as handle:
                bundle = json.load(handle)
        except (IOError, OSError) as exc:
            print(safe("cannot read %s: %s" % (args.replay, exc)))
            return 2
        except ValueError as exc:
            print(safe("%s is not JSON: %s" % (args.replay, exc)))
            return 2
        report = classify_bundle(bundle)
        print_report(bundle, report)
        return exit_code_for(report)

    try:
        base = REQUIREMENTS[0].resolve(args)
        api_key = REQUIREMENTS[1].resolve(args)
        dataset = (args.dataset or "").strip()
        if not dataset:
            raise Usage("--dataset is required: which dataset's compile history should be read?")
    except Usage as exc:
        print(safe(str(exc)))
        return 2

    client = Client(base)
    try:
        client.login_with_key(api_key)
    except EnvUnavailable as exc:
        print(safe("environment unavailable: %s" % exc))
        return 2

    print(safe("w4_compiler_evidence — reading %s dataset %s" % (base, dataset)))
    print(safe("read-only; it will not re-ingest, re-trigger, delete or change anything."))
    try:
        bundle = collect(client, dataset)
    except CaseFail as exc:
        print(safe("FAIL  %s" % exc))
        return 2
    except Exception:                                         # noqa: BLE001
        print(safe("environment unavailable while reading:"))
        traceback.print_exc()
        return 2

    report = classify_bundle(bundle)
    print_report(bundle, report)

    out = args.out or os.path.join(REPO, "tmp", "w4diag", "compiler-evidence.json")
    write_bundle(out, bundle)
    print(safe("evidence bundle: %s (%d request(s), all GET)"
               % (out, len(bundle.get("requests") or []))))
    return exit_code_for(report)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Usage as exc:
        print(safe(str(exc)))
        sys.exit(2)
    except EnvUnavailable as exc:
        print(safe("environment unavailable: %s" % exc))
        sys.exit(2)
