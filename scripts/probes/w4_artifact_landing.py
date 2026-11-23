#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""W-4 — did the compiled artifacts *land*, and can the reader actually see them?

T11's KC-2 experiment needs one thing the plan used to assume away: the dataset a
report is generated over must already carry compiled artifacts, because the only
navigator the treatment arm has is `rgx_report_section.location_hints_json`, and that
column is filled from ready `rgx_artifact_projection` rows (doc/295 R17, A54/A55).

On 151 the projection table is empty (`doc/304` §7 item 23), so both KC-2 arms retrieve
identically and `goldtool kc2 eval` answers `unverified / treatment_without_hints` —
which is the correct reading, not a bug to paper over.

This probe turns "0 rows" from a sentence into a measurement. It exists because the
three things W-4 asks for are *different states*, and collapsing them is how a plan
marks itself green:

    A 已生成     a compilation run reached `confirmed` and the engine reported artifacts
    B 已持久化   rows exist in `rgx_artifact_projection`, `state=ready`, and each row is
                 internally consistent (hash shape, hash inside the JSON == the column,
                 `run_binding` / `content_hash_binding` true, run id known)
    C 可被读取   the *reader* side can see them: `report.locator_hints_enabled` is
                 effective, and the dataset-scoped ready rows are what the locator's
                 own query (`state = ready` by dataset) would return
    D 落在目标修订上  a named revision's sections actually carry `location_hints`

A is the writer's own report; B re-reads the bytes through a second transport; C
re-derives the reader's predicate; D is the only one that means "T11 can use it".

What this probe deliberately does NOT claim:

* it does **not** recompute `artifact_hash` or `content_hash`. Both are
  `sha256` over Go's `json.Marshal` output, and re-implementing that canonical form in
  Python would produce a *look-alike* check — green when it agrees with a bug. The
  hash is verified where the encoder is: the Go tests around
  `artifactProjectionFromRun` / `hashStableJSON`. Here we only assert the row and the
  JSON it carries agree with each other, which is a different (and honest) claim.
* it does **not** prove "the artifact equals the upstream payload". `content_hash`'s
  input (`content_md_rendered`) is not stored in the row, so that comparison needs the
  engine, not this host.

A consequence worth reading twice, because it reorders the plan: hints are frozen in
the **outline** step (`attachReportLocationHints` runs once, next to the evidence
preview). Artifacts that land *after* a revision was generated can never retro-fill
it — so W-4 has to complete before T11's treatment generation, not alongside it.

Usage (Git Bash on Windows, from the repo root). This file is tracked, so it carries no
credential: the console password comes from the environment (doc/304 §7 item 12):

    export RGX_VERIFY_PASS=…                        # console password (no default exists)

    python scripts/probes/w4_artifact_landing.py --offline         # the contract, exit 0
    python scripts/probes/w4_artifact_landing.py --dataset <id|name>
    python scripts/probes/w4_artifact_landing.py --dataset <id> --report <id> --revision 1
    python scripts/probes/w4_artifact_landing.py --selftest        # the verdicts can fail

Exit codes follow the same contract as the other probes: 0 = W-4 成立 (all four states),
1 = a reading contradicts another reading, 2 = environment unusable / usage,
3 = ran, and the honest answer is "not yet" (a state is not established).
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from probe_common import (  # noqa: E402  (path insertion has to come first)
    CaseFail, Client, EnvUnavailable, REPO, Requirement, Usage, expect, print_contract,
    safe,
)

DEFAULT_BASE = "http://192.168.4.151:19193"   # a host is configuration, not a secret
DEFAULT_USER = "admin"
SETTING_GROUP, SETTING_FIELD = "report", "locator_hints_enabled"
HASH_SHAPE = re.compile(r"^sha256:[0-9a-f]{64}$")
MAX_PAGES = 5
STEP_ORDER = ("S1", "S2", "S3", "S4", "S5")

# The four states, in the only order in which one implies the next.
STATES = (
    ("A", "已生成", "a run reached confirmed and the engine reported artifacts"),
    ("B", "已持久化", "ready rows exist and each row agrees with the JSON it carries"),
    ("C", "可被读取", "the locator switch is effective and the dataset has ready rows"),
    ("D", "落在目标修订上", "the named revision's sections carry location_hints"),
)


# ── the verdicts, as pure functions ────────────────────────────────────────
#
# Pure so `--selftest` can flip one field and watch the verdict change. A check that can
# only be exercised against a live host is a check nobody proves can fail.

NOT_YET = "not_yet"        # the external condition is absent: exit 3, never 0
ESTABLISHED = "established"
CONTRADICTED = "contradicted"   # two readings disagree: exit 1, someone must look


def verdict(state, reason, detail=""):
    return {"state": state, "reason": reason, "detail": detail}


def detail_json(run):
    raw = run.get("detail")
    if isinstance(raw, dict):
        return raw
    if not isinstance(raw, str) or not raw.strip():
        return {}
    try:
        parsed = json.loads(raw)
    except ValueError:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def number(node, key):
    value = node.get(key)
    if isinstance(value, bool):
        return 0
    if isinstance(value, (int, float)):
        return value
    try:
        return float(str(value))
    except (TypeError, ValueError):
        return 0


def verdict_a(runs, dataset_id):
    """A: did a compilation run legitimately report artifacts for this dataset?

    `confirmed` is the run state the dispatcher only writes when every projected row is
    run-bound *and* content-bound (`storeCompilationAudit(...artifact_bound)`), so a run
    that says `confirmed` with `hash_bound_artifact_count > 0` is the writer's strongest
    claim. It is still only a claim — B re-reads it.
    """
    if not runs:
        return verdict(NOT_YET, "no_run",
                       "the dataset has no compilation run at all: nothing was ever asked "
                       "of the engine, so 'no artifacts' is not evidence about the engine")
    wrong_dataset = [r for r in runs if str(r.get("dataset_id") or "") not in ("", dataset_id)]
    if wrong_dataset:
        return verdict(CONTRADICTED, "run_for_another_dataset",
                       "%d run(s) returned by the dataset-scoped list carry another "
                       "dataset_id" % len(wrong_dataset))
    confirmed = [r for r in runs if str(r.get("state")) == "confirmed"]
    if not confirmed:
        return verdict(NOT_YET, "no_confirmed_run",
                       "states seen: " + ", ".join(sorted({str(r.get("state")) for r in runs})))
    best = None
    for run in confirmed:
        detail = detail_json(run)
        if number(detail, "artifact_count") > 0 and number(detail, "hash_bound_artifact_count") > 0:
            best = (run, detail)
            break
    if best is None:
        return verdict(CONTRADICTED, "confirmed_without_artifacts",
                       "a run is confirmed but its detail reports artifact_count=0 or "
                       "hash_bound_artifact_count=0: 'confirmed' would then mean something "
                       "weaker than the plan reads it as")
    run, detail = best
    return verdict(ESTABLISHED, "confirmed_run_with_hash_bound_artifacts",
                   "run=%s artifacts=%s ready=%s bound=%s hash_bound=%s" % (
                       run.get("id"), number(detail, "artifact_count"),
                       number(detail, "ready_artifact_count"),
                       number(detail, "bound_artifact_count"),
                       number(detail, "hash_bound_artifact_count")))


def verdict_b(rows, known_run_ids):
    """B: are the rows persisted, ready, and self-consistent?

    "Persisted" is checked against the row itself, not against the write response: shape
    of both hashes, the copy of `artifact_hash` inside `artifact_json` matching the
    column, the two binding flags, and a run id that the run list already knows.
    """
    if not rows:
        return verdict(NOT_YET, "no_projection_row",
                       "rgx_artifact_projection has no row for this dataset: the artifact "
                       "may exist upstream, but nothing was persisted here")
    problems = []
    ready = 0
    for row in rows:
        label = "%s/%s" % (row.get("run_id"), row.get("artifact_id"))
        if str(row.get("state")) != "ready":
            problems.append("%s state=%s" % (label, row.get("state")))
            continue
        ready += 1
        for field in ("artifact_hash", "content_hash"):
            if not HASH_SHAPE.match(str(row.get(field) or "")):
                problems.append("%s %s=%r (want sha256:<64 hex>)" % (
                    label, field, str(row.get(field))[:24]))
        raw = row.get("artifact_json")
        if not isinstance(raw, str) or not raw.strip():
            problems.append("%s artifact_json is empty" % label)
            continue
        try:
            snapshot = json.loads(raw)
        except ValueError:
            problems.append("%s artifact_json does not parse" % label)
            continue
        if not isinstance(snapshot, dict):
            problems.append("%s artifact_json is not an object" % label)
            continue
        if str(snapshot.get("artifact_hash") or "") != str(row.get("artifact_hash") or ""):
            problems.append("%s artifact_json.artifact_hash != column artifact_hash" % label)
        if str(snapshot.get("run_id") or "") != str(row.get("run_id") or ""):
            problems.append("%s artifact_json.run_id != column run_id" % label)
        for flag in ("run_binding", "content_hash_binding"):
            if snapshot.get(flag) is not True:
                problems.append("%s %s=%r" % (label, flag, snapshot.get(flag)))
        if str(snapshot.get("content_hash") or "") != str(row.get("content_hash") or ""):
            problems.append("%s artifact_json.content_hash != column content_hash" % label)
        if known_run_ids and str(row.get("run_id")) not in known_run_ids:
            problems.append("%s run_id is not in this dataset's run list" % label)
    if problems:
        return verdict(CONTRADICTED, "row_not_ready_or_not_self_consistent",
                       "%d row(s) ready; problems: %s" % (ready, "; ".join(problems[:4])))
    return verdict(ESTABLISHED, "rows_ready_and_self_consistent",
                   "%d row(s), all state=ready, hashes shaped and agreeing with their JSON"
                   % len(rows))


def verdict_c(setting_effective, b):
    """C: would the locator query see them?

    The reader's predicate is `state = ready` scoped by the report's dataset bindings and
    gated by `report.locator_hints_enabled` (`attachReportLocationHints` reads the switch
    *before* the rows). Both halves have to hold; a switch that is off makes B unusable
    without making B false, which is exactly the confusion this state separates.
    """
    if setting_effective is not True:
        return verdict(NOT_YET, "locators_disabled",
                       "%s.%s effective=%r: the rows could be perfect and no section would "
                       "get a hint" % (SETTING_GROUP, SETTING_FIELD, setting_effective))
    if b["state"] != ESTABLISHED:
        return verdict(NOT_YET, "no_ready_rows_to_read",
                       "the switch is on, but B is %s (%s)" % (b["state"], b["reason"]))
    return verdict(ESTABLISHED, "switch_on_and_rows_ready",
                   "the locator read would return the rows B just verified")


def verdict_d(hinted, total, c):
    """D: did a hint actually reach a section of the named revision?

    Only this state means "the treatment arm has a navigator". It cannot be inferred from
    A/B/C, because hints are frozen at outline time — a revision generated before the
    artifacts landed will read 0 forever, and calling that "W-4 完成" would hand T11 an
    arm with no treatment.
    """
    if total <= 0:
        return verdict(NOT_YET, "revision_has_no_sections",
                       "the named revision lists 0 sections: nothing could carry a hint")
    if hinted <= 0:
        return verdict(NOT_YET, "hints_absent",
                       "0/%d section(s) carry location_hints; hints are frozen in the "
                       "outline step, so a revision generated before the artifacts landed "
                       "stays empty forever — regenerate it after W-4, do not backfill" % total)
    if c["state"] != ESTABLISHED:
        return verdict(CONTRADICTED, "hints_without_a_readable_source",
                       "%d/%d section(s) carry hints while C is %s: the rows behind those "
                       "hints are no longer readable, so the hints are stale navigation"
                       % (hinted, total, c["state"]))
    return verdict(ESTABLISHED, "hints_present",
                   "%d/%d section(s) carry location_hints" % (hinted, total))


# ── the live half ──────────────────────────────────────────────────────────

def setting_value(view, group, field):
    node = ((view.get("groups") or {}).get(group) or {}).get(field)
    if isinstance(node, dict):
        return node.get("effective")
    return node


def pick_dataset(admin, wanted):
    """`--dataset` accepts a full id, an unambiguous id prefix, or an exact name."""
    data = admin.ok("GET", "/api/v1/datasets?page=1&page_size=200")
    rows = data.get("datasets") if isinstance(data, dict) else data
    rows = rows or []
    exact = [r for r in rows if str(r.get("id")) == wanted or str(r.get("name")) == wanted]
    if len(exact) == 1:
        return exact[0]
    prefixed = [r for r in rows if str(r.get("id")).startswith(wanted)]
    if len(prefixed) == 1:
        return prefixed[0]
    if not prefixed and not exact:
        raise Usage("--dataset %s matches no dataset on this host (%d listed); a probe "
                    "that guesses a dataset would report about the wrong corpus" %
                    (wanted, len(rows)))
    raise Usage("--dataset %s is ambiguous (%d matches); pass a full id" %
                (wanted, len(exact) or len(prefixed)))


def list_rows(admin, path):
    """Every page of a paged endpoint, or the first MAX_PAGES with a loud marker."""
    items, total, page = [], None, 1
    while page <= MAX_PAGES:
        data = admin.ok("GET", "%s?page=%d&page_size=100" % (path, page))
        chunk = data.get("items") if isinstance(data, dict) else data
        chunk = chunk or []
        total = data.get("total") if isinstance(data, dict) else None
        items.extend(chunk)
        if not chunk or total is None or len(items) >= int(total):
            break
        page += 1
    return items, total


def run_probe(admin, args, stamp, outdir):
    results, readings = [], {}

    def record(step, reading, detail):
        readings[step] = reading
        results.append({"step": step, "state": reading["state"],
                        "reason": reading["reason"], "detail": detail})

    # S1 — resolve the dataset. Everything after this is scoped to it.
    dataset = pick_dataset(admin, args.dataset)
    runs, run_total = list_rows(admin, "/api/v1/datasets/%s/compilation/runs" % dataset["id"])
    print(safe("  dataset %s  name=%s  docs=%s  runs=%s" % (
        dataset["id"], dataset.get("name"), dataset.get("document_count"), run_total)))
    record("S1", verdict(ESTABLISHED, "dataset_resolved",
                         "dataset=%s docs=%s" % (dataset["id"],
                                                 dataset.get("document_count"))),
           "dataset resolved")

    # S2 — A, the writer's claim.
    a = verdict_a(runs, dataset["id"])
    for run in runs:
        detail = detail_json(run)
        print(safe("  run %s  kind=%s  state=%s  artifacts=%s  hash_bound=%s  failure=%s" % (
            run.get("id"), run.get("kind"), run.get("state"),
            number(detail, "artifact_count"), number(detail, "hash_bound_artifact_count"),
            run.get("failure_code") or "-")))
    record("S2", a, a["detail"])

    # S3 — B, re-read through a second transport, per row.
    rows, row_total = list_rows(admin, "/api/v1/datasets/%s/artifacts" % dataset["id"])
    known = {str(r.get("id")) for r in runs}
    b = verdict_b(rows, known)
    print(safe("  artifacts total=%s  rows read=%d" % (row_total, len(rows))))
    record("S3", b, b["detail"])
    # B's cross-check: a confirmed run claims `ready_artifact_count`; the rows for that
    # run must be countable and no larger. A claim the table cannot reproduce is a
    # contradiction, not a "not yet".
    for run in runs:
        detail = detail_json(run)
        claimed = number(detail, "ready_artifact_count")
        if str(run.get("state")) != "confirmed" or claimed <= 0:
            continue
        scoped, _ = list_rows(admin, "/api/v1/datasets/%s/artifacts?run_id=%s" % (
            dataset["id"], run.get("id")))
        if len(scoped) < claimed:
            b = verdict(CONTRADICTED, "run_claims_more_rows_than_the_table_has",
                        "run %s claims ready_artifact_count=%s but %d row(s) come back by "
                        "run_id" % (run.get("id"), claimed, len(scoped)))
            readings["S3"] = b
            results[-1].update({"state": b["state"], "reason": b["reason"],
                                "detail": b["detail"]})
            break

    # S4 — C, the reader's predicate (switch first, then the rows).
    view = admin.ok("GET", "/api/v1/system/settings")
    effective = setting_value(view, SETTING_GROUP, SETTING_FIELD)
    c = verdict_c(effective, readings["S3"])
    print(safe("  %s.%s effective=%r" % (SETTING_GROUP, SETTING_FIELD, effective)))
    record("S4", c, c["detail"])

    # S5 — D, the only state that means T11 can use it. Needs a named revision, because
    # "hints exist somewhere" and "hints exist on the arm being measured" differ.
    if not (args.report and args.revision):
        d = verdict(NOT_YET, "no_target_revision_named",
                    "pass --report <id> --revision <n> to read the arm that would carry the "
                    "hints; without it W-4 can be at most A+B+C")
    else:
        listing = admin.ok("GET", "/api/v1/reports/%s/revisions/%d/sections" % (
            args.report, args.revision))
        sections = (listing.get("sections") if isinstance(listing, dict) else listing) or []
        hinted = sum(1 for s in sections if s.get("location_hints"))
        d = verdict_d(hinted, len(sections), readings["S4"])
        print(safe("  revision %s r%d  sections=%d  with hints=%d" % (
            args.report, args.revision, len(sections), hinted)))
    record("S5", d, d["detail"])

    return results, readings, {"dataset": dataset, "runs": len(runs),
                               "rows": len(rows), "rows_total": row_total}


def render(results, readings, args, stamp, outdir):
    print("")
    for row in results:
        print(safe("  [%s] %s  %s  %s" % (row["state"].upper(), row["step"],
                                          row["reason"], row["detail"])))
    print("")
    for index, (code, label, _) in enumerate(STATES, start=2):
        reading = readings["S%d" % index]
        print(safe("  %s %-12s %s (%s)" % (code, label, reading["state"], reading["reason"])))
    states = [readings["S%d" % i]["state"] for i in range(2, 6)]
    if any(s == CONTRADICTED for s in states):
        code = 1
        headline = "W-4 CONTRADICTED: two readings disagree, someone has to look"
    elif all(s == ESTABLISHED for s in states):
        code = 0
        headline = "W-4 成立 on this dataset and revision (A/B/C/D all established)"
    else:
        code = 3
        first = [(c, readings["S%d" % i]) for i, (c, _, _) in enumerate(STATES, start=2)
                 if readings["S%d" % i]["state"] != ESTABLISHED][0]
        headline = ("W-4 NOT YET: first missing state is %s (%s). This is a reading, not "
                    "a pass." % (first[0], first[1]["reason"]))
    payload = {"base": args.base, "stamp": stamp, "dataset": args.dataset,
               "report": args.report, "revision": args.revision,
               "readings": {"S%d" % i: readings["S%d" % i] for i in range(1, 6)},
               "results": results, "exit": code, "headline": headline}
    path = os.path.join(outdir, "w4-summary.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2)
    print("")
    print(safe(headline))
    print(safe("summary: %s" % path))
    print(safe("exit=%d" % code))
    return code


# ── --selftest: prove each verdict can fail ────────────────────────────────

def selftest():
    checks = []

    def check(name, cond):
        checks.append((name, bool(cond)))

    ok_row = {
        "run_id": "run-1", "artifact_id": "topic-a", "state": "ready",
        "artifact_hash": "sha256:" + "a" * 64, "content_hash": "sha256:" + "b" * 64,
        "artifact_json": json.dumps({
            "run_id": "run-1", "artifact_hash": "sha256:" + "a" * 64,
            "content_hash": "sha256:" + "b" * 64,
            "run_binding": True, "content_hash_binding": True,
            "state": "ready", "evidence_state": "payload_hash_verified",
        }),
    }
    ok_run = {"id": "run-1", "dataset_id": "ds-1", "state": "confirmed",
              "detail": json.dumps({"artifact_count": 2, "ready_artifact_count": 2,
                                    "bound_artifact_count": 2,
                                    "hash_bound_artifact_count": 2})}

    a = verdict_a([ok_run], "ds-1")
    check("a healthy run reads A established", a["state"] == ESTABLISHED)
    check("no run at all is NOT 'the engine has no artifacts'",
          verdict_a([], "ds-1")["reason"] == "no_run")
    check("an accepted-only run is not A",
          verdict_a([dict(ok_run, state="accepted")], "ds-1")["reason"] == "no_confirmed_run")
    check("a confirmed run with 0 artifacts is a contradiction",
          verdict_a([dict(ok_run, detail=json.dumps({"artifact_count": 0}))], "ds-1")
          ["state"] == CONTRADICTED)
    check("a run returned for another dataset is a contradiction",
          verdict_a([dict(ok_run, dataset_id="ds-2")], "ds-1")["state"] == CONTRADICTED)

    b = verdict_b([ok_row], {"run-1"})
    check("a ready, self-consistent row reads B established", b["state"] == ESTABLISHED)
    check("0 rows is not_yet, not a pass",
          verdict_b([], set())["state"] == NOT_YET)
    check("an unverified row fails B (the writer's shadow is not ready)",
          verdict_b([dict(ok_row, state="unverified")], {"run-1"})["state"] == CONTRADICTED)
    check("artifact_hash that is not sha256:64hex fails B",
          verdict_b([dict(ok_row, artifact_hash="abc")], {"run-1"})["state"] == CONTRADICTED)
    tampered = dict(ok_row)
    tampered["artifact_json"] = ok_row["artifact_json"].replace("a" * 64, "c" * 64)
    check("artifact_json.artifact_hash drifting from the column fails B",
          verdict_b([tampered], {"run-1"})["state"] == CONTRADICTED)
    unbound = dict(ok_row)
    unbound["artifact_json"] = json.dumps(dict(json.loads(ok_row["artifact_json"]),
                                               content_hash_binding=False))
    check("content_hash_binding=false fails B",
          verdict_b([unbound], {"run-1"})["state"] == CONTRADICTED)
    orphan = dict(ok_row, run_id="run-9")
    orphan["artifact_json"] = json.dumps(dict(json.loads(ok_row["artifact_json"]),
                                              run_id="run-9"))
    check("a row whose run is not in the run list fails B (provenance)",
          verdict_b([orphan], {"run-1"})["state"] == CONTRADICTED)

    est_b = verdict(ESTABLISHED, "x")
    check("switch on + B established reads C established",
          verdict_c(True, est_b)["state"] == ESTABLISHED)
    check("switch off is not_yet even when every row is perfect",
          verdict_c(False, est_b)["reason"] == "locators_disabled")
    check("C needs B: rows absent means the switch has nothing to show",
          verdict_c(True, verdict(NOT_YET, "no_projection_row"))["state"] == NOT_YET)

    est_c = verdict(ESTABLISHED, "x")
    check("hints on a section read D established",
          verdict_d(3, 5, est_c)["state"] == ESTABLISHED)
    check("0/5 sections with hints is not_yet, never 'done'",
          verdict_d(0, 5, est_c)["state"] == NOT_YET)
    check("an empty revision is not D",
          verdict_d(0, 0, est_c)["reason"] == "revision_has_no_sections")
    check("hints with no readable source are a contradiction (stale navigation)",
          verdict_d(2, 5, verdict(NOT_YET, "no_ready_rows_to_read"))["state"] == CONTRADICTED)

    # The headline rule: only four established states may print as 成立.
    def headline(states):
        if any(s == CONTRADICTED for s in states):
            return 1
        return 0 if all(s == ESTABLISHED for s in states) else 3

    check("A+B+C but no D is exit 3 (a revision without hints is not W-4 done)",
          headline([ESTABLISHED, ESTABLISHED, ESTABLISHED, NOT_YET]) == 3)
    check("all four established is exit 0", headline([ESTABLISHED] * 4) == 0)
    check("one contradiction outranks three established",
          headline([ESTABLISHED, ESTABLISHED, ESTABLISHED, CONTRADICTED]) == 1)

    failed = [name for name, ok in checks if not ok]
    print(safe("selftest: %d assertion(s), %d failed" % (len(checks), len(failed))))
    for name in failed:
        print(safe("  FAIL  %s" % name))
    if failed:
        return 1
    print(safe("SELFTEST OK"))
    return 0


REQUIREMENTS = (
    Requirement("--base", "RGX_BASE", "console base URL", default=DEFAULT_BASE),
    Requirement("--user", "RGX_USER", "console account", default=DEFAULT_USER),
    Requirement("--password", "RGX_VERIFY_PASS", "console password", secret=True),
)

STEPS_DOC = (
    ("S1", "resolve the dataset (--dataset id | id-prefix | exact name)"),
    ("S2", "A 已生成: a run reached confirmed and its detail reports hash-bound artifacts"),
    ("S3", "B 已持久化: ready rows exist and each row agrees with the JSON it carries"),
    ("S4", "C 可被读取: report.locator_hints_enabled is effective and B holds"),
    ("S5", "D 落在目标修订上: the named revision's sections carry location_hints"),
)


def parse_args(argv):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default=os.environ.get("RGX_BASE", DEFAULT_BASE))
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=os.environ.get("RGX_VERIFY_PASS", ""),
                    help="console password (or RGX_VERIFY_PASS); no default is stored")
    ap.add_argument("--dataset", default="",
                    help="dataset id, unambiguous id prefix, or exact name (required live)")
    ap.add_argument("--report", default="", help="report id whose revision would carry hints")
    ap.add_argument("--revision", type=int, default=0, help="revision number of that report")
    ap.add_argument("--offline", action="store_true",
                    help="print the contract (what is required, which steps run) and exit 0")
    ap.add_argument("--selftest", action="store_true",
                    help="prove the four verdicts can fail, without a host")
    ap.add_argument("--out", default="")
    return ap.parse_args(argv)


def main(argv):
    args = parse_args(argv)
    if args.offline:
        return print_contract(
            "W-4 编译件落库 (doc/304 §7 item 23, doc/295 R17)",
            REQUIREMENTS, STEPS_DOC,
            notes=("--offline never logs in and never writes evidence.",
                   "A/B/C/D are different states: only A+B+C+D together may print as 成立.",
                   "exit 3 is the expected answer while W-4 is open; it is not a failure "
                   "of this probe.",
                   "the probe does not recompute artifact_hash/content_hash: that encoder "
                   "is Go's, and a Python look-alike would agree with a bug."))
    if args.selftest:
        return selftest()
    if not args.dataset:
        print(safe("usage: --dataset <id|name> is required: W-4 is a statement about one "
                   "corpus, and a probe without one would measure nothing (exit 2)"))
        return 2
    try:
        args.password = Requirement("--password", "RGX_VERIFY_PASS", "console password",
                                    secret=True).resolve(args)
    except Usage as exc:
        print(safe("usage: %s" % exc))
        return 2
    stamp = time.strftime("%y%m%d-%H%M%S")
    outdir = args.out or os.path.join(REPO, "tmp", "w4-%s" % time.strftime("%Y%m%d-%H%M%S"))
    os.makedirs(outdir, exist_ok=True)

    print(safe("W-4 编译件落库 -> %s (--dataset %s)" % (args.base, args.dataset)))
    admin = Client(args.base)
    try:
        admin.login(args.user, args.password)
    except EnvUnavailable as exc:
        print(safe("ENV UNAVAILABLE: %s" % exc))
        return 2
    try:
        results, readings, _ = run_probe(admin, args, stamp, outdir)
    except Usage as exc:
        print(safe("usage: %s" % exc))
        return 2
    except CaseFail as exc:
        print(safe("FAIL: %s" % exc))
        return 1
    except Exception:  # noqa: BLE001 - a probe must print, not swallow
        traceback.print_exc()
        return 2
    return render(results, readings, args, stamp, outdir)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
