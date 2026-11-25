#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""T11 / W-4 first action (doc/304 §2.1c): give the compiled corpus a *reader*.

The 74 `state=ready` projections and a `0 binding` dataset are two facts about the same
corpus; neither is D. D is "a report revision that reads this corpus carries
`location_hints` on its sections", and hints are frozen in the outline step
(internal/service/report_locator.go:236 `attachReportLocationHints`), keyed by the
**dataset ids in the evidence previews** — not by `rgx_dataset_binding_version`.
So the population that satisfies the probe's D gate
(`scripts/probes/w4_artifact_landing.py` `verdict_d_scope`, which reads
`revision.datasets_json`) is "a revision created *now* with `datasets[].dataset_id` =
the measured corpus".

This driver does exactly that and stops at every point where a human decides:

    login -> pick dataset -> create report -> wait for the outline -> (confirm it) ->
    wait for the write -> count hints per section

Nothing is backfilled and no revision is regenerated: a revision that was written before
the artifacts landed reads 0 forever, and rewriting it would destroy the reading.

    python scripts/probes/w4_drill.py --dataset <id> --dry
    python scripts/probes/w4_drill.py --dataset <id>            # stops at awaiting_outline
    python scripts/probes/w4_drill.py --dataset <id> --confirm   # approves it and writes

Exit: 0 the drill reached a written revision; 3 stopped at a named state (a reading);
2 the environment refused.
"""

import argparse
import io
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("RGX_BASE", "http://192.168.4.151:19193/api/v1")
USER = os.environ.get("RGX_VERIFY_USER", "admin")
PASS = os.environ.get("RGX_VERIFY_PASS", "RgxTest#2026a")


def call(method, path, token=None, body=None, key=None, timeout=60):
    url = BASE + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            return resp.status, json.loads(raw.decode("utf-8", "replace"))
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            return exc.code, json.loads(raw.decode("utf-8", "replace"))
        except ValueError:
            return exc.code, {"raw": raw.decode("utf-8", "replace")[:300]}


def login():
    status, body = call("POST", "/auth/login", body={"username": USER, "password": PASS})
    if status != 200:
        raise SystemExit("login failed: %s %s" % (status, body))
    data = body.get("data") or body
    token = data.get("token") or data.get("access_token")
    if not token:
        raise SystemExit("login answered 200 without a token: %s" % json.dumps(body)[:300])
    return token


def data_of(body):
    return (body.get("data") if isinstance(body, dict) else None) or {}


def sections_of(body):
    """Sections arrive as `data.items`, `data.sections`, or a bare list depending on the
    reader; take whichever is present and say nothing when all three are empty."""
    payload = data_of(body)
    if isinstance(payload, list):
        return payload
    return payload.get("items") or payload.get("sections") or []


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--dataset", required=True, help="RGX-side dataset id (rgx_dataset_link.id)")
    ap.add_argument("--question", default="本次合规报告需要覆盖哪些适用规则、执行情况与未闭合差距？")
    ap.add_argument("--template", default="report-compliance")
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--confirm", action="store_true",
                    help="approve the generated outline as-is and let the write run")
    ap.add_argument("--dry", action="store_true")
    args = ap.parse_args(argv)

    if args.dry:
        print("POST %s/auth/login  -> token" % BASE)
        print("POST %s/reports     -> report+revision+job (datasets=[{role:primary,%s}])"
              % (BASE, args.dataset))
        print("GET  %s/reports/:id/revisions/:rev          -> outline state" % BASE)
        if args.confirm:
            print("POST %s/reports/:id/revisions/:rev/outline/confirm -> write" % BASE)
        print("GET  %s/reports/:id/revisions/:rev/sections  -> count location_hints" % BASE)
        return 0

    token = login()
    status, body = call("GET", "/datasets?page=1&page_size=100", token)
    payload = data_of(body)
    # `/datasets` answers the list directly in `data` (page metadata lives in headers on
    # this endpoint); `/reports` nests it in `data.items`. Read both shapes rather than
    # assume one — an empty list from a misread shape would have read as "not visible".
    if isinstance(payload, list):
        items = payload
    else:
        items = payload.get("items") or []
    hit = [d for d in items if str(d.get("id")) == args.dataset]
    if not hit:
        print("dataset %s is not visible to %s (%d datasets visible)"
              % (args.dataset, USER, len(items)), file=sys.stderr)
        return 2
    print("dataset   = %s (%s)" % (args.dataset, hit[0].get("name")))

    key = "w4-drill-%d" % int(time.time())
    payload = {
        "title": "W-4 试点：编译件可导航性复核 %s" % time.strftime("%Y-%m-%d %H:%M"),
        "question": args.question,
        "template_key": args.template,
        "template_version": 0,
        "datasets": [{"role": "primary", "dataset_id": args.dataset}],
        # `required` is the default in spirit and the only value that keeps a human in
        # the loop; `auto` would let the server pick the structure it then evidences.
        "outline_confirmation": "required",
        "research_depth": "standard",
    }
    status, body = call("POST", "/reports", token, payload, key)
    if status not in (200, 201, 202):
        print("create refused: %s %s" % (status, json.dumps(body, ensure_ascii=False)[:500]),
              file=sys.stderr)
        return 2
    d = data_of(body)
    report_id = (d.get("report") or {}).get("id")
    rev_no = (d.get("revision") or {}).get("revision")
    job_id = (d.get("job") or {}).get("id")
    print("report    = %s  revision=%s  job=%s" % (report_id, rev_no, job_id))

    deadline = time.time() + args.timeout
    outline = None
    state = ""
    while time.time() < deadline:
        status, body = call("GET", "/reports/%s/revisions/%s" % (report_id, rev_no), token)
        d = data_of(body)
        rev = d.get("revision") or d
        state = str(rev.get("status") or "")
        job = d.get("job") or {}
        stage = str(job.get("stage") or "")
        print("  [%s] revision=%s job=%s stage=%s" %
              (time.strftime("%H:%M:%S"), state, job.get("status"), stage))
        if state in ("awaiting_outline", "published", "succeeded", "failed", "cancelled"):
            break
        time.sleep(6)

    if state == "awaiting_outline":
        status, body = call("GET", "/reports/%s/revisions/%s/sections" % (report_id, rev_no),
                            token)
        sections = sections_of(body)
        print("outline   = %d section(s): %s"
              % (len(sections), ", ".join(str(s.get("section_key")) for s in sections)))
        if not args.confirm:
            print("\nstopped at awaiting_outline (a human owns this decision). Re-run with "
                  "--confirm to approve it, or approve it in the UI.")
            return 3
        edited = [{"section_key": s.get("section_key"), "title": s.get("title")}
                  for s in sections if s.get("section_key")]
        status, body = call("POST", "/reports/%s/revisions/%s/outline/confirm"
                            % (report_id, rev_no), token, {"sections": edited})
        if status not in (200, 202):
            print("confirm refused: %s %s" % (status, json.dumps(body, ensure_ascii=False)[:400]),
                  file=sys.stderr)
            return 2
        print("confirmed = %s" % json.dumps(data_of(body), ensure_ascii=False)[:240])
        while time.time() < deadline:
            status, body = call("GET", "/reports/%s/revisions/%s" % (report_id, rev_no), token)
            d = data_of(body)
            rev = d.get("revision") or d
            state = str(rev.get("status") or "")
            job = d.get("job") or {}
            print("  [%s] revision=%s job=%s stage=%s done=%s/%s" %
                  (time.strftime("%H:%M:%S"), state, job.get("status"), job.get("stage"),
                   job.get("progress_done"), job.get("progress_total")))
            if state in ("published", "succeeded", "failed", "cancelled"):
                break
            time.sleep(8)

    status, body = call("GET", "/reports/%s/revisions/%s/sections" % (report_id, rev_no), token)
    sections = sections_of(body)
    hinted = [s for s in sections
              if (s.get("location_hints") or s.get("locations") or [])]
    print("\nresult    = revision=%s sections=%d hinted=%d"
          % (state, len(sections), len(hinted)))
    for s in sections:
        hints = s.get("location_hints") or []
        print("  %-20s %-10s hints=%d %s" %
              (s.get("section_key"), s.get("status"), len(hints),
               (hints[0].get("title") if hints and isinstance(hints[0], dict) else "")))
    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                            "..", "..", "tmp", "w4diag", "w4-drill.json")
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    out = {"report_id": report_id, "revision": rev_no, "state": state,
           "sections": len(sections), "hinted": len(hinted),
           "base": BASE, "dataset": args.dataset}
    io.open(out_path, "w", encoding="utf-8").write(
        json.dumps(out, ensure_ascii=False, indent=2))
    print("\nwrote E:/tmpgo/post/w4-drill.json")
    return 0 if state == "published" else 3


if __name__ == "__main__":
    sys.exit(main())
