# 场景模板覆盖矩阵（2026-11-16 实测）

- 被测环境：`http://192.168.4.151:19193`（`mode:engine`，真实 RAGFlow v1.0.0 引擎）
- 被测版本：`v0.6.1` 候选（提交 `57ebc7c` + 本轮修复，镜像 tag `0.6.1-fix`）
- 证据：`out/evidence-20261117e.json`（run 1 `--phase all` 全量，run 2 `--phase lanes` 重放；两轮全绿，重放 14/14 `replayed=true`）
- 语料：`test/安全生产政策法规-docs`、`test/山东省属企业及其下属企业安全生产风险点防控情况的报告-docs`、`test/隐患检查-docs`
- 一次运行规模：9 个知识库（9 个模板各一个）、19 份文档（md/docx/xlsx）、14 个应用实例（8 对话 + 6 检索）、34 次真实问答（20 次关键字断言 + 14 次离题探针）

## 1. 九个内置模板 × 全部可实例化形态

| # | lane | 模板 key | 形态 | 知识库（真实文档） | chunks | 体检 | 修复动作（实际执行） | 实例 | 问答命中 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `enterprise-qa.chat` | `enterprise-qa` | chat | 企业问答库 | 47 | satisfied | `apply_parse_profile` | completed | 2/2 |
| 2 | `enterprise-qa.search` | `enterprise-qa` | search | 企业问答库 | 47 | satisfied | — | completed | 1/1 |
| 3 | `policy.chat` | `policy` | chat | 政策法规库 | 93 | satisfied | `apply_parse_profile` | completed | 2/2 |
| 4 | `policy.search` | `policy` | search | 政策法规库 | 93 | satisfied | — | completed | 1/1 |
| 5 | `training.chat` | `training` | chat | 培训赋能库 | 21 | satisfied | `apply_parse_profile` | completed | 2/2 |
| 6 | `operations.chat` | `operations` | chat | 运维应急库 | 30 | satisfied | `apply_parse_profile` | completed | 1/1 |
| 7 | `operations.search` | `operations` | search | 运维应急库 | 30 | satisfied | — | completed | 1/1 |
| 8 | `customer-service.chat` | `customer-service` | chat | 客服问答库 | 126 | satisfied | — | completed | 1/1 |
| 9 | `customer-service.search` | `customer-service` | search | 客服问答库 | 126 | satisfied | — | completed | 1/1 |
| 10 | `product-manual.search` | `product-manual` | search | 产品手册库 | 8 | satisfied | `enable_pushdown_and_sync_meta` + `apply_parse_profile` | completed | 2/2 |
| 11 | `sales-support.chat` | `sales-support` | chat | 销售支持库 | 6 | satisfied | — | completed | 2/2 |
| 12 | `contract-compliance.chat` | `contract-compliance` | chat | 合同合规库 | 14 | satisfied | `enable_pushdown_and_sync_meta` + `apply_parse_profile` | completed | 2/2 |
| 13 | `finance.chat` | `finance` | chat | 财务口径库 | 15 | satisfied | `enable_pushdown_and_sync_meta` + `apply_parse_profile` | completed | 1/1 |
| 14 | `finance.search` | `finance` | search | 财务口径库 | 15 | satisfied | — | completed | 1/1 |

> lane 8/9 的 chunks=126 是**含 xlsx 台账**解析出的量（同一份 `隐患检查` 台账在引擎侧单独测得 117 chunks）。在此之前 xlsx 根本传不进去，该库只有 md 的 9 个 chunk —— 见 §3 缺陷 1。

## 2. 必须被拒绝的形态（反例 lane）

模板只在 `app_types` 里声明了它真能渲染的形态。声明之外的形态必须**明确拒绝**，不许建出半成品。

| lane | 模板 | 形态 | 期望 | 实际 | 判定 |
| --- | --- | --- | --- | --- | --- |
| `neg.chat-on-search-only` | `product-manual` | chat | 4xx/41104 | 400/40085（拆分前） | ✅ 按预期拒绝 |
| `neg.search-on-chat-only` | `contract-compliance` | search | 4xx/41104 | 400/40085（拆分前） | ✅ 按预期拒绝 |
| `neg.search-on-chat-only-2` | `training` | search | 4xx/41104 | 400/40085（拆分前） | ✅ 按预期拒绝 |
| `neg.agent-without-canvas` | `training` | agent | 4xx/41105 | 400/40085（拆分前） | ✅ 按预期拒绝，且 message 为「该模板无法以智能体形态实例化，请选择对话或检索形态」 |
| `neg.auto-datasets-empty-kb` | `enterprise-qa` | chat | 4xx/41140 | 400/40085（拆分前） | ✅ 按预期拒绝，且 message 为「所选知识库还没有可检索内容，请先上传文档并等待解析完成」 |

> **错误码说明**：`40085` 曾同时承载 18 种语义，本表"实际"列是 2026-11-17 实测记录（`out/evidence-20261117e.json`），写作 `400/40085（拆分前）`。后端已按语义拆分（`40085` 只保留角色自引用，其余迁到 `41100-41199`，见 `internal/pkg/httperr/codes.go` 与 doc/specification/12 §2.2），因此"期望"列是拆分后的契约码；复跑时以新码为准。

> `agent` 形态：9 个模板里有 6 个在 `app_types` 里写了 agent（contract-compliance / customer-service / operations / product-manual / sales-support / training），但它们的 Pack 没有 `canvas_dsl`，`renderable_app_types` 因此不含 agent。catalog 如实标注，实例化如实拒绝（41105），不会建空画布。

## 3. 问答断言（真实引擎作答，命中原文事实）

| lane | 问题 | 期望事实 | 命中 | 答案/引用 |
| --- | --- | --- | --- | --- |
| enterprise-qa.chat | 中央企业依据风险程度分为几类？ | 三类 | ✅ | 248c / 8 refs |
| enterprise-qa.chat | 第一类中央企业和涉矿中央企业集团总部应当配备什么？ | 安全生产总监 | ✅ | 253c / 8 refs |
| enterprise-qa.search | 中央企业依据风险程度分为几类？ | 三类 | ✅ | 96c / 12 refs |
| policy.chat | 本省落实安全生产行政责任制应当坚持哪些原则？ | 谁主管谁负责 | ✅ | 78c / 6 refs |
| policy.chat | 安全生产工作实行什么制度？ | 一岗双责 | ✅ | 22c / 6 refs |
| policy.search | 生产安全事故分为哪几级？ | 特别重大事故 | ✅ | 180c / 12 refs |
| training.chat | 专家年度调用时间原则上不超过多少天？ | 15 | ✅ | 25c / 8 refs |
| training.chat | 专家遴选数量按什么标准掌握？ | 两倍 | ✅ | 32c / 8 refs |
| operations.chat | 省属一级企业接到事发单位事故报告后多长时间内书面报送？ | 半小时 | ✅ | 61c / 6 refs |
| operations.search | 省属企业每年应开展什么活动？ | 演练 | ✅ | 268c / 12 refs |
| customer-service.chat | 安全生产举报工作所需经费如何保障？ | 财政预算 | ✅ | 79c / 8 refs |
| customer-service.search | 县级以上应急管理部门承担举报工作的哪些环节？ | 核查 | ✅ | 210c / 7 refs |
| product-manual.search | 山东大厦排查出的燃气相关风险是什么？ | 燃气 | ✅ | 256c / 8 refs |
| product-manual.search | 山东健康集团按什么原则管控风险？ | 分级管控 | ✅ | 388c / 8 refs |
| sales-support.chat | 山东农业集团聚焦哪些重点领域？ | 粮食加工 | ✅ | 40c / 5 refs |
| sales-support.chat | 南郊集团要求权属单位坚持什么理念？ | 人民至上 | ✅ | 37c / 4 refs |
| contract-compliance.chat | 社会化服务机构服务档案保管期限不得少于几年？ | 5年 | ✅ | 70c / 8 refs |
| contract-compliance.chat | 矿山企业主要负责人对所属矿山每季度至少开展几次重大事故隐患排查？ | 每季度 | ✅ | 61c / 5 refs |
| finance.chat | 一个考核年度内发生1起重大及以上事故扣至多少分？ | 80 | ✅ | 133c / 5 refs |
| finance.search | 山东健康集团如何管控风险？ | 分级管控 | ✅ | 2032c / 12 refs |

每条 lane 另有一条**离题探针**（模板 `evaluation_questions` 里的第一条，写的是「报销标准」「7 天无理由退货」「E03 处理步骤」这类不属于该库的问题）。它不做关键字断言，只断言 **HTTP 200、答案非空、无 5xx**——离题问题必须被正常回答（哪怕是「资料中未提及」），不能把模板自己的示例问题变成 500。

## 4. 与上一轮（tag `20261116e`）的差异

上一轮只覆盖了 3 个知识库 / 9 个应用（policy+expert+assess 三库拼出来），只验证了 `policy`、`finance`、`contract-compliance` 等少数模板；`training`/`operations`/`customer-service`/`product-manual`/`sales-support`/`enterprise-qa` 的 chat/search 形态**没有用真实数据验过**，`agent` 形态与「自动建库」更没验过。本轮补齐，并因此发现 3 个缺陷（见 doc/286）。
