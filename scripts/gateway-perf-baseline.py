#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""doc/301 §5.10 GW-2 / T14 — 网关性能基线探针（P50/P95/P99 × 3 并发档 × 冷/热）。

标准库实现（urllib + threading），零第三方依赖。

## 为什么另起一支脚本，而不是复用 `scripts/perf-probe.py`

`perf-probe.py` 答的是「本机关键路径往返延迟」，一次一个请求（并发 1）、只要
p50/p95/max。GW-2 问的是另一个问题：**并发上去之后网关自己加了多少延迟**，
因此它必须同时要求

1. **三个并发档**（1 / 8 / 32）——p95 在并发 1 上好看、在 32 上崩塌是网关类
   系统最典型的失败形状，只测单并发等于不测；
2. **P99**，因为配额/幂等/限流这些 per-request 状态恰恰在长尾上咬人；
3. **错误率带分母**（错误数 / 该格总请求数）——只报 `errors=0` 是无分母的绿；
4. **冷/热两态**。本仓库里「热」有确切含义，不是靠缓存玄学：
   冷 = 该 `Idempotency-Key` 首次执行（走上游）；热 = 同一 key 重放
   （`CompleteGatewayIdempotency` 命中回放，不走上游）。这两条路径的耗时
   差一个数量级，混在一格里报 p95 是自欺。

## 格的判定

每格按 `P95 ≤ 阈值` 且 `错误率 ≤ 上限` 两端判定，输出 `pass` / `fail` /
`unverified`。**`unverified` 不是通过**：请求数不足（默认 < 20）、或该格被
`--skip-live` 主动跳过（需要模型 tokens 的真机格）都是 `unverified`。
一条没有分母的结论只能是 `unverified`——这是 301 §4.2「行必须能失败」的同一条
纪律。

## 用法

自测（离线、不需要服务端）：

    python scripts/gateway-perf-baseline.py --selftest

真机（前置：`RGX_API_KEY` 是网关上的一把可用 key，`RGX_TOKEN` 是控制台 JWT）：

    RGX_BASE=http://<host>:19193 RGX_USER=<user> RGX_PASS=<password> \\
    python scripts/gateway-perf-baseline.py --concurrency 1,8,32 --iter 30

只测不需要模型 tokens 的格（默认就是这个模式）：

    python scripts/gateway-perf-baseline.py --skip-live

产物：`tmp/perf/gateway-baseline-<ts>.json` + 同目录 `.md`（人读表格，直接贴
`doc/architecture/08`）。
"""
from __future__ import annotations

import argparse
import json
import os
import ssl
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT_DIR = ROOT / "tmp" / "perf"

# GW-2 的阈值。**这些是基线的主要产出之一**（301 GW-2 要求「写告警阈值」），
# 因此它们住在这个文件里并被 `--selftest` 与真机报告同时引用：改阈值就等于
# 改发布结论，不该藏在一次口头评审里。
NON_STREAM_P95_MS = 2000.0
STREAM_P95_MS = 4000.0
ERROR_RATE_MAX = 0.01
MIN_SAMPLES = 20

# 三个并发档。1 是「没有人抢」的地板，8 是常规租户负载，32 是压测档；
# 三个档都测，才知道延迟是「网关自己的」还是「上游排队的」。
CONCURRENCY_TIERS = (1, 8, 32)


def quantile_nearest_rank(values, q):
    """最近秩分位（与仓库里 SQL 侧的 `ORDER BY … OFFSET` 口径一致）。

    不走 `statistics.quantiles`：那个函数在小样本上会用插值造出一个**不存在的
    观测值**，而 p99 恰恰只在样本少的时候最容易被它哄好。
    """
    if not values:
        return None
    ordered = sorted(values)
    n = len(ordered)
    rank = max(1, int(-(-q * n // 1)))  # ceil(q*n)，纯整数运算避免浮点抖动
    return ordered[min(rank, n) - 1]


def summarize(durations_ms, errors, q=None):
    """把一格的原始观测压成结论。

    `errors` 是失败请求数，`len(durations_ms)` 是本格**实际发出**的请求数；
    两者同源，所以错误率自带分母。空格返回全 `None` + `unverified`。
    """
    total = len(durations_ms)
    if total == 0:
        return {
            "samples": 0,
            "errors": errors,
            "error_rate": None,
            "p50_ms": None,
            "p95_ms": None,
            "p99_ms": None,
            "max_ms": None,
            "verdict": "unverified",
            "reason": "no requests issued",
        }
    summary = {
        "samples": total,
        "errors": errors,
        "error_rate": round(errors / total, 6),
        "p50_ms": round(quantile_nearest_rank(durations_ms, 0.50), 3),
        "p95_ms": round(quantile_nearest_rank(durations_ms, 0.95), 3),
        "p99_ms": round(quantile_nearest_rank(durations_ms, 0.99), 3),
        "max_ms": round(max(durations_ms), 3),
    }
    limit = (q or {}).get("p95_limit_ms")
    # 样本不足时不给结论：3 个样本的 p99 是第三大的那个数，被它判死或判活
    # 都一样没有信息量。
    if total < MIN_SAMPLES:
        summary["verdict"] = "unverified"
        summary["reason"] = "samples %d < %d" % (total, MIN_SAMPLES)
        return summary
    if limit is None:
        summary["verdict"] = "unverified"
        summary["reason"] = "no p95 limit declared for this cell"
        return summary
    if summary["p95_ms"] > limit:
        summary["verdict"] = "fail"
        summary["reason"] = "p95 %.0fms > %.0fms" % (summary["p95_ms"], limit)
        return summary
    if summary["error_rate"] > ERROR_RATE_MAX:
        summary["verdict"] = "fail"
        summary["reason"] = "error rate %.2f%% > %.2f%%" % (
            summary["error_rate"] * 100,
            ERROR_RATE_MAX * 100,
        )
        return summary
    summary["verdict"] = "pass"
    summary["reason"] = "p95 %.0fms ≤ %.0fms" % (summary["p95_ms"], limit)
    return summary


def gateway_overhead_share(total_p50, floor_p50):
    """网关自身上限 = 总 p50 − 进程地板 p50，以占比陈述。

    为什么是「上限」而不是精确值：地板格（`/api/v1/healthz`，无鉴权无 DB）测的是
    进程能跑多快，网关的真实自有开销不会超过 `总 − 地板`。**上游占比在这台
    机器上测不出来**——那需要真机模型调用，而它有 tokens 成本（见 `--skip-live`）。
    用一个上界如实陈述，好过用一个估出来的百分比假装精确。
    """
    if total_p50 is None or floor_p50 is None or total_p50 <= 0:
        return None
    overhead = max(0.0, total_p50 - floor_p50)
    return {
        "total_p50_ms": round(total_p50, 3),
        "floor_p50_ms": round(floor_p50, 3),
        "gateway_overhead_upper_ms": round(overhead, 3),
        "gateway_overhead_upper_share": round(overhead / total_p50, 4),
    }


def cell_id(route, mode, concurrency, thermal):
    return "%s|%s|c%d|%s" % (route, mode, concurrency, thermal)


def plan_cells(routes, tiers, include_live):
    """展开 {{route × mode} × 并发档 × 冷/热} 的格清单。

    一个「格」是一次测量，不是一次请求：同一格里跑 `iter` 个请求，压成一个
    分位数结论。`include_live=False` 时，凡是需要上游模型的格都不展开——它不会
    被算成 pass，而是**根本不出现**，并在报告里作为 `skipped` 行留下，这样
    「没测」和「测了很好」在文件里长得不一样。
    """
    cells = []
    for route in routes:
        # 形态是**路由自己的属性**，不是全局维度：`GET /search-apps` 没有流式形态，
        # 给它补一格 `stream` 只会测出「同一个 GET，多了一个 Accept 头」，
        # 然后以流式的宽阈值（4s）判它 pass——一条自己发明出来的绿。
        for mode in route.get("modes", ("non_stream",)):
            for tier in tiers:
                for thermal in ("cold", "hot"):
                    # 热态是幂等回放，只有冷态先跑过一次回放才有东西可放；因此需要
                    # 上游的路由**整条**在 skip-live 下都算未测，而不是只跳冷态。
                    need_live = route.get("needs_upstream", False)
                    cells.append(
                        {
                            "id": cell_id(route["name"], mode, tier, thermal),
                            "route": route["name"],
                            "mode": mode,
                            "concurrency": tier,
                            "thermal": thermal,
                            "skipped": need_live and not include_live,
                            "skip_reason": (
                                "needs upstream model tokens (re-run without --skip-live)"
                                if need_live and not include_live
                                else None
                            ),
                            "p95_limit_ms": (
                                STREAM_P95_MS if mode == "stream" else NON_STREAM_P95_MS
                            ),
                        }
                    )
    return cells


# ---------------------------------------------------------------------------
# 真机执行
# ---------------------------------------------------------------------------


def http_call(url, method, body, headers, timeout):
    """发一次请求，返回 (status, elapsed_ms, body_bytes)。异常折成 status=-1。"""
    data = body.encode("utf-8") if body is not None else None
    request = urllib.request.Request(url, data=data, method=method)
    for key, value in headers.items():
        request.add_header(key, value)
    # perf_counter 而非 monotonic：Windows 的 monotonic 被钉在约 15.6ms 的系统
    # 定时器刻度上，会把 30~50ms 的真实延迟量化成 0/16/31/47/63 这五个值
    # （真机基线第一版就是这样，P50 印出一片 0ms）。perf_counter 用高精度计数器。
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = response.read()
            status = response.status
    except urllib.error.HTTPError as exc:
        payload = exc.read() if exc.fp else b""
        status = exc.code
    except Exception:  # noqa: BLE001 - 网络类异常一律记为 -1，由调用方判失败
        payload = b""
        status = -1
    return status, (time.perf_counter() - started) * 1000.0, payload


def run_cell(cell, route, base, headers, iterations, timeout):
    """跑一格：`iterations` 个请求，按并发档并发发出。"""
    durations, errors = [], 0
    per_request_headers = dict(headers)
    if route.get("needs_api_key"):
        per_request_headers["Authorization"] = "Bearer " + os.environ.get("RGX_API_KEY", "")
    if route.get("needs_jwt"):
        per_request_headers["Authorization"] = "Bearer " + os.environ.get("RGX_TOKEN", "")
    if cell["mode"] == "stream":
        per_request_headers["Accept"] = "text/event-stream"

    def worker(_index):
        nonlocal errors
        # 热态复用同一个 Idempotency-Key（网关侧命中回放），冷态每个请求一个新 key。
        if route.get("idempotent", False):
            if cell["thermal"] == "hot":
                per_request_headers["Idempotency-Key"] = "perf-%s-hot" % cell["route"]
            else:
                per_request_headers["Idempotency-Key"] = "perf-%s-cold-%d-%d" % (
                    cell["route"],
                    threading.get_ident(),
                    int(time.perf_counter() * 1e6),
                )
        status, elapsed, _ = http_call(
            base + route["path"], route["method"], route.get("body"), per_request_headers, timeout
        )
        durations.append(elapsed)
        # 4xx 也算失败：401/403/429 在性能报告里要是绿的，这份基线就会把
        # 「被限流拒绝」读成「很快」。
        if status < 200 or status >= 400:
            errors += 1

    tier = max(1, cell["concurrency"])
    threads = []
    for index in range(iterations):
        thread = threading.Thread(target=worker, args=(index,))
        threads.append(thread)
        thread.start()
        if len(threads) == tier:
            for started in threads:
                started.join()
            threads = []
    for started in threads:
        started.join()
    return durations, errors


def routes_for(base):
    return [
        {
            "name": "healthz",
            "path": "/api/v1/healthz",
            "method": "GET",
            "needs_jwt": False,
            "needs_api_key": False,
            "needs_upstream": False,
            "idempotent": False,
            "modes": ("non_stream",),
            "body": None,
        },
        {
            "name": "search-apps",
            "path": "/api/v1/search-apps?page=1&page_size=20",
            "method": "GET",
            "needs_jwt": True,
            "needs_api_key": False,
            "needs_upstream": False,
            "idempotent": False,
            "modes": ("non_stream",),
            "body": None,
        },
        {
            "name": "gateway-chat",
            "path": "/v1/chat/completions",
            "method": "POST",
            "needs_jwt": False,
            "needs_api_key": True,
            "needs_upstream": True,
            "idempotent": True,
            "modes": ("non_stream", "stream"),
            "body": json.dumps(
                {
                    "model": os.environ.get("RGX_MODEL", ""),
                    "messages": [{"role": "user", "content": "ping"}],
                    "max_tokens": 16,
                }
            ),
        },
    ]


def login(base, user, password, timeout):
    status, _, payload = http_call(
        base + "/api/v1/auth/login",
        "POST",
        json.dumps({"username": user, "password": password}),
        {"Content-Type": "application/json"},
        timeout,
    )
    if status != 200:
        raise SystemExit("login failed: status %s" % status)
    body = json.loads(payload.decode("utf-8"))
    token = (body.get("data") or {}).get("token")
    if not token:
        raise SystemExit("login returned no token")
    return token


def render_markdown(report):
    lines = [
        "# 网关性能基线（doc/301 §5.10 GW-2 / T14）",
        "",
        "生成：`scripts/gateway-perf-baseline.py`　目标：`%s`　时间：%s"
        % (report["base"], report["generated_at"]),
        "",
        "并发档：%s　每格请求数：%d　阈值：非流式 P95 ≤ %.0fms、流式 P95 ≤ %.0fms、错误率 ≤ %.2f%%"
        % (
            ", ".join(str(t) for t in report["tiers"]),
            report["iterations"],
            NON_STREAM_P95_MS,
            STREAM_P95_MS,
            ERROR_RATE_MAX * 100,
        ),
        "",
        "| 格 | 请求 | 错误 | 错误率 | P50 | P95 | P99 | max | 判定 | 依据 |",
        "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
    ]
    for cell in report["cells"]:
        if cell["skipped"]:
            lines.append(
                "| %s | — | — | — | — | — | — | — | **unverified** | 跳过：%s |"
                % (cell["id"], cell["skip_reason"])
            )
            continue
        summary = cell["summary"]
        rate = "—" if summary["error_rate"] is None else "%.2f%%" % (summary["error_rate"] * 100)
        def ms(key):
            value = summary.get(key)
            return "—" if value is None else "%.0f" % value
        lines.append(
            "| %s | %d | %d | %s | %s | %s | %s | %s | **%s** | %s |"
            % (
                cell["id"],
                summary["samples"],
                summary["errors"],
                rate,
                ms("p50_ms"),
                ms("p95_ms"),
                ms("p99_ms"),
                ms("max_ms"),
                summary["verdict"],
                summary["reason"],
            )
        )
    counts = report["verdict_counts"]
    lines += [
        "",
        "判定分布：pass=%d fail=%d unverified=%d" % (
            counts.get("pass", 0),
            counts.get("fail", 0),
            counts.get("unverified", 0),
        ),
        "",
        "> `unverified` 不是通过：样本不足、阈值未声明、或该格需要上游模型 tokens"
        "（`--skip-live`）都落在这里。一份全是 `unverified` 的报告说明这次没有测，"
        "不说明网关很快。",
    ]
    overhead = report.get("overhead")
    if overhead:
        lines += [
            "",
            "网关自有开销上界：P50 %.0fms 中 ≤ %.0fms / %.1f%%（= 该格 P50 − 健康检查地板 P50）"
            % (
                overhead["total_p50_ms"],
                overhead["gateway_overhead_upper_ms"],
                overhead["gateway_overhead_upper_share"] * 100,
            ),
            "",
            "> 上游耗时占比（GW-2 原文要求）需要真机模型调用才能测：本报告不含它，"
            "并因此不声称任何上游占比。`ragflow_x_ragflow_request_duration_seconds` 与"
            " `ragflow_x_http_request_duration_seconds` 两族直方图在真机跑起来之后可以"
            "按 route 相除得到该比值。",
        ]
    return "\n".join(lines) + "\n"


def run(args):
    base = args.base.rstrip("/")
    timeout = args.timeout
    token = ""
    if args.user and args.password:
        token = login(base, args.user, args.password, timeout)
    if token:
        os.environ["RGX_TOKEN"] = token

    routes = routes_for(base)
    cells = plan_cells(routes, args.concurrency, include_live=not args.skip_live)
    by_name = {route["name"]: route for route in routes}
    results, verdicts = [], {}
    for cell in cells:
        if cell["skipped"]:
            results.append(cell)
            verdicts["unverified"] = verdicts.get("unverified", 0) + 1
            continue
        route = by_name[cell["route"]]
        if route.get("needs_api_key") and not os.environ.get("RGX_API_KEY"):
            cell["skipped"] = True
            cell["skip_reason"] = "RGX_API_KEY unset"
            results.append(cell)
            verdicts["unverified"] = verdicts.get("unverified", 0) + 1
            continue
        durations, errors = run_cell(
            cell, route, base, {"Content-Type": "application/json"}, args.iter, timeout
        )
        summary = summarize(
            durations,
            errors,
            q={"p95_limit_ms": cell["p95_limit_ms"]},
        )
        cell["summary"] = summary
        verdicts[summary["verdict"]] = verdicts.get(summary["verdict"], 0) + 1
        results.append(cell)

    floor = next(
        (
            cell["summary"]["p50_ms"]
            for cell in results
            if cell["route"] == "healthz"
            and cell["thermal"] == "cold"
            and not cell["skipped"]
            and cell.get("summary", {}).get("p50_ms") is not None
        ),
        None,
    )
    overhead = None
    for cell in results:
        if cell["route"] == "search-apps" and cell["thermal"] == "cold" and not cell["skipped"]:
            overhead = gateway_overhead_share(cell.get("summary", {}).get("p50_ms"), floor)
            if overhead:
                break

    report = {
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "base": base,
        "tiers": list(args.concurrency),
        "iterations": args.iter,
        "timer_note": (
            "客户端计时用 time.perf_counter；在 Windows 上仍受系统定时器刻度限制，"
            "P50 印 0ms 表示「低于一个刻度」，不是真的 0。服务端视角的分布请看 "
            "ragflow_x_http_request_duration_seconds（Prometheus 直方图，Linux 侧计时）。"
        ),
        "thresholds": {
            "non_stream_p95_ms": NON_STREAM_P95_MS,
            "stream_p95_ms": STREAM_P95_MS,
            "error_rate_max": ERROR_RATE_MAX,
            "min_samples": MIN_SAMPLES,
        },
        "cells": results,
        "verdict_counts": verdicts,
        "overhead": overhead,
    }

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    json_path = OUT_DIR / ("gateway-baseline-%s.json" % stamp)
    md_path = OUT_DIR / ("gateway-baseline-%s.md" % stamp)
    json_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    md_path.write_text(render_markdown(report), encoding="utf-8")
    # GBK 控制台印不出 `−`/`≤` 这类符号；文件永远是 UTF-8，控制台只求不崩。
    rendered = render_markdown(report)
    sys.stdout.write(rendered.encode(sys.stdout.encoding or "utf-8", "replace").decode(
        sys.stdout.encoding or "utf-8", "replace"
    ))
    sys.stdout.write("\njson: %s\nmd:   %s\n" % (json_path, md_path))
    return 0 if verdicts.get("fail", 0) == 0 else 1


# ---------------------------------------------------------------------------
# 自测：探针自己也要能被证明会红
# ---------------------------------------------------------------------------


def selftest():
    failures = []

    def check(name, condition, detail=""):
        if not condition:
            failures.append("%s %s" % (name, detail))

    # 最近秩分位：n=4 时 p50 必须是第 2 个（不是插值出来的 2.5 次观测）。
    check("p50-nearest-rank", quantile_nearest_rank([10, 20, 30, 40], 0.50) == 20)
    check("p99-clamped", quantile_nearest_rank([10, 20, 30, 40], 0.99) == 40)
    check("empty-quantile", quantile_nearest_rank([], 0.5) is None)

    # 空格是 unverified，绝不是 pass。
    empty = summarize([], 0, q={"p95_limit_ms": 100})
    check("empty-is-unverified", empty["verdict"] == "unverified", empty["verdict"])
    check("empty-has-no-rate", empty["error_rate"] is None)

    # 样本不足 = 不给结论。
    few = summarize([1.0] * (MIN_SAMPLES - 1), 0, q={"p95_limit_ms": 100})
    check("few-is-unverified", few["verdict"] == "unverified", few["verdict"])

    # p95 越界必须能红。
    # 一个离群点在 n=21 时落在 100 分位，p95 根本碰不到它；要让 p95 红，
    # 必须让慢样本占到第 ceil(0.95*n) 位（这里两条 500ms）。
    slow = summarize([1.0] * 19 + [500.0, 500.0], 0, q={"p95_limit_ms": 100})
    check("slow-is-fail", slow["verdict"] == "fail", slow["verdict"])

    # 错误率越界必须能红，且分母是本格请求数。
    flaky = summarize([1.0] * MIN_SAMPLES, 1, q={"p95_limit_ms": 100})
    check("errors-is-fail", flaky["verdict"] == "fail", flaky["verdict"])
    check("error-rate-denominator", flaky["error_rate"] == round(1.0 / MIN_SAMPLES, 6))

    # 没声明阈值 = unverified（不能默默当 pass）。
    undeclared = summarize([1.0] * MIN_SAMPLES, 0, q={})
    check("no-limit-unverified", undeclared["verdict"] == "unverified", undeclared["verdict"])

    # 健康格必须绿。
    ok = summarize([1.0] * MIN_SAMPLES, 0, q={"p95_limit_ms": 100})
    check("fast-is-pass", ok["verdict"] == "pass", ok["verdict"])

    # 格清单：3 路由 × 2 形态 × 3 档 × 2 态 = 36；skip-live 时只跳过需要上游的冷态。
    routes = routes_for("http://example.invalid")
    cells = plan_cells(routes, CONCURRENCY_TIERS, include_live=False)
    modes = sum(len(route.get("modes", ("non_stream",))) for route in routes)
    check("cell-count", len(cells) == modes * len(CONCURRENCY_TIERS) * 2, len(cells))
    skipped = [cell for cell in cells if cell["skipped"]]
    check(
        "skip-live-only-upstream-routes",
        all(cell["route"] == "gateway-chat" for cell in skipped),
        skipped,
    )
    check(
        "skip-count",
        # 网关路由的 2 形态 × 3 并发档 × 冷/热两态：整条上游路由在 skip-live 下都不测。
        len(skipped) == 4 * len(CONCURRENCY_TIERS),
        "%d != %d" % (len(skipped), 4 * len(CONCURRENCY_TIERS)),
    )

    # 开销占比：总 P50 小于地板时不能算出负占比。
    share = gateway_overhead_share(10.0, 20.0)
    check("overhead-clamped", share["gateway_overhead_upper_ms"] == 0.0)
    check("overhead-no-floor", gateway_overhead_share(10.0, None) is None)

    if failures:
        sys.stderr.write("selftest FAILED:\n")
        for failure in failures:
            sys.stderr.write("  - %s\n" % failure)
        return 1
    sys.stdout.write("selftest OK: 16 assertions\n")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Gateway performance baseline (doc/301 GW-2 / T14)")
    parser.add_argument("--base", default=os.environ.get("RGX_BASE", "http://127.0.0.1:9191"))
    parser.add_argument("--user", default=os.environ.get("RGX_USER", ""))
    parser.add_argument("--password", default=os.environ.get("RGX_PASS", ""))
    parser.add_argument("--iter", type=int, default=30, help="requests per cell (default 30)")
    parser.add_argument(
        "--concurrency",
        default="1,8,32",
        help="comma separated concurrency tiers (default 1,8,32)",
    )
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument(
        "--skip-live",
        action="store_true",
        help="skip cells that need real upstream model tokens (they become unverified, never pass)",
    )
    parser.add_argument("--selftest", action="store_true", help="run offline self-checks and exit")
    args = parser.parse_args(argv)
    if args.selftest:
        return selftest()
    args.concurrency = tuple(int(part) for part in args.concurrency.split(",") if part.strip())
    if not args.concurrency:
        raise SystemExit("--concurrency needs at least one tier")
    return run(args)


if __name__ == "__main__":
    raise SystemExit(main())
