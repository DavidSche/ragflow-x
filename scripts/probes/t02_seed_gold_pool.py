#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""T02 data half — seed the *source* a gold set needs (301 §9.1, T04b).

`cmd/goldtool pool` exports candidates from real published revisions, and §9.1 needs
three templates and a non-empty `rgx_report_conflict_candidate`. On 151 both were
missing: only `report-compliance` had published revisions, and the candidate table
was empty everywhere because `retrieval.scan_top_k` defaults to 0 (304 E63).

This script makes the two conditions true instead of wishing them true:

1. widen the candidate scan (`report.candidate_scan_top_k`, clamped at 20) *before*
   the run — candidates are frozen at generation time and never backfilled, so a
   setting applied afterwards would only produce a nicer-looking empty table;
2. generate one report per missing template against a dataset that already has
   chunks, confirm the outline, wait for the terminal job;
3. restore the setting and print what the pool export should now say.

What it deliberately does NOT do: create the gold file. A pool is not a gold set
(no labels), and a script that "produced a gold set" by writing verdicts itself would
be the exact failure mode the gold tool exists to prevent.

Usage (Git Bash on Windows, from the repo root). This file is tracked, so it carries no
credential: the console password comes from the environment (doc/304 §7 item 12):

    export RGX_VERIFY_PASS=…                        # console password (no default exists)

    python scripts/probes/t02_seed_gold_pool.py --offline        # print the contract, exit 0
    python scripts/probes/t02_seed_gold_pool.py --dry-run        # read the host, change nothing
    python scripts/probes/t02_seed_gold_pool.py                  # K1–K5 for real
    python scripts/probes/t02_seed_gold_pool.py --templates report-postmortem

Exit codes follow the drill's contract: 0 = every step PASS, 1 = at least one FAIL,
2 = environment unusable or a required value is missing, 3 = ran without failures but
skipped a step.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import traceback
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

# Imported from `probe_common`, not from the drill: a data-seeding probe that depends on
# the T16 drill module could not run when the drill changed, and vice versa (doc/304 §7
# item 12 — the two used to be one file's worth of coupling in `localenv/`).
from probe_common import (  # noqa: E402  (path insertion has to come first)
    CaseFail, Client, EnvUnavailable, REPO, Requirement, Usage, expect, print_contract,
    safe, wait_job,
)

DEFAULT_BASE = "http://192.168.4.151:19193"   # a drill host is configuration, not a secret
DEFAULT_USER = "admin"
DEFAULT_TEMPLATES = ("report-postmortem", "report-tech-review")
QUESTION = "山东省属企业安全生产风险点防控的责任分工与主要措施是什么？"
SETTING_KEY = "report.candidate_scan_top_k"
STEP_ORDER = ("K1", "K2", "K3", "K4", "K5")


class Shim:
    """The two methods `wait_job` wants from a drill."""

    def __init__(self, outdir):
        self.outdir = outdir

    def dump(self, name, payload):
        path = os.path.join(self.outdir, "K-%s.json" % name)
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)

    def note(self, text):
        print("  - " + safe(text))


def setting_number(view, group, field):
    groups = view.get("groups") or {}
    node = (groups.get(group) or {}).get(field)
    if isinstance(node, dict):
        return node.get("effective")
    return node


def pick_dataset(admin):
    """The dataset with the most documents, so retrieval has something to contrast.

    Ranked by `document_count`, not `chunk_count`: chunks live in the engine, and the
    control plane's list only knows how many documents were bound to the dataset.
    Assuming a `chunk_count` field would have silently ranked everything 0 and then
    reported "no dataset has chunks" — a wrong diagnosis of a working host.
    """
    data = admin.ok("GET", "/api/v1/datasets?page=1&page_size=200")
    rows = data.get("datasets") if isinstance(data, dict) else data
    rows = rows or []
    ranked = sorted(rows, key=lambda r: int(r.get("document_count") or 0), reverse=True)
    for row in ranked:
        if int(row.get("document_count") or 0) > 0:
            return row
    raise CaseFail("no dataset on this host has documents; a report would be generated "
                   "from nothing and its candidates would be empty by construction")


def patch_scan_top_k(admin, want, note):
    """Widen (or restore) the candidate scan. Returns the value actually in force."""
    view = admin.ok("GET", "/api/v1/system/settings")
    current = setting_number(view, "report", "candidate_scan_top_k")
    if str(current) == str(want):
        return current
    for attempt in (1, 2):
        status, body = admin.api("PATCH", "/api/v1/system/settings", json_body={
            "expected_revision": view.get("revision"),
            "changes": {SETTING_KEY: want},
            "note": note,
            "confirmed": True,
        })
        if status in (200, 202):
            after = admin.ok("GET", "/api/v1/system/settings")
            return setting_number(after, "report", "candidate_scan_top_k")
        if status == 409 and body.get("code") == 40901 and attempt == 1:
            view = admin.ok("GET", "/api/v1/system/settings")
            continue
        raise CaseFail("PATCH %s=%s -> %s code=%s %s" %
                       (SETTING_KEY, want, status, body.get("code"),
                        str(body.get("message"))[:200]))
    raise CaseFail("settings kept moving under us")


def generate(admin, shim, template_key, dataset_id, stamp, timeout):
    """Create one report from `template_key` and wait for the revision to publish."""
    payload = {
        "title": "T02 gold seed %s %s" % (template_key, stamp),
        "question": QUESTION,
        "template_key": template_key,
        "template_version": 0,
        "datasets": [{"role": "primary", "dataset_id": dataset_id}],
        "outline_confirmation": "required",
    }
    status, body = admin.api("POST", "/api/v1/reports", json_body=payload,
                             headers={"Idempotency-Key": str(uuid.uuid4())})
    expect(status == 202, "create report(%s) -> %s %s" % (template_key, status,
                                                          str(body)[:300]))
    data = body.get("data") or {}
    report_id = (data.get("report") or {}).get("id")
    revision = (data.get("revision") or {}).get("revision") or 1
    job_id = (data.get("job") or {}).get("id")
    expect(report_id and job_id, "create returned no ids: %s" % data)

    snapshot = wait_job(admin, shim, job_id, "awaiting_outline", timeout)
    planned = snapshot.get("sections") or []
    expect(planned, "%s: the outline job exposes no sections" % template_key)
    first = planned[0]
    # Confirmation is "titles only, and only the ones you changed" (`report_outline.go`
    # reads this list as *edits*), so echoing one unchanged title confirms the whole
    # outline without making this script's text part of the revision.
    admin.ok("POST",
             "/api/v1/reports/%s/revisions/%s/outline/confirm" % (report_id, revision),
             json_body={"sections": [{"section_key": first.get("section_key")
                                      or first.get("key"),
                                      "title": first.get("title")}]})
    job = wait_job(admin, shim, job_id, "terminal", timeout)
    detail = admin.ok("GET", "/api/v1/reports/%s/revisions/%s" % (report_id, revision))
    listing = admin.ok(
        "GET", "/api/v1/reports/%s/revisions/%s/sections" % (report_id, revision))
    sections = (listing.get("sections") if isinstance(listing, dict) else listing) or []
    body_chars = sum(len(s.get("content_md") or "") for s in sections)
    rev = detail.get("revision") or detail
    metrics = detail.get("metrics") or {}
    citations = int(metrics.get("citations") or metrics.get("evidence_sections") or 0)
    expect(job.get("status") in ("succeeded", "partial"),
           "%s job ended %s error=%s" % (template_key, job.get("status"),
                                         job.get("error_code")))
    expect(sections, "%s: revision has 0 sections" % template_key)
    expect(body_chars > 0, "%s: %d sections but 0 characters" % (template_key, len(sections)))
    expect(str(rev.get("status")) == "published",
           "%s revision status=%s (a pool only reads published revisions)" %
           (template_key, rev.get("status")))
    return {
        "template_key": template_key, "report_id": report_id, "revision": revision,
        "revision_id": rev.get("id"), "status": rev.get("status"),
        "sections": len(sections), "body_chars": body_chars, "citations": citations,
        "metrics": metrics, "job_status": job.get("status"),
    }


def run_steps(admin, args, stamp, outdir):
    shim = Shim(outdir)
    state = {}
    results = []

    def record(step, ok, detail, warned=False):
        results.append({"step": step, "status": "PASS" if ok else "FAIL",
                        "detail": detail, "warn": warned})

    # K1 — environment: login worked, and there is a corpus to generate from.
    if args.dry_run:
        dataset = {"id": "<dry-run>", "name": "<dry-run>", "chunk_count": 0}
    else:
        dataset = pick_dataset(admin)
        state["dataset"] = dataset
    record("K1", True, "dataset=%s docs=%s name=%s" %
           (dataset.get("id"), dataset.get("document_count"), dataset.get("name")))

    # K2 — the pre-condition that makes E63 possible to fix: read the knob first.
    view = admin.ok("GET", "/api/v1/system/settings")
    before = setting_number(view, "report", "candidate_scan_top_k")
    record("K2", True, "%s before=%s (platform default is 0 = freeze nothing)" %
           (SETTING_KEY, before))

    # K3 — widen it. This is the step that has to precede generation, not follow it.
    if args.dry_run:
        record("K3", True, "would set %s=%s" % (SETTING_KEY, args.scan_top_k), warned=True)
    else:
        after = patch_scan_top_k(admin, args.scan_top_k,
                                 "T02 gold seed %s: widen the candidate scan so a "
                                 "revision freezes its uncited pool" % stamp)
        expect(str(after) == str(args.scan_top_k),
               "setting did not take: wanted %s, effective %s" %
               (args.scan_top_k, after))
        state["scan_top_k_before"] = before
        record("K3", True, "%s=%s (clamped at 20)" % (SETTING_KEY, after))

    # K4 — one report per template that has no published revision yet.
    published = []
    if args.dry_run:
        record("K4", True, "would generate: %s" % ", ".join(args.templates), warned=True)
    else:
        failures = []
        for key in args.templates:
            try:
                row = generate(admin, shim, key, dataset["id"], stamp, args.report_timeout)
                published.append(row)
                shim.note("%s: rev=%s sections=%s chars=%s citations=%s" %
                          (key, row["revision"], row["sections"], row["body_chars"],
                           row["citations"]))
            except CaseFail as exc:
                failures.append("%s: %s" % (key, exc))
        record("K4", not failures,
               "; ".join(["%s rev=%s sections=%s" % (r["template_key"], r["revision"],
                                                     r["sections"]) for r in published] +
                         failures))

    # K5 — put the knob back. A setting left wide is a corpus copy left behind.
    if args.dry_run or "scan_top_k_before" not in state:
        record("K5", True, "nothing to restore", warned=True)
    else:
        restored = patch_scan_top_k(admin, state["scan_top_k_before"],
                                    "T02 gold seed %s: restore the candidate scan" % stamp)
        expect(str(restored) == str(state["scan_top_k_before"]),
               "restore failed: wanted %s, effective %s" %
               (state["scan_top_k_before"], restored))
        record("K5", True, "%s restored to %s" % (SETTING_KEY, restored))

    return results, published


REQUIREMENTS = (
    Requirement("--base", "RGX_BASE", "console base URL", default=DEFAULT_BASE),
    Requirement("--user", "RGX_USER", "console account", default=DEFAULT_USER),
    Requirement("--password", "RGX_VERIFY_PASS", "console password", secret=True),
)

STEPS_DOC = (
    ("K1", "pick the dataset with the most documents (nothing to retrieve otherwise)"),
    ("K2", "read report.candidate_scan_top_k before touching it (frozen at generation)"),
    ("K3", "widen it to --scan-top-k before any run; candidates are never backfilled"),
    ("K4", "generate one report per missing template, confirm the outline, wait to publish"),
    ("K5", "restore the setting: a knob left wide is a corpus copy left behind"),
)


def parse_args(argv):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default=os.environ.get("RGX_BASE", DEFAULT_BASE))
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=os.environ.get("RGX_VERIFY_PASS", ""),
                    help="console password (or RGX_VERIFY_PASS); no default is stored")
    ap.add_argument("--offline", action="store_true",
                    help="print the contract (what is required, which steps run) and exit 0")
    ap.add_argument("--templates", default=",".join(DEFAULT_TEMPLATES),
                    help="comma separated template keys to publish a revision under")
    ap.add_argument("--scan-top-k", type=int, default=8,
                    help="candidate scan width to apply before generating (default 8)")
    ap.add_argument("--report-timeout", type=float, default=900.0)
    ap.add_argument("--out", default="")
    ap.add_argument("--dry-run", action="store_true",
                    help="read the host and print the plan; change and generate nothing")
    return ap.parse_args(argv)


def main(argv):
    args = parse_args(argv)
    args.templates = [t.strip() for t in args.templates.split(",") if t.strip()]
    if args.offline:
        return print_contract("T02 gold seed (doc/304 §5.3, 301 §9.1)", REQUIREMENTS,
                              STEPS_DOC,
                              notes=("--offline never logs in and never writes evidence.",
                                     "this script does not create a gold file: a pool is "
                                     "not a gold set.",
                                     "exit 1 on a failed step, 3 when a step was skipped."))
    try:
        args.password = Requirement("--password", "RGX_VERIFY_PASS", "console password",
                                    secret=True).resolve(args)
    except Usage as exc:
        print(safe("usage: %s" % exc))
        return 2
    stamp = time.strftime("%y%m%d-%H%M%S")
    outdir = args.out or os.path.join(
        REPO, "tmp", "t02-seed-%s" % time.strftime("%Y%m%d-%H%M%S"))
    os.makedirs(outdir, exist_ok=True)

    print(safe("T02 gold seed -> %s (%s)" % (args.base, outdir)))
    admin = Client(args.base)
    try:
        admin.login(args.user, args.password)
    except EnvUnavailable as exc:
        print(safe("ENV UNAVAILABLE: %s" % exc))
        return 2

    code = 0
    published = []
    try:
        results, published = run_steps(admin, args, stamp, outdir)
    except CaseFail as exc:
        results = [{"step": s, "status": "FAIL", "detail": str(exc), "warn": False}
                   for s in ("K?" + str(exc)[:80],)]
    except Exception:  # noqa: BLE001 - a probe must print, not swallow
        traceback.print_exc()
        return 2

    print("")
    for row in results:
        mark = row["status"] + (" (warn)" if row.get("warn") else "")
        print(safe("  [%s] %s  %s" % (mark, row["step"], row["detail"])))
    failed = [r for r in results if r["status"] == "FAIL"]
    skipped = [r for r in results if r.get("warn")]
    if failed:
        code = 1
    elif skipped:
        code = 3

    summary = {"base": args.base, "stamp": stamp, "dry_run": args.dry_run,
               "templates": args.templates, "scan_top_k": args.scan_top_k,
               "published": published, "results": results, "exit": code}
    path = os.path.join(outdir, "summary.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(summary, fh, ensure_ascii=False, indent=2)
    print(safe("\nsummary: %s" % path))
    if published:
        print(safe("next: RGX_GOLDTOOL_DSN=… ./tmp/goldtool.exe pool --out "
                   "tmp/gold/pool-v1.jsonl   # expect templates>=3 and candidate_rows>0"))
    print(safe("exit=%d" % code))
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
