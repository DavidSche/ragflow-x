# 场景模板全量端到端验证（真实文档 × 真实引擎）

这套脚本回答一个单元测试回答不了的问题：**用户下载下来的包，拿真实文档建库、挑一个内置场景模板、点「创建」，到底能不能用。**

覆盖 9 个内置模板的**全部可实例化形态**（chat / search）、模板声明了却不可渲染的形态（agent）的**显式拒绝**、以及「自动建库」这条向导里真实存在的岔路。所有知识库都由 `test/` 下的真实业务文档建成（山东省安全生产政策法规、省属企业风险防控报告、隐患检查台账），所有问答都由真实 RAGFlow 引擎回答，答案要命中原文里的具体事实（「15 天」「两倍」「0.5 分」「80 分」「三类」…）。

## 1. 怎么跑

```bash
# 全量（建库 + 全 lane + 问答），约 15–25 分钟
RGX_E2E_TAG=20261117e python test/e2e/scenario-templates/e2e_scenario_templates.py

# 只跑问答（复用 state-<tag>.json 里已建好的库与实例）
RGX_E2E_TAG=20261117e python test/e2e/scenario-templates/e2e_scenario_templates.py --phase lanes

# 只建库（上传 + 解析 + 等 chunks）
RGX_E2E_TAG=20261117e python test/e2e/scenario-templates/e2e_scenario_templates.py --phase data

# 单个 lane
RGX_E2E_TAG=20261117e python test/e2e/scenario-templates/e2e_scenario_templates.py --only policy.chat,finance.search
```

Windows 下务必带 `PYTHONUTF8=1 PYTHONIOENCODING=utf-8`（脚本里有中文资源名与中文断言的输出）。

退出码：`0` 全绿；`1` 有 FAIL。**FAIL 一律代表产品或环境缺陷，没有「已知问题」豁免**——上一轮就吃过亏：把 `502/50203` 当成引擎 flake 加窄豁免，实际是我们自己给 `POST /datasets/:id/parse` 传了空 `document_ids`（doc/285 §11.4）。

## 2. 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `RGX_E2E_BASE` | `http://192.168.4.151:19193/api/v1` | 被测网关 |
| `RGX_E2E_TAG` | UTC 时间戳 | 资源后缀，一次运行一个 tag，互不干扰 |
| `RGX_E2E_PLATFORM_USER/PASS` | `admin` / `RgxTest#2026a` | 只在「全新库需要建工作区用户」时用到 |
| `RGX_E2E_WS_USER/PASS` | `e2e-admin` / `E2e#2026pass` | 真正干活的租户管理员 |
| `RGX_E2E_WS_NAME` | `E2E-场景模板全量验证` | 工作区名 |
| `RGX_E2E_PROJECT` | `E2E 场景模板验证项目` | 实例归属项目 |
| `RGX_E2E_OUT` | `test/e2e/scenario-templates/out` | 证据与续跑态目录 |
| `RGX_E2E_PARSE_TIMEOUT` | `900` | 等 chunks 的上限（秒） |
| `RGX_E2E_QA_TIMEOUT` | `600` | 单次问答上限（秒） |

## 3. 产物

- `out/evidence-<tag>.json`：每一步的 `stage/method/path/http/code/summary`，外加每个 lane 的体检结论、修复动作、实例 id、问答全文摘要（前 600 字）；`meta.fails` 是失败清单。
- `out/state-<tag>.json`：租户/项目/知识库 id，用于 `--phase lanes` 续跑（复用已建好的库与实例）。
- 证据是**交付物**，最后一次全绿的 evidence 应随代码一起提交（供复核），历次证据不必保留。

## 4. 断言设计（为什么这么写）

1. **先解析出 `chunk_count>0` 才进体检**。没解析完的库谈体检没有意义。
2. **体检没有 verdict 直接 fail lane**。曾经有一版脚本在 `probe_limited` 时静默跳过，lane 变绿但什么都没验证。
3. **每条 repair 只走它自己声明的通道**：`apply_parse_profile` → `POST /datasets/:id/parse-profile`；`enable_pushdown_and_sync_meta` → `PUT /datasets/:id`。同一族里两个通道不能混用（doc/285 §10.1）。
4. **修复前后对全部数据集做快照**，只允许目标库变化。共享库被顺手重切是真实发生过的缺陷（doc/285 §11.1）。
5. **下推修复要轮询**：`enable_pushdown_and_sync_meta` 只是异步回填 `meta_fields`，立刻复检会得到「还是 repairable」。脚本轮询到 `satisfied`，界面则显示「稍后复检」，两者不矛盾。
6. **实例化用固定 `Idempotency-Key`（`e2e-<lane>-<tag>`）**，重放必须 `replayed=true` 且 id 不变。
7. **全流程零 5xx**，`assert no-5xx` 是硬断言，无豁免。
8. **两个租户/用户列表不可靠**（平台管理员看不到工作区租户，列表回 `total:0`），所以引导阶段先试登录工作区用户，`409/40911` 视为「已存在」。
9. **解析按界面做法逐个传文档 id**。引擎没有「parse all」形态（`{}`/`[]` 都回 `code:102 document_ids is required`）。
10. **反例 lane 也是真断言**：不可渲染形态必须 4xx + 明确 code，且 message 里不许出现 `agent_dsl` 这类内部字段名。

## 5. 覆盖矩阵

模板 × 形态 × 知识库 × 问答题见 [matrix.md](./matrix.md)。九个模板全部覆盖，没有「没测」项；agent 形态因为 pack 没有画布，被 catalog 标为不可渲染，脚本按「必须显式拒绝」验证。

## 6. 清理

```bash
RGX_E2E_TAG=20261117b python test/e2e/scenario-templates/cleanup_tag.py --dry-run
RGX_E2E_TAG=20261117b python test/e2e/scenario-templates/cleanup_tag.py
```

按 tag 后缀删除知识库 / 对话应用 / 检索应用，幂等（再跑一次输出 `nothing to clean`）。**保留最新 tag 供人工复核**，其余删掉，否则一次运行就在测试租户里留 9 个库。

## 7. 已知边界

- 只验证**内置模板**（seed 出来的 9 个）。租户 fork（`source=copy`）后的模板走的是同一套实例化服务，但 fork 后的 `dataset_suggestions` 与能力需求可能被改过，未在本矩阵内。
- 只验证**真实引擎**（151 环境 `mode:engine`），不验证 stub/离线模式。
- 问答断言是**关键字命中**，不是语义评分：命中说明「答案里出现了原文那个事实」，不保证措辞或引用定位完美。
- 评测集分数（`scenario-eval-gate`）不在本脚本范围内，由发布闸门单独跑。
