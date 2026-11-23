#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Is a document safe to write under? — the pure half of the W-4 pilot's wait.

Why this file exists at all (doc/304 §4 E97, §5.3, doc/305 §2.19):

On 2026-11-23 the W-4 pilot bound three documents, ingested them, and then destroyed a
good reading. The documents had reached `COMPLETED` with 27 products; a re-ingest turned
one document `FAILED` with 0 chunks, the merge pass then deleted the 27 products and
rewrote 0, and still reported `state: completed`. One of the reasons the write was allowed
to proceed was that the driver asked the wrong question:

    document_running(doc) := str(doc.get("run") or "").upper() in ("1", "RUNNING")

`run` is RAGFlow's *task* flag. A document is not safe to reconfigure because `run` is
`0` — it is safe because its own ingestion state says a parse finished and nothing is
queued behind it. The two disagree in exactly the situations that cost data: after a
timed-out task (`progress` 1.0, `ingestion_status` FAILED), after a merge that reports
`completed` with no products, and on builds that no longer publish `run` at all
(see `NormalizeParseStatus` in internal/provider/ragflow/ragflow.go: the field is
optional and `ingestion_status` is the real one).

So the classifier is a pure function of one document row, with four answers and no
fifth:

    idle     no task is running and nothing claims a half-written result
    busy     a parse/merge is running or queued — a config write is refused (code 102)
    failed   the engine gave up; waiting longer cannot help
    unknown  the row does not say, so it is *not* idle (this is the whole point)

`unknown` is deliberately reachable from several directions — row missing, no state
fields at all, `progress` 1.0 with no `ingestion_status` (a timed-out task also reaches
1.0), an unrecognised status string (vocabulary drift on a new engine build). Every one
of them collapses to "I cannot tell", and the caller must treat that as "not safe".

The vocabulary is not invented here; it is the set the Go provider already maps in
`NormalizeParseStatus`:
    done     COMPLETED DONE PARSED SUCCESS SUCCEEDED
    failed   FAILED FAIL ERROR      cancelled CANCELED CANCELLED STOPPED
    pending  PENDING QUEUED UNSTART NOT_STARTED
    running  RUNNING PROCESSING STARTED

There is a *second* question, and it is deliberately not a fifth state (doc/304 §7 item
34, E108, which named this gap the day E101 shipped):

    the four states answer "may I write config under these documents?"
    `safe_to_redispatch` answers "may I dispatch parsing again?"

The first refuses a FAILED document and must keep refusing it — a config write under a
half-written result is how the 2026-11-23 run lost 27 products. The second is the engine's
own repair for exactly that document: a FAILED row carries `run=0`, so nothing is running
and nothing hits code 102; refusing it meant a dataset that once failed could never be
repaired through the pilot, and the workaround on 2026-11-23 was deleting documents. So the
repair question exists, and it is **narrower** than `safe`: it requires a FAILED document
(the thing being repaired) and it refuses on busy/unknown for the same reasons as before.
What it must never become is item 34's forbidden downgrade — "`failed` 也算 safe".

This module imports nothing but the standard library, and it never talks to a host. That
is on purpose: `probes.sh` may run this file's tests, and it must never gain the ability
to start a compile through them. The writer (`w4_wiki_pilot.py`) imports *this*, not the
other way round.
"""

from __future__ import annotations

import time

STATE_IDLE = "idle"
STATE_BUSY = "busy"
STATE_FAILED = "failed"
STATE_UNKNOWN = "unknown"

# `run` is the legacy parse-task flag: "1" is RUNNING on 1.0.0, and the Go provider's
# fallback codes are 0 queued / 1 parsing / 2 cancelled / 3 done / 4 failed.
RUN_BUSY = ("1", "RUNNING", "PROCESSING", "STARTED", "PARSING")
RUN_NOT_BUSY = ("", "0", "2", "3", "4")

STATUS_DONE = ("COMPLETED", "DONE", "PARSED", "SUCCESS", "SUCCEEDED")
STATUS_FAILED = ("FAILED", "FAIL", "ERROR")
STATUS_CANCELLED = ("CANCELED", "CANCELLED", "STOPPED")
STATUS_PENDING = ("PENDING", "QUEUED", "UNSTART", "NOT_STARTED")
STATUS_BUSY = ("RUNNING", "PROCESSING", "STARTED", "PARSING", "HANDLING", "MERGING")

COMPILE_DONE = ("COMPLETED", "COMPLETE", "DONE", "SUCCESS", "SUCCEEDED", "FINISHED")
COMPILE_BUSY = ("RUNNING", "QUEUED", "PENDING", "SCHEDULED", "IN_PROGRESS", "DISPATCHED")


def _text(value):
    if value is None:
        return ""
    return str(value).strip()


def _progress(value):
    """Return (number, present). A wrong type is 'present but unreadable' → None."""
    if value is None or _text(value) == "":
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


class DocumentState(object):
    """One document's answer, with the evidence that produced it."""

    __slots__ = ("state", "reason", "progress", "status", "run")

    def __init__(self, state, reason, progress=None, status="", run=""):
        self.state = state
        self.reason = reason
        self.progress = progress
        self.status = status
        self.run = run

    def describe(self):
        bits = [self.state, self.reason]
        if self.progress is not None:
            bits.append("progress=%s" % self.progress)
        if self.status:
            bits.append("status=%s" % self.status)
        if self.run:
            bits.append("run=%s" % self.run)
        return "(%s)" % " ".join(bits)

    def __repr__(self):  # pragma: no cover - debugging aid
        return "DocumentState%s" % self.describe()

    def __eq__(self, other):
        return (isinstance(other, DocumentState)
                and (self.state, self.reason) == (other.state, other.reason))

    def __ne__(self, other):
        return not self.__eq__(other)


def classify(doc):
    """One document row → idle | busy | failed | unknown, never anything else.

    Order matters. A row that says `run=1` is busy even if `ingestion_status` still
    carries the *previous* run's COMPLETED: the task flag is the newer fact. A row that
    says FAILED is failed even if `progress` reached 1.0: that is what a timeout looks
    like, and it is the exact row the old `run`-only check called idle.
    """
    if doc is None:
        return DocumentState(STATE_UNKNOWN, "document_missing")
    if not isinstance(doc, dict):
        return DocumentState(STATE_UNKNOWN, "document_not_an_object")

    run = _text(doc.get("run")).upper()
    status = _text(doc.get("ingestion_status")).upper()
    progress = _progress(doc.get("progress"))
    has_progress_field = "progress" in doc and _text(doc.get("progress")) != ""
    raw_status_present = _text(doc.get("ingestion_status")) != ""
    raw_run_present = _text(doc.get("run")) != ""

    # ① a running task beats every other field
    if run in RUN_BUSY:
        return DocumentState(STATE_BUSY, "run_running", progress, status, run)

    # ② terminal-bad before terminal-good: a FAILED row keeps its old progress
    if status in STATUS_FAILED:
        return DocumentState(STATE_FAILED, "engine_said_failed", progress, status, run)
    if status in STATUS_CANCELLED:
        return DocumentState(STATE_FAILED, "engine_said_cancelled", progress, status, run)

    # ③ busy states that are published through ingestion_status, not through `run`
    if status in STATUS_BUSY:
        return DocumentState(STATE_BUSY, "status_busy", progress, status, run)
    if status in STATUS_PENDING:
        return DocumentState(STATE_BUSY, "status_pending", progress, status, run)

    # ④ no state at all: nothing to read is not "nothing is running"
    if not raw_status_present and not raw_run_present and not has_progress_field:
        return DocumentState(STATE_UNKNOWN, "no_state_fields", progress, status, run)

    if status in STATUS_DONE:
        # `run` unknown here is fine: it was checked for busy above, and the engine's
        # own verdict on ingestion is the field RAGFlow documents as authoritative.
        return DocumentState(STATE_IDLE, "status_completed", progress, status, run)

    if status:
        # ⑤ vocabulary drift: a new engine build may publish a status this file has
        # never seen. Guessing "probably idle" is how the 2026-11-23 write went wrong.
        return DocumentState(STATE_UNKNOWN, "unrecognised_status", progress, status, run)

    # status empty from here on ─────────────────────────────────────────────────
    if not has_progress_field:
        return DocumentState(STATE_UNKNOWN, "run_without_status", progress, status, run)
    if progress is None:
        return DocumentState(STATE_UNKNOWN, "progress_unreadable", progress, status, run)
    if progress >= 1.0:
        # progress 1.0 with no status is the timeout shape: the task ended, the engine
        # never said how. `document_terminal` in the writer calls this terminal too —
        # terminal is not the same question as safe-to-write.
        return DocumentState(STATE_UNKNOWN, "done_without_status", progress, status, run)
    if progress > 0.0:
        return DocumentState(STATE_UNKNOWN, "progress_without_status", progress, status, run)
    # progress 0, no run, no status: uploaded and never parsed. Nothing is running and
    # no result is half-written, so a *config* write is safe here — this is the state
    # the S4 bind step legitimately starts from.
    return DocumentState(STATE_IDLE, "never_parsed", progress, status, run)


class Assessment(object):
    """The four buckets for a named set of documents, plus whether writing is allowed."""

    __slots__ = ("states", "names", "contradictions", "warnings", "compilation")

    def __init__(self, states, names, contradictions=None, warnings=None):
        self.states = states
        self.names = list(names)
        self.contradictions = list(contradictions or [])
        self.warnings = list(warnings or [])
        # filled by assess_before_write(); "absent" means the caller did not ask
        self.compilation = "absent"

    def _of(self, wanted):
        return [name for name in self.names
                if name in self.states and self.states[name].state == wanted]

    @property
    def idle(self):
        return self._of(STATE_IDLE)

    @property
    def busy(self):
        return self._of(STATE_BUSY)

    @property
    def failed(self):
        return self._of(STATE_FAILED)

    @property
    def unknown(self):
        return [name for name in self.names
                 if name not in self.states or self.states[name].state == STATE_UNKNOWN]

    @property
    def safe(self):
        if not self.names:
            return False
        if self.contradictions:
            return False
        return not (self.busy or self.failed or self.unknown)

    def bucket(self, wanted):
        out = {}
        for name in self.names:
            state = self.states.get(name)
            if state is not None and state.state == wanted:
                out[name] = state.reason
        return out

    def describe(self):
        parts = []
        for label, wanted in (("busy", STATE_BUSY), ("failed", STATE_FAILED),
                              ("unknown", STATE_UNKNOWN)):
            rows = self.bucket(wanted)
            if rows:
                parts.append("%s=%s" % (label, ["%s(%s)" % (k, v) for k, v in rows.items()]))
        if not parts:
            parts.append("all %d document(s) idle" % len([n for n in self.names
                                                          if n in self.states]))
        if self.contradictions:
            parts.append("contradiction=%s" % "; ".join(self.contradictions))
        if self.warnings:
            parts.append("warning=%s" % "; ".join(self.warnings))
        return " ".join(parts)


def assess(docs_by_name, names):
    """Classify every named document. A name the listing does not carry is `unknown`."""
    rows = docs_by_name if isinstance(docs_by_name, dict) else {}
    states = {}
    for name in names:
        if name in rows:
            states[name] = classify(rows[name])
        else:
            states[name] = DocumentState(STATE_UNKNOWN, "document_missing")
    return Assessment(states, names)


def compilation_state(payload):
    """`GET /datasets/{id}/compilation/status` → completed | busy | absent | unknown.

    `completed` is the sentence that made doc/304 E97 confusing: the merge pass reported
    `state: completed` *and* rewrote 0 products after deleting 27. So `completed` here is
    only ever used as "the upstream claims it is finished", never as "there are products".
    """
    if payload is None:
        return "absent"
    if not isinstance(payload, dict):
        return "unknown"
    state = _text(payload.get("state")).upper()
    if not state:
        return "absent"
    if state in COMPILE_DONE:
        return "completed"
    if state in COMPILE_BUSY:
        return "busy"
    return "unknown"


def assess_before_write(docs_by_name, names, compilation_status=None):
    """assess(), plus the rule the 2026-11-23 run needed and did not have.

    An upstream that says `completed` while its own documents are FAILED or still parsing
    is contradicting itself. Neither reading is allowed to win: the write stops, and the
    contradiction is printed instead of resolved by preference.
    """
    base = assess(docs_by_name, names)
    comp = compilation_state(compilation_status)
    contradictions = []
    if comp == "completed":
        if base.failed:
            contradictions.append(
                "upstream compilation says completed, but these documents FAILED: %s"
                % sorted(base.failed))
        if base.busy:
            contradictions.append(
                "upstream compilation says completed, but these documents are still "
                "parsing: %s" % sorted(base.busy))
    warnings = []
    if comp == "completed":
        warnings.append("upstream compilation is 'completed'; that is not a claim that "
                        "artifacts exist (doc/304 E97), so products are still a precondition")
    for name in base.idle:
        doc = (docs_by_name or {}).get(name) or {}
        try:
            chunks = int(doc.get("chunk_count") or 0)
        except (TypeError, ValueError):
            chunks = 0
        if chunks <= 0:
            warnings.append("%s is idle with chunk_count=0" % name)
    return Assessment(base.states, names, contradictions, warnings)


# ── the second question: is a *repair* write allowed? ────────────────────────
#
# Two named actions, so no call site can answer one question and print the other.
ACTION_BIND = "bind"              # config write under a healthy set: needs every doc idle
ACTION_REDISPATCH = "redispatch"  # repair a failed set: needs a FAILED doc, no busy/unknown


class RepairVerdict(object):
    """`safe_to_redispatch`'s answer, with the sentence that refuses or permits it.

    Not a wider `Assessment.safe` (doc/304 §7 item 34):

        bind        every document idle                      → Assessment.safe
        redispatch  no busy, no unknown, ≥1 FAILED document   → this class

    The `≥1 FAILED` clause is what keeps the two questions apart. Without it, "redispatch"
    would authorise a write on a set that has nothing wrong with it, i.e. it would be `safe`
    with an extra word on top — and the S6 run dispatch would lose the contradiction check
    it gets from `assess_before_write`.

    Contradictions are *carried*, not used as a veto: "upstream says completed while these
    documents FAILED" is the disease this write treats, and blocking on it restores the
    deadlock item 34 describes. They still veto the *bind* path and the S6 dispatch, which
    is where a contradiction means "do not believe this ledger yet".
    """

    __slots__ = ("allowed", "reason", "action", "repair", "left_alone", "blocked",
                 "contradictions", "warnings")

    def __init__(self, allowed, reason, repair=None, left_alone=None, blocked=None,
                 contradictions=None, warnings=None):
        self.allowed = bool(allowed)
        self.reason = reason
        self.action = ACTION_REDISPATCH
        self.repair = list(repair or [])
        self.left_alone = list(left_alone or [])
        self.blocked = dict(blocked or {})
        self.contradictions = list(contradictions or [])
        self.warnings = list(warnings or [])

    def describe(self):
        verdict = "allowed" if self.allowed else "refused(%s)" % self.reason
        bits = ["%s=%s" % (self.action, verdict)]
        if self.repair:
            bits.append("repair=%s" % sorted(self.repair))
        if self.left_alone:
            bits.append("left_alone=%s" % sorted(self.left_alone))
        if self.blocked:
            bits.append("blocked=%s" % ["%s(%s)" % (k, v) for k, v in self.blocked.items()])
        if self.contradictions:
            bits.append("contradiction=%s (carried: the bind path and S6 still refuse it)"
                        % "; ".join(self.contradictions))
        return " ".join(bits)

    def __repr__(self):  # pragma: no cover - debugging aid
        return "RepairVerdict(%s)" % self.describe()

    def __eq__(self, other):
        return (isinstance(other, RepairVerdict)
                and (self.allowed, self.reason, sorted(self.repair), sorted(self.left_alone))
                == (other.allowed, other.reason, sorted(other.repair), sorted(other.left_alone)))

    def __ne__(self, other):
        return not self.__eq__(other)


def safe_to_redispatch(assessment):
    """One assessment → may this pilot dispatch parsing again? Never a fifth state.

    Refusals, in the order they are asked:

        no_reading      not an Assessment (a caller bug, not a document state)
        no_documents    an empty name list is not permission
        busy            a parse is already running: a second dispatch queues behind it and
                        the config write in the same action would hit code 102
        unknown         the row does not say, and "probably finished" is the reading that
                        cost 27 products; the answer is not "try anyway"
        nothing_to_repair  nothing FAILED, so this is not a repair — use ACTION_BIND

    Only then `allowed=True`, with `repair` naming the documents the engine gave up on and
    `left_alone` naming the idle ones (a set can be mixed: one document timed out while its
    siblings finished, and re-dispatching only the failed one is the narrow repair).
    """
    if not isinstance(assessment, Assessment):
        return RepairVerdict(False, "no_reading",
                             blocked={"<": "not an Assessment"})
    if not assessment.names:
        return RepairVerdict(False, "no_documents",
                             blocked={"<": "empty name list"})

    busy = assessment.bucket(STATE_BUSY)
    if busy:
        return RepairVerdict(False, "busy", blocked=busy,
                             contradictions=assessment.contradictions,
                             warnings=assessment.warnings)

    unknown = {}
    for name in assessment.names:
        state = assessment.states.get(name)
        if state is None:
            unknown[name] = "document_missing"
        elif state.state == STATE_UNKNOWN:
            unknown[name] = state.reason
    if unknown:
        return RepairVerdict(False, "unknown", blocked=unknown,
                             contradictions=assessment.contradictions,
                             warnings=assessment.warnings)

    failed = assessment.failed
    if not failed:
        return RepairVerdict(False, "nothing_to_repair", left_alone=assessment.idle,
                             contradictions=assessment.contradictions,
                             warnings=assessment.warnings)

    return RepairVerdict(True, "", repair=failed, left_alone=assessment.idle,
                         contradictions=assessment.contradictions,
                         warnings=assessment.warnings)


class WaitOutcome(object):
    """The verdict of a wait: safe or not, and the sentence that says why not."""

    __slots__ = ("safe", "attempts", "elapsed", "last", "reason", "compilation")

    def __init__(self, safe, attempts, elapsed, last, reason, compilation="absent"):
        self.safe = safe
        self.attempts = attempts
        self.elapsed = elapsed
        self.last = last
        self.reason = reason
        self.compilation = compilation

    def why(self):
        if self.safe:
            return "documents idle after %d attempt(s), %.0fs" % (self.attempts, self.elapsed)
        if self.reason == "documents_failed":
            # Terminal, and terminal is not the same question as writable (item 34): the
            # caller may ask `safe_to_redispatch` about a repair, and must not hear this
            # sentence as "keep waiting".
            return ("engine gave up after %d attempt(s), %.0fs: %s — waiting cannot repair "
                    "a verdict the engine already gave (doc/304 §7 item 34)"
                    % (self.attempts, self.elapsed,
                       self.last.describe() if self.last is not None else "no reading"))
        detail = self.last.describe() if self.last is not None else "no reading"
        return ("not safe to write after %d attempt(s), %.0fs: %s — %s"
                % (self.attempts, self.elapsed, self.reason, detail))


def wait_for_idle(fetch, names, timeout, poll=10.0, clock=None, sleep=None, on_poll=None):
    """Poll until every named document is idle. Fail closed on timeout.

    `fetch` is injected so the loop can be tested without a host: it returns the
    `{name: row}` mapping `GET /datasets/{id}/documents` would have produced.

    Returning `safe=False` is the *only* failure mode — this function never returns a
    half-answer, and it never returns `safe=True` because it ran out of patience, because
    a row was missing, or because the ledger of `names` was empty. An exception from
    `fetch` propagates: a read that failed is not a read that said idle.
    """
    clock = clock or time.monotonic
    sleep = sleep or time.sleep
    names = [str(name) for name in names]

    if not names:
        return WaitOutcome(False, 0, 0.0, Assessment({}, []), "no_documents")

    started = clock()
    attempts = 0
    last = None
    while True:
        attempts += 1
        rows = fetch()
        last = assess(rows or {}, names)
        if last.safe:
            return WaitOutcome(True, attempts, clock() - started, last, "")
        if last.failed:
            # waiting does not repair a verdict the engine already gave
            return WaitOutcome(False, attempts, clock() - started, last, "documents_failed")
        elapsed = clock() - started
        if elapsed >= timeout:
            return WaitOutcome(False, attempts, elapsed, last, "timeout_after_%.0fs" % timeout)
        if on_poll is not None:
            on_poll(last, elapsed)
        sleep(poll)
