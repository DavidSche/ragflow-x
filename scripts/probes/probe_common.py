#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Shared pieces of the live probes: one HTTP client and one credential contract.

Doc/304 §7 item 12 said the probes that produced T02's data half and T16's live
drill lived in `localenv/` (gitignored), so a clone could read the evidence in doc/305
but could not run it. They live here now (and W-4's probe joined them in v1.41). Moving them into the repository has one
price: nothing in a tracked file may be a credential.

Hence the rule this module exists to enforce:

    the host is configuration, the password is not.

* `--base` keeps a documented drill default, because a hostname is not a secret;
* every credential comes from a flag or from the environment, and a missing one is a
  **usage** error (exit 2) with a sentence naming the variable — never a silent
  fallback to something that happens to work on the author's machine;
* the throwaway accounts the drill creates get a password generated per run, so no
  fixed string from the drill appears anywhere.

Exit codes are the same sentences the probes print (doc/305 §4.3):

    0 = every step PASS          1 = at least one step FAIL
    2 = environment unusable / usage (login failed, missing credential, host down)
    3 = ran without failures but skipped a step (a `--only` slice, or a step whose
        input an earlier step did not produce)

3 exists so a partial run cannot be quoted as "the drill passed".
"""

from __future__ import annotations

import os
import sys
import time

import requests

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))


class EnvUnavailable(Exception):
    """The host, the login or the database could not be reached (exit code 2)."""


class CaseFail(Exception):
    """One step's assertion failed. The drill continues with the next step."""


class CaseSkip(Exception):
    """A step could not run because an earlier step did not produce its input.

    Deliberately distinct from PASS: a drill whose report step never ran because the
    model proxy was down must not look like a drill whose report step passed.
    """


def safe(text):
    """Console-safe echo. The Git-Bash console is GBK; a Chinese step title is not.

    The evidence files stay UTF-8 — this only affects what scrolls past the operator.
    """
    raw = str(text)
    enc = sys.stdout.encoding or "utf-8"
    return raw.encode(enc, "replace").decode(enc, "replace")


def expect(cond, msg):
    if not cond:
        raise CaseFail(msg)


class Client:
    """One authenticated console session. Every call is JSON in / JSON out."""

    def __init__(self, base: str, timeout: float = 60.0):
        self.base = base.rstrip("/")
        self.timeout = timeout
        self.session = requests.Session()
        self.token = ""

    def login(self, username: str, password: str) -> None:
        try:
            resp = self.session.post(
                self.base + "/api/v1/auth/login",
                json={"username": username, "password": password},
                timeout=30)
        except requests.RequestException as exc:
            raise EnvUnavailable(f"login {self.base} failed: {exc}")
        if resp.status_code != 200:
            raise EnvUnavailable(
                f"login HTTP {resp.status_code}: {resp.text[:200]}")
        body = resp.json()
        token = (body.get("data") or {}).get("token")
        if not token:
            raise EnvUnavailable(f"login returned no token: {body}")
        self.token = token
        self.session.headers["Authorization"] = "Bearer " + token

    def login_with_key(self, api_key: str) -> None:
        """Engine-side auth: a bearer API key, no login round-trip.

        The console (19193) takes a username/password and returns a token; the engine
        (9380) takes a `Bearer ragflow-…` key. Same header, different provenance, so it is
        a second method rather than a flag — and a key is *not* a password: there is no
        user to name, and a failed key surfaces as an HTTP 401 on the first read rather
        than a login error nobody can interpret.

        Only `w4_compiler_evidence.py` uses this: the ingestion logs that hold the
        Compiler failure reason are served by the engine, not the console (doc/304 E103).
        """
        key = (api_key or "").strip()
        if not key:
            raise EnvUnavailable("an empty API key was provided")
        self.token = key
        self.session.headers["Authorization"] = "Bearer " + key

    def request(self, method: str, path: str, **kw):
        # `json_body` is this probe family's name for the JSON payload; requests calls
        # it `json`, and a probe that leaked its own vocabulary into the transport
        # would fail with a TypeError instead of a product answer.
        if "json_body" in kw:
            kw["json"] = kw.pop("json_body")
        kw.setdefault("timeout", self.timeout)
        return self.session.request(method, self.base + path, **kw)

    def api(self, method: str, path: str, **kw):
        """Returns (status, body). A non-JSON body is returned as text."""
        resp = self.request(method, path, **kw)
        try:
            return resp.status_code, resp.json()
        except ValueError:
            return resp.status_code, {"_raw": resp.text[:2000]}

    def ok(self, method: str, path: str, **kw):
        status, body = self.api(method, path, **kw)
        if not (200 <= status < 300) or body.get("code") not in (0, None):
            raise CaseFail(f"{method} {path} -> {status} code={body.get('code')} "
                           f"{str(body.get('message'))[:200]}")
        return body.get("data")


def wait_job(admin, drill, job_id, condition, timeout):
    """Poll a report job. `terminal` is the job's own word, never a guess."""
    deadline = time.time() + timeout
    last = {}
    while time.time() < deadline:
        last = admin.ok("GET", f"/api/v1/report-jobs/{job_id}")
        if condition == "terminal" and last.get("terminal") is True:
            return last
        if last.get("status") == condition:
            return last
        if last.get("terminal") is True:
            return last
        time.sleep(4)
    drill.dump("wait-timeout", last)
    raise CaseFail(f"job {job_id} did not reach {condition} in {timeout}s "
                   f"(status={last.get('status')} stage={last.get('stage')} "
                   f"error={last.get('error_code')})")


# ── the credential contract ────────────────────────────────────────────────

class Requirement:
    """One value a probe needs, and where it may come from.

    `secret=True` values are never printed, never stored in the evidence dump, and
    never have a default: a probe that ships a working password is a probe that
    teaches everyone to hard-code one.
    """

    def __init__(self, flag, env, purpose, secret=False, default=""):
        self.flag = flag
        self.env = env
        self.purpose = purpose
        self.secret = secret
        self.default = default

    def resolve(self, args):
        value = getattr(args, self.flag.replace("--", "").replace("-", "_"), "") or ""
        if value:
            return value
        value = os.environ.get(self.env, "")
        if value:
            return value
        if self.default:
            return self.default
        if self.secret:
            raise Usage("--%s (or %s) is required: %s. No default credential is stored "
                        "in the repository (doc/304 §7 item 12)."
                        % (self.flag.replace("--", ""), self.env, self.purpose))
        raise Usage("--%s (or %s) is required: %s."
                    % (self.flag.replace("--", ""), self.env, self.purpose))

    def describe(self):
        shown = "<from %s or --%s>" % (self.env, self.flag.replace("--", ""))
        return "  %-22s %s%s" % (self.env, shown,
                                 "" if not self.purpose else "  # " + self.purpose)


class Usage(Exception):
    """A missing or unusable argument (exit code 2, not 1: nothing was tested)."""


def print_contract(title, requirements, steps, notes=()):
    """Print what this probe needs and what it will do, without touching the network.

    `--offline` is the hermetic half of a live probe: it lets the release gate (and a
    reader on a laptop with no drill host) check the contract — which variables are
    required, which steps exist, which exit codes it can return — and never mistake
    "I could not run it" for "it passed".
    """
    print(safe("%s — the contract, offline (nothing was contacted)" % title))
    print("")
    print("required values:")
    for r in requirements:
        print(safe(r.describe()))
    print("")
    print("steps, in order:")
    for step, what in steps:
        print(safe("  %-4s %s" % (step, what)))
    if notes:
        print("")
        for note in notes:
            print(safe("  note: " + note))
    print("")
    print(safe("exit codes: 0 = every step PASS, 1 = at least one FAIL, "
               "2 = environment unusable/usage, 3 = ran but skipped a step"))
    return 0
