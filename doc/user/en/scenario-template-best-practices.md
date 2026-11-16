# Scenario Template Best Practices

> Applies to: RAGFlow-X v0.6.0 ・ Verified: 2026-11-16 ・ Environment: 4.151 (`192.168.4.151:19193`, real data kept for re-verification)

This guide is for workspace admins (`tenant_admin`) and application builders who already manage
knowledge bases and conversational apps in RAGFlow-X. It answers three questions: **which scenario
template to pick, how to drive it, and what “production-ready” actually means after rollout**. Every
flow below was exercised against the 4.151 environment with two real cases (enterprise Q&A and
contract compliance); the structures, error messages, and data ids cited in this document can be
re-verified.

## Table of contents

1. [Concept map: template, catalog, fork, capability, instance](#1-concept-map-template-catalog-fork-capability-instance)
2. [Five core best practices (quick reference)](#2-five-core-best-practices-quick-reference)
3. [Case A: Enterprise Q&A — the fast-start path](#3-case-a-enterprise-qa--the-fast-start-path)
4. [Case B: Contract compliance — the high-barrier path and how to close the gaps](#4-case-b-contract-compliance--the-high-barrier-path-and-how-to-close-the-gaps)
5. [Pitfalls and how to avoid them (verified)](#5-pitfalls-and-how-to-avoid-them-verified)
6. [How to read a capability probe](#6-how-to-read-a-capability-probe)
7. [Operations and keeping verification data](#7-operations-and-keeping-verification-data)
8. [API quick reference](#8-api-quick-reference)
9. [Changelog](#9-changelog)

---

## 1. Concept map: template, catalog, fork, capability, instance

Keep this path in mind — every best practice below builds on it:

```
Platform catalog (published built-in baselines, 9 scenarios)
        │  fork (a WORKSPACE action, not a platform action)
        ▼
Workspace copy  ← platform baselines are immutable; the copy belongs to the workspace and can evolve
        │  capability-check (is my knowledge base qualified? what is missing?)
        ▼
Probe verdict (repairable / all_required_unsatisfied …)
        │  instantiate (bind / auto-create datasets, produce conversational app instances)
        ▼
Instance (chat, …) + template health board (activity, eval return rate)
```

Four verified facts to remember:

- **Fork is a “workspace” action.** If a platform admin forks an in-catalog template it is rejected
  (`400/40108`); the workspace admin forks within their own workspace. A brand-new workspace owns 0
  template rows; templates are visible through a **catalog** that merges platform baselines with
  workspace copies.
- **Forking means “taking over” locally**: after a fork, the merged catalog shows the workspace copy
  (`source=copy`) in place of the platform baseline row. Edits on the copy no longer affect the
  platform baseline.
- **Capability probe** (`capability-check`) maps “knowledge base + app type” onto a capability set and
  reports an achieved level per capability: `operational` / `configured` / `unknown` (and higher once
  verified). The probe **requires ≥1 bound dataset** (an empty array returns `400/40105`), so the real
  order is “build a content-first knowledge base, then probe”.
- **Instantiation** (`instantiate`) is idempotent via `Idempotency-Key`: replaying the same key never
  duplicates an instance, which enables safe retry / resume.

## 2. Five core best practices (quick reference)

| # | Rule | In one line |
|---|---|---|
| 1 | **Content first** | Build and parse the real knowledge base before instantiating; instantiating an empty base is rejected upstream (§5-#3) |
| 2 | **Probe before you commit** | Run `capability-check` every time you change the base or the app type — let the system name the gap instead of guessing |
| 3 | **Idempotency-key discipline** | Always send an `Idempotency-Key`; on failure retry with the **same** key and never duplicate instances |
| 4 | **Iterate on the copy** | Fork your own copy on top of the production baseline; keep the baseline as the platform-controlled golden version |
| 5 | **Keep the evidence** | Completed instances, the real Q&A you run against them, and health-board metrics are the proof of “usable” — keep them for re-verification (§7) |

## 3. Case A: Enterprise Q&A — the fast-start path

**Scenario**: a company turns its expense / travel policy into a conversational assistant for employees
(“May I reimburse a first-class high-speed-rail ticket?”). Chosen template: `enterprise-qa` (chat,
retrieval, book-style Q&A; low bar — core requirement is `vector=operational`) — the canonical
fast-start template.

**Verified steps and results** (4.151, workspace “最佳实践演示工作区-BP”):

| Step | Action | Verified result |
|---|---|---|
| 1 | Workspace admin logs in, locates the template in the catalog, forks `enterprise-qa` | `200`, copy `f9d4f73bd4d54889afe3b4290b618600`, `source=copy`, derived from baseline `af59830768a647febbda47fcac57be8b` |
| 2 | Create dataset “BP-企业知识问答-知识库”, upload 《报销与差旅实务.md》 (standards/invoices/deadlines/flow) | dataset `776916e38dee4d84a875f62bd3deea58`, document `b0830dd6faf84b3b93242aa60e12721f`, **chunks=4** after parse |
| 3 | `capability-check` (app_type=chat, bind that base) | `200/0`, `verdict=repairable`; `vector=operational ✓`; `metadata_filter=configured` (no meta_fields yet) |
| 4 | `instantiate` (chat, bind prepared base, carry `Idempotency-Key`) | `200/0`, `instantiation_state=completed`, instance `1758890c698a4aabb5931408e5a3c415`, upstream chat `3a6a…` active |
| 5 | Template `health` board | `200/0`, 7-day window, metrics in §7 |

**Points**:

- **Do not treat “verdict=repairable” as failure.** It means the base is a step short of carrying the
  template fully. In step 3 `vector` already passed; the only gap was an enhancement capability
  (metadata filtering) — instantiating is perfectly normal (verified `completed`).
- `instantiate` returns the **upstream chat app id + idempotency result**; you bind the local dataset
  id (`776916e38…`), and the underlying RAGFlow dataset is wired automatically — no need to create the
  app upstream yourself.
- To reach `metadata_filter=ready`, maintain `meta_fields` on documents (e.g. department, year) and
  re-probe. Optional depth, not a blocker.

## 4. Case B: Contract compliance — the high-barrier path and how to close the gaps

**Scenario**: legal/procurement turns contract clauses into a compliance Q&A (“What is the penalty
cap?”) that must **cite source text** and filter by metadata. Chosen template: `contract-compliance`
(chat/agent, `citation-strict`). It is the high-barrier template: on top of `vector` it requires
`citation_locator=configured` and `metadata_filter=ready`.

**Verified steps and results** (content-first as well; corpus 《采购合同条款合集.md》 — payment /
breach / IP & confidentiality / dispute resolution):

| Step | Action | Verified result |
|---|---|---|
| 1 | Fork `contract-compliance` | `200`, copy `e841d543562d41da8dbda7e3af6766d9`, `source=copy` |
| 2 | Create “BP-合同与合规-知识库” + upload + parse | dataset `e29c8139855e435f8e9fbe7a79835153`, document `5f01f9049af241e58d4329a62b7cf281`, **chunks=4** |
| 3 | `capability-check` (chat, bind that base) | `200/0`, `verdict=repairable`, `aggregate=all_required_unsatisfied`:<br>· `vector` `operational ✓` (satisfied=true)<br>· `citation_locator` **`unknown`, `satisfied=false`, `reason=below_min_level`** (needs `configured`)<br>· `metadata_filter` `configured` vs required `ready` → `metadata_not_ready` |
| 4 | `instantiate` (chat, bind prepared base, with key) | `200/0`, `completed`, instance `8b82e547e5fe47ebac94f24007323a7a`, upstream chat `a4f6…` active (prompt injected with compliance semantics) |

**Points**:

- For high-barrier templates the probe is **not a pass/fail gate but an actionable gap list**: it
  states precisely that `citation_locator` is missing (“no citation anchor established”) and that
  `metadata_filter` is not ready. Close them as follows:
  1. **Citation locator**: add locatable anchors (clause numbers, section ids) when documents are
     parsed/ingested so retrieval can return to the source position; re-probe → `configured`.
  2. **Metadata readiness**: maintain `meta_fields` on documents (contract number, counterparty,
     amount band, signing date); re-probe → `ready`.
- Even with the gaps open and `verdict=repairable`, instantiation still **completes** (verified) — the
  system allows “ship with gaps + track on the board” rather than hard-blocking. Combine with the §7
  board for ongoing measurement.
- `contract-compliance` injects compliance-aware prompt semantics (verified: the generated chat’s
  `prompt_config.empty_response` reads “未找到对应条款，不能进行合规判断…” — i.e., refuse to judge
  without supporting clauses). **That is part of the template’s delivered value**: not just RAG, but
  scenario-shaped behavior.

## 5. Pitfalls and how to avoid them (verified)

| # | Symptom (verified error) | Cause | Avoid |
|---|---|---|---|
| ① | Platform admin fork → `400/40108` | fork is a workspace action | fork within your workspace as workspace admin |
| ② | `capability-check` with empty `datasets` → `400/40105` | probe needs ≥1 (role, dataset_id) | content first; send `[{role:"primary", dataset_id}]` |
| ③ | Instantiating an empty base → `40085`: `ragflow rejected chat creation: the dataset <id> doesn't own parsed file` | upstream RAGFlow requires the chat-bound base to hold parsed files | upload → parse (wait for `chunk_count>0`) → instantiate; bind the ready base with `create_missing_datasets=false` |
| ④ | Parse endpoint intermittently `502/50203` (`ragflow parse documents failed`) | upstream parses async and the endpoint replies early | **do not trust a single status**: poll `GET /datasets/:id/documents` and rely on `chunk_count` (verified: chunks=4 arrived within ~5s despite the 502) |

## 6. How to read a capability probe

Key fields of the probe response (verified values above):

- `verdict`: template-level conclusion. `repairable` = unmet but fixable; `ok` = fully met;
  `unimplemented` = a hard gap unreachable in this environment.
- `aggregate`: `all_required_unsatisfied` = the required set is not simultaneously satisfied (**read
  `requirements[]` item by item — do not stop at this field**).
- `requirements[]`: one line per required capability = `{capability, min_level, achieved, satisfied,
  reason?}`. `satisfied=false` plus the `reason` tells you what is missing (`below_min_level`,
  `metadata_not_ready`, …).
- `capabilities[]`: observable capabilities, their source (`src`) and notes (`note`), e.g.
  `metadata_filter src=document.meta_fields note=no document carries meta_fields`.

**Reading rule**: first look for any `reason` — the system has already translated the gap into plain
guidance; fix it. `verdict=repairable` does not mean unusable; combine with the §7 board to decide
“fix before ship” vs “ship with gaps + measure”.

## 7. Operations and keeping verification data

### 7.1 Template health board
Template `health` returns 7-day-window metrics (verified): `first_run_completion_rate` (share of
instances producing ≥1 answer within 24h), `app_active_7d` (7-day active share), `eval_return_rate` /
`eval_*` (retrieval-only, not answer-accuracy). **Measure “completed” and “actually used” separately**
— `completed` is only the start; the board is the proof of usable.

### 7.2 Orphans and real hygiene
The built-in `instantiation_orphan_reconcile` resident worker (v0.6.0+) reclaims resources left behind
by interrupted instantiations. Do not hand-script cleanup of failed leftovers; rely on the worker and
audit the audit chain.

### 7.3 4.151 verification data (kept for re-verification)
Full data chain of this guide’s cases (environment `192.168.4.151:19193`, workspace admin `bp-admin`):

| Belongs to | Id |
|---|---|
| Workspace (最佳实践演示工作区-BP) | tenant `6959642db90443d2a1dfe167c6f1aa4d` |
| Project (最佳实践项目) | `721f1fbf0f084372b6ba4d107896f446` |
| Case A fork / dataset / doc / instance | fork `f9d4f73bd4d54889afe3b4290b618600` · dataset `776916e38dee4d84a875f62bd3deea58` · doc `b0830dd6faf84b3b93242aa60e12721f` (4 chunks) · instance `1758890c698a4aabb5931408e5a3c415` |
| Case B fork / dataset / doc / instance | fork `e841d543562d41da8dbda7e3af6766d9` · dataset `e29c8139855e435f8e9fbe7a79835153` · doc `5f01f9049af241e58d4329a62b7cf281` (4 chunks) · instance `8b82e547e5fe47ebac94f24007323a7a` |

> Note: the `health` board’s `instances` count includes every instantiation attempt during the run
> period (early failed attempts included); completed instances are the ids above.

**Suggested re-verification**: ① converse with the Case A/B instances and check answers/citations;
② for Case B add `citation_locator`/`meta_fields` and re-probe to observe
`citation_locator unknown→configured` and `metadata_filter configured→ready`; ③ watch
`first_run_completion_rate` move from 0.

## 8. API quick reference

```
POST /api/v1/scenario-template-assets/:id/fork                    JSON, no body (workspace admin)
POST /api/v1/scenario-template-assets/:id/capability-check         {app_type, datasets:[{role,dataset_id}]}
POST /api/v1/scenario-template-assets/:id/instantiate              {app_type,name,project_id,dataset_ids|create_missing_datasets,...} + Idempotency-Key
GET  /api/v1/scenario-template-assets/:id/health                   7-day template health board
```

- `instantiate` is idempotent: `Idempotency-Key` is required; replaying the same key returns the
  existing result and never creates a duplicate instance.
- Bind a ready base with `dataset_ids:[...]` + `create_missing_datasets:false`; auto-create with
  `create_missing_datasets:true` (mind §5-#3: an empty base is rejected upstream).

## 9. Changelog

| Date | Version | Note |
|---|---|---|
| 2026-11-16 | v0.6.0 | First edition; built on the 4.151-verified Cases A/B, four verified pitfalls, and the retained verification data catalogue |
