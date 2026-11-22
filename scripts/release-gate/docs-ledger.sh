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
#           set is exactly T01–T25 + T04b, each once (a dropped row is the failure mode that
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
#   ⑦ 305 — `## 2.x` sections strictly ascending, no duplicates.
#
# What this does NOT check, said out loud: whether a 证据 cell's *content* still proves the
# claim it sits beside. That is a review, and it is exactly what §7 items 2/3 ask a person
# to do; the gate only guarantees the cell is not empty and the vocabulary did not drift.
#
# Usage:
#   bash scripts/release-gate/docs-ledger.sh             # the check (hermetic, <1s)
#   bash scripts/release-gate/docs-ledger.sh --selftest  # prove the check can fail
#
# LEDGER_304 / LEDGER_301 / LEDGER_305 exist for `--selftest` only: proving a refusal must
# not require editing a document that five audits already green.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

D304="${LEDGER_304:-doc/develop/304-v0.7实施情况与跟踪：可信证据、问答收口与网关保障.md}"
D301="${LEDGER_301:-doc/develop/301-v0.7下一阶段工作计划：可信证据、问答收口与网关保障.md}"
D305="${LEDGER_305:-doc/develop/305-v0.7实测与演练报告：网关压测、E0-E3与live drill.md}"

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

# ── selftest: four mutations, each of which must be refused ─────────────────
if [ "${1:-}" = "--selftest" ]; then
  say "selftest: can this check refuse? (an invented status word, a dropped §7 row, a footer that disagrees with its own table, a changelog out of order, a fourth disposition word, a hole in §4's drift log)"
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

  say "SELFTEST OK"
  exit 0
fi

# The develop/ tree is gitignored, so on some checkouts these files simply do not exist.
# That is refused rather than skipped, for the same reason s14/s15 refuse it: «I could not
# read the ledger» is not «the ledger is consistent», and a release gate that turns an
# unreadable input into a green line is the exact failure mode this whole file watches for.
if [ ! -f "$D304" ] || [ ! -f "$D301" ] || [ ! -f "$D305" ]; then
  say "docs-ledger: FAIL — doc/develop is not present in this tree, so 304 §2/§3/§6/§7, 301 §14.J and 305 §2 cannot be read; reading nothing is not a pass"
  exit 1
fi

say "docs ledger (doc/304 §7 items 2 and 3) — §2 / §3 / §6 / §7, 301 §14.J, 305 §2 — hermetic"
"$PYTHON" - "$D304" "$D301" "$D305" <<'PY'
import io, re, sys

d304, d301, d305 = sys.argv[1:4]
fails = []
oks = 0

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
if len(tables) != 2:
    bad("304 §2 应有 2 张任务表（v0.7.0 与 v0.7.1 两种列数），读到 %d 张" % len(tables))
else:
    ok("304 §2 有 2 张任务表（列数各自从表头推导：%s）" %
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

want_ids = ['T%02d' % n for n in range(1, 26)] + ['T04b']
want_ids.sort()
got = sorted(seen_ids)
if got != sorted(want_ids):
    missing = [t for t in want_ids if t not in seen_ids]
    extra = [t for t in seen_ids if t not in want_ids]
    dupes = sorted({t for t in seen_ids if seen_ids.count(t) > 1})
    bad("304 §2 的任务 id 集不是 T01–T25 + T04b：缺 %s 多 %s 重 %s" %
        (missing or '无', extra or '无', dupes or '无'))
else:
    ok("304 §2 的任务 id 恰为 T01–T25 + T04b（26 行，各一次）")

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
    for l in lines[i6:i7]:
        m = re.match(r'^\| \d{4}-\d{2}-\d{2} \| v(\d+)\.(\d+) \|', l)
        if m:
            vers.append((int(m.group(1)), int(m.group(2))))
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
if len(secs) < 5:
    bad("305 只读到 %d 个 `## 2.x` 小节" % len(secs))
elif len(set(secs)) != len(secs):
    bad("305 的 `## 2.x` 编号有重复：%s" % secs)
elif secs != sorted(secs):
    bad("305 的 `## 2.x` 编号不是递增：%s" % secs)
else:
    ok("305 的 %d 个 `## 2.x` 小节编号递增且不重复（2.%d … 2.%d）" %
       (len(secs), secs[0], secs[-1]))

print("")
if fails:
    print("docs-ledger: FAIL (%d ok, %d problem(s))" % (oks, len(fails)))
    sys.exit(1)
print("docs-ledger: PASS (%d checks; the rituals are shapes here, the review is still a person)" % oks)
PY
