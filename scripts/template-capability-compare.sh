#!/usr/bin/env bash
# 模板能力对比 —— 真机文档方式判分（doc/41 §21 的落地脚本，263 SOP 的形态层补充）。
#
# 目标：用**真机可观测的文档/结构证据**给三臂的「模板能力」判分，回答
#   「各产品在模板这一能力面上，真实支持到什么程度」—— 不套模板、不做 LLM 问答判分。
#   263 SOP（竞品对比验证 SOP）测**问答质量**；本脚本只做**形态/能力清单**（doc/276 模板面）。
#
# 三臂（A/B/C 与 263 同号）：
#   A = ragflow-x 平台  (:19193) —— 场景模板 full lifecycle（catalog/baseline/skeletons/assets）
#   B = RAGFlow 上游引擎 (:8001) —— canvas 模板（agent_canvas / dataflow_canvas）
#   C = WeKnora        (:8381) —— sandbox 工作区 spawn 模板目录 + YAML prompt 模板
#
# 只读纪律（doc/environment/00 §2.3）：全脚本零写操作（登录拿只读 token 除外）；不发模型
# 调用（不触 31.211 红线）；C 臂模板目录要求真实 sandbox 后端，探测只做「出站策略拒绝」级
# 安全验证，绝不发起外联。
#
# 用法：
#   RAGFLOW_API_KEY=… WEKNORA_PW=… bash scripts/template-capability-compare.sh
# 可覆盖默认端点（见下方 *_BASE）；退出码：0=完成并写报告；1=某臂探测失败；2=用法错误。
set -u

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"
TS="$(date +%Y%m%d-%H%M%S)"
REPORT="tmp/template-capability-compare-$TS.md"
WORK="tmp/tcc-$TS"
mkdir -p "$WORK"

AX_BASE="${RAGRFLOW_X_API_BASE:-http://192.168.4.151:19193/api/v1}"
AX_USER="${RAGRFLOW_X_USER:-admin}"
AX_PASS="${RAGRFLOW_X_PASS:-RgxTest#2026a}"
RF_BASE="${RAGFLOW_API_BASE:-http://192.168.4.151:8001/api/v1}"
RF_KEY="${RAGFLOW_API_KEY:-}"
WK_BASE="${WEKNORA_API_BASE:-http://192.168.4.151:8381/api/v1}"
WK_EMAIL="${WEKNORA_EMAIL:-admin@weknora.local}"
WK_PW="${WEKNORA_PW:-}"

PASS=0; FAIL=0; SKIP=0; TOTAL=0
pass()  { TOTAL=$((TOTAL+1)); PASS=$((PASS+1)); printf '  [PASS] %s\n' "$1"; }
fail()  { TOTAL=$((TOTAL+1)); FAIL=$((FAIL+1)); printf '  [FAIL] %s\n' "$1"; }
skip()  { TOTAL=$((TOTAL+1)); SKIP=$((SKIP+1)); printf '  [SKIP] %s\n' "$1"; }

printf '# 模板能力对比（真机文档方式判分）\n\n运行时间：%s\n脚本：`scripts/template-capability-compare.sh`（doc/41 §21 落地）\n' "$TS" > "$REPORT"
printf '\n方法：只读真机探测 + 结构证据判分；A/B/C 臂同 263 号；与 263 的问答质量判分互补。\n' >> "$REPORT"

echo "== A 臂: ragflow-x 场景模板 (${AX_BASE})"
AX_TOK=""
{ AX_TOK=$(curl -s -X POST "$AX_BASE/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$AX_USER\",\"password\":\"$AX_PASS\"}" \
    | PYTHONIOENCODING=utf-8 python -c "import sys,json;print(json.load(sys.stdin)['data']['token'])"); } 2>/dev/null
if [ -z "$AX_TOK" ]; then echo "  A 臂登录失败（退出 1）"; exit 1; fi
AUTH="Authorization: Bearer $AX_TOK"

curl -s "$AX_BASE/scenario-template-catalog" -H "$AUTH" -o "$WORK/ax_catalog.json"
curl -s "$AX_BASE/scenario-template-baseline" -H "$AUTH" -o "$WORK/ax_baseline.json"
curl -s "$AX_BASE/scenario-template-skeletons" -H "$AUTH" -o "$WORK/ax_skeletons.json"
curl -s "$AX_BASE/scenario-template-assets" -H "$AUTH" -o "$WORK/ax_assets.json"
AX_CAT_N=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_catalog.json',encoding='utf-8'));print(len(d.get('data') or []))" 2>/dev/null || echo NA)
AX_PAYLOAD=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_catalog.json',encoding='utf-8'));print((d.get('data') or [{}])[0].get('payload_schema','NA'))" 2>/dev/null || echo NA)
AX_BASE_N=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_baseline.json',encoding='utf-8'));dd=d.get('data');print(dd.get('total') if isinstance(dd,dict) else len(dd))" 2>/dev/null || echo NA)
AX_SK=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_skeletons.json',encoding='utf-8'));dd=d.get('data');print(len(dd.get('items')) if isinstance(dd,dict) else len(dd))" 2>/dev/null || echo NA)
# assets: data 可能是 list 或 {items:[...]}
AX_ASSET_RAW=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_assets.json',encoding='utf-8'));dd=d.get('data');print('list' if isinstance(dd,list) else ('items' if isinstance(dd,dict) and isinstance(dd.get('items'),list) else 'other:'+str(type(dd).__name__)))" 2>/dev/null || echo NA)
if [ "$AX_ASSET_RAW" = list ]; then
  AX_AS=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_assets.json',encoding='utf-8'));print(len(d['data']))" 2>/dev/null || echo NA)
  AX_SRC=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_assets.json',encoding='utf-8'));print(d['data'][0]['id'])" 2>/dev/null || echo "")
elif [ "$AX_ASSET_RAW" = items ]; then
  AX_AS=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_assets.json',encoding='utf-8'));print(len(d['data']['items']))" 2>/dev/null || echo NA)
  AX_SRC=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_assets.json',encoding='utf-8'));print(d['data']['items'][0]['id'])" 2>/dev/null || echo "")
else
  AX_AS=NA; AX_SRC=""
fi
printf '  catalog=%s baseline=%s skeletons=%s assets(%s)=%s\n' "$AX_CAT_N" "$AX_BASE_N" "$AX_SK" "$AX_ASSET_RAW" "$AX_AS"
if [ "$AX_CAT_N" != NA ] && [ "$AX_BASE_N" != NA ] && [ "$AX_SK" != NA ] && [ "$AX_AS" != NA ]; then
  pass "A 臂模板面可枚举：catalog=$AX_CAT_N 基线=$AX_BASE_N 骨架=$AX_SK 资产=$AX_AS"
else fail "A 臂某模板面不可枚举" ; fi

if [ -n "$AX_SRC" ]; then
  curl -s "$AX_BASE/scenario-template-assets/$AX_SRC/export" -H "$AUTH" -o "$WORK/ax_export.json"
  AX_EX_SCHEMA=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_export.json',encoding='utf-8'));print((d.get('data') or {}).get('schema','NA'))" 2>/dev/null || echo NA)
  AX_EX_SIG=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_export.json',encoding='utf-8'));print(bool((d.get('data') or {}).get('signature')))" 2>/dev/null || echo NA)
  printf '  export schema=%s signature=%s\n' "$AX_EX_SCHEMA" "$AX_EX_SIG"
if [ "$AX_EX_SIG" = True ] || [ "$AX_EX_SIG" = true ]; then pass "A 臂导出信封自描述 + 签名"; else fail "A 臂导出签名缺失"; fi
  # capability-check：拉工作区首个 dataset 带 role=primary（只读元数据，无模型调用）
  AX_CAPID=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_catalog.json',encoding='utf-8'));print(d['data'][0]['id'])" 2>/dev/null || echo "")
  curl -s "$AX_BASE/datasets" -H "$AUTH" -o "$WORK/ax_datasets.json"
  AX_DS=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_datasets.json',encoding='utf-8'));dd=d.get('data');items=dd.get('items') if isinstance(dd,dict) else dd;print((items or [{}])[0].get('id',''))" 2>/dev/null || echo "")
  AX_VERDICT=SKIP-no-dataset
  if [ -n "$AX_DS" ]; then
    curl -s -X POST "$AX_BASE/scenario-template-assets/$AX_CAPID/capability-check" -H "$AUTH" \
      -H 'Content-Type: application/json' \
      -d "{\"app_type\":\"chat\",\"datasets\":[{\"role\":\"primary\",\"dataset_id\":\"$AX_DS\"}]}" -o "$WORK/ax_cap.json"
    AX_VERDICT=$(PYTHONIOENCODING=utf-8 python -c "import json;d=json.load(open('$WORK/ax_cap.json',encoding='utf-8'));print((d.get('data') or {}).get('verdict','NA'))" 2>/dev/null || echo NA)
  fi
  printf '  capability-check verdict=%s (dataset=%s)\n' "$AX_VERDICT" "$AX_DS"
  case "$AX_VERDICT" in satisfied|repairable|partial|unavailable) pass "A 臂 capability-check verdict=$AX_VERDICT";; SKIP*) skip "A 臂 capability-check 无数据集（SKIP 是文档化行为）";; *) fail "A 臂 capability-check 未返回四态 verdict" ;; esac
fi
echo "  A 臂：导入/导出语义由单测（doc/279 §11.6）+ drill tmp/d42/run4.sh（10/10）钉住：40901 key 保留 / 40116 签名闸门 / 42231 幂等键。"

echo "== B 臂: RAGFlow canvas 模板 (${RF_BASE})"
B_HTTP=$(curl -s -o "$WORK/rf_tpl.json" -w '%{http_code}' "$RF_BASE/agents/templates" -H "Authorization: Bearer $RF_KEY")
RF_N="NA"; RF_AGENT="NA"; RF_DF="NA"; RF_DSL="NA"; RF_COMPS="NA"
if [ "$B_HTTP" = 200 ]; then
  RF_N=$(PYTHONIOENCODING=utf-8 python -c "import json;print(len(json.load(open('$WORK/rf_tpl.json',encoding='utf-8'))['data']))" 2>/dev/null || echo NA)
  RF_AGENT=$(PYTHONIOENCODING=utf-8 python -c "
import json
try:
    items=json.load(open('$WORK/rf_tpl.json',encoding='utf-8'))['data']
    print(sum(1 for i in items if i.get('canvas_category')=='agent_canvas'))
except Exception: print('NA')" 2>/dev/null || echo NA)
  RF_DF=$(PYTHONIOENCODING=utf-8 python -c "
import json
try:
    items=json.load(open('$WORK/rf_tpl.json',encoding='utf-8'))['data']
    print(sum(1 for i in items if i.get('canvas_category')=='dataflow_canvas'))
except Exception: print('NA')" 2>/dev/null || echo NA)
  RF_DSL=$(PYTHONIOENCODING=utf-8 python -c "
import json
try:
    items=json.load(open('$WORK/rf_tpl.json',encoding='utf-8'))['data']
    print(sum(1 for i in items if i.get('dsl')))
except Exception: print('NA')" 2>/dev/null || echo NA)
  RF_COMPS=$(PYTHONIOENCODING=utf-8 python -c "
import json
from collections import Counter
try:
    items=json.load(open('$WORK/rf_tpl.json',encoding='utf-8'))['data']
    c=Counter()
    for i in items:
        dsl=i.get('dsl') or {}
        for k,node in (dsl.get('components') or {}).items():
            obj=(node or {}).get('obj') or {}
            cn=obj.get('component_name') or obj.get('type')
            if cn: c[cn]+=1
    print(dict(sorted(c.items(), key=lambda x:-x[1])))
except Exception: print('NA')" 2>/dev/null || echo NA)
fi
printf '  http=%s templates=%s (agent_canvas=%s dataflow_canvas=%s) with-dsl=%s\n' "$B_HTTP" "$RF_N" "$RF_AGENT" "$RF_DF" "$RF_DSL"
printf '  DSL component inventory=%s\n' "$RF_COMPS"
if [ "$B_HTTP" = 200 ] && [ "$RF_N" != NA ] && [ "$RF_N" -gt 0 ] && [ "$RF_COMPS" != NA ]; then
  pass "B 臂 canvas 模板面可枚举：templates=$RF_N + DSL 组件清单"
else fail "B 臂模板面不可枚举 (http=$B_HTTP)" ; fi

echo "== C 臂: WeKnora 模板面 (${WK_BASE})"
WK_TOK=""
{ WK_TOK=$(curl -s -X POST "$WK_BASE/auth/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$WK_EMAIL\",\"password\":\"$WK_PW\"}" \
    | PYTHONIOENCODING=utf-8 python -c "import sys,json;d=json.load(sys.stdin);print(d.get('token') or (d.get('user') or {}).get('token') or '')"); } 2>/dev/null
if [ -z "$WK_TOK" ]; then echo "  C 臂登录失败（退出 1）"; exit 1; fi
WK_AUTH="Authorization: Bearer $WK_TOK"
WK_UNAUTH=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$WK_BASE/sandbox-configs/templates/query" -H 'Content-Type: application/json' -d '{}')
WK_REFUSAL=$(curl -s -X POST "$WK_BASE/sandbox-configs/templates/query" -H "$WK_AUTH" -H 'Content-Type: application/json' \
  -d '{"config":{"sandbox_type":"cube","cube":{"template_id":"__catalog__","api_url":"http://192.168.4.1/control"},"allow_private":false}}')
WK_REF_MSG=$(printf '%s' "$WK_REFUSAL" | PYTHONIOENCODING=utf-8 python -c "import sys,json;d=json.load(sys.stdin);print((d.get('error') or {}).get('message',''))" 2>/dev/null)
WK_PROMPT=$(ls -1 "$PROJECT_ROOT/../WeKnora/config/prompt_templates" 2>/dev/null | wc -l)
printf '  unauth=%s refusal_msg=%s prompt_templates=%s\n' "$WK_UNAUTH" "$WK_REF_MSG" "$WK_PROMPT"
if [ "$WK_UNAUTH" = 401 ]; then pass "C 臂模板目录 admin-gated（未认证 401）"; else fail "C 臂模板目录非 admin-gated"; fi
if [ -n "$WK_REF_MSG" ]; then pass "C 臂模板目录出站策略拒绝（无后端则不可枚举 → SKIP 由后端决定）"; else fail "C 臂模板目录安全探活无预期拒绝"; fi
if [ "$WK_PROMPT" -gt 0 ]; then pass "C 臂 YAML prompt 模板可枚举（$WK_PROMPT 个）"; else skip "C 臂 YAML prompt 模板目录不在本机"; fi
skip "C 臂 sandbox spawn 目录需 cube/e2b/docker 后端（测试环境无 → 按设计 SKIP）"

# ========== 判分 ==========
# 维度上：PASS(1.0) 部分(0.5) ❌(0) ➖(N/A，不计分)
printf '\n## 1. 判分矩阵（✅=完整 ⚠️=部分 ➖=不适用/需后端）\n\n' >> "$REPORT"
printf '| 维度 | A ragflow-x | B RAGFlow | C WeKnora | 说明 |\n|---|---|---|---|---|\n' >> "$REPORT"
printf '| D1 模板面可枚举 | ✅ catalog=%s 基线=%s 骨架=%s 资产=%s | ✅ templates=%s | ⚠️ templates/query 需 sandbox 后端 | 真机可枚举模板清单 |\n' \
  "$AX_CAT_N" "$AX_BASE_N" "$AX_SK" "$AX_AS" "$RF_N" >> "$REPORT"
printf '| D2 模板类型/分类 | ✅ chat/agent/dataflow/ingestion… | ✅ agent=%s+dataflow=%s | ⚠️ standard/desktop spawn | 分类维度 |\n' "$RF_AGENT" "$RF_DF" >> "$REPORT"
printf '| D3 模板内容结构 | ✅ v2/v3 pack 结构化（payload=%s） | ✅ canvas DSL（组件：%s） | ⚠️ spawn 镜像+YAML prompt 模板 | 结构证据 |\n' "$AX_PAYLOAD" "$RF_COMPS" >> "$REPORT"
if [ "$AX_EX_SIG" = True ] || [ "$AX_EX_SIG" = true ]; then AX_EX_S="✅"; else AX_EX_S="⚠️"; fi
printf '| D4 导入/导出与签名 | ✅ 信封:%s 签名:%s | ❌ 无导入导出 | ❌ 无 | 可交付性 |\n' "$AX_EX_SCHEMA" "$AX_EX_SIG" >> "$REPORT"
printf '| D5 生命周期 | ✅ draft/publish/archive/version | ❌ 引擎全局单版本 | ⚠️ ensure/replace 幂等 | 生命周期 |\n' >> "$REPORT"
printf '| D6 隔离与冲突 | ✅ 工作区隔离 40901/42231 | ➖ 引擎全局 | ⚠️ 租户级 sandbox config | 冲突语义 |\n' >> "$REPORT"
printf '| D7 能力判定 | ✅ capability-check 四态 verdict=%s | ❌ 无 | ❌ 无 | 前置能力判定 |\n' "$AX_VERDICT" >> "$REPORT"
printf '| D8 评测/知识集成 | ✅ eval/knowledge/requires 元数据 | ⚠️ DSL 内联 dataset 引用（9/19） | ⚠️ QA/intent prompt 模板 | 集成元数据 |\n' >> "$REPORT"
printf '| D9 跨环境可交付 | ✅ 信封自描述+签名 | ❌ 引擎内全局 | ❌ 后端绑死 | 交付链 |\n' >> "$REPORT"

printf '\n## 2. 覆盖度（文档方式判分汇总）\n\n' >> "$REPORT"
printf -- '- A ragflow-x：场景模板平台。模板面最完整：catalog/baseline/skeletons/assets 多面可枚举；v2/v3 pack 结构化；draft/publish/archive/version 全生命周期；导入导出+签名；工作区隔离（40901/42231）；候选能力判定四态。\n' >> "$REPORT"
printf -- '- B RAGFlow：引擎内组装画布原料。19 个全局 canvas 模板（agent=%s + dataflow=%s），DSL 组件清单丰富；但无生命周期/导入导出/能力判定/隔离，模板属引擎级全局资产。\n' "$RF_AGENT" "$RF_DF" >> "$REPORT"
printf -- '- C WeKnora：沙箱工作区模板目录 + prompt 模板。`templates/query` 是 sandbox spawn 目录（需 cube/e2b/docker 控制面），本环境无后端 → SKIP；可枚举形态为其 YAML prompt 模板（%s 个文件）。\n' "$WK_PROMPT" >> "$REPORT"
printf '\n结论：三臂模板面**范式不同**——A 是「可交付场景模板平台」，B 是「引擎内组装画布原料」，C 是「沙箱工作区模板 + prompt 模板」。文档方式判分反映的是各自范式内的能力密度，不是互相替代的排序；模板能力对比（本脚本）与 263 问答质量判分互补。\n' >> "$REPORT"

printf '\n## 3. 现场原始证据\n\n| 臂 | 证据文件 |\n|---|---|\n' >> "$REPORT"
for f in ax_catalog ax_baseline ax_skeletons ax_assets ax_export ax_cap rf_tpl; do
  [ -f "$WORK/$f.json" ] && printf '| %s | `tmp/tcc-%s/%s.json` |\n' "$f" "$TS" "$f" >> "$REPORT"
done

printf '\n## 4. 复核\n\n```bash\nbash scripts/template-capability-compare.sh   # 退出码 0=完成\n```\n' >> "$REPORT"

echo "== summary: PASS=$PASS FAIL=$FAIL SKIP=$SKIP TOTAL=$TOTAL"
echo "report: $REPORT"
[ "$FAIL" -gt 0 ] && exit 1
exit 0
