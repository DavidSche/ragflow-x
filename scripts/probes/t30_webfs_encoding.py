#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""T30 — 前门对 `Accept-Encoding` 的理解，与 RFC 9110 一致吗？（doc/304 §4 E176）

E176 的第一个 bug 在 `internal/webfs`：`acceptsGzip` 用子串找 `gzip;q=0`，
于是

    Accept-Encoding: gzip;q=0.000        -> 仍然压缩（q=0 是"不要"）
    Accept-Encoding: gzip;q=0.5          -> 压缩（这个碰巧对，但理由不对）
    Accept-Encoding: gzip, *;q=0         -> `*` 把 gzip 关掉了，它仍然压缩

单元测试证明 `acceptsGzip` 这个函数同意。这个探针问的是**一个部署好的实例**：
同一个 URL，五个 `Accept-Encoding`，答案是不是五个正确的答案。

    S1 默认    一个正常浏览器（gzip, deflate）拿到压缩答案，且 Vary/Cache-Control 都在
    S2 q=0     四种写法（0 / 0.0 / 0.00 / 0.000，含大写 Q）都不压缩
    S3 q>0     q=0.5 / q=1.0 压缩；q 是数字，不是"出现了就开"
    S4 别的编码 identity、`*;q=0`、空值都不压缩
    S5 深链接  /datasets/123 的 SPA 兜底同样是 200 + 同一套规则（Gin 在这条路径上预置过 404）
    S6 资源    真资源：压缩体解开后与不压缩体逐字节相同，且 immutable
    S7 缓存键  每一条答案都带 Vary: Accept-Encoding（压缩与否都要，否则共享缓存会串味）

这个探针**只读**：只有 GET，没有任何凭证，也不需要凭证。它跑在任何一次部署之后，
用于回答"我改的是 webfs，线上那个二进制是不是我改的那个"。

    python scripts/probes/t30_webfs_encoding.py --offline
    RGX_BASE=http://<host>:19193 python scripts/probes/t30_webfs_encoding.py

Exit: 0 七个状态都成立 / 1 有断言失败 / 2 环境不可用或用法错误 / 3 跑到中途跳过。
"""

from __future__ import annotations

import argparse
import gzip
import http.client
import os
import re
import sys
import time
from urllib.parse import urlsplit

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from probe_common import (  # noqa: E402
    CaseFail, CaseSkip, EnvUnavailable, Requirement, Usage, print_contract, safe,
)

# A host is not a credential, but this one is private: no default, same rule as T29.
REQUIREMENTS = [
    Requirement("--base", "RGX_BASE", "deployed console, e.g. http://<host>:19193"),
]

STEPS = [
    ("S1", "default: a browser's Accept-Encoding gets gzip, Vary and a cache policy"),
    ("S2", "q=0: four spellings (0, 0.0, 0.00, 0.000, upper-case Q) all mean 'do not compress'"),
    ("S3", "q>0: 0.5 and 1.0 compress; a weight is a number, not the presence of a substring"),
    ("S4", "other: identity, *;q=0 and an empty value all come back plain"),
    ("S5", "deep link: /datasets/123 falls back to the shell with 200 and the same rules"),
    ("S6", "asset: the gzip body inflates to the identity body, byte for byte; immutable"),
    ("S7", "cache key: every answer carries Vary: Accept-Encoding, compressed or not"),
]

NOTES = [
    "read-only: GET only, no credential, no account; it can be run against production",
    "the q-value grammar is RFC 9110 §12.5.3: `gzip;q=0` is a refusal, `q=0.000` too, and a "
    "malformed weight means 'no preference' (compress), not 'refuse'",
    "S5 is not decoration: Gin pre-sets 404 on the NoRoute path, so the SPA fallback has to "
    "claim 200 explicitly — a deep link that renders the app and answers 404 breaks every "
    "uptime check in front of it",
]

# q=0 in every spelling the RFC allows, including the three-decimal form that a
# substring test reads as "gzip is mentioned, so compress".
Q_ZERO = [
    "gzip;q=0",
    "gzip;q=0.0",
    "gzip;q=0.00",
    "gzip;q=0.000",
    "gzip;Q=0.0",
]
Q_POSITIVE = ["gzip;q=0.5", "gzip;q=1.0", "gzip;q=0.001"]
PLAIN = ["identity", "*;q=0", "identity;q=1", ""]


class FrontDoor:
    """`http.client`, not `requests`: nothing here may decompress behind our back.

    The question is *what bytes the server sent* and *which headers it sent with them*;
    a client that transparently inflates makes a missing `Content-Encoding` look like a
    compressed answer, which is exactly the confusion this probe exists to remove.
    """

    def __init__(self, base):
        parts = urlsplit(base if "://" in base else "http://" + base)
        self.host = parts.hostname or ""
        self.port = parts.port or (443 if parts.scheme == "https" else 80)
        self.tls = parts.scheme == "https"
        if not self.host:
            raise Usage("--base has no host: %r" % base)
        self.prefix = parts.path.rstrip("/")

    def get(self, path, accept_encoding=None):
        cls = http.client.HTTPSConnection if self.tls else http.client.HTTPConnection
        conn = cls(self.host, self.port, timeout=60)
        headers = {"User-Agent": "rgx-t30-probe/1", "Connection": "close"}
        if accept_encoding is not None:
            headers["Accept-Encoding"] = accept_encoding
        try:
            conn.request("GET", self.prefix + path, headers=headers)
            resp = conn.getresponse()
            body = resp.read()
            return resp.status, {k.lower(): v for k, v in resp.getheaders()}, body
        except OSError as exc:
            raise EnvUnavailable("%s:%s unreachable: %s" % (self.host, self.port, exc))
        finally:
            conn.close()


def enc(headers):
    return (headers.get("content-encoding") or "").strip().lower()


def inflate(headers, body):
    if enc(headers) == "gzip":
        try:
            return gzip.decompress(body)
        except OSError as exc:
            raise CaseFail("Content-Encoding says gzip but the body does not inflate: %s" % exc)
    return body


class Drill:
    def __init__(self, args):
        self.args = args
        self.results = []
        self.door = FrontDoor(args.base)

    def step(self, sid, fn):
        try:
            fn()
            self.results.append((sid, "PASS", ""))
            print(safe("[%s] PASS" % sid))
        except CaseSkip as exc:
            self.results.append((sid, "SKIP", str(exc)))
            print(safe("[%s] SKIP  %s" % (sid, exc)))
        except CaseFail as exc:
            self.results.append((sid, "FAIL", str(exc)))
            print(safe("[%s] FAIL  %s" % (sid, exc)))

    def shell(self):
        """The identity answer for `/`, fetched once and reused as the reference."""
        status, headers, body = self.door.get("/", "identity")
        if status != 200:
            raise CaseFail("GET / with Accept-Encoding: identity -> %d" % status)
        return headers, body

    # ── S1 ────────────────────────────────────────────────────────────────
    def s1_default(self):
        status, headers, body = self.door.get("/", "gzip, deflate, br")
        if status != 200:
            raise CaseFail("GET / -> %d" % status)
        if enc(headers) != "gzip":
            raise CaseFail("a browser's Accept-Encoding did not get gzip: encoding=%r" % enc(headers))
        html = inflate(headers, body)
        if b"<!doctype html" not in html[:200].lower():
            raise CaseFail("the gzip body is not the HTML shell: %r" % html[:80])
        if (headers.get("cache-control") or "").strip() != "no-cache":
            raise CaseFail("index.html is served %r, want no-cache"
                           % headers.get("cache-control"))
        print(safe("    / -> 200 gzip, %d B wire / %d B html, no-cache" % (len(body), len(html))))

    # ── S2 / S3 / S4 ──────────────────────────────────────────────────────
    def s2_q_zero(self):
        for spec in Q_ZERO:
            status, headers, body = self.door.get("/", spec)
            if status != 200:
                raise CaseFail("Accept-Encoding: %r -> %d" % (spec, status))
            if enc(headers) == "gzip":
                raise CaseFail("Accept-Encoding: %r was compressed: q=0 means 'do not send "
                               "gzip', and the three-decimal form is the one a substring "
                               "test reads as a request" % spec)
        print(safe("    %d refusals honoured (%s)" % (len(Q_ZERO), ", ".join(Q_ZERO))))

    def s3_q_positive(self):
        for spec in Q_POSITIVE:
            status, headers, body = self.door.get("/", spec)
            if status != 200:
                raise CaseFail("Accept-Encoding: %r -> %d" % (spec, status))
            if enc(headers) != "gzip":
                raise CaseFail("Accept-Encoding: %r was not compressed: a positive weight is "
                               "a preference for gzip" % spec)
        print(safe("    %d positives compressed (%s)" % (len(Q_POSITIVE), ", ".join(Q_POSITIVE))))

    def s4_other_encodings(self):
        for spec in PLAIN:
            status, headers, body = self.door.get("/", spec)
            if status != 200:
                raise CaseFail("Accept-Encoding: %r -> %d" % (spec, status))
            if enc(headers):
                raise CaseFail("Accept-Encoding: %r -> encoding=%r, want none"
                               % (spec or "<empty>", enc(headers)))
        print(safe("    identity / *;q=0 / empty all plain"))

    # ── S5 ────────────────────────────────────────────────────────────────
    def s5_deep_link(self):
        status, headers, body = self.door.get("/datasets/123", "identity")
        if status != 200:
            raise CaseFail("a deep link answered %d: the SPA fallback must claim 200 "
                           "(Gin pre-sets 404 on NoRoute)" % status)
        if not (headers.get("content-type") or "").startswith("text/html"):
            raise CaseFail("the deep link is %r, want text/html" % headers.get("content-type"))
        if (headers.get("cache-control") or "").strip() != "no-cache":
            raise CaseFail("the shell fallback is served %r, want no-cache"
                           % headers.get("cache-control"))
        status, headers2, body2 = self.door.get("/datasets/123", "gzip;q=0")
        if enc(headers2):
            raise CaseFail("the deep link ignored gzip;q=0: encoding=%r" % enc(headers2))
        if inflate(headers, body) != inflate(headers2, body2):
            raise CaseFail("the deep link's two answers differ")
        print(safe("    /datasets/123 -> 200 text/html, no-cache, q=0 honoured"))

    # ── S6 ────────────────────────────────────────────────────────────────
    def s6_asset(self):
        headers, html = self.shell()
        found = re.findall(rb'(?:src|href)="(/[^"]+\.js)"', html)
        if not found:
            raise CaseSkip("index.html names no /…js asset: cannot compare the two bodies")
        asset = found[0].decode()
        s_id, h_id, b_id = self.door.get(asset, "identity")
        s_gz, h_gz, b_gz = self.door.get(asset, "gzip")
        if s_id != 200 or s_gz != 200:
            raise CaseFail("%s -> identity %d / gzip %d" % (asset, s_id, s_gz))
        if enc(h_id):
            raise CaseFail("%s answered identity with encoding=%r" % (asset, enc(h_id)))
        if enc(h_gz) != "gzip":
            raise CaseFail("%s did not compress on request: encoding=%r" % (asset, enc(h_gz)))
        if inflate(h_gz, b_gz) != b_id:
            raise CaseFail("%s: the inflated body is not the identity body (%d vs %d B)"
                           % (asset, len(inflate(h_gz, b_gz)), len(b_id)))
        cache = (h_gz.get("cache-control") or "").strip()
        if "immutable" not in cache:
            raise CaseFail("%s is served %r: a content-hashed asset must be immutable"
                           % (asset, cache))
        if len(b_gz) >= len(b_id):
            raise CaseFail("%s: compression grew the body (%d -> %d B)"
                           % (asset, len(b_id), len(b_gz)))
        print(safe("    %s: %d B -> %d B gzip, identical after inflate, immutable"
                   % (asset, len(b_id), len(b_gz))))

    # ── S7 ────────────────────────────────────────────────────────────────
    def s7_vary(self):
        for path, spec in (("/", "gzip"), ("/", "identity"), ("/datasets/123", "gzip;q=0")):
            status, headers, _ = self.door.get(path, spec)
            vary = (headers.get("vary") or "").lower()
            if "accept-encoding" not in vary:
                raise CaseFail("%s with Accept-Encoding: %r answered Vary: %r — a shared "
                               "cache would replay the identity form to a gzip client"
                               % (path, spec, headers.get("vary")))
        print(safe("    3/3 answers carry Vary: Accept-Encoding"))


def run_all(drill):
    """The seven steps, in order. Shared by the live run and `--selftest`."""
    for sid, fn in (("S1", drill.s1_default), ("S2", drill.s2_q_zero),
                    ("S3", drill.s3_q_positive), ("S4", drill.s4_other_encodings),
                    ("S5", drill.s5_deep_link), ("S6", drill.s6_asset),
                    ("S7", drill.s7_vary)):
        drill.step(sid, fn)
    failed = [r for r in drill.results if r[1] == "FAIL"]
    skipped = [r for r in drill.results if r[1] == "SKIP"]
    return failed, skipped


# ── --selftest: two front doors, one drill ──────────────────────────────────
# A green run against a host proves "this binary is not the broken one" only if the drill
# can tell the two apart. So the selftest builds both: a local server running E176's old
# substring rule (`"gzip" in Accept-Encoding`) and the same server running the q-value
# rule. The old one must make the drill go red, and specifically on S2 — not "some step
# failed", which a typo in the probe would also produce.
FAKE_SHELL = (b"<!doctype html><html><head><title>fake</title>"
              b"<script type=\"module\" src=\"/assets/app.js\"></script></head>"
              b"<body><div id=\"root\"></div><!-- " + b"x" * 1100 + b" --></body></html>")
FAKE_ASSET = b"export const a = 1;" + b"//padding\n" * 200


def _parse_q(accept_encoding):
    """RFC 9110 §12.5.3, narrowly: does this header accept gzip?"""
    gzip_q, star_q = None, None
    for part in (accept_encoding or "").split(","):
        bits = [b.strip() for b in part.split(";")]
        if not bits or not bits[0]:
            continue
        coding = bits[0].lower()
        q = 1.0
        for param in bits[1:]:
            if param.lower().startswith("q="):
                try:
                    q = float(param[2:])
                except ValueError:
                    q = 1.0          # malformed weight = no preference
        if coding == "gzip":
            gzip_q = q
        elif coding == "*":
            star_q = q
    if gzip_q is not None:
        return gzip_q > 0
    if star_q is not None:
        return star_q > 0
    return False


def _start_fake(buggy):
    import http.server
    import threading

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *a):   # silence: a selftest must not print a request log
            pass

        def do_GET(self):
            path = self.path.split("?")[0]
            body = FAKE_ASSET if path.startswith("/assets/") else FAKE_SHELL
            ctype = "application/javascript" if path.startswith("/assets/") else "text/html"
            ae = self.headers.get("Accept-Encoding", "")
            if buggy:
                use_gzip = "gzip" in ae and len(body) >= 1024
            else:
                use_gzip = _parse_q(ae) and len(body) >= 1024
            if use_gzip:
                body = gzip.compress(body, 9)
            self.send_response(200)
            self.send_header("Content-Type", ctype)
            self.send_header("Vary", "Accept-Encoding")
            self.send_header("Cache-Control",
                             "public, max-age=31536000, immutable"
                             if path.startswith("/assets/") else "no-cache")
            if use_gzip:
                self.send_header("Content-Encoding", "gzip")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, "http://127.0.0.1:%d" % srv.server_address[1]


def selftest():
    ok = True
    checks = []
    for buggy, want in ((True, "FAIL"), (False, "PASS")):
        srv, base = _start_fake(buggy)
        try:
            drill = Drill(argparse.Namespace(base=base))
            print(safe("-- front door with the %s rule --"
                       % ("old substring" if buggy else "RFC q-value")))
            failed, skipped = run_all(drill)
            verdict = "FAIL" if failed else ("SKIP" if skipped else "PASS")
            names = sorted({r[0] for r in failed})
            print(safe("-- verdict: %s%s" % (verdict,
                       (" (" + ", ".join(names) + ")") if names else "")))
        finally:
            srv.shutdown()
            srv.server_close()
        if verdict != want:
            print(safe("SELFTEST FAIL: %s rule -> %s, want %s"
                       % ("old" if buggy else "RFC", verdict, want)))
            ok = False
            continue
        if buggy and "S2" not in names:
            print("SELFTEST FAIL: the old rule was refused, but not by S2 (q=0): %s" % names)
            ok = False
            continue
        checks.append(names)
    if not ok:
        return 1
    print("")
    print(safe("SELFTEST OK: the old substring rule goes red on %s; the q-value rule "
               "passes all seven" % ", ".join(checks[0])))
    return 0


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", default="")
    ap.add_argument("--offline", action="store_true")
    ap.add_argument("--selftest", action="store_true",
                    help="run the drill against two local front doors: the old substring "
                         "rule must be refused, the q-value rule must pass")
    args = ap.parse_args(argv)

    if args.selftest:
        return selftest()
    if args.offline:
        return print_contract("T30 front-door encoding drill", REQUIREMENTS, STEPS, NOTES)
    if not args.base:
        try:
            args.base = REQUIREMENTS[0].resolve(args)
        except Usage as exc:
            print(safe("usage: %s" % exc), file=sys.stderr)
            return 2

    drill = Drill(args)
    print(safe("T30 front-door encoding drill @ %s (started %s)"
               % (args.base, time.strftime("%Y-%m-%d %H:%M:%S"))))
    failed, skipped = run_all(drill)

    print("")
    print(safe("summary: %d PASS / %d FAIL / %d SKIP"
               % (len(drill.results) - len(failed) - len(skipped), len(failed), len(skipped))))
    if failed:
        for sid, _, msg in failed:
            print(safe("  %s %s" % (sid, msg)), file=sys.stderr)
        return 1
    if skipped:
        print(safe("  (a skipped step is not a passed step: the drill did not fully run)"))
        return 3
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except EnvUnavailable as exc:
        print(safe("environment: %s" % exc), file=sys.stderr)
        sys.exit(2)
