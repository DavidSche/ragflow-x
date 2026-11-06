#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""2.1c 落地（数据侧锚定分块）— 为「无主体锚定」职责表生成块首补头文本。

背景（doc/264 §6.2 / f-0022 根因）：V1.0 引擎无 prompt 注入面，X 层不可在检索
阶段注入提示 → 混叠温床只能从切片层消除（Correct-by-design）。docreader 镜像
当前不可得 + §7.1 禁自建解析器，故「把锚定写进入库参数」的 X 侧可落地部分 =
**预生成补头文本清单**，镜像恢复后由入库链路在分块前把 header 作为块首行。

产出（纯文本、零网络、无污染可回归）：
  tmp/d1-anchor-patch.json  每份职责表「条号+主体+职责表」补头清单
  stdout                    摘要 + 3 条样例供人工核对主体推导

主体推导优先级（防主体漂移）：
  1) 条标题本身含主体词（ANCHOR_WORDS）→ 取条标题（截 20 字，如“县级以上人民
     政府负有…的部门”）；
  2) 否则取该条所属的最近上层节标题（如「二、安全生产监管职责分工」「（一）公司党委」）；
  3) 再否则回退文件级主体（doc 名去 .md）。
"""
from __future__ import annotations

import json
import re
from pathlib import Path

DOCS = Path(r"E:/study-place/AI-LLM/rag/KnowEval/datasets/d1-shandong-anquan/docs")
OUT = Path(__file__).resolve().parent.parent / "tmp"
OUT.mkdir(parents=True, exist_ok=True)

CLAUSE_RE = re.compile(r"^第([一二三四五六七八九十百千零]+)条[　\s]*(.*)$")
ITEM_RE = re.compile(r"^[（(]([一二三四五六七八九十]+)[）)]\s*(.+)$")
ANCHOR_WORDS = ["人民政府", "部门", "委员会", "机构", "乡镇", "开发区", "园区", "应急管理", "企业", "公司", "集团"]


def doc_name(doc: str) -> str:
    return doc.replace("_", " ").removesuffix(".md").strip()


def subject_for(title: str, last_heading: str, doc: str) -> str:
    if any(w in title for w in ANCHOR_WORDS):
        return title[:20]
    if last_heading:
        return last_heading[:24]
    base = doc_name(doc).split(" ")[0]
    return base if base else doc_name(doc)


def analyze(path: Path):
    lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    rows = []            # 职责表清单
    last_heading = ""    # 最近上层节标题
    for raw in lines:
        line = raw.strip()
        if not line:
            continue
        cm = CLAUSE_RE.match(line)
        if cm:
            title = cm.group(2).strip()
            subject = subject_for(title, last_heading, path.name)
            rows.append(
                {
                    "doc": path.name,
                    "clause": f"第{cm.group(1)}条",
                    "title": title[:40],
                    "subject": subject,
                    "source": (
                        "clause-title"
                        if any(w in title for w in ANCHOR_WORDS)
                        else ("last-heading" if last_heading else "doc-name")
                    ),
                }
            )
            continue
        im = ITEM_RE.match(line)
        if im:
            continue  # 职责条目行不参与节标题
        if len(line) <= 24 and not line.endswith("："):
            last_heading = line  # 一级/上级节标题（如「二、××监管职责分工」）
    return rows


def main() -> int:
    mds = sorted(DOCS.glob("*.md"))
    all_rows = []
    for md in mds:
        rs = analyze(md)
        for r in rs:
            r["header"] = f"【{doc_name(r['doc'])}·{r['clause']}｜主体责任：{r['subject']}】"
        all_rows.extend(rs)

    risk = sum(1 for r in all_rows if r["source"] in ("last-heading", "doc-name"))
    lines = [
        f"=== 2.1c 数据侧锚定分块补头清单（{len(mds)} doc / {len(all_rows)} 职责表）===",
        f"  其中：条标题已含主体词（source=clause-title）→ {len(all_rows) - risk} 份",
        f"       需上层节/文件级推导（source=last-heading|doc-name）→ {risk} 份",
        "",
        "=== 前 3 条样例（核对主体推导） ===",
    ]
    for r in all_rows[:3]:
        lines.append(f"  {r['doc']} | {r['clause']} 「{r['title'][:22]}」 <- {r['source']}({r['subject'][:24]})")
        lines.append(f"      header: {r['header']}")
    lines.append("")
    lines.append("=== 推导需人工兜底（doc-name）==")
    for r in all_rows:
        if r["source"] == "doc-name":
            lines.append(f"  {r['doc']} {r['clause']}")

    (OUT / "d1-anchor-patch.json").write_text(
        json.dumps(all_rows, ensure_ascii=False, indent=1), encoding="utf-8"
    )
    print("\n".join(lines))
    print(f"\n[落盘] {OUT / 'd1-anchor-patch.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
