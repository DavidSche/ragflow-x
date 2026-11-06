#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""doc/264 3.3 Variance watch — 多轮 judge checkpoint 的跨轮稳定性观测。

Measurable-stability 巡检工具：同一题集多轮判分（同探针复打 / rejudge）的
正确率极差与 qid 级判定一致性，波动超阈值即告警留档。

每轮文件为 JSON Lines，每行至少含：
  {"qid": "...", "judgement": {"verdict": "CORRECT|...", ...}}
（tmp/fresh29_run/out/checkpoint.r1.jsonl 等即为该格式。）

用法:
  python scripts/variance-watch.py \
    --label "fresh29 r1 vs rejudge (2026-11-06)" \
    --files tmp/fresh29_run/out/checkpoint.r1.jsonl \
            tmp/fresh29_run/judged_rejudge/out/checkpoint.r1.jsonl \
    --alert-pts 5

输出: 每轮正确率（百分点）、全轮正确率极差（≥ alert-pts 打 ALERT）、
      跨轮 qid 级判定不一致清单；追加留档 tmp/variance-watch.log。
退出码: 0 = 稳定（无告警）; 1 = 触发告警（接 CI 门禁）。
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import sys
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOG = ROOT / "tmp" / "variance-watch.log"


def load_round(path: str) -> list[dict]:
    rows = []
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            obj = json.loads(line)
            rows.append(obj)
    return rows


def verdict_of(row: dict) -> str:
    return str(row.get("judgement", {}).get("verdict", "UNKNOWN"))


def correct_rate(rows: list[dict]) -> tuple[float, int, dict[str, int]]:
    cnt = Counter(verdict_of(r) for r in rows)
    n = len(rows) or 1
    return 100.0 * cnt.get("CORRECT", 0) / n, len(rows), dict(cnt)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--files", nargs="+", required=True, help="每轮 judge checkpoint jsonl（一轮一个文件）")
    ap.add_argument("--label", default="", help="本次观测标签（建议含日期/题集/轮次）")
    ap.add_argument("--alert-pts", type=float, default=5.0, help="正确率极差(百分点)告警阈值，默认 5")
    args = ap.parse_args()

    ts = dt.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    rounds = []  # (name, rows)
    for fp in args.files:
        p = Path(fp).resolve()
        if not p.exists():
            print(f"!! 文件不存在（跳过）: {fp}", file=sys.stderr)
            continue
        try:
            name = str(p.relative_to(ROOT))
        except ValueError:
            name = str(p)
        rounds.append((name, load_round(str(p))))
    if len(rounds) < 2:
        print("!! 至少需要两轮 checkpoint 才能算极差", file=sys.stderr)
        return 2

    lines = [f"== variance-watch {ts} label={args.label or '-'} =="]
    rates = []
    for name, rows in rounds:
        rate, n, cnt = correct_rate(rows)
        rates.append(rate)
        lines.append(f"  [{name}] n={n} CORRECT={cnt.get('CORRECT',0)} rate={rate:.2f}% verdicts={cnt}")

    spread = max(rates) - min(rates)
    lines.append(f"  正确率极差 (max-min) = {spread:.2f}pt  (阈值 {args.alert_pts}pt)")
    alert = spread >= args.alert_pts

    # qid 级跨轮一致性（仅统计在全部轮次都出现的 qid）
    by_qid: dict[str, list[tuple[str, str]]] = defaultdict(list)  # qid -> [(round, verdict)]
    for name, rows in rounds:
        for r in rows:
            q = r.get("qid", "?")
            if q:
                by_qid[q].append((name, verdict_of(r)))
    unstable = [
        (q, vs)
        for q, vs in by_qid.items()
        if len(vs) == len(rounds) and len({v for _, v in vs}) > 1
    ]
    if unstable:
        lines.append(f"  跨轮判定不一致题 {len(unstable)}/共 {len(by_qid)} qid:")
        for q, vs in sorted(unstable):
            lines.append(f"    {q} -> {vs}")
    else:
        lines.append("  跨轮判定完全一致（qid 级稳定）")

    verdict = "ALERT" if alert else "OK"
    lines.append(f"  【{verdict}】 spread={spread:.2f}pt")
    out = "\n".join(lines)
    print(out)

    LOG.parent.mkdir(parents=True, exist_ok=True)
    with open(LOG, "a", encoding="utf-8") as fh:
        fh.write(out + "\n\n")
    print(f"\n[留档] {LOG}")
    return 1 if alert else 0


if __name__ == "__main__":
    sys.exit(main())
