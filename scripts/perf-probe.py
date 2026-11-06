#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""doc/264 5.3 性能基线探针 — 本地网关关键路径往返延迟（p50/p95/max）。

标准库实现（urllib），零第三方依赖，本地 :9191 可直接跑；接 CI 时注入
PERF_TOKEN / 或 PERF_USER+PERF_PASS 覆盖受保护路径。

端点分档：
  - 免鉴权: /api/v1/healthz
  - 鉴权（需 token）: GET /api/v1/search-apps 列表 → p95>3s 告警
                    GET /api/v1/search-apps/<id>/config 详情
  - 登录（免鉴权，需凭据）: POST /api/v1/auth/login

用法:
  PERF_BASE=http://127.0.0.1:9191 PERF_ITER=5 \
  PERF_USER=... PERF_PASS=... PERF_SEARCH_APP_ID=... \
  python scripts/perf-probe.py

输出: 各端点 times/分位数/告警 → 留档 tmp/perf/perf-baseline-<ts>.json
      并 append tmp/perf/perf-probe.log（供 5.3 基线演进对比）。
"""
from __future__ import annotations

import datetime as dt
import json
import os
import statistics
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT_DIR = ROOT / "tmp" / "perf"
LOG = ROOT / "tmp" / "perf-probe.log"
BASE = os.environ.get("PERF_BASE", "http://127.0.0.1:9191")
ITER = int(os.environ.get("PERF_ITER", "5"))
ALERT_P50 = float(os.environ.get("PERF_ALERT_P50", "3.0"))


def time_one(method: str, url: str, body: bytes | None = None, token: str | None = None) -> float | None:
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            resp.read()
        return time.perf_counter() - t0
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        print(f"  !! {url} -> {e}", file=sys.stderr)
        return None


def pct(xs: list[float], p: float) -> float:
    if not xs:
        return 0.0
    s = sorted(xs)
    k = (len(s) - 1) * p / 100.0
    lo = int(k)
    hi = min(lo + 1, len(s) - 1)
    return s[lo] + (s[hi] - s[lo]) * (k - lo)


def main() -> int:
    ts = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    user = os.environ.get("PERF_USER", "")
    passwd = os.environ.get("PERF_PASS", "")
    token = os.environ.get("PERF_TOKEN", "")
    sa_id = os.environ.get("PERF_SEARCH_APP_ID", "")

    report: dict = {"base": BASE, "ts": ts, "iter": ITER, "endpoints": {}}
    lines = [f"== perf-probe {ts} base={BASE} iter={ITER} =="]

    # 免鉴权健康检查
    health = [t for t in (time_one("GET", f"{BASE}/api/v1/healthz") for _ in range(ITER)) if t is not None]
    report["endpoints"]["healthz"] = {"n": len(health), "p50": pct(health, 50), "p95": pct(health, 95), "max": max(health) if health else None}
    lines.append(f"  healthz        p50={pct(health,50)*1000:.0f}ms p95={pct(health,95)*1000:.0f}ms")

    # 登录（凭据可选注入）
    if user and passwd:
        body = json.dumps({"username": user, "password": passwd}).encode()
        toks = []
        for _ in range(ITER):
            t0 = time.perf_counter()
            try:
                req = urllib.request.Request(f"{BASE}/api/v1/auth/login", data=body, method="POST")
                req.add_header("Content-Type", "application/json")
                with urllib.request.urlopen(req, timeout=10) as resp:
                    data = json.loads(resp.read())
                toks.append(time.perf_counter() - t0)
                token = (token or "") or (data.get("token") or (data.get("data") or {}).get("token") or "")
            except Exception as e:  # noqa: BLE001
                print(f"  !! login -> {e}", file=sys.stderr)
        report["endpoints"]["login"] = {"n": len(toks), "p50": pct(toks, 50), "p95": pct(toks, 95), "max": max(toks) if toks else None}
        lines.append(f"  login          p50={pct(toks,50)*1000:.0f}ms p95={pct(toks,95)*1000:.0f}ms")

    # 受保护核心路径（列表 + 详情）
    if token:
        lst = [t for t in (time_one("GET", f"{BASE}/api/v1/search-apps?page=1&page_size=20", token=token) for _ in range(ITER)) if t is not None]
        report["endpoints"]["search_apps_list"] = {"n": len(lst), "p50": pct(lst, 50), "p95": pct(lst, 95), "max": max(lst) if lst else None}
        lines.append(f"  search-apps    p50={pct(lst,50)*1000:.0f}ms p95={pct(lst,95)*1000:.0f}ms (list)")
        if sa_id:
            cfg_time = [
                t for t in (
                    time_one("GET", f"{BASE}/api/v1/search-apps/{sa_id}/config", token=token)
                    for _ in range(ITER)
                ) if t is not None
            ]
            report["endpoints"]["search_app_config"] = {"n": len(cfg_time), "p50": pct(cfg_time, 50), "p95": pct(cfg_time, 95), "max": max(cfg_time) if cfg_time else None}
            lines.append(f"  search-apps    p50={pct(cfg_time,50)*1000:.0f}ms p95={pct(cfg_time,95)*1000:.0f}ms (config)")

    # 告警闸：受保护核心路径 p50 > threshold
    alert = False
    for ep, st in report["endpoints"].items():
        if ep in ("search_apps_list", "search_app_config") and st.get("p50", 0) > ALERT_P50:
            alert = True
    verdict = "ALERT" if alert else "OK"
    lines.append(f"  【{verdict}】 p50 阈值={ALERT_P50}s")

    print("\n".join(lines))
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    (OUT_DIR / f"perf-baseline-{ts}.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    with open(LOG, "a", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n\n")
    print(f"\n[留档] {OUT_DIR / f'perf-baseline-{ts}.json'}")
    return 1 if alert else 0


if __name__ == "__main__":
    sys.exit(main())
