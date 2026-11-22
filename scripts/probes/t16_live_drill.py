#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""301 §11.1 T16 live drill — the half of the release gate that needs a real host.

`scripts/release-gate/assurance.sh` (`s13.assurance-gold`) proves the *offline* half:
the 12 gate rows, the closed error-code set, the refusal of `required` before
calibration. It says so in its own last line:

    not in scope: production coverage (real traffic -> T16 live drill)

This script is that live drill. It walks the ten steps of
`doc/develop/305 §4` against a deployed ragflow-x and records each step as PASS /
FAIL / SKIP with the evidence that decided it.

Why a separate script instead of extending `report_live_verify.py`: that probe
answers "does the report product work end to end" (R01–R27, one failure aborts it).
This one answers "is the v0.7 assurance/gateway seam true against a real engine and a
real database", and every step is independently meaningful — a failing export must
not erase the evidence that the gateway replay was byte-identical.

Design rules this script holds itself to:

1. **A step that did not run is `SKIP`, never `PASS`.** A drill that reports green
   because the model proxy was down is the same class of lie as a gate row with no
   denominator.
2. **The replay is compared byte for byte, on the streaming route.** The non-stream
   gateway path stores a *reference* (`search-app:<request_id>`) rather than the
   response, so "replay is byte-identical" is only a claim the streaming route makes
   — and it is the claim doc/301 §5.10 constraint ③ actually makes ("new frames must
   write through the same MultiWriter, otherwise replay bytes drift").
3. **Database-side assertions go through psql over ssh**, not through a second API
   read. "The ledger has one row" and "the API says it has one row" are different
   statements, and the first is the one the plan asks for.
4. **Cleanup is a step, not a `finally`.** Compute left behind is how a test
   environment becomes unexplainable; the step is recorded like any other and its
   failure does not turn the drill red (a leftover dataset is a mess, not a product
   defect) — it is printed as a warning.

Usage (Git Bash on Windows, from the repo root). This file is tracked, so it carries
no credential: the console password and the ssh sudo password come from the environment
(doc/304 §7 item 12 — the drill used to live in `localenv/`, where a clone could read
the evidence in doc/305 §4.3 but could not run it):

    export RGX_VERIFY_PASS=…            # console password (no default exists)
    export RGX_DRILL_SSH_PASS=…         # sudo password; only the psql steps need it

    python scripts/probes/t16_live_drill.py --offline           # print the contract, exit 0
    python scripts/probes/t16_live_drill.py                     # all steps
    python scripts/probes/t16_live_drill.py --only S1,S3,S4,S5  # cheap slice
    python scripts/probes/t16_live_drill.py --keep              # do not delete the dataset

Exit codes: 0 = every step PASS, 1 = at least one FAIL, 2 = the environment is not
usable (login failed / host down) or a required value is missing, 3 = ran without
failures but skipped steps (a `--only` slice, or a step whose input an earlier step did
not produce). 3 exists so a partial run cannot be quoted as "the drill passed".
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import time
import traceback
import uuid

import requests

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from probe_common import (  # noqa: E402  (path insertion has to come first)
    CaseFail, CaseSkip, Client, EnvUnavailable, REPO, Requirement, Usage, expect,
    print_contract, safe, wait_job,
)

DEFAULT_BASE = "http://192.168.4.151:19193"   # a drill host is configuration, not a secret
DEFAULT_USER = "admin"
DEFAULT_CORPUS = os.path.join(
    REPO, "test", "山东省属企业及其下属企业安全生产风险点防控情况的报告-docs")

# The corpus is regulatory prose in Chinese; a question about it is answerable from
# any of the three markdown files, so a top-1 hit is expected and the drill does not
# have to be clever about query wording.
QUESTION = "山东省属企业安全生产风险点防控的责任分工与主要措施是什么？"

TEMPLATE_KEY = "report-compliance"
EXPORT_FORMATS = ("md", "docx", "pdf", "html", "xlsx")
# The outsider account S10 creates is thrown away with its tenant. Its password is
# generated per run: a fixed string here would be one more credential in a tracked file,
# and the account never outlives the step that made it.

# Model calls are the expensive part of the drill. The gateway needs two (one
# non-stream answer, one stream whose bytes are replayed) and the report needs
# whatever its sections cost; the assurance run is bounded by the `report` budget
# setting, not by this script.
STEP_ORDER = ("S1", "S2", "S3", "S4", "S5", "S6", "S7", "S8", "S9", "S10", "S11")


def sha256_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


class Drill:
    def __init__(self, args):
        self.args = args
        self.base = args.base.rstrip("/")
        self.stamp = time.strftime("%Y%m%d-%H%M%S")
        self.dir = os.path.join(REPO, "tmp", f"t16-drill-{self.stamp}")
        os.makedirs(self.dir, exist_ok=True)
        self.results = []
        self.state = {}
        self.warnings = []

    # -- evidence ---------------------------------------------------------
    def dump(self, name, payload):
        path = os.path.join(self.dir, name + ".json")
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        return path

    def keep_bytes(self, name, raw: bytes):
        path = os.path.join(self.dir, name)
        with open(path, "wb") as fh:
            fh.write(raw)
        return path

    def note(self, text):
        print("    - " + safe(text))

    def warn(self, text):
        self.warnings.append(text)
        print("    ! " + safe(text))

    # -- psql -------------------------------------------------------------
    def psql(self, sql):
        """One query, one scalar/multi-row string. See tmp/patch/pgq.py for why ssh."""
        import paramiko

        cfg = self.args
        if not cfg.ssh_password:
            # Lazy, not upfront: a cheap slice (--only S1,S2,S5) never reaches psql and
            # must not demand an ssh password it will not use. The step that does need
            # it says so, and the drill exits 2 — "I could not look" is not "it held".
            raise EnvUnavailable(
                "RGX_DRILL_SSH_PASS (or --ssh-password) is required: the database-side "
                "assertions go through psql over ssh, and no default is stored in the repo")
        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        client.connect(cfg.ssh_host, username=cfg.ssh_user,
                       password=cfg.ssh_password, timeout=20)
        quoted = "'" + sql.replace("'", "'\\''") + "'"
        inner = (f"docker exec {cfg.pg_container} psql -U {cfg.pg_user} "
                 f"-d {cfg.pg_db} -tAc {quoted}")
        # The password goes in over stdin, not into the command string: `echo '…' | sudo -S`
        # would leave it in the remote shell's process list and in any command log the host
        # keeps. `-p ''` then keeps sudo from printing a prompt we never read.
        stdin, out, err = client.exec_command("sudo -S -p '' " + inner, timeout=120)
        stdin.write(cfg.ssh_password + chr(10))
        stdin.flush()
        stdin.channel.shutdown_write()
        stdout = out.read().decode("utf-8", "replace")
        stderr = err.read().decode("utf-8", "replace")
        client.close()
        stderr = "\n".join(l for l in stderr.splitlines() if "password for" not in l)
        if stderr.strip():
            raise CaseFail("psql failed: " + stderr.strip()[:400])
        return stdout.strip()

    # -- runner -----------------------------------------------------------
    def run(self, admin):
        for step in STEP_ORDER:
            if self.args.only and step not in self.args.only:
                continue
            fn = getattr(self, "step_" + step.lower(), None)
            if fn is None:
                continue
            title = (fn.__doc__ or step).strip().splitlines()[0]
            print(f"[{step}] {safe(title)}")
            started = time.time()
            record = {"step": step, "title": title, "status": "PASS",
                      "detail": "", "seconds": 0.0}
            try:
                detail = fn(admin)
                record["detail"] = detail or ""
            except CaseSkip as exc:
                record["status"] = "SKIP"
                record["detail"] = str(exc)
            except CaseFail as exc:
                record["status"] = "FAIL"
                record["detail"] = str(exc)
            except EnvUnavailable:
                raise
            except Exception as exc:  # noqa: BLE001 - a drill records, it does not crash
                record["status"] = "FAIL"
                record["detail"] = f"{type(exc).__name__}: {exc}"
                self.dump(f"{step}-traceback", traceback.format_exc())
            record["seconds"] = round(time.time() - started, 1)
            print(f"    => {record['status']} ({record['seconds']}s) "
                  f"{safe(record['detail'])[:400]}")
            self.results.append(record)
        return self.report()

    def report(self):
        failed = [r for r in self.results if r["status"] == "FAIL"]
        skipped = [r for r in self.results if r["status"] == "SKIP"]
        lines = [
            f"# T16 live drill {self.stamp}",
            "",
            f"- base: `{self.base}`",
            f"- evidence: `tmp/t16-drill-{self.stamp}/`",
            "",
            "| step | what it proves | verdict | seconds | detail |",
            "| --- | --- | --- | --- | --- |",
        ]

        def cell(text):
            return str(text).replace("|", "\\|").replace("\n", " ")[:220]

        for r in self.results:
            lines.append(f"| {r['step']} | {cell(r['title'])} | {r['status']} | "
                         f"{r['seconds']} | {cell(r['detail'])} |")
        passed = len(self.results) - len(failed) - len(skipped)
        lines += ["", f"**{passed}/{len(self.results)} PASS, {len(failed)} FAIL, "
                      f"{len(skipped)} SKIP**", ""]
        if self.warnings:
            lines.append("## warnings")
            lines += ["- " + w for w in self.warnings]
            lines.append("")
        with open(os.path.join(self.dir, "summary.md"), "w", encoding="utf-8") as fh:
            fh.write("\n".join(lines))
        print()
        print("\n".join(lines[-6:]))
        print(f"evidence: {self.dir}")
        if failed:
            return 1
        if skipped:
            # 3 = "ran, but not completely". A slice run (`--only S1,S3`) is exactly
            # this, and it must not be quoted later as if the whole drill passed.
            print(f"incomplete: {len(skipped)} step(s) skipped")
            return 3
        return 0

    # -- steps ------------------------------------------------------------
    def step_s1(self, admin):
        """S1 数据集入库并解析完成（3 篇 / chunk>0 / 无失败）"""
        files = corpus_files(self.args.corpus)
        expect(len(files) >= 3, f"corpus {self.args.corpus} has {len(files)} files")
        name = f"t16-drill-{self.stamp}"
        ds = admin.ok("POST", "/api/v1/datasets", json_body={"name": name})
        ds_id = ds.get("id") or ds.get("dataset_id")
        expect(ds_id, f"create dataset returned no id: {ds}")
        self.state["dataset_id"] = ds_id

        doc_ids = []
        for path in files:
            with open(path, "rb") as fh:
                blob = fh.read()
            base = os.path.basename(path)
            status, body = admin.api(
                "POST", f"/api/v1/datasets/{ds_id}/documents",
                files={"file": (base, blob)}, data={"source_key": base}, timeout=300)
            expect(200 <= status < 300 and body.get("code") == 0,
                   f"upload {base} -> {status} {str(body)[:200]}")
            doc = body.get("data") or {}
            doc_id = doc.get("id") or doc.get("document_id")
            expect(doc_id, f"upload {base} returned no document id")
            doc_ids.append(doc_id)
        admin.ok("POST", f"/api/v1/datasets/{ds_id}/parse",
                 json_body={"document_ids": doc_ids})

        deadline = time.time() + self.args.parse_timeout
        docs = []
        while time.time() < deadline:
            docs = admin.ok("GET", f"/api/v1/datasets/{ds_id}/documents") or []
            if docs and all(str(d.get("status")) == "3"
                            and int(d.get("chunk_count") or 0) > 0 for d in docs):
                break
            failed = [d for d in docs if str(d.get("status")) == "4"]
            if failed:
                raise CaseFail("parse failed: " + "; ".join(
                    f"{d.get('name')}: {d.get('progress_msg')}" for d in failed))
            time.sleep(5)
        else:
            raise CaseFail("parse timeout: " + "; ".join(
                f"{d.get('name')}={d.get('status')}/{d.get('progress_msg')}"
                for d in docs))
        self.dump("S1-documents", docs)
        chunks = sum(int(d.get("chunk_count") or 0) for d in docs)
        expect(chunks > 0, "parsed but chunk total is 0")
        self.state["corpus_files"] = len(files)
        return f"dataset={ds_id} docs={len(docs)} chunks={chunks}"

    def step_s2(self, admin):
        """S2 检索应用绑定该库，检索命中 > 0"""
        ds_id = require(self.state, "dataset_id")
        app = admin.ok("POST", "/api/v1/search-apps", json_body={
            "name": f"t16-drill-{self.stamp}",
            "dataset_ids": [ds_id],
            "search_config": {"kb_ids": [ds_id], "top_k": 8,
                              "similarity_threshold": 0.2},
        })
        app_id = app.get("id")
        expect(app_id, f"create search app returned no id: {app}")
        self.state["search_app_id"] = app_id
        shadow = admin.ok("GET", f"/api/v1/search-apps/{app_id}")
        # `dataset_ids` on the shadow holds the *engine* dataset id, which is exactly
        # why this check is "non-empty" rather than "equals ours": the ragflow-x id
        # and the engine id are different namespaces, and the mapping between them is
        # the thing being proven here. S3 then proves the answer really came from this
        # corpus, which an id comparison never could.
        bound = shadow.get("dataset_ids") or ""
        expect(str(bound).strip(), f"search app {app_id} is bound to no dataset")
        expect(shadow.get("owner_id"),
               f"search app {app_id} has no owner (the gateway authorises on ownership)")
        probe = admin.ok("POST", f"/api/v1/datasets/{ds_id}/retrieval-test",
                         json_body={"question": QUESTION, "page_size": 5})
        total = int(probe.get("total") or 0)
        self.dump("S2-retrieval", probe)
        expect(total > 0, f"retrieval returned 0 hits: {str(probe)[:300]}")
        return (f"search_app={app_id} engine_dataset={bound} "
                f"retrieval_hits={total}")

    def step_s3(self, admin):
        """S3 网关 /v1/search-apps/:id/completions 非流式：答案 + reference + 治理头"""
        app_id = require(self.state, "search_app_id")
        secret = self.gateway_key(admin)
        body = {"question": QUESTION, "stream": False}
        resp = requests.post(
            self.base + f"/v1/search-apps/{app_id}/completions",
            headers={"Authorization": "Bearer " + secret,
                     "Content-Type": "application/json"},
            json=body, timeout=300)
        self.dump("S3-body", {"status": resp.status_code,
                              "headers": dict(resp.headers),
                              "text": resp.text[:4000]})
        expect(resp.status_code == 200, f"gateway HTTP {resp.status_code}: {resp.text[:300]}")
        payload = resp.json()
        data = payload.get("data") or {}
        answer = (data.get("answer") or "").strip()
        expect(answer, "gateway answer is empty")
        reference = data.get("reference") or []
        expect(reference, "gateway answer carries no reference (evidence lost)")

        policy = resp.headers.get("X-RGX-Policy", "")
        expect(policy, "X-RGX-Policy header is absent (GW-1 contract not advertised)")
        expect(policy.startswith("gateway.v1"), f"X-RGX-Policy={policy!r}")
        remaining = resp.headers.get("X-Quota-Remaining")
        req_remaining = resp.headers.get("X-Quota-Requests-Remaining")
        expect(remaining is not None or req_remaining is not None,
               "no X-Quota-* header although the key has a quota")
        governance = data.get("governance") or {}
        self.state["answer_len"] = len(answer)
        return (f"policy={policy} quota_remaining={remaining}/{req_remaining} "
                f"refs={len(reference)} governance_keys={sorted(governance)}")

    def step_s4(self, admin):
        """S4 同一 Idempotency-Key 流式重放：字节级相同 + 幂等账本只 1 行"""
        app_id = require(self.state, "search_app_id")
        secret = self.gateway_key(admin)
        key = "t16-" + uuid.uuid4().hex
        body = {"question": QUESTION, "stream": True}
        headers = {"Authorization": "Bearer " + secret,
                   "Content-Type": "application/json",
                   "Idempotency-Key": key,
                   "Accept": "text/event-stream"}
        url = self.base + f"/v1/search-apps/{app_id}/completions"
        first = requests.post(url, headers=headers, json=body, timeout=300)
        expect(first.status_code == 200,
               f"stream HTTP {first.status_code}: {first.text[:300]}")
        first_bytes = first.content
        self.keep_bytes("S4-stream-first.sse", first_bytes)

        second = requests.post(url, headers=headers, json=body, timeout=300)
        expect(second.status_code == 200,
               f"replay HTTP {second.status_code}: {second.text[:300]}")
        second_bytes = second.content
        self.keep_bytes("S4-stream-replay.sse", second_bytes)

        self.state["stream_bytes"] = first_bytes
        expect(sha256_bytes(first_bytes) == sha256_bytes(second_bytes),
               "replay bytes differ from the live stream: "
               f"{sha256_bytes(first_bytes)[:12]} != {sha256_bytes(second_bytes)[:12]} "
               f"({len(first_bytes)} vs {len(second_bytes)} bytes)")

        rows = self.psql("select count(*) from rgx_gateway_idempotency "
                         f"where idempotency_key = '{key}'").strip()
        expect(rows == "1", f"idempotency ledger has {rows} rows for the key, want 1")
        frames = first_bytes.count(b"event: ragflowx.governance")
        return (f"bytes={len(first_bytes)} sha={sha256_bytes(first_bytes)[:12]} "
                f"ledger_rows=1 governance_frames={frames}")

    def step_s5(self, admin):
        """S5 流式终态唯一且最后：终态帧恰好一次，治理帧在它之前"""
        raw = self.state.get("stream_bytes")
        expect(raw, "S4 produced no stream bytes to inspect")
        text = raw.decode("utf-8", "replace").replace("\r\n", "\n")
        frames = [f for f in text.split("\n\n") if f.strip()]
        expect(frames, "stream contains no SSE frames")

        done_frames = [i for i, f in enumerate(frames) if is_terminal_frame(f)]
        expect(len(done_frames) == 1,
               f"terminal frame appears in {len(done_frames)} frames, want exactly 1: "
               f"{[frames[i][:60] for i in done_frames]}")
        expect(done_frames[0] == len(frames) - 1,
               f"the terminal frame is {done_frames[0] + 1} of {len(frames)} — not last")

        gov = [i for i, f in enumerate(frames) if "event: ragflowx.governance" in f]
        expect(len(gov) == 1,
               f"governance frame appears {len(gov)} times, want exactly 1")
        expect(gov[0] == done_frames[0] - 1,
               f"governance frame is at {gov[0] + 1}, terminal at {done_frames[0] + 1}: "
               "it must sit immediately before the terminal frame")
        payload = governance_frame_payload(frames[gov[0]])
        self.dump("S5-governance-frame", payload)
        expect(payload.get("trace_id"),
               f"governance frame carries no trace_id: {payload}")
        return (f"frames={len(frames)} terminal_at={done_frames[0] + 1} "
                f"governance_at={gov[0] + 1} keys={sorted(payload)}")

    def gateway_key(self, admin):
        """Create (once) the gateway key this drill authenticates with."""
        if self.state.get("gateway_secret"):
            return self.state["gateway_secret"]
        created = admin.ok("POST", "/api/v1/keys", json_body={
            "name": f"t16-drill-{self.stamp}",
            "token_quota": 5000000,
            "request_quota": 10000,
        })
        secret = created.get("secret") or (created.get("key") or {}).get("secret")
        expect(secret, f"create key returned no secret: {str(created)[:200]}")
        key_id = (created.get("key") or {}).get("id") or created.get("id")
        self.state.update(gateway_secret=secret, gateway_key_id=key_id)
        return secret

    def step_s6(self, admin):
        """S6 建报告并生成 1 个修订：修订终态 + 章节数 > 0"""
        ds_id = require(self.state, "dataset_id")
        payload = {
            "title": f"T16 演练报告-{self.stamp}",
            "question": QUESTION,
            "template_key": TEMPLATE_KEY,
            "template_version": 0,
            "datasets": [{"role": "primary", "dataset_id": ds_id}],
            "outline_confirmation": "required",
        }
        status, body = admin.api("POST", "/api/v1/reports", json_body=payload,
                                 headers={"Idempotency-Key": str(uuid.uuid4())})
        expect(status == 202, f"create report -> {status} {str(body)[:300]}")
        data = body.get("data") or {}
        report_id = (data.get("report") or {}).get("id")
        revision = (data.get("revision") or {}).get("revision") or 1
        job_id = (data.get("job") or {}).get("id")
        expect(report_id and job_id, f"create report returned no ids: {data}")
        self.state.update(report_id=report_id, revision=revision, job_id=job_id)
        self.dump("S6-created", data)

        snapshot = wait_job(admin, self, job_id, "awaiting_outline",
                            self.args.outline_timeout)
        planned = snapshot.get("sections") or []
        expect(planned, "the outline job exposes no sections to confirm")
        self.dump("S6-outline", planned)
        # Confirm the outline as proposed (one row, unchanged): the drill is about the
        # assurance seam downstream, not about outline editing, and an edited title
        # would make the revision's text depend on this script.
        admin.ok("POST",
                 f"/api/v1/reports/{report_id}/revisions/{revision}/outline/confirm",
                 json_body={"sections": [{
                     "section_key": planned[0].get("section_key") or planned[0].get("key"),
                     "title": planned[0].get("title")}]})
        job = wait_job(admin, self, job_id, "terminal", self.args.report_timeout)
        expect(job.get("status") in ("succeeded", "partial"),
               f"report job ended {job.get('status')} error={job.get('error_code')}")
        detail = admin.ok("GET", f"/api/v1/reports/{report_id}/revisions/{revision}")
        self.dump("S6-revision", detail)
        # The revision view carries metrics, not bodies: sections are read from their
        # own surface (`GET …/revisions/:n/sections`), which is also the only reading
        # path a client has. Asserting on the view would have "passed" a revision with
        # no text at all.
        listing = admin.ok(
            "GET", f"/api/v1/reports/{report_id}/revisions/{revision}/sections")
        sections = (listing.get("sections") if isinstance(listing, dict) else listing) or []
        self.dump("S6-sections", sections)
        expect(len(sections) > 0, "generated revision has 0 sections")
        # `content_md` is the body field on the read model; an empty one is the
        # interesting failure, so it is counted rather than assumed.
        words = sum(len((s.get("content_md") or "")) for s in sections)
        expect(words > 0, f"{len(sections)} sections but 0 characters of body")
        metrics = detail.get("metrics") or {}
        expect(int(metrics.get("evidence_sections") or 0) > 0,
               f"no section carries evidence: {metrics.get('evidence_sections')}")
        return (f"report={report_id} rev={revision} status={job.get('status')} "
                f"sections={len(sections)} chars={words}")

    def ensure_assurance_enabled(self, admin):
        """Turn on the tenant whitelist if the drill needs it, and say so.

        `report.assurance_enabled` is a rollout switch, and on 151 it is off: the
        feature ships dark, and an administrator enables it per tenant. A drill that
        reported "assurance is broken" because the whitelist was off would be blaming
        a working refusal (44117) for the absence of a decision. So the drill makes the
        decision, records it, and undoes it in S10.
        """
        for attempt in (1, 2):
            view = admin.ok("GET", "/api/v1/system/settings")
            groups = view.get("groups") or {}
            field = (groups.get("report") or {}).get("assurance_enabled")
            effective = field.get("effective") if isinstance(field, dict) else field
            if str(effective).strip().lower() in ("true", "1", "on", "yes"):
                return "already enabled"
            self.warn("report.assurance_enabled is off; enabling it for this drill "
                      "(S11 restores it)")
            # `expected_revision` is not decoration: settings are versioned, and a patch
            # without it is refused 409/40901 rather than silently clobbering a change
            # someone else made in between.
            status, body = admin.api("PATCH", "/api/v1/system/settings", json_body={
                "expected_revision": view.get("revision"),
                "changes": {"report.assurance_enabled": True},
                "note": (f"t16 live drill {self.stamp}: enable assurance to exercise "
                         "the seam"),
                "confirmed": True,
            })
            if status in (200, 202):
                self.state["assurance_enabled_by_drill"] = True
                return "enabled by the drill"
            if status == 409 and body.get("code") == 40901 and attempt == 1:
                continue
            expect(False, f"PATCH system settings -> {status} {str(body)[:200]}")
        expect(False, "could not enable assurance: settings kept moving under us")

    def step_s7(self, admin):
        """S7 复核 run 跑完：终态四值之一、裁决行 > 0、release_eligibility 已定、advisory"""
        report_id = require(self.state, "report_id")
        revision = require(self.state, "revision")
        self.ensure_assurance_enabled(admin)
        path = f"/api/v1/reports/{report_id}/revisions/{revision}/assurance-runs"
        # `mode` is the *scan scope* (`semantic` | `citation_audit` | `full`); the
        # `advisory` half of the design is the report's policy, reported back as
        # `requested_policy`. Sending "advisory" here is a 400/44105 — the drill found
        # that the two names are easy to conflate, which is exactly why it asserts both.
        status, body = admin.api("POST", path, json_body={"mode": "full"},
                                 headers={"Idempotency-Key": str(uuid.uuid4())})
        expect(status == 202, f"trigger assurance -> {status} {str(body)[:300]}")
        view = body.get("data") or {}
        run_id = view.get("id")
        expect(run_id, f"trigger returned no run id: {view}")
        expect(view.get("requested_policy") == "advisory",
               f"requested_policy={view.get('requested_policy')} (want advisory)")
        expect(view.get("release_eligibility") == "pending",
               f"a queued run already claims {view.get('release_eligibility')}")
        self.state["assurance_run_id"] = run_id
        self.dump("S7-triggered", view)

        # The terminal set and the eligibility set are read from the model, not
        # guessed: `model.ReportAssuranceTerminal` says
        # succeeded|degraded|failed|cancelled, and `release_eligibility` is
        # pending|tier_0|tier_1|tier_2|blocked. A drill that asserted
        # "eligible|not_eligible" would call a correct `blocked` a failure — and that
        # is the value a v0.7.0 run with no calibrated review model actually reaches.
        terminal = ("succeeded", "degraded", "failed", "cancelled")
        deadline = time.time() + self.args.assurance_timeout
        final = {}
        while time.time() < deadline:
            final = admin.ok("GET", f"{path}/{run_id}")
            if final.get("status") in terminal:
                break
            time.sleep(5)
        self.dump("S7-run", final)
        status_now = final.get("status")
        expect(status_now in terminal,
               f"assurance run did not reach a terminal state in "
               f"{self.args.assurance_timeout}s (status={status_now})")
        expect(status_now != "cancelled",
               f"assurance run was cancelled: {final.get('failure_reason')}")
        expect(status_now != "failed",
               f"assurance run failed: {final.get('failure_code')} "
               f"{final.get('failure_reason')}")

        evaluations = self.psql("select count(*) from rgx_report_claim_evaluation "
                                f"where run_id = '{run_id}'").strip()
        expect(evaluations.isdigit() and int(evaluations) > 0,
               f"run {run_id} wrote {evaluations} claim evaluations")
        eligibility = final.get("release_eligibility")
        expect(eligibility in ("tier_0", "tier_1", "tier_2", "blocked"),
               f"a terminal run still says release_eligibility={eligibility!r}")
        self.state["evaluations"] = evaluations

        # `degraded` is a terminal state and must be reported, not smoothed over: it
        # says a stage fell back. On a host with no `report.assurance_review_model`
        # that stage is the semantic judge, every claim comes back `abstained` or
        # `insufficient_evidence`, and `blocked` is the honest consequence. Recording
        # the method keeps "the seam works" from being read as "the judge works".
        method = self.psql(
            "select coalesce(max(method), 'none') from rgx_report_claim_evaluation "
            f"where run_id = '{run_id}'").strip()
        verdicts = self.psql(
            "select support_verdict || '=' || count(*)::text from "
            "rgx_report_claim_evaluation "
            f"where run_id = '{run_id}' group by support_verdict "
            "order by count(*) desc")
        verdicts = " ".join(line for line in verdicts.splitlines() if line.strip())
        self.dump("S7-verdicts", verdicts.strip())
        if status_now == "degraded":
            self.warn(f"run degraded (method={method or 'n/a'}): {verdicts.strip()} — "
                      "the semantic judge abstained, so this run cannot reach tier_1; "
                      "set report.assurance_review_model to exercise the model layer")
        return (f"run={run_id} status={status_now} method={method or 'n/a'} "
                f"claim_evaluations={evaluations} "
                f"policy={final.get('requested_policy')} eligibility={eligibility}")

    def step_s8(self, admin):
        """S8 对 1 条 finding 做裁决：状态落库 + `report.finding_reviewed` 审计行"""
        report_id = require(self.state, "report_id")
        revision = require(self.state, "revision")
        run_id = self.state.get("assurance_run_id") or ""
        view = admin.ok("GET",
                        f"/api/v1/reports/{report_id}/revisions/{revision}/review"
                        + (f"?run_id={run_id}" if run_id else ""))
        findings = view.get("findings") or []
        self.dump("S8-review", view)
        if not findings:
            raise CaseSkip("this revision produced no findings to adjudicate")
        finding = findings[0]
        finding_id = finding.get("id")
        expect(finding_id, f"finding row has no id: {finding}")
        decided = admin.ok(
            "PATCH",
            f"/api/v1/reports/{report_id}/revisions/{revision}/findings/{finding_id}",
            json_body={"status": "acknowledged", "note": f"t16 drill {self.stamp}"})
        self.dump("S8-finding", decided)
        expect(decided.get("status") == "acknowledged",
               f"finding status={decided.get('status')} after adjudication")

        detail = self.psql(
            "select count(*) from rgx_audit_log where action = 'report.finding_reviewed' "
            f"and resource_id = '{report_id}'").strip()
        expect(detail.isdigit() and int(detail) > 0,
               f"no `report.finding_reviewed` audit row for report {report_id} ({detail})")
        return f"finding={finding_id} status=acknowledged audit_rows={detail}"

    def step_s9(self, admin):
        """S9 五种格式导出：各自 sha256 稳定、字节可下载、互相不同"""
        report_id = require(self.state, "report_id")
        revision = require(self.state, "revision")
        base = f"/api/v1/reports/{report_id}/revisions/{revision}/artifacts"
        detail = admin.ok("GET", f"/api/v1/reports/{report_id}/revisions/{revision}")
        rev = detail.get("revision") or {}
        snapshot_hash = (rev.get("execution_snapshot_hash")
                         or rev.get("content_hash") or "")
        if not snapshot_hash:
            self.warn("revision exposes no snapshot hash to compare artifacts against")

        seen = {}
        skipped = []
        for fmt in EXPORT_FORMATS:
            status, body = admin.api("POST", base,
                                     json_body={"format": fmt,
                                                "acknowledge_assurance": True})
            if status in (400, 409) and body.get("code") in (44115, 44116, 44118, 44119,
                                                            44120, 44121, 44122):
                skipped.append(f"{fmt}:{body.get('code')}")
                continue
            expect(200 <= status < 300 and body.get("code") == 0,
                   f"export {fmt} -> {status} {str(body)[:240]}")
            data = body.get("data") or {}
            artifact = data.get("export_artifact") or {}
            expect(artifact.get("artifact_status") == "ready",
                   f"{fmt} artifact_status={artifact.get('artifact_status')}")
            expect(data.get("downloadable") is True, f"{fmt} downloadable=false")
            sha = artifact.get("sha256") or ""
            expect(len(sha) == 64, f"{fmt} sha256={sha!r}")
            resp = requests.get(
                self.base + f"{base}/{artifact.get('id')}/download",
                headers={"Authorization": "Bearer " + admin.token}, timeout=300)
            expect(resp.status_code == 200, f"{fmt} download HTTP {resp.status_code}")
            expect(sha256_bytes(resp.content) == sha,
                   f"{fmt} downloaded bytes do not match the registered sha256")
            expect(resp.content, f"{fmt} downloaded 0 bytes")
            self.keep_bytes(f"S9-{fmt}", resp.content)
            seen[fmt] = sha
        self.dump("S9-artifacts", seen)
        expect(len(seen) >= 4, f"only {len(seen)} formats exported: {sorted(seen)}")
        dupes = [f for f in seen if list(seen.values()).count(seen[f]) > 1]
        expect(not dupes, f"{dupes} share a sha256 with another format — outputs collide")
        if snapshot_hash:
            eq = [f for f, sha in seen.items() if sha == snapshot_hash]
            expect(not eq, f"{eq} artifact equals the revision snapshot hash")
        note = f"{len(seen)} formats" + (f" (skipped {skipped})" if skipped else "")
        return f"{note}: " + ", ".join(f"{f}={seen[f][:8]}" for f in seen)

    def step_s10(self, admin):
        """S10 跨租户读该报告：另一租户的租户管理员读到 404/44109，且不泄露正文"""
        report_id = require(self.state, "report_id")
        title = ""
        try:
            view = admin.ok("GET", f"/api/v1/reports/{report_id}")
            title = (view.get("title") or (view.get("report") or {}).get("title") or "")
        except Exception as exc:  # noqa: BLE001 - the leak check degrades, it does not crash
            self.warn(f"could not read the report title for the leak check: {exc}")

        # The outsider is a *tenant_admin* of its own tenant, not a viewer. That choice is
        # the whole point: a viewer would get 403 because the role matrix says viewers may
        # not read reports, which proves nothing about tenant isolation. A tenant admin
        # *is* allowed to read reports, so a refusal can only come from the row not being
        # in its scope — and 404/44109 ("report not found") is that refusal, while a 403
        # here would be the wrong answer (it would mean we compare roles, not tenants).
        stamp = self.stamp
        outsider_password = "Drill-" + uuid.uuid4().hex[:16]
        tenant = admin.ok("POST", "/api/v1/tenants",
                          json_body={"name": f"t16-drill-{stamp}"})
        tenant_id = tenant.get("id")
        expect(tenant_id, f"tenant create returned no id: {tenant}")
        self.state["foreign_tenant_id"] = tenant_id
        username = f"t16out{stamp}"
        created = admin.ok("POST", "/api/v1/users", json_body={
            "tenant_id": tenant_id, "username": username,
            "password": outsider_password, "role": "tenant_admin"})
        expect(created.get("id"), f"user create returned no id: {created}")

        outsider = Client(self.base)
        outsider.login(username, outsider_password)
        status, body = outsider.api("GET", f"/api/v1/reports/{report_id}")
        self.dump("S10-cross-tenant-read", {"status": status, "body": body})
        expect(status in (403, 404),
               f"cross-tenant read -> {status} (a tenant admin must not read it)")
        expect(status == 404,
               f"cross-tenant read -> {status}; 403 would mean the refusal came from the "
               "role matrix, not from tenant scoping")
        expect(body.get("code") == 44109,
               f"cross-tenant read -> 404 with code {body.get('code')} "
               f"(expected 44109 report not found)")
        blob = json.dumps(body, ensure_ascii=False)
        expect(not (title and title in blob),
               "the refusal echoed the report title — that is a leak, not a refusal")

        # A 404 on one id is weaker on its own than "the report is not in their list".
        listing = outsider.ok("GET", "/api/v1/reports")
        items = listing.get("items") or []
        expect(all(str(i.get("id")) != report_id for i in items),
               "the report appears in another tenant's list")
        self.dump("S10-outsider-list", {"status": 200, "items": len(items)})

        # And the owner still reads it: a 404 that also fires for the owner is not isolation.
        own = admin.ok("GET", f"/api/v1/reports/{report_id}")
        expect((own.get("id") or (own.get("report") or {}).get("id")) == report_id,
               "the owner can no longer read the report")
        return (f"tenant={tenant_id} read=404/44109 "
                f"outsider_list_items={len(items)} title_leak=no owner_read=ok")

    def step_s11(self, admin):
        """S11 收尾：删除演练租户、数据集与检索应用、吊销 API key、复原设置"""
        if self.args.keep:
            return "skipped (--keep)"
        removed = []
        foreign = self.state.get("foreign_tenant_id")
        if foreign:
            # `force=true` is required: the tenant has a user and the last-tenant-admin
            # guard otherwise refuses (409/40905), and then the drill leaves a workspace
            # behind — a cleanup step that only looks like it cleans up.
            status, body = admin.api("DELETE", f"/api/v1/tenants/{foreign}?force=true")
            if status in (200, 204):
                removed.append("tenant")
            else:
                self.warn(f"delete drill tenant {foreign} -> {status} {str(body)[:120]}")
        app_id = self.state.get("search_app_id")
        if app_id:
            status, body = admin.api("DELETE", f"/api/v1/search-apps/{app_id}")
            if status in (200, 204):
                removed.append("search-app")
            else:
                self.warn(f"delete search app {app_id} -> {status} {str(body)[:120]}")
        ds_id = self.state.get("dataset_id")
        if ds_id:
            status, body = admin.api("DELETE", f"/api/v1/datasets/{ds_id}")
            if status in (200, 204):
                removed.append("dataset")
            else:
                self.warn(f"delete dataset {ds_id} -> {status} {str(body)[:120]}")
        key_id = self.state.get("gateway_key_id")
        if key_id:
            # Revoke, not delete: the drill's audit trail must keep referring to a
            # credential that existed. A key that vanished leaves the audit rows
            # pointing at nothing.
            status, body = admin.api("POST", f"/api/v1/keys/{key_id}/revoke",
                                     json_body={})
            if status in (200, 202, 204):
                removed.append("api-key")
            else:
                self.warn(f"revoke key {key_id} -> {status} {str(body)[:120]}")
        restored = ""
        if self.state.get("assurance_enabled_by_drill"):
            # Put the tenant back the way it was found. The drill's job is to prove the
            # seam works, not to leave a product switch on behind it.
            ok = False
            for _ in range(2):
                view = admin.ok("GET", "/api/v1/system/settings")
                status, body = admin.api("PATCH", "/api/v1/system/settings", json_body={
                    "expected_revision": view.get("revision"),
                    "changes": {"report.assurance_enabled": False},
                    "note": f"t16 live drill {self.stamp}: restore the whitelist",
                    "confirmed": True,
                })
                if status in (200, 202):
                    ok = True
                    break
                if status == 409 and body.get("code") == 40901:
                    continue
                self.warn(f"could not restore report.assurance_enabled -> {status} "
                          f"{str(body)[:120]}")
                break
            restored = (" assurance_enabled=restored" if ok
                        else " assurance_enabled=NOT-restored")
        kept = [f"report={self.state.get('report_id')}",
                f"run={self.state.get('assurance_run_id')}"]
        return (f"removed={removed or 'nothing'}{restored}; "
                f"kept for evidence: {', '.join(kept)}")


# -- module-level helpers --------------------------------------------------

def require(state, key):
    value = state.get(key)
    if not value:
        raise CaseSkip(f"blocked: `{key}` is missing (an earlier step did not produce it)")
    return value


def corpus_files(path):
    """The three markdown documents of the drill corpus, sorted for determinism."""
    if not os.path.isdir(path):
        raise EnvUnavailable(f"corpus directory not found: {path}")
    files = sorted(os.path.join(path, n) for n in os.listdir(path)
                   if n.lower().endswith((".md", ".docx")))
    markdown = [f for f in files if f.lower().endswith(".md")]
    return markdown or files


def is_terminal_frame(frame):
    """Does this SSE frame end the stream? Two shapes, both real (doc/304 E62).

    `/v1/chat/completions` ends with `data: [DONE]`. The knowledge routes
    (`/v1/search-apps/:id/completions`, `/v1/agents/:id/completions`) end with
    `data: {"code":0,...,"data":true}`, which is a terminal frame too — a drill that
    only knew `[DONE]` would call a correctly terminated knowledge stream a failure.
    """
    for line in frame.split("\n"):
        line = line.strip()
        if not line.startswith("data:"):
            continue
        payload = line[len("data:"):].strip()
        if payload == "[DONE]":
            return True
        if not payload.startswith("{"):
            continue
        try:
            parsed = json.loads(payload)
        except ValueError:
            continue
        if (isinstance(parsed, dict) and parsed.get("code") == 0
                and parsed.get("data") is True):
            return True
    return False


def governance_frame_payload(frame):
    """The JSON of the `data:` line of a governance frame, or {} if unparseable."""
    for line in frame.split("\n"):
        line = line.strip()
        if not line.startswith("data:"):
            continue
        raw = line[len("data:"):].strip()
        if not raw or raw == "[DONE]":
            continue
        try:
            parsed = json.loads(raw)
        except ValueError:
            continue
        if isinstance(parsed, dict):
            return parsed
    return {}


# What this drill needs, in the order a reader has to supply it. `--offline` prints this
# table, so a laptop with no drill host can still say what the run would require.
REQUIREMENTS = (
    Requirement("--base", "RGX_VERIFY_BASE", "console base URL", default=DEFAULT_BASE),
    Requirement("--user", "RGX_VERIFY_USER", "console account", default=DEFAULT_USER),
    Requirement("--password", "RGX_VERIFY_PASS", "console password", secret=True),
    Requirement("--corpus", "RGX_VERIFY_CORPUS", "the three markdown files S1 uploads",
                default=DEFAULT_CORPUS),
    Requirement("--ssh-host", "RGX_DRILL_HOST", "ssh host for the psql asserts",
                default="192.168.4.151"),
    Requirement("--ssh-user", "RGX_DRILL_USER", "ssh user", default="test"),
    Requirement("--ssh-password", "RGX_DRILL_SSH_PASS",
                "sudo password (only S4/S7/S8 reach psql)", secret=True),
)

STEPS_DOC = (
    ("S1", "upload the corpus to a fresh dataset, parse, chunk count > 0"),
    ("S2", "bind a search app to it and prove retrieval hits > 0"),
    ("S3", "gateway non-stream completion: answer + reference + governance headers"),
    ("S4", "replay on the streaming route: byte-identical + one idempotency row"),
    ("S5", "the terminal frame is unique and last, governance frame before it"),
    ("S6", "create a report, confirm the outline, wait for the revision to publish"),
    ("S7", "run assurance review: terminal status, verdict rows, eligibility, policy"),
    ("S8", "acknowledge one finding: status stored + one audit row"),
    ("S9", "export five formats: stable sha256, downloadable, mutually different"),
    ("S10", "another tenant's admin reads the report: 404/44109, no title leak"),
    ("S11", "cleanup: tenant, dataset, search app, API key, restored settings"),
)


def parse_args(argv):
    parser = argparse.ArgumentParser(description="T16 live drill (doc/305 §4)")
    parser.add_argument("--base", default=os.environ.get("RGX_VERIFY_BASE", DEFAULT_BASE))
    parser.add_argument("--user", default=os.environ.get("RGX_VERIFY_USER", DEFAULT_USER))
    parser.add_argument("--password", default=os.environ.get("RGX_VERIFY_PASS", ""),
                        help="console password (or RGX_VERIFY_PASS); no default is stored")
    parser.add_argument("--corpus", default=os.environ.get("RGX_VERIFY_CORPUS", DEFAULT_CORPUS))
    parser.add_argument("--offline", action="store_true",
                        help="print the contract (what is required, which steps run) and exit 0")
    parser.add_argument("--only", default="",
                        help="comma-separated step ids, e.g. S1,S3,S4,S5")
    parser.add_argument("--keep", action="store_true", help="do not delete the drill data")
    parser.add_argument("--parse-timeout", type=int, default=900)
    parser.add_argument("--outline-timeout", type=int, default=600)
    parser.add_argument("--report-timeout", type=int, default=2400)
    parser.add_argument("--assurance-timeout", type=int, default=1800)
    parser.add_argument("--ssh-host", default=os.environ.get("RGX_DRILL_HOST", "192.168.4.151"))
    parser.add_argument("--ssh-user", default=os.environ.get("RGX_DRILL_USER", "test"))
    parser.add_argument("--ssh-password",
                        default=os.environ.get("RGX_DRILL_SSH_PASS", ""),
                        help="sudo password for the psql asserts (or RGX_DRILL_SSH_PASS)")
    parser.add_argument("--pg-container", default="rgx-drill-pg")
    parser.add_argument("--pg-db", default="rgx_test")
    parser.add_argument("--pg-user", default="rgx_test")
    args = parser.parse_args(argv)
    args.only = {s.strip().upper() for s in args.only.split(",") if s.strip()}
    return args


def main(argv):
    args = parse_args(argv)
    if args.offline:
        return print_contract("T16 live drill (doc/305 §4)", REQUIREMENTS, STEPS_DOC,
                              notes=("--offline never logs in and never writes evidence.",
                                     "a partial run (--only …) exits 3, never 0.",
                                     "the one line this drill must not print is "
                                     "'11/11 PASS' when a step was skipped."))
    try:
        # Resolve credentials before touching the network, so a missing variable is a
        # usage sentence (exit 2) rather than a login failure blamed on the host.
        password = Requirement("--password", "RGX_VERIFY_PASS", "console password",
                               secret=True).resolve(args)
        if not os.path.isdir(args.corpus):
            raise Usage("--corpus (or RGX_VERIFY_CORPUS) does not exist: %s" % args.corpus)
    except Usage as exc:
        print(safe("usage: %s" % exc))
        return 2
    args.password = password
    drill = Drill(args)
    admin = Client(drill.base)
    try:
        admin.login(args.user, args.password)
    except EnvUnavailable as exc:
        print(f"environment unavailable: {exc}")
        return 2
    print(f"T16 live drill {drill.stamp} -> {drill.base}")
    print(f"evidence dir: {drill.dir}")
    try:
        return drill.run(admin)
    except EnvUnavailable as exc:
        print(f"environment unavailable mid-drill: {exc}")
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
