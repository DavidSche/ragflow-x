# RAGFlow-X User Guide

> Applies to: RAGFlow-X v0.5.0 (schema migration v161 and later) | Updated: 2026-11-10 | Screenshots: local development environment (sample data)

This guide covers administrator and business-user workflows in RAGFlow-X. The
product closes the gap between enterprise production requirements and RAGFlow's
core RAG engine through governed security, a full-lifecycle knowledge system,
and a unified conversation experience.

## Sign in

1. Open the web UI and sign in with your username and password.
2. If enterprise OIDC is enabled, use the enterprise SSO entry.
3. After sign-in, the sidebar is generated from your effective permissions.

Common login errors:

| Error | Meaning | Action |
|---|---|---|
| `401/40101` | Invalid username or password | Check credentials; avoid repeated attempts. |
| `429` | Too many login attempts | Wait for the rate-limit window to expire. |
| `403/40301` | Workspace disabled | Contact a platform administrator. |

## Dashboard

The dashboard summarizes usage, tenant/workspace activity, service health, and
job state. Use it as the first stop when checking DB, Redis, document engine,
and RAGFlow health.

## Datasets and documents

### Create and configure a dataset

1. Go to **Datasets** and create a dataset.
2. Choose the parser, chunking method, embedding model, similarity threshold,
   top-k, and rerank settings.
3. Save the dataset before uploading documents.

### Upload and parse documents

1. Upload supported files to the dataset.
2. Start parsing from the document list.
3. Track progress in the task center.
4. After parsing completes, inspect chunks, enable the document, and test
   retrieval.

### Quality tracking

The document list shows parser quality status and gate actions. Open the
quality dialog to review the final decision, scores, and fallback attempts.

## Workbench and conversation center

1. Select a Chat, Search App, or Agent target.
2. Enter a question; streaming output starts automatically.
3. Review inline citations, citations in the side panel, tool summaries, and
   execution status.
4. Give feedback, stop a long-running response, or retry after a failure.
5. Export conversations to Markdown, HTML, JSON, or PDF where available.

References are tenant-checked before a document deep link is allowed.
Citations expand to the matched chunk, document, and page, and jump to the
corresponding compiled artifact section. Citations travel with the answer into
exported snapshots so they can be re-checked later.

## Trial runs

A trial run validates an assistant or scenario template without publishing it:
the current configuration executes one real retrieval and answer, and the
result, latency, and parameter snapshot are archived for release review.

1. Use **Trial run** on the assistant or scenario template, or call
   `POST /api/v1/trial-runs`.
2. Pick the dataset and question; confirm the model and retrieval parameter
   profile.
3. The result shows the answer, citations, parameter snapshot, and
   `policy_hash`. Repeating a trial run with the same configuration must
   produce the same snapshot; a difference means the configuration drifted.
4. Release review can cite the trial-run id as evidence that the assistant was
   actually exercised.

Starting or cancelling a trial run requires `trial-run:execute`; reading results
requires `trial-run:read`. Trial runs are their own resource and no longer
borrow evaluation-set execution rights. Failures are recorded as
`retrieval_failed` rather than being reported as an empty but successful answer.

## Knowledge compilation and enterprise memory

Knowledge compilation turns parsed documents into auditable structured
artifacts (`wiki` pages) used for cross-document answers and citation
back-tracing. Parsing still owns chunking and indexing; compilation owns the
cited knowledge pages.

1. Start a run from the dataset page or `POST /api/v1/datasets/:id/compilation/runs`;
   track it with `GET /api/v1/datasets/:id/compilation/runs`.
2. Compilation honours the dataset compilation token budget. Too small a
   budget truncates artifacts, too large a budget slows the run; validate the
   path on a single small document before scaling out.
3. Inspect artifacts with `GET /api/v1/datasets/:id/artifacts`; each entry
   carries the rendered body and its source chunk ids for line-by-line
   citation checks.
4. Compiled artifacts are retrievable by conversations and search apps.

**Engine constraint:** RAGFlow v1.0.0 exposes `wiki` as the only readable
artifact kind. The legacy `page_index` variant is rejected at the entry point
with `422/42261` and a message to use `wiki`, instead of degrading silently into
a run that claims success but produces no artifact. Engines outside the verified
version band require a contract drill before the feature is enabled.

Enterprise memory persists facts confirmed in conversation at workspace, user,
and session scope. Workspace administrators own the policies
(`memory-policy`); business users may write and forget their own memory content
(`memory-write`, `memory-forget`); operators manage workspace-level content;
forgetting is audited and irreversible. `viewer` has no memory write or forget
permission, and reading memory content across workspaces is a sensitive
governance read that plain `memory:read` does not grant.

## Agents

Agents support configurable workflows and versioning. A published agent can be
selected from the conversation center. Use the execution timeline to inspect
titles, summaries, durations, and statuses without exposing raw tool arguments.

## Approvals

1. Submit a governed action.
2. If a policy matches, the action is held in the approval center.
3. Approvers can approve, reject, delegate, or batch-process requests.
4. Approved actions are executed by the worker and audited.

Approval holds are expected behavior, not errors.

## Models and API keys

Administrators manage model providers, instances, routes, and API keys. API
keys are tenant-scoped and support expiry, rate limits, and quotas. Credentials
must be stored in environment variables or a secret store, not committed to
source control.

## Audit

Users with `audit:read` can inspect immutable audit records and verify the hash
chain. Platform administrators can repair a specific tenant through
`POST /api/v1/audit/repair`. See the audit SOP for backup and repair rules.

## System settings and RAGFlow synchronization

The system settings center controls approvals, alerting, observability,
security, gateway, and retention behavior. RAGFlow resource synchronization
supports manual reconciliation and scheduled reconciliation.

For scheduled runs, both **sync enabled** and **scheduled reconciliation** must
be enabled. Only one active run is allowed per source at a time.

## Alerts and quotas

- **Alerts:** overdue approvals, failed release gates, compilation failures, and
  delivery errors surface in **System → Alerts** (`GET /api/v1/alerts`).
  Operators can mark an alert read, claim it, and retry a failed delivery
  (`GET /api/v1/alerts/deliveries`,
  `POST /api/v1/alerts/:id/deliveries/:channel/retry`).
- **Quotas and rate limits** are counted in multi-dimensional buckets per
  tenant, model, and assistant. Multi-instance deployments keep buckets in
  Redis so each replica does not count its own. Throttled requests return `429`
  with the retry window instead of being dropped silently.
- **Observability:** `GET /api/v1/system/settings/runtime/report` and the metrics
  endpoints report quota hits, alert backlog, and compilation task state.

## Daily checks

| Check | Frequency | Notes |
|---|---|---|
| Engine health | Daily | DB, Redis, document engine, RAGFlow |
| Audit-chain verification | Weekly | Follow the audit SOP on failure |
| Approval backlog | Daily | Clear overdue requests |
| Task center | Daily | Handle failed or stopped tasks |
| Compilation runs | Daily when compilation is on | Review failed/cancelled runs before re-running on a small sample |
| RAGFlow resource sync | Daily when scheduled | Review conflicts and missing resources |
| Alerts and notifications | Daily | Clear OPEN alerts; keep deliveries out of long PENDING/RETRY |
| Usage and quotas | Weekly | Adjust limits as needed |
| Backup | Daily | Operational database and audit table |

## Troubleshooting

- **Menus are missing:** the account lacks the corresponding resource
  permissions. Ask an administrator to review the role.
- **Document parsing does not progress:** check the task center, document
  enabled state, RAGFlow health, embedding model, and queue health.
- **No citations:** confirm the dataset is parsed, the document is enabled, and
  the question is semantically covered by the corpus.
- **Sync conflict:** resolve `identity`, `mapping`, or `content` conflicts
  explicitly. Do not allow automatic overwrites.
- **UI is blank:** refresh, check `/api/v1/system/health`, verify the session,
  and inspect browser console/network errors.
- **Compilation is rejected:** a `422/42261` message such as
  `compilation kind ... is not supported by engine ...` means the engine version
  does not support the requested artifact kind. Use `wiki` on RAGFlow v1.0.0;
  do not treat the rejection as a permission problem.
- **Trial run cannot be started or is invisible:** starting needs
  `trial-run:execute` (a creative action that read-only roles never hold); a
  missing entry point means `trial-run:read` is absent or the assistant is out
  of scope. A failed trial run stays `retrieval_failed`; fix the cause and
  re-run rather than expecting an empty fallback answer.
- **Slow retrieval:** separate gateway latency from engine latency with the
  `ragflow_provider_roundtrip` and `qa_ragflow_chat` baselines. Do not judge
  trial runs or release gates on a slow environment.
