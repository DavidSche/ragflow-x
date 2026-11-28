#!/usr/bin/env bash
# s19.docs-ledger-snapshot — 304 §7 item 26, route ① chosen 2026-11-23.
#
# The problem item 26 names: five required checks read `doc/develop/*.md`, and before doc/321
# `doc/` was gitignored, so a clean checkout — CI, a fresh clone — could only answer
# "N/A". A green CI therefore never meant "the ledger was checked"; it meant "the ledger
# was not there to check". Both statements were true and only one of them was visible.
# The archive is not tracked (2026-11-28 policy: documents are admitted one at a time and
# doc/develop/ is not among them), so a shipped checkout carries none of these three sources and
# the snapshot comparison can only run on a tree that has them; the exit-3 branch is therefore
# the *normal* answer on a clone, not an edge case.
#
# Route ① is a *controlled snapshot*: `docs-ledger.json` is tracked, it carries the ledger
# facts and a sha256 of each source document, and this check reads the snapshot. So:
#
#   * in CI (no `doc/`)      — the ledger is judged from the snapshot: schema, closed sets,
#                              contiguous numbering, internal consistency. Green means the
#                              *artifact the repo carries* is well-formed, not "we skipped it".
#   * in a tree with `doc/`  — the snapshot must agree with the documents byte for byte.
#                              A snapshot that lags its source is a FAIL, because a frozen
#                              copy of an older ledger is exactly the formalism route ① risks.
#
# Freshness is a *content fingerprint*, never a timestamp: `generated_at` is provenance and
# nothing reads it for a decision. Two people regenerating the same documents must produce
# the same hashes, and an edited document must change them without anyone remembering to
# touch a date.
#
# What this does NOT establish, said out loud (it is the boundary §7 item 26 keeps): a
# snapshot proves that a row exists, that its status word is one of §0's five, that the
# numbering has no holes, and that no document changed since it was taken. It cannot prove
# that a 证据 cell's content still supports the claim beside it. That is a review, and the
# gate's own sentence says so.
#
# Usage:
#   bash scripts/release-gate/docs-ledger-snapshot.sh             # the check (<1s)
#   bash scripts/release-gate/docs-ledger-snapshot.sh --selftest  # prove it can refuse
#
# LEDGER_SNAPSHOT / LEDGER_304 / LEDGER_301 / LEDGER_305 exist for `--selftest` only.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

SNAP="${LEDGER_SNAPSHOT:-scripts/release-gate/docs-ledger.json}"
D304="${LEDGER_304:-doc/develop/304-v0.7实施情况与跟踪：可信证据、问答收口与网关保障.md}"
D301="${LEDGER_301:-doc/develop/301-v0.7下一阶段工作计划：可信证据、问答收口与网关保障.md}"
D305="${LEDGER_305:-doc/develop/305-v0.7实测与演练报告：网关压测、E0-E3与live drill.md}"

say() { printf '%s\n' "$*"; }
export PYTHONIOENCODING=utf-8

pick_python() {
  local c
  for c in python3 python py; do
    if command -v "$c" >/dev/null 2>&1 && "$c" -c pass >/dev/null 2>&1; then
      printf '%s' "$c"; return 0
    fi
  done
  return 1
}
PYTHON="$(pick_python)" || { say "docs-ledger-snapshot: FAIL — no python on PATH, so the snapshot is unreadable (unreadable is not 'consistent')"; exit 1; }

# ── selftest: six mutations, each of which must be refused ──────────────────
if [ "${1:-}" = "--selftest" ]; then
  say "selftest: can this check refuse? (an invented status word, a dropped task, a schema bump, a footer that disagrees with its own table, a snapshot that lags its source, a changelog out of order)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  jmut() {  # jmut <name> <python-expression on `d`>   (mutates the snapshot copy)
    local name="$1" expr="$2"
    "$PYTHON" - "$SNAP" "$tmp/$name" "$expr" <<'PY'
import io, json, sys
src, out, expr = sys.argv[1:4]
d = json.loads(io.open(src, encoding='utf-8').read())
exec(expr)
io.open(out, 'w', encoding='utf-8', newline='\n').write(json.dumps(d, ensure_ascii=False, indent=2) + '\n')
PY
    [ $? -eq 0 ] || { say "SELFTEST FAIL: could not build the mutation <$name>"; exit 1; }
  }

  expect_refusal() {  # expect_refusal <sentence fragment> [env assignments...]
    local want="$1"; shift
    local out rc
    out="$(env "$@" bash "$0" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -qF -- "$want"; then
      say "SELFTEST FAIL: a broken snapshot was accepted (or refused for the wrong reason)"
      say "  wanted: $want"
      printf '%s\n' "$out" | sed -n '1,12p'
      exit 1
    fi
    say "ok      $want"
  }

  # ① a status word §0 does not allow. The snapshot is what CI reads, so a word invented
  # here would be the only description of that task a clean checkout ever sees.
  jmut "bad-status" "d['ledger']['tasks'][0]['status'] = '基本完成'"
  expect_refusal "状态 基本完成 不在 §0 的闭集" LEDGER_SNAPSHOT="$tmp/bad-status"

  # ② a task dropped. The count and the id set are two independent witnesses: a row that
  # disappears from a frozen artifact must not read as "28 tasks, all fine".
  jmut "drop-task" "d['ledger']['tasks'] = [t for t in d['ledger']['tasks'] if t['id'] != 'T04b']"
  expect_refusal "任务 id 集不是 T01–T25 + T04b + T27 + T28" LEDGER_SNAPSHOT="$tmp/drop-task"

  # ③ the schema id bumped without a reader. A snapshot whose version nobody recognises must
  # not be read as "same shape, different number" — that is how a format silently forks.
  jmut "schema" "d['schema'] = 'rgx.docs-ledger/2'"
  expect_refusal "schema 不是 rgx.docs-ledger/1" LEDGER_SNAPSHOT="$tmp/schema"

  # ④ the E-tail footer disagrees with the last row it is derived from. Same defect class as
  # the live check's ③, but here it is the frozen copy that lies.
  jmut "e-tail" "d['ledger']['e_tail']['301 §14.J']['next'] = d['ledger']['e_tail']['301 §14.J']['last'] + 3"
  expect_refusal "的 next 不是 last+1" LEDGER_SNAPSHOT="$tmp/e-tail"

  # ⑤ the snapshot lags its source: one hex digit of 304's fingerprint flipped. This is the
  # mutation route ① exists to catch — a green snapshot describing a document nobody has
  # seen since. Only meaningful where `doc/` is present; in a clean checkout the check says
  # so instead of pretending (and this mutation is skipped with a reason, never silently).
  jmut "stale" "d['sources'][0]['sha256'] = ('0' if d['sources'][0]['sha256'][0] != '0' else '1') + d['sources'][0]['sha256'][1:]"
  if [ -f "$D304" ]; then
    expect_refusal "已过期" LEDGER_SNAPSHOT="$tmp/stale"
  else
    say "skip    已过期（doc/develop 不在本树，无法证明「源文档改过」——这条变异只在有文档的树上成立）"
  fi

  # ⑥ a changelog out of order inside the frozen history.
  jmut "changelog" "c = d['ledger']['changelog']; c[0], c[1] = c[1], c[0]"
  expect_refusal "changelog 不是严格递减" LEDGER_SNAPSHOT="$tmp/changelog"

  say "SELFTEST OK"
  exit 0
fi

say "docs ledger snapshot (304 §7 item 26, route ①: a frozen ledger + a freshness check)"
"$PYTHON" - "$SNAP" "$D304" "$D301" "$D305" <<'PY'
import hashlib, io, json, os, re, sys

snap_path, d304, d301, d305 = sys.argv[1:5]
fails = []
oks = 0

def ok(msg):
    global oks
    oks += 1
    print("ok    " + msg)

def bad(msg):
    fails.append(msg)
    print("FAIL  " + msg)

STATUS_OK = ('未开始', '进行中', '已完成', '受阻', '已取消')
WANT_IDS = sorted(['T%02d' % n for n in range(1, 26)] + ['T04b', 'T27', 'T28'])
SCHEMA = 'rgx.docs-ledger/1'

if not os.path.exists(snap_path):
    bad("%s 不存在——发布证据的快照没有进仓，CI 读不到任何东西（这不是 N/A，是一份缺件）" % snap_path)
    print("")
    print("docs-ledger-snapshot: FAIL (the tracked snapshot is missing)")
    sys.exit(1)
try:
    snap = json.loads(io.open(snap_path, encoding='utf-8').read())
except Exception as exc:                                   # noqa: BLE001 — any parse error is the same defect
    bad("%s 不是合法 JSON（%s）" % (snap_path, exc))
    print("")
    print("docs-ledger-snapshot: FAIL (the snapshot does not parse)")
    sys.exit(1)

# ── ① schema: an unrecognised version is not "the same shape with a bigger number" ────────
if snap.get('schema') != SCHEMA:
    bad("schema 不是 %s（读到 %r）——快照格式分叉时不能按旧读者解析" % (SCHEMA, snap.get('schema')))
else:
    ok("schema 恰为 %s" % SCHEMA)

# ── ② provenance: what was frozen, and where it came from ────────────────────────────────
srcs = snap.get('sources') or []
names = [os.path.basename(s.get('path', '')) for s in srcs]
if len(srcs) != 3:
    bad("sources 应有 3 个（301/304/305），读到 %d 个" % len(srcs))
else:
    shape = 0
    for s in srcs:
        if not re.match(r'^[0-9a-f]{64}$', s.get('sha256', '')):
            shape += 1
            bad("sources[%s] 的 sha256 不是 64 位十六进制" % s.get('path', '?'))
        if not isinstance(s.get('bytes'), int) or s['bytes'] <= 0:
            shape += 1
            bad("sources[%s] 的 bytes 不是正整数" % s.get('path', '?'))
    for want in ('301', '304', '305'):
        if not any(n.startswith(want) for n in names):
            shape += 1
            bad("sources 缺少 %s 文档的指纹" % want)
    if shape == 0:
        ok("sources 记了 301/304/305 三条指纹（path/sha256/bytes/lines 四列齐全）")

# ── ③ the ledger facts, re-validated here (the snapshot is what CI reads; trusting the
#      writer would make tampering invisible exactly where it matters most) ────────────────
led = snap.get('ledger') or {}
tasks = led.get('tasks') or []
ids = [t.get('id') for t in tasks]
if len(tasks) != 28 or sorted(ids) != WANT_IDS:
    bad("任务 id 集不是 T01–T25 + T04b + T27 + T28（28 行，各一次）：读到 %d 行，缺 %s，多 %s" %
        (len(tasks), [t for t in WANT_IDS if t not in ids] or '无',
         [t for t in ids if t not in WANT_IDS] or '无'))
else:
    ok("任务 id 恰为 T01–T25 + T04b + T27 + T28（28 行，各一次）")

bad_status = [(t.get('id'), t.get('status')) for t in tasks if t.get('status') not in STATUS_OK]
if bad_status:
    for tid, word in bad_status:
        bad("%s 的状态 %s 不在 §0 的闭集 %s 里" % (tid, word, ' / '.join(STATUS_OK)))
elif len(tasks) == 28:
    counts = {}
    for t in tasks:
        counts[t['status']] = counts.get(t['status'], 0) + 1
    ok("28 行的状态全在 §0 闭集内（%s）" %
       ' / '.join('%s %d' % (w, counts[w]) for w in STATUS_OK if w in counts))

no_evidence = [t.get('id') for t in tasks if not t.get('evidence')]
if no_evidence:
    bad("这些行的证据格在被冻结时就是空的：%s（§7 第 2 条要的是命令 + 输出摘要）" % no_evidence)
elif len(tasks) == 28:
    ok("28 行都有非空证据格（冻结时才写入，空行不会被冻成「已核」）")

devs = led.get('deviations') or []
want_devs = ['D-%02d' % n for n in range(1, len(devs) + 1)]
if devs != want_devs:
    bad("偏差编号不是从 D-01 起连续：%s" % devs)
else:
    ok("偏差 %d 行编号连续 D-01..D-%02d" % (len(devs), len(devs)))

change = led.get('changelog') or []
def ver(v):
    m = re.match(r'^v(\d+)\.(\d+)$', v or '')
    return (int(m.group(1)), int(m.group(2))) if m else None
vers = [ver(v) for v in change]
if len(change) < 5 or any(v is None for v in vers):
    bad("changelog 读不出至少 5 个 vX.Y 版本号：%s" % change[:6])
elif any(not b < a for a, b in zip(vers, vers[1:])):
    bad("changelog 不是严格递减：%s" % change[:8])
else:
    ok("changelog %d 个版本号严格递减（%s … %s）" % (len(change), change[0], change[-1]))

s7 = led.get('s7_items')
if not isinstance(s7, int) or s7 < 1:
    bad("s7_items 不是正整数：%r" % (s7,))
else:
    ok("§7 收尾项 %d 条（冻结时的计数，编号连续性由 live 检查管）" % s7)

et = led.get('e_tail') or {}
tail_ok = True
for name in ('301 §14.J', '304 §4'):
    row = et.get(name)
    if not isinstance(row, dict) or not all(isinstance(row.get(k), int) for k in ('last', 'next', 'rows')):
        bad("e_tail[%s] 缺 last/next/rows 三个整数" % name); tail_ok = False; continue
    if row['next'] != row['last'] + 1:
        bad("e_tail[%s] 的 next 不是 last+1（last=%d next=%d）" % (name, row['last'], row['next'])); tail_ok = False
    if row['rows'] < 1:
        bad("e_tail[%s] 的 rows 不是正数" % name); tail_ok = False
if tail_ok and et:
    ok("E 号尾部 E%d…E%d / E%d…E%d 的 last+1 == next（页脚是从最后一行推出的）" %
       (et['301 §14.J'].get('first', 0), et['301 §14.J']['last'],
        et['304 §4'].get('first', 41), et['304 §4']['last']))

s2x = led.get('s2x_305') or []
if len(s2x) < 5 or s2x != sorted(set(s2x)):
    bad("305 §2.x 的小节号不是递增且无重复：%s" % s2x)
else:
    ok("305 的 %d 个 `## 2.x` 小节号递增且无重复（2.%d … 2.%d）" % (len(s2x), s2x[0], s2x[-1]))

# ── ④ freshness: content fingerprints, never timestamps ─────────────────────────────────
# `generated_at` is provenance and nothing reads it for a decision: a clock can be wrong,
# arranged, or simply not run, while a sha256 of the bytes cannot agree with a document that
# changed. Where the document is absent the check says so instead of claiming freshness.
present, stale, compared = 0, [], 0
for s in srcs:
    p = s.get('path', '')
    if not os.path.exists(p):
        continue
    present += 1
    compared += 1
    raw = io.open(p, 'rb').read()
    got = hashlib.sha256(raw).hexdigest()
    if got != s.get('sha256'):
        stale.append((p, s.get('sha256', '?')[:12], got[:12], len(raw), s.get('bytes')))
if stale:
    for p, want, got, nbytes, wantbytes in stale:
        bad("%s 已过期：快照记 sha256 %s / %d 字节，盘上是 %s / %d 字节——"
            "源文档改过而快照没重生成（`bash scripts/release-gate/docs-ledger.sh --write-snapshot`）" %
            (p, want, wantbytes or 0, got, nbytes))
elif present == 3:
    ok("三份源文档的 sha256 与快照逐字节一致（新鲜度按内容指纹判，不按时间戳）")
else:
    print("note  这份树里只有 %d/3 份源文档在盘上：新鲜度在本树无法证明（干净检出只能核结构，" % present)
    print("      发布运行会在有 doc/ 的树上重核这 3 个指纹）")

# ── the boundary, printed every run so it cannot be forgotten ───────────────────────────
print("")
if fails:
    print("docs-ledger-snapshot: FAIL (%d ok, %d problem(s))" % (oks, len(fails)))
    sys.exit(1)
print("docs-ledger-snapshot: PASS (%d checks; a snapshot proves rows, words, order and"
      " freshness — not that an evidence cell still proves its claim)" % oks)
PY
