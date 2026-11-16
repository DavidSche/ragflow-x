# 场景模板使用最佳实践

> 适用版本：RAGFlow-X v0.6.0｜验证时间：2026-11-16｜验证环境：4.151（`192.168.4.151:19193`，真实数据保留待复核）

本指南面向已在 RAGFlow-X 中管理知识库与对话应用的工作区管理员（`tenant_admin`）和业务应用搭建者。它回答三个问题：**场景模板怎么选、怎么用、怎么落地后才算“真正可用”**。所有流程均基于 4.151 环境真实跑通的案例（企业知识问答、合同与合规），文中引用的结构、错误信息和数据 id 都可回查。

阅读前建议先了解「用户手册」中的统一对话与知识库章节；本指南不重复入门内容，聚焦模板工作流的取舍。

## 目录

1. [概念地图：模板、目录、副本、能力与实例](#1-概念地图模板目录副本能力与实例)
2. [五条核心最佳实践（快查）](#2-五条核心最佳实践快查)
3. [案例 A：企业知识问答——快速上手路径](#3-案例-a企业知识问答快速上手路径)
4. [案例 B：合同与合规——高门槛路径与补齐指引](#4-案例-b合同与合规高门槛路径与补齐指引)
5. [常见坑与规避（实测）](#5-常见坑与规避实测)
6. [能力探测结果怎么读](#6-能力探测结果怎么读)
7. [运维与验证数据留存](#7-运维与验证数据留存)
8. [API 速查](#8-api-速查)
9. [变更记录](#9-变更记录)

---

## 1. 概念地图：模板、目录、副本、能力与实例

把路径记牢，后面所有最佳实践都建立在这张图上：

```
平台目录（由平台管理员发布的内置基线，9 个场景）
        │  fork（工作区动作，不是平台动作）
        ▼
工作区副本（copy）← 平台基线不可改，副本归工作区所有，可用其继续迭代
        │  capability-check（探测：我的知识库够不够格？差在哪？）
        ▼
能力探测结论（repairable / all_required_unsatisfied …）
        │  instantiate（绑定知识库 / 自动建库，生成对话应用实例）
        ▼
实例（chat 等应用）+ 健康看板（运行率、活跃、评测返回率）
```

四个关键事实（均为实测确认）：

- **fork 是“工作区”动作**。平台管理员对库内模板执行 fork 会被拒绝（`400/40108`）；应由工作区管理员在自己工作区内 fork。新工作区的资产表为 0 行，模板通过「平台基线 + 工作区副本」合并的**目录**可见。
- **fork 即“接管”**：工作区 fork 后，该模板在**该工作区的目录里显示为副本**（`source=copy`，平台基线在目录中被顶替）。你在副本上的改动不再影响平台基线。
- **能力探测**（`capability-check`）把「知识库 + 应用类型」映射到能力集，给出每个能力的达成等级：`operational`（可用）/ `configured`（已配置）/ `unknown`（无法证明）/ 更高等级（验证后）。**探测需要至少绑定 1 个数据集**（空数组会返回 `400/40105`），所以实际顺序应是「先有（有内容的）知识库，再探测」。
- **实例化**（`instantiate`）由 `Idempotency-Key` 保证幂等：同一 key 重放不产生重复实例，可用于断点续跑与重试。

## 2. 五条核心最佳实践（快查）

| # | 原则 | 一句话 |
|---|---|---|
| 1 | **内容先行** | 先把真实知识库建好、解析出 chunk，再实例化；空库实例化会在上游被拒（见 §5-③） |
| 2 | **探测先行** | 每次换知识库/换应用类型都跑一次 `capability-check`，让系统告诉你差在哪，而不是靠猜 |
| 3 | **幂等键纪律** | 每次实例化固定携带 `Idempotency-Key`；失败后用**同一个 key** 重试，不重复产生实例 |
| 4 | **副本迭代** | 在生产基线之上 fork 出自己的副本再改，基线保持平台可控的“黄金版本” |
| 5 | **留存验证数据** | 实例化完成的实例、用它跑的真实问答、健康看板指标，都构成“可用”证据，保留供复核（§7） |

## 3. 案例 A：企业知识问答——快速上手路径

**场景**：一家企业要把内部的报销/差旅制度做成对话助手，供员工提问（“高铁一等座能不能报？”）。选型 `enterprise-qa`（企业知识问答）——支持 chat、检索、book 式问答，能力门槛低（核心要求 `vector=operational`），是标准的新手快速上手模板。

**实测步骤与结果**（4.151，工作区「最佳实践演示工作区-BP」）：

| 步骤 | 动作 | 实测结果 |
|---|---|---|
| 1 | 工作区管理员登录，进入目录，fork `enterprise-qa` | `200`，副本 `f9d4f73bd4d54889afe3b4290b618600`，`source=copy`，从基线 `af59830768a647febbda47fcac57be8b` 派生 |
| 2 | 建知识库「BP-企业知识问答-知识库」，上传《报销与差旅实务.md》（差旅标准/发票/时限/流程） | 数据集 `776916e38dee4d84a875f62bd3deea58`，文档 `b0830dd6faf84b3b93242aa60e12721f`，解析后 **chunks=4** |
| 3 | `capability-check`（app_type=chat，绑定该库） | `200/0`，`verdict=repairable`；`vector=operational ✓`；`metadata_filter=configured`（尚无 meta_fields） |
| 4 | `instantiate`（app_type=chat，绑定已建库，携带 `Idempotency-Key`） | `200/0`，`instantiation_state=completed`，实例 `1758890c698a4aabb5931408e5a3c415`，上游 chat `3a6a…` 状态 active |
| 5 | 模板 `health` 看板 | `200/0`，模板级健康板（7 日窗口，指标见 §7） |

**要点**：

- **别把“探测 verdict=repairable”当成失败**。它表示“当前知识库差一点才带满模板所需”，step 3 里 `vector` 已达标，差的只是元数据过滤这种增强能力——此时直接实例化完全正常（实测 completed）。
- `instantiate` 返回的是**上游 chat 应用 id + 幂等结果**；绑定的是本地数据集 id（`776916e38…`），真正落到 RAGFlow 的是上游数据集（chat 的 `dataset_ids` 指向上游库）。不需要自己再去上游创建应用。
- 若要达到 `metadata_filter=ready`，给文档维护 `meta_fields`（如部门、年份）后重新探测即可；这是可选的纵深能力。

## 4. 案例 B：合同与合规——高门槛路径与补齐指引

**场景**：法务/采购要把合同条款做成合规问答（“违约金上限是多少？”），并要求**回答必须带引用原文**、能按元数据过滤。选型 `contract-compliance`（合同与合规，`chat/agent` + 引用严格 `citation-strict`）。它是高门槛模板：除 `vector` 外还要求 `citation_locator`（引用定位）达到 `configured`、`metadata_filter` 达到 `ready`。

**实测步骤与结果**（同样是内容先行，语料《采购合同条款合集.md》：付款/违约/知识产权/争议解决）：

| 步骤 | 动作 | 实测结果 |
|---|---|---|
| 1 | fork `contract-compliance` | `200`，副本 `e841d543562d41da8dbda7e3af6766d9`，`source=copy` |
| 2 | 建库「BP-合同与合规-知识库」+ 上传 + 解析 | 数据集 `e29c8139855e435f8e9fbe7a79835153`，文档 `5f01f9049af241e58d4329a62b7cf281`，**chunks=4** |
| 3 | `capability-check`（chat，绑定该库） | `200/0`，`verdict=repairable`、`aggregate=all_required_unsatisfied`：<br>· `vector` `operational ✓`（satisfied=true）<br>· `citation_locator` **`unknown`、`satisfied=false`、`reason=below_min_level`**（要求 `configured`）<br>• `metadata_filter` `configured` vs 要求 `ready` → `metadata_not_ready` |
| 4 | `instantiate`（chat，绑定已建库，带 key） | `200/0`，`completed`，实例 `8b82e547e5fe47ebac94f24007323a7a`，上游 chat `a4f6…` active（prompt 已按合规语义注入：无可信依据时明确拒绝判断） |

**要点**：

- **高门槛模板的探测不是“能否通过”的二值判断**，而是一份可执行的差距清单：系统精确告诉你 `citation_locator` 差在“未建立引用定位”、`metadata_filter` 差在“元数据未就绪”。照单补齐即可：
  1. **引用定位（citation_locator）**：在文档解析/入库时为关键条款打上可定位的引用锚点（如条款编号、章节号），使检索结果能回到原文位置，重新探测 → `configured`。
  2. **元数据就绪（metadata_filter）**：为文档维护 `meta_fields`（合同编号、乙方、金额区间、签署日期），重新探测 → `ready`。
- **即使未补齐、探测为 `repairable`，实例化本身仍可完成**（实测 completed）——系统是"允许带差距上线 + 看板跟踪"，不是"不达标就禁止"，请结合 §7 的看板持续度量。
- `contract-compliance` 触发的提示词模板自带合规语义（实测生成 chat 的 `prompt_config.empty_response` 为“未找到对应条款，不能进行合规判断，请补充合同资料或咨询法务。”）——**这正是“模板交付价值”的一部分**：不是只有 RAG，还有场景化行为。

## 5. 常见坑与规避（实测）

| # | 现象（实测错误） | 原因 | 规避 |
|---|---|---|---|
| ① | 平台管理员 fork → `400/40108` | fork 是工作区动作 | 用工作区管理员在自己工作区内 fork |
| ② | `capability-check` 传空 `datasets` → `400/40105` | 探测需 ≥1 个 (role, dataset_id) | 内容先行：先建好库，传 `[{role:"primary", dataset_id}]` |
| ③ | 空库实例化 → `40085`：`ragflow rejected chat creation: the dataset <id> doesn't own parsed file` | RAGFlow 上游要求 chat 绑定的库已解析文件 | 先上传→解析（等 `chunk_count>0`）→再实例化；绑定已就绪库，`create_missing_datasets=false` |
| ④ | 解析接口偶发 `502/50203`（`ragflow parse documents failed`） | 上游解析是异步批次，接口提前回落 | **别信单次状态**：轮询 `GET /datasets/:id/documents`，以 `chunk_count` 为准（实测 502 后 5 秒内 chunks 仍到位=4） |

## 6. 能力探测结果怎么读

探测响应的核心字段（实测值示例见 §3/§4）：

- `verdict`：模板级总体结论。`repairable`= 存在未满足要求但**可修复**；`ok`= 全部满足；`unimplemented`= 存在无法在当前环境达成的硬缺口。
- `aggregate`：`all_required_unsatisfied`= 全部必备能力未同时满足（**逐条看下面 requirements**，别只看这条）。
- `requirements[]`：每条必备能力 = `{capability, min_level, achieved, satisfied, reason?}`。**satisfied=false 且 reason 告诉你缺什么**（`below_min_level`/`metadata_not_ready`/……）。
- `capabilities[]`：探测发现的实际可观测能力与来源（`src`）与说明（`note`），例如 `metadata_filter src=document.meta_fields note=no document carries meta_fields`。

**读法口诀**：先看有没有 `reason`，有则说明系统已把“差距”翻译成人话，照着补；`verdict=repairable` 不等于不能用，结合 §7 看板决定“补到到位再上”还是“带差距上线 + 度量”。

## 7. 运维与验证数据留存

### 7.1 模板健康看板

模板 `health` 返回 7 日窗口的运行指标（实测已见）：`first_run_completion_rate`（首次实例化 24h 内产生 ≥1 次回答的实例占比）、`app_active_7d`（7 日活跃实例）、`eval_return_rate`/`eval_*`（评测返回率，retrieval_only 口径）。**将“实例化完成”与“真正被用起来”分开度量**——实例 completed 只是开始，健康看板才是“可用”的证据。

### 7.2 孤儿与实际治理

系统内置 `instantiation_orphan_reconcile` 常驻 worker 负责回收“实例化中断遗留的资源”（v0.6.0+）。团队不应对失败残留做手工脚本清理，依赖该 worker 并按审计链复核即可。

### 7.3 4.151 验证数据（保留待复核）

以下为本次指南案例的完整数据链，后续复核可直接回查（环境：`192.168.4.151:19193`，工作区管理员 `bp-admin`）：

| 归属 | 数据 id |
|---|---|
| 工作区（最佳实践演示工作区-BP） | 租户 `6959642db90443d2a1dfe167c6f1aa4d` |
| 项目（最佳实践项目） | `721f1fbf0f084372b6ba4d107896f446` |
| 案例 A fork / 知识库 / 文档 / 实例 | fork `f9d4f73bd4d54889afe3b4290b618600` · 库 `776916e38dee4d84a875f62bd3deea58` · 文档 `b0830dd6faf84b3b93242aa60e12721f`（4 chunks）· 实例 `1758890c698a4aabb5931408e5a3c415` |
| 案例 B fork / 知识库 / 文档 / 实例 | fork `e841d543562d41da8dbda7e3af6766d9` · 库 `e29c8139855e435f8e9fbe7a79835153` · 文档 `5f01f9049af241e58d4329a62b7cf281`（4 chunks）· 实例 `8b82e547e5fe47ebac94f24007323a7a` |

> 说明：`health` 报告的 `instances` 数会包含该工作区运行期内的全部实例化尝试（含早期失败尝试的计数），完成态实例以本表实例 id 为准。

**复核建议**：① 用案例 A/B 的实例对话一次真实提问，验证答案与引用；② 对案例 B 补 `citation_locator`/`meta_fields` 后重新探测，观察 `citation_locator` 由 `unknown→configured`、`metadata_filter` 由 `configured→ready` 的过程；③ 观察健康看板 `first_run_completion_rate` 由 0 上升。

## 8. API 速查

```
POST /api/v1/scenario-template-assets/:id/fork                    JSON 无 body（工作区管理员）
POST /api/v1/scenario-template-assets/:id/capability-check         {app_type, datasets:[{role,dataset_id}]}
POST /api/v1/scenario-template-assets/:id/instantiate              {app_type,name,project_id,dataset_ids|create_missing_datasets,...} + Idempotency-Key
GET  /api/v1/scenario-template-assets/:id/health                   模板 7 日健康看板
```

- `instantiate` 幂等：`Idempotency-Key` 必带；同 key 重放返回既有结果，不重复建实例。
- 绑定已就绪库用 `dataset_ids:[...]` + `create_missing_datasets:false`；自动建库用 `create_missing_datasets:true`（注意 §5-③ 空库被上游拒绝）。

## 9. 变更记录

| 日期 | 版本 | 说明 |
|---|---|---|
| 2026-11-16 | v0.6.0 | 初版；基于 4.151 实测案例 A/B、四个实测坑、验证数据清单建立 |
