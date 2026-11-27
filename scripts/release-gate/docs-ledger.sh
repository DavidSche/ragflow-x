#!/usr/bin/env bash
# s18.docs-ledger — the two rituals 304 §7 items 2 and 3 ask for, made checkable.
#
# §7 item 2: 「§2 的 `证据` 列在每次里程碑后补齐（命令 + 输出摘要）」.
# §7 item 3: 「§4 漂移日志在每次代码评审后检查一次」.
#
# Both are prose rituals, and prose rituals decay in a specific way: not by being deleted,
# but by *silently* going stale — a row whose 证据 cell was written for the previous round,
# a status word invented on the spot ("基本完成"), a 偏差 row whose 处置 names a disposition
# outside the three §0 allows, a §7 item number reused after an insert. None of those make
# the document look wrong; they make it quietly untrue, which is worse.
#
# What a checker *can* do here is exactly the part that is a shape, not a judgement:
#
#   ① §2  — every row matches its own table's header (the two §2 tables have different
#           column counts, so the header is derived per table, never hard-coded); the status
#           word is one of §0's five; 证据 is non-empty; 更新于 is a real date; and the id
#           set is exactly T01–T25 + T04b + T27 + T28, each once (a dropped row is the failure mode that
#           reads as "no news");
#   ② §3  — D-NN contiguous from D-01, 事实/影响/处置 all non-empty, 处置 inside §0's three
#           values (a fourth word makes the column uncountable), dates parse;
#   ③ §6  — changelog versions strictly descending (an inserted release out of order is how
#           a changelog stops being a history);
#   ④ §7  — item numbers contiguous 1..N (the table is *the* list of what is left);
#   ⑤ 301 §14.J — the E-numbered tail (scoped to §14.J, not the whole file) is contiguous,
#           and the footer's head is *derived*: 「E41–**E86** 已用，下一条从 **E87** 起」 must
#           agree with the largest E row present. Hard-coding the number would make this
#           check lie the moment the next row lands;
#   ⑥ 304 §4 — the same tail, starting at E41 (it is 301 §14.E's continuation), same derived footer;
#   ⑦ 305 — `## 2.x` sections strictly ascending, no duplicates;
#   ⑨ 305 §5 — every changelog row is a *row* (exactly 4 cells, a real date, a `v0.x`)
#           and the versions strictly descend. This exists because two of them stopped
#           being rows: a repair inserted a version *inside* another version's line, so
#           `v0.37 | ... | 2026-11-24 |` sat on its own with no leading `| date |` and the
#           document read fine in a terminal. A row no regex matches is a row the check
#           never sees — which is why ③ now also refuses the *shape* it cannot parse.
#   ⑧ 304 §7/§5.3 — every `E<n>` they cite (n ≥ 41) is a row of 304 §4. A closure note that
#           points at a drift row nobody wrote is worse than no note: it reads as "this was
#           handled" and sends the reader looking for a record that does not exist. E1–E40 are
#           skipped on purpose — they live in 301 §14.E, which is frozen and not read here.
#
# What this does NOT check, said out loud: whether a 证据 cell's *content* still proves the
# claim it sits beside. That is a review, and it is exactly what §7 items 2/3 ask a person
# to do; the gate only guarantees the cell is not empty and the vocabulary did not drift.
#
# ⑩ `--write-snapshot` freezes exactly these facts into `scripts/release-gate/docs-ledger.json`
# (304 §7 item 26, route ① 2026-11-23). The snapshot is what `s19.docs-ledger-snapshot` reads
# in a tree without `doc/`, so CI judges the ledger instead of recording N/A for it. It is
# written *only* when every check above passes: a broken document is never frozen into an
# artifact that a clean checkout would then read as "checked".
#
# Usage:
#   bash scripts/release-gate/docs-ledger.sh                  # the check (hermetic, <1s)
#   bash scripts/release-gate/docs-ledger.sh --selftest        # prove the check can fail
#   bash scripts/release-gate/docs-ledger.sh --write-snapshot  # freeze the facts (needs doc/)
#
# LEDGER_304 / LEDGER_301 / LEDGER_305 exist for `--selftest` only: proving a refusal must
# not require editing a document that five audits already green.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

D304="${LEDGER_304:-doc/develop/304-v0.7实施情况与跟踪：可信证据、问答收口与网关保障.md}"
D301="${LEDGER_301:-doc/develop/301-v0.7下一阶段工作计划：可信证据、问答收口与网关保障.md}"
D305="${LEDGER_305:-doc/develop/305-v0.7实测与演练报告：网关压测、E0-E3与live drill.md}"

SNAP="${LEDGER_SNAPSHOT_OUT:-scripts/release-gate/docs-ledger.json}"
MODE="check"
[ "${1:-}" = "--write-snapshot" ] && MODE="snapshot"

say()  { printf '%s\n' "$*"; }

# Python's stdout on Windows defaults to the console code page (cp936 here), while this
# script's own strings are UTF-8 bytes: `--selftest` greps a UTF-8 fragment against the
# checker's output, so the two encodings made every refusal read as «refused for the wrong
# reason». Pin the child's encoding instead of normalising the grep.
export PYTHONIOENCODING=utf-8

# `command -v python3` on Windows can resolve to the Store alias, which exits 9009 and
# prints nothing; the first candidate that can run `-c pass` wins. Same discipline as
# `pick_python()` in scripts/release-gate.sh.
pick_python() {
  local c
  for c in python3 python py; do
    if command -v "$c" >/dev/null 2>&1 && "$c" -c pass >/dev/null 2>&1; then
      printf '%s' "$c"; return 0
    fi
  done
  return 1
}

# Picked before the selftest block, because the selftest itself builds mutations with it:
# it was previously assigned *after* the `--selftest` early-exit, so `--selftest` died on
# `PYTHON: unbound variable` and reported the crash as a failure of the document.
PYTHON="$(pick_python)" || { say "docs-ledger: FAIL — no python on PATH, so the ledger is unreadable (unreadable is not 'consistent')"; exit 1; }

# ── selftest: fourteen mutations, each of which must be refused ───────────
if [ "${1:-}" = "--selftest" ]; then
  say "selftest: can this check refuse? (an invented status word, a dropped §7 row, a footer that disagrees with its own table, a changelog out of order, a fourth disposition word, a hole in §4's drift log, a §7 note that cites a drift row nobody wrote, a changelog row that lost its date cell in each of the two documents, a governed row out of order, a governed row renumbered onto a taken number, a §5 that lost its governor date, and a task table no milestone knows about)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  mutate() {  # mutate <name> <file> <old> <new>: exits nonzero if the mutation did not apply
    local name="$1" file="$2" old="$3" new="$4"
    "$PYTHON" - "$file" "$old" "$new" "$tmp/$name" <<'PY'
import io, sys
src, old, new, out = sys.argv[1:5]
s = io.open(src, encoding='utf-8').read()
if s.count(old) != 1:
    sys.stderr.write("selftest: anchor %r appears %d times in %s\n" % (old, s.count(old), src))
    sys.exit(2)
io.open(out, 'w', encoding='utf-8', newline='\n').write(s.replace(old, new))
PY
    if [ $? -ne 0 ]; then say "SELFTEST FAIL: could not build the mutation <$name>"; exit 1; fi
  }

  expect_refusal() {  # expect_refusal <sentence fragment> [env assignments...]
    local want="$1"; shift
    local out rc
    out="$(env "$@" bash "$0" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -qF -- "$want"; then
      say "SELFTEST FAIL: a broken document was accepted (or refused for the wrong reason)"
      say "  wanted: $want"
      printf '%s\n' "$out" | sed -n '1,12p'
      exit 1
    fi
    say "ok      $want"
  }

  # ① a status word §0 does not allow. It reads like a status, sorts like one, and would sit
  # in the table for months: the whole point of a closed set is that this cannot happen.
  # The anchor is a *status* + a unique句, not a row number: inserting a task row above it
  # must not silently turn this mutation into "anchor appears 0 times".
  mutate "bad-status" "$D304" "**已完成** | \`internal/handler/report.go:766-768\`" \
    "**基本完成** | \`internal/handler/report.go:766-768\`"
  expect_refusal "状态 基本完成 不在 §0 的闭集" \
    LEDGER_304="$tmp/bad-status"

  # ② a §7 row removed. The list still reads fine — 1, 2, 4 … — and «what is left» now
  # silently omits one item, which is the only thing this table is for.
  mutate "drop-s7" "$D304" "| 3 | §4 漂移日志在每次代码评审后检查一次" \
    "| 3x | §4 漂移日志在每次代码评审后检查一次"
  expect_refusal "§7 的编号不连续" \
    LEDGER_304="$tmp/drop-s7"

  # ③ the footer's E-head drifts from the table. The mutation inserts one digit instead of
  # naming a number: a hard-coded "E84" here would silently stop being a mutation the very
  # next time a drift row is appended (the anchor would vanish, not the defect).
  mutate "s14j-head" "$D301" "已用，下一条从 **E" "已用，下一条从 **E1"
  expect_refusal "§14.J 的页脚与表自身的最大 E 号不一致" \
    LEDGER_301="$tmp/s14j-head"

  # ④ a changelog row that jumps *up* inside the history (v1.29, v1.40, v1.27). A changelog is
  # a history; unordered rows make «when did this land» unanswerable without reading dates.
  # The anchor is a middle row: bumping the *top* row is legal and would not be a mutation.
  mutate "changelog" "$D304" "| v1.28 |" "| v1.40 |"
  expect_refusal "§6 的版本号不是严格递减" \
    LEDGER_304="$tmp/changelog"

  # ⑤ a disposition word outside §0's three. «已消除» / «按方案改方案» / «补方案 + 改实现» all
  # read like dispositions; none of them can be counted as «改代码» or «改方案», which is the
  # only reason the column exists (7 of 11 rows were outside the set when this check landed).
  mutate "disposition" "$D304" '`接受偏差（记录风险）`：跑测试用' '`已消除`：跑测试用'
  expect_refusal "的处置 '已消除' 不在 §0 的三值闭集里" \
    LEDGER_304="$tmp/disposition"

  # ⑥ a hole in 304 §4: one drift row renumbered, so E42 is gone and E44 appears twice. The
  # section is a ledger of what has already landed; a hole means a revision cycle whose
  # record was lost — the one thing a numbered log is supposed to make impossible.
  mutate "drift-hole" "$D304" "| E42 |" "| E44 |"
  expect_refusal "304 §4 的 E 号不连续" \
    LEDGER_304="$tmp/drift-hole"

  # ⑦ a §7 row that claims closure against a drift row that does not exist. Every other
  # numbered thing here is checked for holes; the *citations* were not, so "已闭环（E999）"
  # would read as a handled item forever. This is the failure mode §7 item 22 was in when
  # the gate was still certifying a sentence nothing could evaluate (E128).
  # The anchor carries the bold markup of the *closure sentence*, not the bare reference:
  # the same number also appears in item 22's status cell (a trigger that has been
  # replaced by its closure), and an anchor that matches twice cannot build this mutation.
  mutate "e-ref" "$D304" "**——已闭环（E128，2026-11-24）**" "**——已闭环（E199，2026-11-24）**"
  expect_refusal "引用了 §4 里不存在的 E 号：E199"     LEDGER_304="$tmp/e-ref"

  # ⑧ a 305 changelog row that lost its `| date |` head — the exact defect this check was
  # written for, twice in one round: a repair inserted a version *inside* another version's
  # line, and both documents still read fine in a terminal. 305 had no row-shape check at all.
  mutate "305-row-shape" "$D305" "| 2026-11-24 | v0.38 |" "| v0.38 |"
  expect_refusal "305 §5 有 1 行不是"     LEDGER_305="$tmp/305-row-shape"

  # ⑨ the same defect in 304 §6, where it had also already happened (a `| v1.19 | …` row with
  # no date cell, found by this assertion on its first run). ③ used to skip unparseable rows
  # silently, so the count of "versions" was a count of *the rows that happened to parse*.
  mutate "304-row-shape" "$D304" "| 2026-11-21 | v1.19 |" "| v1.19 |"
  expect_refusal "304 §6 有 1 行长得像变更记录但不是"     LEDGER_304="$tmp/304-row-shape"

  # ⑩ the governor rule's own failure: two rows *after* the start date swapped, so the newest
  # write no longer sits at the top. §7 item 41 decided the history keeps its order; this is
  # the one property the decision makes checkable, and it must fail when the order goes.
  mutate "305-governed-order" "$D305" "| 2026-11-24 | v0.40 |" "| 2026-11-24 | v0.01 |"
  expect_refusal "305 §5 在治理起点 2026-11-24 之后有"     LEDGER_305="$tmp/305-governed-order"

  # ⑪ the same rule for uniqueness: renumber a governed row onto a number already used after
  # the start date. Two entries then share a name *and* both are new, which is exactly what
  # the decision forbids going forward (the eight historical clashes stay as history).
  mutate "305-governed-dup" "$D305" "| 2026-11-24 | v0.39 |" "| 2026-11-24 | v0.40 |"
  expect_refusal "305 §5 在治理起点 2026-11-24 之后出现重号"     LEDGER_305="$tmp/305-governed-dup"

  # ⑫ and the boundary itself: drop the policy line. Without a start date the rule has no
  # population, and the honest answer is "cannot judge", not "the file looks fine".
  mutate "305-governor-missing" "$D305" "> **治理起点 = 2026-11-24**" "> **起点 = 2026-11-24**"
  expect_refusal "305 §5 缺治理说明行"     LEDGER_305="$tmp/305-governor-missing"

  # ⑬ a cell that swallowed a `|` and split its row into an extra column. This is not
  # hypothetical: it happened twice in one round on the E151 row (once writing a cell id
  # like `route|mode|tier|thermal` into prose). Every renderer then shows one row shifted
  # by a column and the numbers land under the wrong heading — a table that still looks
  # like a table. The anchor is E41's frozen 处置 cell, which will not move.
  # The anchor inserts **two** pipes on purpose: one only reaches header+1, which is a
  # historical shape here; two is the shape a swallowed cell takes.
  mutate "drift-row-width" "$D304" "\`report_assurance_audit_test.go\` 3 例" "\`report_assurance_audit_test.go\`|| 3 例"
  expect_refusal "304 §4 的行比表头宽"     LEDGER_304="$tmp/drift-row-width"

  # ⑭ a task table appeared that no milestone knows about. It renders perfectly — a heading,
  # a header, zero rows — and that is the shape a half-written workstream takes: the count
  # rule exists so that "who owns this work?" is answered by the document, not by the reader.
  mutate "fourth-table" "$D304" "### 2.2b v0.6.5 补丁（T27–T28，8 pd，不在 33 之内）" \
    "### 2.2b v0.6.5 补丁（T27–T28，8 pd，不在 33 之内）

| ID | 任务 | pd | 状态 | 证据 | 更新于 |
| --- | --- | --- | --- | --- | --- |"
  expect_refusal "304 §2 应有 3 张任务表" LEDGER_304="$tmp/fourth-table"

  say "SELFTEST OK"
  exit 0

fi

# Since doc/321 the develop/ tree is tracked, so a checkout carries 301/304/305; but a tree
# can still be handed one without them (a partial export). Two situations live here and they must
# not share an exit code:
#
#   * the whole directory is absent  -> nothing was compared, nothing was violated. That
#     is exit 3 ("this tree cannot answer"), which the release gate records as N/A under
#     `--ci` and as FAIL for any run that could push a tag. It is *not* a pass: exit 3
#     never reaches the green branch, and the sentence printed says so.
#   * one of the three is absent but the directory is there — someone moved or deleted a
#     document in a tree that otherwise has them. That is exit 1, because it is a fault
#     to find, not a property of the checkout.
if [ ! -d "$(dirname "$D304")" ]; then
  if [ "$MODE" = "snapshot" ]; then
    say "docs-ledger: N/A — --write-snapshot cannot freeze documents this tree does not have."
    say "  The snapshot is generated where doc/develop exists and then committed; freezing"
    say "  nothing would produce an empty artifact that a clean checkout reads as \"checked\"."
    exit 3
  fi
  say "docs-ledger: N/A — doc/develop is not in this tree, so 304 §2/§3/§6/§7, 301 §14.J and"
  say "  305 §2 could not be read. Nothing was compared; reading nothing is not a pass."
  say "  Exit 3: N/A under --ci, FAIL for a run that could tag."
  exit 3
fi
if [ ! -f "$D304" ] || [ ! -f "$D301" ] || [ ! -f "$D305" ]; then
  missing=""
  [ -f "$D304" ] || missing="$missing 304"
  [ -f "$D301" ] || missing="$missing 301"
  [ -f "$D305" ] || missing="$missing 305"
  say "docs-ledger: FAIL — doc/develop exists but is missing:$missing — a document moved or"
  say "  deleted in a tree that has the others is a fault, not a property of the checkout."
  exit 1
fi

say "docs ledger (doc/304 §7 items 2 and 3) — §2 / §3 / §6 / §7, 301 §14.J, §4↔§7 引用, 305 §2 — hermetic"
"$PYTHON" - "$D304" "$D301" "$D305" "$MODE" "$SNAP" <<'PY'
import hashlib, io, json, os, re, sys

d304, d301, d305, mode, snap_path = sys.argv[1:6]
fails = []
oks = 0
snap = {'tasks': [], 'deviations': [], 'changelog': [], 's7': None, 'e_tail': {}, 's2x': []}

def ok(msg):
    global oks
    oks += 1
    print("ok    " + msg)

def bad(msg):
    fails.append(msg)
    print("FAIL  " + msg)

def load(path):
    return io.open(path, encoding='utf-8').read().split('\n')

CELL = 1  # placeholder for the escaped-pipe substitution below

def ncols(row):
    # Cells in a `| a | b |` row: the two outer pipes are the table's, not a cell's. The
    # `split`-based count runs two high, and a message that says "7 cols" about a 5-column
    # table is the same defect it reports one layer down.
    return len(cells(row)) - 2


def cells(row):
    # `\|` inside a CJK cell must not split the row (the E82/E83 row's `--exp E1\|E2`).
    return [c.strip() for c in row.replace('\\|', '\x01').split('|')]

def table_after(lines, header_prefix, stop):
    """Yield (header, [rows]) for each table whose header line starts with header_prefix,
    reading forward until a blank line that is not part of the table."""
    i = 0
    while i < len(lines):
        if lines[i].startswith(header_prefix):
            header = cells(lines[i])
            # the `| --- |` separator follows immediately
            j = i + 2
            rows = []
            while j < len(lines) and lines[j].startswith('|'):
                rows.append((j + 1, lines[j]))
                j += 1
            yield lines[i], header, rows
            i = j
        i += 1

DATE = re.compile(r'^\d{4}-\d{2}-\d{2}$')
# 更新于 may carry a parenthetical pointer («2026-02-14（7 日 drill 见 §7-6）»): the date is
# still the first thing in the cell, and forbidding the note would push the pointer out of
# the row it belongs to. §3's 日期 column stays strict — a deviation is dated, not annotated.
DATE_HEAD = re.compile(r'^\d{4}-\d{2}-\d{2}')
STATUS_OK = ('未开始', '进行中', '已完成', '受阻', '已取消')
DISPOSITIONS = ('按方案改代码', '按代码改方案（需评审）', '接受偏差（记录风险）')

# ── ① 304 §2 ────────────────────────────────────────────────────────────────
lines = load(d304)
tables = list(table_after(lines, '| ID |', '## '))
# Three, and named: v0.7.0 (7 columns), v0.7.1 (6) and the v0.6.5 patch (6, T27–T28).
# A literal count is the point — a task table that appears without a decision is exactly
# what this rule is for, and a fourth one means somebody grew a workstream that no
# milestone, no pd total and no dependency map knows about.
if len(tables) != 3:
    bad("304 §2 应有 3 张任务表（v0.7.0 / v0.7.1 / v0.6.5 补丁），读到 %d 张" % len(tables))
else:
    ok("304 §2 有 3 张任务表（列数各自从表头推导：%s）" %
       " / ".join(str(len(h)) for _, h, _ in tables))

seen_ids = []
shape_bad = 0
for header, hcells, rows in tables:
    want = len(hcells)
    for lineno, row in rows:
        rc = cells(row)
        tid = rc[1] if len(rc) > 1 else '?'
        seen_ids.append(tid)
        if len(rc) != want:
            shape_bad += 1
            bad("304 §2 第 %d 行 %s 有 %d 格，表头 %d 格（一个格子里混进了 `|`）" %
                (lineno, tid, len(rc), want))
            continue
        # status is the second-to-last-but-two in the 8-column table and easier to find by
        # the closed set itself: exactly one cell must be a status word in bold.
        status = None
        for c in rc:
            m = re.match(r'^\*\*(.+?)\*\*', c)
            if m and m.group(1).split('（')[0].strip() in STATUS_OK + ('基本完成',):
                status = (m.group(1).split('（')[0].strip(), c)
                break
        if status is None:
            shape_bad += 1
            bad("304 §2 第 %d 行 %s 没有 §0 闭集里的状态词" % (lineno, tid))
            continue
        word = status[0]
        snap['tasks'].append({'id': tid, 'status': word, 'evidence': bool(status[1]) and
                              bool((rc[rc.index(status[1]) + 1] if rc.index(status[1]) + 1 < len(rc) else '').strip('— `'))})
        if word not in STATUS_OK:
            shape_bad += 1
            bad("304 §2 第 %d 行 %s 的状态 %s 不在 §0 的闭集 %s 里" %
                (lineno, tid, word, ' / '.join(STATUS_OK)))
        # 证据: the cell after the status, non-empty and not a dash. The predicate used to be
        # inverted (`evidence.strip('— `')` is *truthy* for every real evidence cell), so the
        # check fired on all 26 rows and would have passed on an empty one.
        idx = rc.index(status[1])
        evidence = rc[idx + 1] if idx + 1 < len(rc) else ''
        if len(evidence) < 4 or not evidence.strip('— `'):
            shape_bad += 1
            bad("304 §2 第 %d 行 %s 的证据格是空的（§7 第 2 条要的是命令 + 输出摘要）" % (lineno, tid))
        date = rc[-1] if rc[-1] else rc[-2]
        if not DATE_HEAD.match(date):
            shape_bad += 1
            bad("304 §2 第 %d 行 %s 的更新于 %r 不是 YYYY-MM-DD" % (lineno, tid, date))
if shape_bad == 0:
    ok("304 §2 的 %d 行全部匹配各自表头、状态在闭集内、证据非空、日期可解析" % len(seen_ids))

want_ids = ['T%02d' % n for n in range(1, 26)] + ['T04b', 'T27', 'T28']
want_ids.sort()
got = sorted(seen_ids)
if got != sorted(want_ids):
    missing = [t for t in want_ids if t not in seen_ids]
    extra = [t for t in seen_ids if t not in want_ids]
    dupes = sorted({t for t in seen_ids if seen_ids.count(t) > 1})
    bad("304 §2 的任务 id 集不是 T01–T25 + T04b + T27 + T28：缺 %s 多 %s 重 %s" %
        (missing or '无', extra or '无', dupes or '无'))
else:
    ok("304 §2 的任务 id 恰为 T01–T25 + T04b + T27 + T28（28 行，各一次）")

# ── ② 304 §3 ────────────────────────────────────────────────────────────────
i3 = next((k for k, l in enumerate(lines) if l.startswith('## 3.')), None)
i4 = next((k for k, l in enumerate(lines) if l.startswith('## 4.')), None)
if i3 is None or i4 is None:
    bad("304 §3 找不到（## 3. / ## 4. 之间应当是偏差记录区）")
else:
    sec = lines[i3:i4]
    hdr = next((cells(l) for l in sec if l.startswith('| 编号 |')), None)
    rows = [(i3 + k + 1, l) for k, l in enumerate(sec) if re.match(r'^\| D-\d', l)]
    if hdr is None:
        bad("304 §3 没有以 `| 编号 |` 开头的表头")
    nums = []
    bad3 = 0
    for lineno, row in rows:
        rc = cells(row)
        m = re.match(r'^\| D-(\d+) \|', row)
        nums.append(int(m.group(1)))
        if len(rc) != len(hdr):
            bad3 += 1
            bad("304 §3 第 %d 行有 %d 格，表头 %d 格" % (lineno, len(rc), len(hdr)))
            continue
        # 日期 is cell 2; 事实/与301的差异/影响/处置 are the last four before 决定人.
        if not DATE.match(rc[2]):
            bad3 += 1
            bad("304 §3 第 %d 行 D-%02d 的日期 %r 不是 YYYY-MM-DD" % (lineno, nums[-1], rc[2]))
        for name, k in (('事实', 4), ('处置', 7), ('影响', 6)):
            if not rc[k]:
                bad3 += 1
                bad("304 §3 第 %d 行 D-%02d 的 %s 列是空的（§0 第 3 条要求三列都写）" %
                    (lineno, nums[-1], name))
        # 处置 is a closed set of three (§0 第 3 条): 「已消除」「按方案改方案」「补方案 + 改实现」
        # all read like dispositions and none of them is one, so a count of “how many
        # deviations were closed by changing code” cannot be taken from this column.
        if rc[7] and not any(rc[7].startswith('`' + w) or rc[7].startswith(w) for w in DISPOSITIONS):
            m = re.match(r'`([^`]*)`', rc[7]) or re.match(r'^([^（(：:]*)', rc[7])
            bad3 += 1
            bad("304 §3 第 %d 行 D-%02d 的处置 %r 不在 §0 的三值闭集里" %
                (lineno, nums[-1], (m.group(1) if m else rc[7][:24])))
    snap['deviations'] = ['D-%02d' % n for n in nums]
    if nums != list(range(1, len(nums) + 1)):
        bad("304 §3 的编号不是从 D-01 起连续的：%s" % nums)
    elif bad3 == 0:
        ok("304 §3 的 %d 行编号连续、日期可解析、事实/影响/处置三列非空" % len(nums))
    else:
        bad("304 §3 有 %d 处形状问题（行已逐条打印）" % bad3)
    # The three dispositions §0 allows must still be the three the section names: a
    # vocabulary that drifts in §0 is invisible in §3 until someone invents a fourth word.
    for word in DISPOSITIONS:
        if word not in '\n'.join(lines):
            bad("304 里找不到 §0 第 3 条的处置词 %r" % word)

# ── ③ 304 §6 ────────────────────────────────────────────────────────────────
i6 = next((k for k, l in enumerate(lines) if l.startswith('## 6.')), None)
i7 = next((k for k, l in enumerate(lines) if l.startswith('## 7.')), None)
if i6 is None or i7 is None:
    bad("304 §6 找不到（变更记录）")
else:
    vers = []
    # A row is a line that starts with `|`. If it does not parse as `| date | vX.Y | ...`,
    # the old loop simply skipped it and counted the rest as "the changelog" — the same
    # blind spot ⑨ was written for, one document over. Name them instead of counting past.
    stray = [l for l in lines[i6:i7]
             if l.startswith('|') and not re.match(r'^\| (---|日期|编号)', l)
             and not re.match(r'^\| \d{4}-\d{2}-\d{2} \| v\d+\.\d+ \|', l)]
    if stray:
        bad("304 §6 有 %d 行长得像变更记录但不是 `| 日期 | vX.Y | …` 的形状（例如 %r）" %
            (len(stray), stray[0][:60]))
    for l in lines[i6:i7]:
        m = re.match(r'^\| \d{4}-\d{2}-\d{2} \| v(\d+)\.(\d+) \|', l)
        if m:
            vers.append((int(m.group(1)), int(m.group(2))))
    snap['changelog'] = ['v%d.%d' % v for v in vers]
    if len(vers) < 5:
        bad("304 §6 只读到 %d 个版本行，变更记录不像一份历史" % len(vers))
    else:
        badv = [(a, b) for a, b in zip(vers, vers[1:]) if not b < a]
        if badv:
            bad("304 §6 的版本号不是严格递减：%s" % badv)
        else:
            ok("304 §6 的 %d 个版本号严格递减（v%d.%d … v%d.%d）" %
               (len(vers), vers[0][0], vers[0][1], vers[-1][0], vers[-1][1]))

# ── ④ 304 §7 ────────────────────────────────────────────────────────────────
if i7 is not None:
    sec = lines[i7:]
    nums = [int(m.group(1)) for m in (re.match(r'^\| (\d+) \|', l) for l in sec) if m]
    snap['s7'] = len(nums)
    if nums != list(range(1, len(nums) + 1)):
        bad("304 §7 的编号不连续（1..%d 应为 %s）：%s" % (len(nums), '1..N', nums))
    else:
        ok("304 §7 的 %d 条收尾项编号连续 1..%d" % (len(nums), len(nums)))

# ── ⑤ 301 §14.J / ⑥ 304 §4 ──────────────────────────────────────────────
lines301 = load(d301)

def check_e_tail(name, lines, a, b, first):
    """An E-row tail is a numbered ledger: contiguous, and with a footer whose two numbers
    are *derived* from the last row. Both properties are one function because both files
    carry the same footer sentence; only the section they live in differs."""
    rows = [(k + 1, int(m.group(1)))
            for k, l in enumerate(lines[a:b], start=a)
            for m in [re.match(r'^\| E(\d+) \|', l)] if m]
    if not rows:
        bad("%s 里读不到任何 `| E<n> |` 行" % name)
        return
    nums = [n for _, n in rows]
    gaps = [(x, y) for x, y in zip(nums, nums[1:]) if y != x + 1]
    head = nums[-1]
    # Row width. A `|` inside a cell does not make a cell unreadable — it makes the *row*
    # unreadable: one row shifts by a column and its numbers land under the neighbouring
    # headings. That cost two redraftings in one round (E151), so it is checked rather than
    # remembered. The rule is deliberately about the *upper* end only: this tail's rows are
    # historically 3/4/5/6 cells wide (the 边界 and 状态 columns arrived at different times)
    # and shape governance does not re-typeset history (305 §5's rule, E136). What must never
    # happen is a row two or more cells wider than its header — that is a swallowed pipe, not
    # a historical shape. A reading that demanded one width flagged 83 frozen E-rows instead.
    hdr = None
    for k, l in enumerate(lines[a:b], start=a):
        if re.match(r'^\|[ :\-|]+\|\s*$', l) and k > a:
            hdr = lines[k - 1]
            break
    widths = {}
    for idx, l in enumerate(lines[a:b], start=a):
        if re.match(r'^\| E\d+ \|', l):
            # `cells()` already treats `\|` as a literal, not a boundary. Counting raw
            # pipe characters flagged E86, whose cell has carried `\|` escapes since it was
            # written — a reading that counted the wrong population, one document over.
            widths[idx + 1] = ncols(l)
    if hdr is None:
        bad("%s 找不到 `| 编号 |` 表头，行宽无从比较" % name)
    else:
        want = ncols(hdr)
        too_wide = [(k, w) for k, w in widths.items() if w > want + 1]
        wider = [(k, w) for k, w in widths.items() if w == want + 1]
        if too_wide:
            bad("%s 的行比表头宽出两格及以上：表头 %d 格，行 %s（格子里混进了 `|`）" %
                (name, want, ', '.join('%d:%d 格' % (k, w) for k, w in too_wide[:5])))
        else:
            spread = {}
            for w in widths.values():
                spread[w] = spread.get(w, 0) + 1
            ok("%s 的 %d 行行宽可读（表头 %d 格；分布 %s，没有宽出两格的行）" %
               (name, len(rows), want, ' / '.join('%d 格 %d 行' % (w, spread[w]) for w in sorted(spread))))
    snap['e_tail'][name] = {'first': nums[0], 'last': head, 'next': head + 1, 'rows': len(nums)}
    m = re.search(r'E41–\*\*E(\d+)\*\* 已用，下一条从 \*\*E(\d+)\*\* 起',
                  '\n'.join(lines[a:b]))
    if gaps:
        bad("%s 的 E 号不连续：%s（每个 E 号是一次已落地的修订）" % (name, gaps))
    elif first is not None and nums[0] != first:
        bad("%s 的 E 号不是从 E%d 起：%s" % (name, first, nums[0]))
    elif m is None:
        bad("%s 找不到 `E41–**E<n>** 已用，下一条从 **E<m>** 起` 的页脚句" % name)
    elif int(m.group(1)) != head or int(m.group(2)) != head + 1:
        bad("%s 的页脚与表自身的最大 E 号不一致：页脚说 E%s/E%s，表里最大是 E%d" %
            (name, m.group(1), m.group(2), head))
    else:
        ok("%s 的 E 号连续 E%d..E%d（%d 行），页脚 E41–**E%d** / 下一条 **E%d** 与表自身的最大号一致" %
           (name, nums[0], nums[-1], len(nums), head, head + 1))


# Scoped to §14.J. Reading the whole document found §14.E's frozen E1–E40 table and then
# jumped to §14.J's E78+ tail, reporting the section boundary as a numbering gap.
j_start = next((k for k, l in enumerate(lines301) if re.match(r'^### J\. ', l)), None)
j_end = next((k for k, l in enumerate(lines301) if j_start is not None and k > j_start
              and re.match(r'^## ', l)), len(lines301))
if j_start is None:
    bad("301 找不到 `### J. ` 小节（实施期补入的 E 号表在那里）")
else:
    check_e_tail("301 §14.J", lines301, j_start, j_end, None)

# 304 §4 is 301 §14.E's continuation from E41: it must start at E41 and stay contiguous.
lines304 = load(d304)
m_start = next((k for k, l in enumerate(lines304) if l.startswith('## 4.')), None)
m_end = next((k for k, l in enumerate(lines304) if l.startswith('## 5.')), len(lines304))
if m_start is None or m_end <= m_start:
    bad("304 §4 找不到（漂移日志）")
else:
    check_e_tail("304 §4", lines304, m_start, m_end, 41)

# ── ⑦ 305 §2 ───────────────────────────────────────────────────────────────
# 305's §2 subsections are `## 2.n` (with `### 2.n.m` below them): matching `### 2.` read the
# third-level headings and reported 2.3 / 2.3 / 2.3 as duplicates.
lines305 = load(d305)
secs = [int(m.group(1)) for m in (re.match(r'^## 2\.(\d+)(?!\d)', l) for l in lines305) if m]
snap['s2x'] = secs
if len(secs) < 5:
    bad("305 只读到 %d 个 `## 2.x` 小节" % len(secs))
elif len(set(secs)) != len(secs):
    bad("305 的 `## 2.x` 编号有重复：%s" % secs)
elif secs != sorted(secs):
    bad("305 的 `## 2.x` 编号不是递增：%s" % secs)
else:
    ok("305 的 %d 个 `## 2.x` 小节编号递增且不重复（2.%d … 2.%d）" %
       (len(secs), secs[0], secs[-1]))

# ── ⑧ 304 §7 / §5.3 的 E 号引用 ─────────────────────────────────────────────
# §2's 证据 column and §5.3 are where "命令 + 结果" lives, and a result often ends in
# «已闭环（E119）». Nothing checked that pointer. The failure is quiet and expensive: the
# item reads as handled, and the reader who follows the citation finds no record — which is
# exactly the state §7 item 22 was in while s14 was certifying a sentence that counted a
# column which does not exist (E128). E1–E40 are skipped: they are 301 §14.E, frozen and
# deliberately not read here.
if m_start is not None and m_end > m_start:
    e_rows = set(int(m.group(1)) for m in
                 (re.match(r'^\| E(\d+) \|', l) for l in lines304[m_start:m_end]) if m)
    s53 = next((k for k, l in enumerate(lines304) if l.startswith('### 5.3')), None)
    s53_end = None
    if s53 is not None:
        s53_end = next((k for k, l in enumerate(lines304[s53 + 1:], start=s53 + 1)
                        if re.match(r'^#{2,3} ', l)), len(lines304))
    scope = list(lines304[i7:]) if i7 is not None else []
    if s53 is not None and s53_end is not None:
        scope += lines304[s53:s53_end]
    cited = sorted(set(int(n) for n in re.findall(r'E(\d+)', chr(10).join(scope))))
    # A citation must be resolvable *or* explicitly out of range: `E1–E40` is the frozen
    # 301 table, anything above it has to be a row here.
    dangling = [n for n in cited if n > 40 and n not in e_rows]
    if not cited:
        bad("304 §7/§5.3 里读不到任何 `E<n>` 引用（读者与文档都变了形状？）")
    elif dangling:
        bad("304 §7/§5.3 引用了 §4 里不存在的 E 号：%s（§4 是 E41..E%d）" %
            ('、'.join('E%d' % n for n in dangling), max(e_rows) if e_rows else 40))
    else:
        ok("304 §7/§5.3 引用的 %d 个 E 号（E%d..E%d）都能在 §4 的 %d 行里找到" %
           (len([n for n in cited if n > 40]), min([n for n in cited if n > 40]),
            max(cited), len(e_rows)))

# ── ⑨ 305 §5 变更记录 ───────────────────────────────────────────────────────
# 305's own changelog has no checker until now: 304 §6 is checked (③) and 305 §2's
# headings are checked (⑦), so a row shape that died in 305 was invisible. Two rows did
# die that way (v0.36, v0.37): a repair inserted a version *inside* another version's line,
# which keeps the file readable and turns two changelog entries into prose. A changelog
# whose rows are not rows is a history nobody can count.
i5 = next((k for k, l in enumerate(lines305) if l.startswith('## 5.')), None)
i5_end = next((k for k, l in enumerate(lines305[i5 + 1:], start=i5 + 1)
               if re.match(r'^## ', l)), len(lines305)) if i5 is not None else None
if i5 is None:
    bad("305 找不到 §5（变更记录）")
else:
    rows = [l for l in lines305[i5:i5_end]
            if l.startswith('|') and not re.match(r'^\| (---|日期)', l)]
    shaped, v305 = [], []
    for l in rows:
        # 305's rows carry 3 or 4 cells by design (the last one is sometimes a date, sometimes
        # `—`). What must hold is the *head* — a date and a `v0.x` — and a closing pipe: those
        # two are exactly what the two broken rows lost, while the file still read fine.
        # Trailing space after the closing pipe is harmless (five 2026-11-21 rows have it);
        # a missing leading `| date |` is what actually happened, twice.
        m = re.match(r'^\| (\d{4}-\d{2}-\d{2}) \| v0\.(\d+) \| .+ \| *$', l)
        if m:
            shaped.append(l)
            v305.append(int(m.group(2)))
    malformed = [l for l in rows if l not in shaped]
    body = rows
    if len(v305) < 5:
        bad("305 §5 只读到 %d 个合规的版本行（一共 %d 行），变更记录不像一份历史" %
            (len(v305), len(body)))
    elif malformed:
        bad("305 §5 有 %d 行不是 `| 日期 | v0.x | … |` 的形状（例如 %r）" %
            (len(malformed), malformed[0][:60]))
    else:
        ok("305 §5 的 %d 个版本行都是 `| 日期 | v0.x | … |` 的形状" % len(v305))
    # Order and uniqueness: 304 §7 item 41 was decided in E136, and the decision is *not*
    # "re-sort the file". 305 §5 keeps its historical numbering (four numbers are reused:
    # v0.7–v0.10 and v0.30–v0.33 are two different writes each), and the section says so in a
    # policy block whose first line carries the date the rule takes effect. So the rule this
    # check can hold is the going-forward one: rows dated on/after that governor date must be
    # unique and strictly decreasing. Re-sorting is a shape; renumbering is a decision about
    # history, and it stays a human's. The start date is *read from the document* — a constant
    # here would be a second copy of a fact that lives in 305 §5, and the day someone moves the
    # boundary the constant would quietly keep checking the wrong population.
    # The anchor is the policy *line*, not the phrase: §5 also explains the rule in prose
    # ("写进文档那一行 `治理起点 = …`"), so a bare phrase match would read the explanation
    # instead of the decision — and a document that lost its policy line would still parse.
    m_gov = re.search(r'\*\*治理起点 = (\d{4}-\d{2}-\d{2})\*\*', "".join(lines305[i5:i5_end]))
    if not m_gov:
        bad("305 §5 缺治理说明行（`> **治理起点 = YYYY-MM-DD**`）：没有起点，"
            "「起点之后号码唯一且递减」就无法判，乱序与重号只能又变回一句 note（304 §4 E136）")
    else:
        gov = m_gov.group(1)
        governed = [(d, v) for d, v in
                    [(m.group(1), int(m.group(2))) for m in
                     (re.match(r'^\| (\d{4}-\d{2}-\d{2}) \| v0\.(\d+) \| .+ \| *$', l) for l in shaped)]]
        after = [(d, v) for d, v in governed if d >= gov]
        dup_after = sorted({v for _, v in after if [w for _, w in after].count(v) > 1})
        unordered_after = sum(1 for a, b in zip([v for _, v in after], [v for _, v in after][1:]) if b > a)
        dup305 = sorted({v for _, v in governed if [w for _, w in governed].count(v) > 1})
        unordered305 = sum(1 for a, b in zip([v for _, v in governed], [v for _, v in governed][1:]) if b > a)
        if len(after) < 2:
            bad("305 §5 在治理起点 %s 之后只读到 %d 条记录：规则没有可判的population（304 §4 E136）"
                % (gov, len(after)))
        elif dup_after:
            bad("305 §5 在治理起点 %s 之后出现重号：%s（新记录必须号码唯一；旧重号留在原处是决定本身）"
                % (gov, '、'.join('v0.%d' % v for v in dup_after)))
        elif unordered_after:
            bad("305 §5 在治理起点 %s 之后有 %d 处逆序：新记录按写入顺序、最新在上"
                % (gov, unordered_after))
        else:
            ok("305 §5 治理起点 %s 之后的 %d 条记录号码唯一且严格递减（%s …），"
               "之前的历史（%d 处逆序、重号 %s）按决定保留、不再新增"
               % (gov, len(after), 'v0.%d' % after[0][1], unordered305,
                  '、'.join('v0.%d' % v for v in dup305) or '无'))

if mode == 'snapshot':
    if fails:
        print("")
        print("docs-ledger: refusing to freeze a document that fails its own shape check (%d problem(s))" % len(fails))
        sys.exit(1)
    def fingerprint(p):
        raw = io.open(p, 'rb').read()
        return {
            'path': p.replace(os.sep, '/'),
            'sha256': hashlib.sha256(raw).hexdigest(),
            'bytes': len(raw),
            'lines': raw.count(b'\n') + (0 if raw.endswith(b'\n') else 1),
        }
    doc = {
        'schema': 'rgx.docs-ledger/1',
        'note': ('provenance, not a signature: this records what the ledger said when it was '
                 'frozen and it is checked by sha256 against the documents. It proves rows '
                 'exist, words are in the closed set, numbering has no holes and nothing '
                 'changed since — never that an evidence cell still proves its claim.'),
        'generated_by': 'scripts/release-gate/docs-ledger.sh --write-snapshot',
        'generated_at': os.environ.get('LEDGER_NOW', ''),
        'sources': [fingerprint(p) for p in (d304, d301, d305)],
        'ledger': {
            'tasks': snap['tasks'],
            'deviations': snap['deviations'],
            'changelog': snap['changelog'],
            's7_items': snap['s7'],
            'e_tail': snap['e_tail'],
            's2x_305': snap['s2x'],
        },
    }
    io.open(snap_path, 'w', encoding='utf-8', newline='\n').write(
        json.dumps(doc, ensure_ascii=False, indent=2) + '\n')
    print("docs-ledger: wrote %s (%d tasks, %d deviations, %d changelog rows, %d §7 items, %d bytes of sources)" %
          (snap_path, len(snap['tasks']), len(snap['deviations']), len(snap['changelog']),
           snap['s7'] or 0, sum(s['bytes'] for s in doc['sources'])))
    sys.exit(0)

print("")
if fails:
    print("docs-ledger: FAIL (%d ok, %d problem(s))" % (oks, len(fails)))
    sys.exit(1)
# `oks` counts *ok lines*, not checks — a check may grade two populations (⑨ does: row shape,
# then the governor rule). Saying "11 checks" and printing a number that happened to equal it
# was a coincidence that stopped being true the moment ⑨ grew a second branch.
print("docs-ledger: PASS (%d ok line(s), every check green; the rituals are shapes here, "
      "the review is still a person)" % oks)
PY
