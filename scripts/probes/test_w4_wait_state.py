#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Tests for `w4_wait_state` — the wait classifier the W-4 pilot writes through.

What this file is allowed to do, and what it is not:

* it imports `w4_wait_state` only, which has no network code and no `requests` import;
* it never imports, executes or shells out to `w4_wiki_pilot.py` (doc/304 E97 is the reason:
  a gate that can start a compile is a gate that can destroy a dataset);
* every case is a hand-written document row plus an injected `fetch`, so the reverse cases
  ("run ended but the document is busy", "run absent but busy", "no state fields",
  "timeout") are reproducible on a laptop with no engine.

`probes.sh` runs this file, so the safety properties below are checked by the same gate
that checks the probes carry no credential. If one of these tests is deleted, the gate
goes red — that is deliberate: the classifier is the difference between the 2026-11-23
loss (FAILED document, 0 chunks, 27 products deleted) and a refusal.

Usage:
    python scripts/probes/test_w4_wait_state.py          # exit 0 + SELFTEST OK
    PROBES_SH=path/to/probes.sh python .../test_w4_wait_state.py   # separation check
"""

from __future__ import annotations

import io
import os
import ast
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)

import w4_wait_state as ws  # noqa: E402

PROBES_SH = os.environ.get("PROBES_SH") or os.path.join(
    REPO, "scripts", "release-gate", "probes.sh")


def doc(**kw):
    row = {"id": "d1", "name": "a.md"}
    row.update(kw)
    return row


class FakeClock(object):
    """monotonic() that advances only when the waiter sleeps."""

    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class ClassifierStates(unittest.TestCase):
    """The four answers, including the ones the old `run`-only check got wrong."""

    def test_completed_and_not_running_is_idle(self):
        state = ws.classify(doc(run="0", ingestion_status="COMPLETED", progress=1,
                                chunk_count=7))
        self.assertEqual(state.state, ws.STATE_IDLE)
        self.assertEqual(state.reason, "status_completed")

    def test_never_parsed_is_idle_with_its_own_reason(self):
        state = ws.classify(doc(run="", ingestion_status="", progress=0))
        self.assertEqual(state.state, ws.STATE_IDLE)
        self.assertEqual(state.reason, "never_parsed")

    def test_failed_is_failed_even_at_progress_one(self):
        """The 2026-11-23 row: a timed-out task reaches progress 1.0 *and* FAILED."""
        state = ws.classify(doc(run="0", ingestion_status="FAILED", progress=1.0,
                                progress_msg="", chunk_count=0))
        self.assertEqual(state.state, ws.STATE_FAILED)
        self.assertEqual(state.reason, "engine_said_failed")

    def test_cancelled_is_a_failure_not_a_go(self):
        self.assertEqual(ws.classify(doc(ingestion_status="CANCELLED")).state,
                         ws.STATE_FAILED)

    def test_run_running_beats_a_stale_completed_status(self):
        """Reverse case ①: run says running while the status field lags behind."""
        state = ws.classify(doc(run="1", ingestion_status="COMPLETED", progress=1))
        self.assertEqual(state.state, ws.STATE_BUSY)
        self.assertEqual(state.reason, "run_running")

    def test_run_absent_but_status_says_parsing_is_busy(self):
        """Reverse case ②: the field the old check relied on is gone on 1.0.0 builds."""
        for status in ("RUNNING", "PROCESSING", "STARTED", "PARSING", "MERGING"):
            state = ws.classify(doc(ingestion_status=status, progress=0.4))
            self.assertEqual(state.state, ws.STATE_BUSY, status)
            self.assertEqual(state.reason, "status_busy")

    def test_queued_and_pending_are_busy(self):
        for status in ("PENDING", "QUEUED", "UNSTART", "NOT_STARTED"):
            self.assertEqual(ws.classify(doc(ingestion_status=status)).state, ws.STATE_BUSY)

    def test_no_state_fields_is_unknown(self):
        """Reverse case ③: nothing to read is not 'nothing is running'."""
        for row in ({}, {"id": "d1", "name": "a.md"}, {"run": "", "ingestion_status": ""}):
            state = ws.classify(row)
            self.assertEqual(state.state, ws.STATE_UNKNOWN, row)
            self.assertEqual(state.reason, "no_state_fields")

    def test_missing_and_non_object_rows_are_unknown(self):
        self.assertEqual(ws.classify(None).reason, "document_missing")
        self.assertEqual(ws.classify("a.md").reason, "document_not_an_object")

    def test_progress_one_without_status_is_unknown_not_idle(self):
        state = ws.classify(doc(run="0", ingestion_status="", progress=1.0))
        self.assertEqual(state.state, ws.STATE_UNKNOWN)
        self.assertEqual(state.reason, "done_without_status")

    def test_partial_progress_without_status_is_unknown(self):
        state = ws.classify(doc(ingestion_status="", progress=0.72))
        self.assertEqual(state.state, ws.STATE_UNKNOWN)
        self.assertEqual(state.reason, "progress_without_status")

    def test_unreadable_progress_is_unknown(self):
        self.assertEqual(ws.classify(doc(ingestion_status="", progress="n/a")).state,
                         ws.STATE_UNKNOWN)

    def test_unrecognised_status_is_unknown(self):
        """Vocabulary drift on a new build must not read as idle."""
        state = ws.classify(doc(ingestion_status="HANDLING_V2", progress=0.5))
        self.assertEqual(state.state, ws.STATE_UNKNOWN)
        self.assertEqual(state.reason, "unrecognised_status")

    def test_run_running_without_status_is_busy(self):
        self.assertEqual(ws.classify(doc(run="1", progress=0.1)).state, ws.STATE_BUSY)

    def test_row_with_only_run_terminal_code_is_unknown(self):
        self.assertEqual(ws.classify(doc(run="3", ingestion_status="", progress="")).state,
                         ws.STATE_UNKNOWN)

    def test_states_are_the_documented_four(self):
        seen = set()
        for row in (doc(ingestion_status="COMPLETED", progress=1), doc(ingestion_status="RUNNING"),
                    doc(ingestion_status="FAILED"), doc(ingestion_status="???"),
                    None, {}, doc(progress=1.0)):
            seen.add(ws.classify(row).state)
        self.assertEqual(seen, {ws.STATE_IDLE, ws.STATE_BUSY, ws.STATE_FAILED,
                                ws.STATE_UNKNOWN})


class Assessment(unittest.TestCase):
    """A set is safe only when every member is idle; unknown is not idle."""

    def test_all_idle_is_safe(self):
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1)}
        got = ws.assess(rows, ["a.md"])
        self.assertTrue(got.safe)

    def test_one_unknown_makes_the_set_unsafe(self):
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1),
                "b.md": doc(ingestion_status="")}
        got = ws.assess(rows, ["a.md", "b.md"])
        self.assertFalse(got.safe)
        self.assertEqual(got.unknown, ["b.md"])

    def test_a_name_the_listing_forgot_is_unknown(self):
        got = ws.assess({"a.md": doc(ingestion_status="COMPLETED")}, ["a.md", "b.md"])
        self.assertFalse(got.safe)
        self.assertEqual(got.states["b.md"].reason, "document_missing")

    def test_empty_name_set_is_not_safe(self):
        self.assertFalse(ws.assess({}, []).safe)

    def test_bucket_lists_are_disjoint_and_complete(self):
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1),
                "b.md": doc(ingestion_status="RUNNING"),
                "c.md": doc(ingestion_status="FAILED"),
                "d.md": doc()}
        got = ws.assess(rows, ["a.md", "b.md", "c.md", "d.md"])
        self.assertEqual(sorted(got.idle), ["a.md"])
        self.assertEqual(sorted(got.busy), ["b.md"])
        self.assertEqual(sorted(got.failed), ["c.md"])
        self.assertEqual(sorted(got.unknown), ["d.md"])
        total = len(got.idle) + len(got.busy) + len(got.failed) + len(got.unknown)
        self.assertEqual(total, 4)


class ReplayOf20261123(unittest.TestCase):
    """The recorded rows from the run that lost 27 products (doc/304 E97).

    Three documents were bound and ingested: two COMPLETED (9 and 7 chunks), one reached
    progress 0.72 and FAILED with an empty progress_msg. The old wait read `run` only,
    saw `0`, and let the write proceed; the merge pass then deleted 27 products, rewrote 0
    and reported `state: completed`. Replaying those exact rows must be refused twice over:
    once as a FAILED document, once as an upstream/documents contradiction.
    """

    def rows(self):
        return {
            "安全生产社会化服务机构管理办法.md": doc(ingestion_status="COMPLETED", progress=1,
                                             chunk_count=9, run="0"),
            "山东省安全生产举报办法.md": doc(ingestion_status="COMPLETED", progress=1,
                                  chunk_count=7, run="0"),
            "山东省属企业安全生产考核标准.md": doc(ingestion_status="FAILED", progress=0.72,
                                      chunk_count=0, progress_msg="", run="0"),
        }

    def test_the_old_run_only_check_would_have_said_idle(self):
        for row in self.rows().values():
            self.assertEqual(str(row.get("run") or "").upper() in ("1", "RUNNING"), False)

    def test_the_classifier_refuses_the_set(self):
        got = ws.assess(self.rows(), list(self.rows()))
        self.assertFalse(got.safe)
        self.assertEqual(got.failed, ["山东省属企业安全生产考核标准.md"])

    def test_the_pre_write_gate_names_the_contradiction(self):
        got = ws.assess_before_write(self.rows(), list(self.rows()), {"state": "completed"})
        self.assertFalse(got.safe)
        self.assertTrue(any("FAILED" in c for c in got.contradictions))

    def test_dropping_the_failed_document_makes_the_set_idle(self):
        """Which is why deleting it is the wrong repair: the run then sees 2 of 3 sources."""
        rows = self.rows()
        rows.pop("山东省属企业安全生产考核标准.md")
        got = ws.assess_before_write(rows, list(rows), {"state": "completed"})
        self.assertTrue(got.safe)
        self.assertTrue(got.warnings)


class UpstreamCompletedIsNotAGo(unittest.TestCase):
    """Requirement 4: `state: completed` may not overrule the documents."""

    def test_completed_upstream_with_failed_documents_is_refused(self):
        rows = {"a.md": doc(ingestion_status="FAILED", progress=1.0)}
        got = ws.assess_before_write(rows, ["a.md"], {"state": "completed"})
        self.assertFalse(got.safe)
        self.assertEqual(len(got.contradictions), 1)
        self.assertIn("FAILED", got.contradictions[0])

    def test_completed_upstream_with_documents_still_parsing_is_refused(self):
        rows = {"a.md": doc(ingestion_status="RUNNING", progress=0.6)}
        got = ws.assess_before_write(rows, ["a.md"], {"state": "completed"})
        self.assertFalse(got.safe)
        self.assertIn("still", got.contradictions[0])

    def test_completed_upstream_with_clean_documents_is_allowed_but_warns(self):
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=9)}
        got = ws.assess_before_write(rows, ["a.md"], {"state": "completed"})
        self.assertTrue(got.safe)
        self.assertEqual(got.contradictions, [])
        self.assertTrue(any("not a claim that artifacts exist" in w for w in got.warnings))

    def test_completed_upstream_with_zero_chunks_warns(self):
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=0)}
        got = ws.assess_before_write(rows, ["a.md"], None)
        self.assertTrue(got.safe)
        self.assertTrue(any("chunk_count=0" in w for w in got.warnings))

    def test_compilation_vocabulary(self):
        self.assertEqual(ws.compilation_state(None), "absent")
        self.assertEqual(ws.compilation_state({}), "absent")
        self.assertEqual(ws.compilation_state({"state": "completed"}), "completed")
        self.assertEqual(ws.compilation_state({"state": "RUNNING"}), "busy")
        self.assertEqual(ws.compilation_state({"state": "weird"}), "unknown")
        self.assertEqual(ws.compilation_state("completed"), "unknown")
        # 'completed' is never upgraded into 'there are products' anywhere in this module
        self.assertNotIn("artifacts", ws.compilation_state({"state": "completed"}))


class RepairRedispatch(unittest.TestCase):
    """doc/304 §7 item 34: a FAILED set is repairable — and repair is not a wider `safe`.

    E101 shipped the four-state gate and, with it, an unintended prohibition: a document the
    engine gave up on refused *every* write, including the one action that exists to fix it
    (re-dispatch parsing). The 2026-11-23 workaround was deleting documents, and the same
    section records that deleting a document is how 27 products disappeared.

    So the repair question exists. What follows pins it in both directions: it must say yes
    where the engine has a repair, and it must never become item 34's forbidden downgrade
    ("`failed` 也算 safe"), which would cost S6 its contradiction check.
    """

    def failed_set(self):
        return {"a.md": doc(ingestion_status="FAILED", progress=0.72, chunk_count=0)}

    def test_a_failed_document_is_repairable_and_still_not_safe(self):
        rows = self.failed_set()
        gate = ws.assess_before_write(rows, ["a.md"], None)
        self.assertFalse(gate.safe, "a FAILED document must keep refusing the bind write")
        got = ws.safe_to_redispatch(gate)
        self.assertTrue(got.allowed)
        self.assertEqual(got.reason, "")
        self.assertEqual(got.repair, ["a.md"])
        self.assertEqual(got.action, ws.ACTION_REDISPATCH)

    def test_a_mixed_set_repairs_only_the_failed_document(self):
        rows = {
            "a.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=9),
            "b.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=7),
            "c.md": doc(ingestion_status="FAILED", progress=0.72, chunk_count=0),
        }
        got = ws.safe_to_redispatch(ws.assess_before_write(rows, list(rows), None))
        self.assertTrue(got.allowed)
        self.assertEqual(got.repair, ["c.md"])
        self.assertEqual(sorted(got.left_alone), ["a.md", "b.md"])

    def test_cancelled_counts_as_repairable(self):
        rows = {"a.md": doc(ingestion_status="CANCELLED", progress=0.4)}
        got = ws.safe_to_redispatch(ws.assess(rows, ["a.md"]))
        self.assertTrue(got.allowed)
        self.assertEqual(got.repair, ["a.md"])

    def test_a_clean_set_has_nothing_to_repair(self):
        """The width guard. Without it this predicate is `safe` under a second name."""
        rows = {"a.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=9)}
        gate = ws.assess_before_write(rows, ["a.md"], None)
        self.assertTrue(gate.safe)
        got = ws.safe_to_redispatch(gate)
        self.assertFalse(got.allowed)
        self.assertEqual(got.reason, "nothing_to_repair")
        self.assertEqual(got.left_alone, ["a.md"])

    def test_busy_refuses_the_repair(self):
        """A second dispatch behind a running parse queues; the config write hits code 102."""
        rows = {"a.md": doc(ingestion_status="RUNNING", progress=0.5)}
        got = ws.safe_to_redispatch(ws.assess(rows, ["a.md"]))
        self.assertFalse(got.allowed)
        self.assertEqual(got.reason, "busy")
        self.assertIn("a.md", got.blocked)

    def test_unknown_refuses_the_repair(self):
        rows = {"a.md": doc()}  # no state fields at all
        got = ws.safe_to_redispatch(ws.assess(rows, ["a.md"]))
        self.assertFalse(got.allowed)
        self.assertEqual(got.reason, "unknown")
        self.assertEqual(got.blocked, {"a.md": "no_state_fields"})

    def test_a_name_the_listing_forgot_refuses_the_repair(self):
        got = ws.safe_to_redispatch(ws.assess({"a.md": doc(ingestion_status="FAILED")},
                                             ["a.md", "gone.md"]))
        self.assertFalse(got.allowed)
        self.assertEqual(got.reason, "unknown")
        self.assertEqual(got.blocked, {"gone.md": "document_missing"})

    def test_empty_name_set_is_not_permission(self):
        got = ws.safe_to_redispatch(ws.assess({}, []))
        self.assertFalse(got.allowed)
        self.assertEqual(got.reason, "no_documents")

    def test_a_non_assessment_is_refused_not_defaulted(self):
        for wrong in (None, {}, {"a.md": doc(ingestion_status="FAILED")}, "failed"):
            self.assertFalse(ws.safe_to_redispatch(wrong).allowed)
            self.assertEqual(ws.safe_to_redispatch(wrong).reason, "no_reading")

    def test_a_contradiction_is_carried_not_a_veto(self):
        """'upstream says completed over FAILED documents' is the disease, not the reason
        to withhold treatment — but the bind path and S6 must still refuse it."""
        rows = self.failed_set()
        gate = ws.assess_before_write(rows, ["a.md"], {"state": "completed"})
        got = ws.safe_to_redispatch(gate)
        self.assertTrue(got.allowed)
        self.assertEqual(len(got.contradictions), 1)
        self.assertIn("FAILED", got.contradictions[0])
        self.assertIn("carried", got.describe())
        self.assertFalse(gate.safe)

    def test_the_two_questions_never_collapse_into_one(self):
        """The invariant item 34 asks for, over every shape this module can produce."""
        sets = [self.failed_set(),
                {"a.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=9)},
                {"a.md": doc(ingestion_status="RUNNING", progress=0.5)},
                {"a.md": doc()},
                {"a.md": doc(ingestion_status="CANCELLED", progress=0.4),
                 "b.md": doc(ingestion_status="COMPLETED", progress=1, chunk_count=3)}]
        for rows in sets:
            names = list(rows)
            gate = ws.assess_before_write(rows, names, {"state": "completed"})
            verdict = ws.safe_to_redispatch(gate)
            # `safe` never becomes true for a set that has a FAILED document…
            if gate.failed:
                self.assertFalse(gate.safe, rows)
            # …and the repair question never authorises a write on a healthy set, which is
            # what would make it `safe` under another name.
            if gate.safe:
                self.assertFalse(verdict.allowed, rows)
            # a refusal is never silent: it names itself
            if not verdict.allowed:
                self.assertTrue(verdict.reason, rows)

    def test_verdict_does_not_mutate_the_assessment(self):
        gate = ws.assess_before_write(self.failed_set(), ["a.md"], {"state": "completed"})
        before = (dict(gate.states), list(gate.contradictions), list(gate.warnings))
        ws.safe_to_redispatch(gate)
        ws.safe_to_redispatch(gate)
        after = (dict(gate.states), list(gate.contradictions), list(gate.warnings))
        self.assertEqual(before, after)

    def test_describe_names_the_action_and_the_verdict(self):
        allowed = ws.safe_to_redispatch(ws.assess(self.failed_set(), ["a.md"]))
        self.assertIn("redispatch=allowed", allowed.describe())
        refused = ws.safe_to_redispatch(ws.assess({"a.md": doc(progress=0.5)}, ["a.md"]))
        self.assertIn("redispatch=refused(unknown)", refused.describe())


class WaitLoop(unittest.TestCase):
    """Fail closed: timeout, failure, missing rows and a broken read are all refusals."""

    def test_waits_through_busy_then_returns_safe(self):
        clock = FakeClock()
        reads = [{"a.md": doc(ingestion_status="RUNNING", progress=0.2)},
                 {"a.md": doc(ingestion_status="RUNNING", progress=0.8)},
                 {"a.md": doc(ingestion_status="COMPLETED", progress=1)}]
        calls = {"n": 0}

        def fetch():
            row = reads[min(calls["n"], len(reads) - 1)]
            calls["n"] += 1
            return row

        got = ws.wait_for_idle(fetch, ["a.md"], timeout=100, poll=5,
                               clock=clock, sleep=clock.sleep)
        self.assertTrue(got.safe)
        self.assertEqual(got.attempts, 3)
        self.assertEqual(clock.now, 10.0)

    def test_timeout_is_a_refusal_and_says_so(self):
        """Reverse case ④: the loop ran out of patience — it must not call that idle."""
        clock = FakeClock()

        def fetch():
            return {"a.md": doc(ingestion_status="RUNNING", progress=0.5)}

        got = ws.wait_for_idle(fetch, ["a.md"], timeout=30, poll=10,
                               clock=clock, sleep=clock.sleep)
        self.assertFalse(got.safe)
        self.assertEqual(got.reason, "timeout_after_30s")
        self.assertIn("busy=", got.why())
        self.assertIn("not safe to write", got.why())

    def test_a_failed_document_fails_fast_instead_of_waiting_an_hour(self):
        clock = FakeClock()

        def fetch():
            return {"a.md": doc(ingestion_status="FAILED", progress=0.72)}

        got = ws.wait_for_idle(fetch, ["a.md"], timeout=3600, poll=10,
                               clock=clock, sleep=clock.sleep)
        self.assertFalse(got.safe)
        self.assertEqual(got.reason, "documents_failed")
        self.assertEqual(got.attempts, 1)
        self.assertEqual(clock.now, 0.0)

    def test_a_document_missing_from_the_listing_eventually_times_out(self):
        clock = FakeClock()
        got = ws.wait_for_idle(lambda: {}, ["a.md"], timeout=20, poll=10,
                               clock=clock, sleep=clock.sleep)
        self.assertFalse(got.safe)
        self.assertEqual(got.reason, "timeout_after_20s")
        self.assertIn("document_missing", got.last.describe())

    def test_empty_name_set_never_returns_safe(self):
        clock = FakeClock()
        got = ws.wait_for_idle(lambda: {}, [], timeout=10, clock=clock, sleep=clock.sleep)
        self.assertFalse(got.safe)
        self.assertEqual(got.reason, "no_documents")
        self.assertEqual(got.attempts, 0)

    def test_a_broken_read_propagates_instead_of_reading_as_idle(self):
        def fetch():
            raise IOError("GET /datasets/x/documents -> 502")

        clock = FakeClock()
        with self.assertRaises(IOError):
            # `sleep=clock.sleep` is not decoration: with a fake clock and a real sleep a
            # swallowed read error would loop forever instead of failing the test.
            ws.wait_for_idle(fetch, ["a.md"], timeout=10, clock=clock, sleep=clock.sleep)

    def test_on_poll_receives_the_unsafe_assessment(self):
        clock = FakeClock()
        seen = []
        reads = [{"a.md": doc(ingestion_status="RUNNING")},
                 {"a.md": doc(ingestion_status="COMPLETED", progress=1)}]
        calls = {"n": 0}

        def fetch():
            row = reads[min(calls["n"], 1)]
            calls["n"] += 1
            return row

        got = ws.wait_for_idle(fetch, ["a.md"], timeout=100, poll=1, clock=clock,
                               sleep=clock.sleep, on_poll=lambda a, e: seen.append((a, e)))
        self.assertTrue(got.safe)
        self.assertEqual(len(seen), 1)
        self.assertFalse(seen[0][0].safe)


class Separation(unittest.TestCase):
    """Requirement 6: the gate may run this file, and must never reach the writer."""

    def _code_lines(self, path):
        with io.open(path, encoding="utf-8") as handle:
            for number, line in enumerate(handle, 1):
                yield number, line.split("#", 1)[0]

    def test_this_module_imports_nothing_that_can_reach_a_host(self):
        """AST, not grep: the prose above quotes `w4_wiki_pilot.py`, the imports must not."""
        import ast
        path = os.path.join(HERE, "w4_wait_state.py")
        with io.open(path, encoding="utf-8") as handle:
            tree = ast.parse(handle.read(), path)
        imported = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imported.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
                imported.add(node.module.split(".")[0])
        self.assertEqual(imported, {"time",
                                    "__future__"},
                         "w4_wait_state must stay pure: stdlib only, no writer, no HTTP")

    def test_probes_sh_never_executes_the_writer(self):
        self.assertTrue(os.path.exists(PROBES_SH), PROBES_SH)
        offenders = [(number, line.strip()) for number, line in self._code_lines(PROBES_SH)
                     if "w4_wiki_pilot" in line]
        self.assertEqual(offenders, [],
                         "probes.sh must not run the writer (doc/304 E97: a gate that can "
                         "start a compile is a gate that can lose a dataset)")

    def test_probes_sh_runs_these_tests(self):
        with io.open(PROBES_SH, encoding="utf-8") as handle:
            source = handle.read()
        self.assertIn("test_w4_wait_state.py", source,
                      "probes.sh has to run the wait-state tests, or nothing protects them")

    def test_the_writer_asks_both_questions(self):
        """Repair was added *beside* the contradiction gate, not on top of it.

        Static on purpose: importing the writer would hand a gate the ability to start a
        compile (doc/304 E97), so this reads the file's AST and nothing else. Two calls are
        required — `assess_before_write` (the bind/contradiction gate S6 depends on) and
        `safe_to_redispatch` (the repair permission). Item 34's failure mode is exactly the
        second replacing the first.
        """
        writer = os.path.join(HERE, "w4_wiki_pilot.py")
        self.assertTrue(os.path.exists(writer), writer)
        with io.open(writer, encoding="utf-8") as handle:
            tree = ast.parse(handle.read(), writer)
        asked = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute):
                owner = node.func.value
                if isinstance(owner, ast.Name) and owner.id == "wait_state":
                    asked.add(node.func.attr)
        self.assertIn("assess_before_write", asked,
                      "the pre-write gate must stay the contradiction check: repair may not "
                      "replace it (doc/304 §7 item 34)")
        self.assertIn("safe_to_redispatch", asked,
                      "S4 must ask the repair question, or a FAILED dataset is unrepairable "
                      "again and the only way back is deleting documents")

    def test_the_batch_never_lands_on_the_single_machine(self):
        """The Compiler's chat model must not resolve to `gpustack-local` (doc/304 E107).

        E107 was an operator decision, and 2026-11-24 moved the batch off AMD as well
        (E125). The invariant that stayed is the red line itself: a rewrite-type batch
        may not be pointed at the one machine behind the `:18081` gate. Reading the two
        constants as AST literals keeps this check off the network and out of the
        writer (E97) while still failing if someone re-pins the batch there.
        """
        writer = os.path.join(HERE, "w4_wiki_pilot.py")
        with io.open(writer, encoding="utf-8") as handle:
            tree = ast.parse(handle.read(), writer)
        consts = {}
        for node in tree.body:
            if isinstance(node, ast.Assign) and len(node.targets) == 1:
                target = node.targets[0]
                if isinstance(target, ast.Name) and isinstance(node.value, ast.Constant):
                    consts[target.id] = node.value.value
        self.assertEqual(consts.get("CHAT_INSTANCE_NAME") != "gpustack-local", True,
                         "the Compiler batch must not be pinned to gpustack-local "
                         "(doc/304 E107 / item 32)")
        self.assertTrue(consts.get("CHAT_MODEL_NAME"),
                        "CHAT_MODEL_NAME must name the model the batch uses")
        self.assertNotEqual(consts.get("CHAT_MODEL_NAME"),
                            consts.get("EMBEDDING_MODEL_NAME"),
                            "one name for both roles hides which model answers")


def main():
    suite = unittest.TestLoader().loadTestsFromModule(sys.modules[__name__])
    result = unittest.TextTestRunner(verbosity=2, stream=sys.stdout).run(suite)
    total = result.testsRun
    if not result.wasSuccessful():
        print("SELFTEST FAIL: %d of %d case(s) failed" % (len(result.failures) + len(result.errors), total))
        return 1
    print("wait-state selftest: %d case(s), 0 failed (nothing was contacted)" % total)
    print("SELFTEST OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
