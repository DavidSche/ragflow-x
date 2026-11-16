#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Delete the knowledge bases, chats and search apps one E2E tag left behind.

    RGX_E2E_TAG=20261117b python test/e2e/scenario-templates/cleanup_tag.py
    RGX_E2E_TAG=20261117b python test/e2e/scenario-templates/cleanup_tag.py --dry-run

The harness names everything with a `-<tag>` suffix, so one tag is a complete,
independent slice of the test tenant. Keep the newest tag around for manual
review (the wizard evidence refers to those ids), delete the older ones so the
test environment does not accumulate a dozen knowledge bases per run.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("RGX_E2E_BASE", "http://192.168.4.151:19193/api/v1")
WS_USER = os.environ.get("RGX_E2E_WS_USER", "e2e-admin")
WS_PASS = os.environ.get("RGX_E2E_WS_PASS", "E2e#2026pass")
TAG = os.environ.get("RGX_E2E_TAG", "")
sys.stdout.reconfigure(encoding="utf-8", errors="replace")


def call(path, method="GET", body=None, token=None):
    req = urllib.request.Request(BASE + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=180) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def items(payload):
    data = payload.get("data")
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        return data.get("items", [])
    return []


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    if not TAG:
        sys.exit("RGX_E2E_TAG is required (it is the suffix every harness resource carries)")

    st, pl = call("/auth/login", "POST", {"username": WS_USER, "password": WS_PASS})
    token = ((pl.get("data") or {}).get("token"))
    if not token:
        sys.exit(f"login failed: {st}/{pl.get('code')}")

    plans = []
    for path, label in (("/chats?page=1&page_size=200", "chat"),
                        ("/search-apps?page=1&page_size=200", "search_app"),
                        ("/datasets?page=1&page_size=200", "dataset")):
        st, pl = call(path, token=token)
        for item in items(pl):
            name = item.get("name") or ""
            if TAG not in name:
                continue
            # The harness names resources `...-<tag>`; `neg` probes are named with
            # the tag too, so the filter is deliberately a suffix test.
            if not any(name.endswith(s) for s in (TAG, f"-neg-{TAG}")):
                continue
            plans.append((label, item.get("id"), name))

    if not plans:
        print(f"nothing to clean for tag {TAG}")
        return
    for label, item_id, name in plans:
        print(("dry-run " if args.dry_run else "") + f"delete {label:10} {item_id} {name}")
        if args.dry_run:
            continue
        path = {"chat": "/chats/", "search_app": "/search-apps/", "dataset": "/datasets/"}[label] + item_id
        st, pl = call(path, "DELETE", token=token)
        if st >= 400 or pl.get("code") != 0:
            print(f"  !! {st}/{pl.get('code')} {pl.get('message')}")


if __name__ == "__main__":
    main()
