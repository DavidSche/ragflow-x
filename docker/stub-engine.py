#!/usr/bin/env python3
"""Minimal stand-in for a RAGFlow engine, sized for container verification.

Only what the platform touches while it boots: the health probe, the version
probe, and a permissive catch-all so optional registration calls do not make the
container fall back into the setup wizard. verify-image.sh starts it on the
compose network:

    python3 docker/stub-engine.py 9380
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HEALTH = {"status": "ok", "db": "ok", "redis": "ok", "doc_engine": "elasticsearch"}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _respond(self, payload, code=200):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802 (http.server API)
        path = self.path.split("?")[0]
        sys.stderr.write(f"stub GET {path}\n")
        if path.endswith("/system/healthz"):
            return self._respond(HEALTH)
        if path.endswith("/system/version") or path == "/api/v1/version":
            return self._respond({"code": 0, "data": {"version": "v0.19.0"}})
        if path.endswith("/datasets"):
            return self._respond({"code": 0, "data": []})
        if path.endswith("/models") or path.endswith("/factories"):
            return self._respond({"code": 0, "data": []})
        return self._respond({"code": 0, "data": {}})

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        sys.stderr.write(f"stub POST {self.path.split('?')[0]}\n")
        return self._respond({"code": 0, "data": {"id": "stub"}})

    def do_PUT(self):  # noqa: N802
        return self.do_POST()

    def log_message(self, fmt, *args):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9380
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
