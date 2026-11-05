#!/usr/bin/env bash
# 上游 RAGFlow 版本升级验证流水线（doc/262 SOP）。
#
# 每次上游发布新版本并在本地 151 部署后运行。职责：
#   1. 探测引擎版本并跑 fail-closed 版本门禁预检（cmd/upstream-verify）
#   2. 跑 live 契约集（P1 provider-seam；--full 追加 service E2E）
#   3. 汇总一份可留档验证报告 tmp/upstream-verify-<ts>.md
#   4. 以退出码反映升级能否进入「抬版钉」阶段
#
# 红线纪律（doc/00 §2.3 / §4.4）：聊天类 drill 单轮串行零并发、模型钉
# AMD radeon-external（--full 需要 RGX_RAGFLOW_LIVE_CHAT_MODEL，缺省
# fail-closed 不会静默退到 31.211）、临时资源用完即删（本脚本只读，
# 不产生持久资源）。
#
# 环境变量（与 live 契约测试同一套）：
#   RGX_RAGFLOW_LIVE_BASE_URL / RGX_RAGFLOW_LIVE_API_KEY
#   RGX_RAGFLOW_LIVE_CHAT_MODEL   可选；--full 聊天类钉死模型
#
# 用法：
#   RGX_RAGFLOW_LIVE_BASE_URL=... RGX_RAGFLOW_LIVE_API_KEY=... \
#     bash scripts/upstream-verify.sh [--full]
#
# 退出码：0=契约集通过（可评估抬版钉 / pass 例行）；1=任一契约 FAIL 或
# 版本探测 block。
set -u

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

FULL=0
for a in "$@"; do
  case "$a" in
    --full) FULL=1 ;;
    *) printf 'usage: bash scripts/upstream-verify.sh [--full]\n' >&2; exit 2 ;;
  esac
done

if [ -z "${RGX_RAGFLOW_LIVE_BASE_URL:-}" ] || [ -z "${RGX_RAGFLOW_LIVE_API_KEY:-}" ]; then
  printf 'upstream-verify: missing env RGX_RAGFLOW_LIVE_BASE_URL / RGX_RAGFLOW_LIVE_API_KEY\n' >&2
  exit 2
fi
if [ "$FULL" = "1" ] && [ -z "${RGX_RAGFLOW_LIVE_CHAT_MODEL:-}" ]; then
  printf 'upstream-verify: --full needs RGX_RAGFLOW_LIVE_CHAT_MODEL (chat drills fail-closed without it)\n' >&2
  exit 2
fi

TS="$(date +%Y%m%d-%H%M%S)"
TDIR="tmp/verify-${TS}"
mkdir -p "$TDIR"
REPORT="$TDIR/report.md"
BINARY="$TDIR/upstream-verify.exe"

PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0

pass() { printf '  [PASS] %s\n' "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
warn() { printf '  [WARN] %s\n' "$1"; WARN_COUNT=$((WARN_COUNT + 1)); }

# summarize_go_test "name" <logfile> <rc>
# Sets LAST_P/LAST_F/LAST_S so callers can inspect the per-set outcome.
LAST_P=0; LAST_F=0; LAST_S=0
summarize_go_test() {
  local name="$1" log="$2" rc="$3"
  local p f s
  p="$(grep -c '^--- PASS' "$log" 2>/dev/null || true)"
  f="$(grep -c '^--- FAIL' "$log" 2>/dev/null || true)"
  s="$(grep -c '^--- SKIP' "$log" 2>/dev/null || true)"
  LAST_P=$((p + 0)); LAST_F=$((f + 0)); LAST_S=$((s + 0))
  if [ "$rc" -ne 0 ] || [ "$LAST_F" -gt 0 ]; then
    fail "$name (PASS=$LAST_P FAIL=$LAST_F SKIP=$LAST_S) -> FAIL"
    return 1
  fi
  pass "$name (PASS=$LAST_P FAIL=$LAST_F SKIP=$LAST_S)"
  return 0
}

{
  printf '# upstream-verify %s\n\n' "$TS"
  printf 'target engine : %s\n' "${RGX_RAGFLOW_LIVE_BASE_URL}"
  printf 'scope         : %s\n' "$([ "$FULL" = "1" ] && echo full || echo p1-provider-seam)"
  printf '\n## 1. 版本探测与门禁预检\n\n'
} > "$REPORT"

printf 'upstream-verify %s (target %s)\n' "$TS" "${RGX_RAGFLOW_LIVE_BASE_URL}"
printf 'scope: %s\n\n' "$([ "$FULL" = "1" ] && echo full || echo p1-provider-seam)"

# ---- 1. build + preflight verdict -------------------------------------
printf '1. build upstream-verify + version preflight\n'
if ! go build -o "$BINARY" ./cmd/upstream-verify; then
  fail "go build ./cmd/upstream-verify"
  { printf '    [FAIL] go build ./cmd/upstream-verify\n'; } >> "$REPORT"
  # not worth running the rest
  exit 1
fi
pass "go build ./cmd/upstream-verify"

if ! "$BINARY" > "$TDIR/preflight.txt" 2>&1; then
  P_RC=$?
  case "$P_RC" in
    3) VERDICT="degrade" ;;
    4) VERDICT="verification_required" ;;
    *) VERDICT="block/fail" ;;
  esac
else
  P_RC=0
  VERDICT="pass"
fi
printf '  verdict: %s (exit %s)\n' "$VERDICT" "$P_RC"
{
  printf '    verdict: **%s** (exit %s)\n\n' "$VERDICT" "$P_RC"
  printf '```text\n'
  grep -E '^  (observed version|pinned baseline|verified max|decision|capability gates|unclassified|known version|regressions|note|next)' "$TDIR/preflight.txt" | sed 's/^  /    /'
  printf '```\n'
} >> "$REPORT"

if [ "$VERDICT" = "block/fail" ]; then
  fail "version preflight (block) - engine unreachable or malformed, aborting"
  { printf '    [FAIL] preflight block：引擎版本无法建立，升级止步（fail-closed）。检查网络与环境变量后重跑。\n'; } >> "$REPORT"
  # stop: further contract sets against an unreachable engine are noise
  exit 1
fi

# ---- 2. P1 provider-seam live 契约集 ---------------------------------
printf '\n2. P1 provider-seam live contracts\n'
printf '\n## 2. P1 provider-seam live contracts\n\n' >> "$REPORT"
go test ./internal/provider/ragflow/ -count=1 -v -timeout 15m \
  -run 'TestLiveRAGFlowVersionContract|TestLiveInternalEndpointContract|TestLiveGetDatasetArtifactContract|TestLiveCompilationStateObservation' \
  > "$TDIR/p1.log" 2>&1
P1_RC=$?
{ printf '```text\n'; grep -E '^--- (PASS|FAIL|SKIP):' "$TDIR/p1.log" | sed 's/^/    /'; printf '```\n\n'; } >> "$REPORT"
summarize_go_test "P1 provider-seam contracts" "$TDIR/p1.log" "$P1_RC" \
  && P1_OK=1 || P1_OK=0
P1_S=$LAST_S

# ---- 3. memory write contracts ---------------------------------------
printf '\n3. memory write/idempotency contracts\n'
printf '## 3. memory write/idempotency contracts\n\n' >> "$REPORT"
go test ./internal/provider/ragflow/ -count=1 -v -timeout 15m \
  -run 'TestLiveMemorySearchFilterContractWithFixture|TestLiveMemoryCompensationContractWithFixture|TestLiveMemoryDispatchIdempotencyObservation' \
  > "$TDIR/mem.log" 2>&1
MEM_RC=$?
{ printf '```text\n'; grep -E '^--- (PASS|FAIL|SKIP):' "$TDIR/mem.log" | sed 's/^/    /'; printf '```\n\n'; } >> "$REPORT"
summarize_go_test "memory write contracts" "$TDIR/mem.log" "$MEM_RC" \
  && MEM_OK=1 || MEM_OK=0
MEM_S=$LAST_S
if [ "$MEM_S" -gt 0 ]; then
  warn "memory 写契约 SKIP=$MEM_S：fixture 双保险未激活（设 RGX_ALLOW_MEMORY_FIXTURE=1 [+ RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1] 才执行，避免在升级目标引擎留临时数据）"
fi

# ---- 4. (--full) service E2E  ----------------------------------------
E2E_OK=1
if [ "$FULL" = "1" ]; then
  printf '\n4. (--full) service E2E\n'
  printf '## 4. (--full) service E2E\n\n' >> "$REPORT"
  go test ./internal/service/ -count=1 -v -timeout 30m \
    -run 'TestLivePreModelMemoryInjectionChatAndAgent|TestLiveCompilationCompletedArtifactBindingE2E|TestLiveCompilationReplacementRunE2E|TestLiveAssistantReleaseGoldenPathWithRAGFlowV1RC1' \
    > "$TDIR/e2e.log" 2>&1
  E2E_RC=$?
  { printf '```text\n'; grep -E '^--- (PASS|FAIL|SKIP):' "$TDIR/e2e.log" | sed 's/^/    /'; printf '```\n\n'; } >> "$REPORT"
  summarize_go_test "service E2E (chat/compilation/release)" "$TDIR/e2e.log" "$E2E_RC" \
    && E2E_OK=1 || E2E_OK=0
else
  printf '\n4. (skipped unless --full) service E2E (AMD chat / compilation)\n'
fi

# ---- 5. s24 readiness (WARN-only: platform demo prereq, not engine contract)
printf '\n5. s24 readiness check (WARN-only)\n'
RGX_RAGFLOW_BASE_URL="$RGX_RAGFLOW_LIVE_BASE_URL" bash scripts/s24_readiness_check.sh > "$TDIR/s24.log" 2>&1
S24_RC=$?
S24_OK=1
if [ "$S24_RC" -ne 0 ]; then
  warn "s24 readiness check FAIL (回看 $TDIR/s24.log；可选，不阻断契约结论)"
  S24_OK=0
else
  pass "s24 readiness check"
fi

# ---- summary ----------------------------------------------------------
printf '\n'
{
  printf '## 汇总\n\n'
  printf '| 项 | 结果 |\n|---|---|\n'
  printf '| P1 provider-seam 契约 | %s |\n' "$([ "$P1_OK" = "1" ] && echo PASS || echo FAIL)"
  printf '| memory 写契约 | %s |\n' "$([ "$MEM_OK" = "1" ] && echo PASS || echo FAIL)"
  printf '| service E2E（--full） | %s |\n' "$([ "$E2E_OK" = "1" ] && echo PASS || echo SKIP/FAIL)"
  printf '| s24 就绪（WARN-only） | %s |\n' "$([ "$S24_OK" = "1" ] && echo PASS || echo WARN)"
  printf '\n版本判决 **%s**：' "$VERDICT"
  case "$VERDICT" in
    pass)                    printf '引擎在已验证带内，无契约风险\n' ;;
    verification_required)   printf '引擎高于已验证带，契约集已全绿 → 可进入抬版钉清单（doc/262 §抬版钉）\n' ;;
    degrade)                 printf '引擎低于已验证带且无 MinVersion 下限证明 → 保守降级或先升级引擎\n' ;;
    *)                       printf '（block 已提前止步）\n' ;;
  esac
  printf '\n日志：%s\n' "$TDIR"
} >> "$REPORT"

printf 'summary: %d passed, %d failed, %d warn\n' "$PASS_COUNT" "$FAIL_COUNT" "$WARN_COUNT"
printf 'report: %s\n' "$REPORT"

if [ "$P1_OK" = "0" ] || [ "$MEM_OK" = "0" ] || [ "$E2E_OK" = "0" ]; then
  printf 'contract FAIL - 该版本不可抬版钉，先处置失败项（doc/262 §处置）\n'
  exit 1
fi
printf 'contract ok - 版本判决 %s（doc/262 §判定）\n' "$VERDICT"
exit 0
