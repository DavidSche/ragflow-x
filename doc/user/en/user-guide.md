# RAGFlow-X User Guide

> Applies to: RAGFlow-X v0.2.0 (schema migration v129) | Updated: 2026-10-08 | Screenshots: local development environment

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

## Daily checks

| Check | Frequency | Notes |
|---|---|---|
| Engine health | Daily | DB, Redis, document engine, RAGFlow |
| Audit-chain verification | Weekly | Follow the audit SOP on failure |
| Approval backlog | Daily | Clear overdue requests |
| Task center | Daily | Handle failed or stopped tasks |
| RAGFlow resource sync | Daily when scheduled | Review conflicts and missing resources |
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
