# RAGFlow-X

> An enterprise knowledge-infrastructure layer for RAGFlow in private data centers (Control Plane + Gateway). RAGFlow remains the core RAG execution and data plane — integrated strictly through its official HTTP APIs, never forked or bypassed. RAGFlow-X is the single business operations surface: all enterprise workflows (knowledge bases, parsing, chunk governance, retrieval tuning, conversations, agents, approvals, audit) are performed in the RAGFlow-X UI; RAGFlow's native web UI is reserved for engine-level administration only.

[English](./README.md) | [简体中文](./README.zh-CN.md)

[![license](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](./LICENSE)
[![version](https://img.shields.io/badge/version-v0.5.1-green.svg)](./CHANGELOG.md)
[![Discord](https://img.shields.io/badge/Discord-community-blueviolet?logo=discord&logoColor=white)](https://discord.gg/fnc8Ds9nj)
![backend](https://img.shields.io/badge/backend-Go%20%2B%20Gin-informational.svg)
![frontend](https://img.shields.io/badge/frontend-React%20%2B%20shadcn--admin--kit-blueviolet.svg)

RAGFlow-X is built for private data centers and real enterprise production
environments. "No fork" is a data-plane architecture decision, not a
feature-scope statement: RAGFlow stays the sole execution and data plane
(parsing, chunking, retrieval, model inference, workflow execution), accessed
exclusively through its official `/api/v1` HTTP APIs. At the business
operations layer the opposite applies — RAGFlow-X is the single pane of glass.
Business users, knowledge operators, and administrators perform every daily
workflow in the RAGFlow-X UI and never need to touch RAGFlow's native web UI,
which remains reserved for engine-level administration. On top of this unified
surface, RAGFlow-X adds secure multi-tenant governance, RBAC/ABAC, an
OpenAI-compatible gateway, audit compliance, approvals, usage metering and
quota, observability, alerting, full-lifecycle knowledge operations, and a
unified conversation experience.

## Highlights

- **No fork, API-first.** RAGFlow operations use the official `/api/v1` HTTP API; operational data lives in an independent `rgx_*` PostgreSQL/SQLite database.
- **Single operations surface.** All business workflows happen in the RAGFlow-X UI — including equivalents of RAGFlow's own business features (datasets, parsing, chunks, retrieval testing, chat, agents); RAGFlow's native web UI is engine-admin-only.
- **Security and governance.** Tenant governance, RBAC/ABAC, approvals, audit, observability, metering, retention, and a governed enterprise gateway.
- **Gateway.** `POST /v1/chat/completions` with authentication, RBAC, rate limiting, atomic quota reservation, metering, audit, 429 handling, and degradation.
- **Full-lifecycle knowledge system.** Dataset integration, parsing, quality, versioning, publication, consumption, feedback, evaluation, and operations.
- **Unified conversation center.** A consistent entry point for Chat, Search, and Agent experiences with role-aware routing, citations, rich content, tools, and export.
- **Production readiness.** Version/capability probing, contract tests, Docker Compose, structured logging, Prometheus, and audit hash-chain verification.
- **Verified against RAGFlow v1.0.0 (stable).** All 97 upstream endpoints in the capability registry are pinned to the v1.0.0 engine
  (re-verified on a live v1.0.0 deployment), and the five real behaviour drifts found on v1.0.0 (dataset-create schema,
  chat completion route, artifact surface, canvas list, document parse binding) are all fixed and guarded by
  a declarative *version band → artifact kind* map with fail-closed rejection instead of silent degradation.
- **Citation transparency.** Citations carry ref_index/positions for precise in-document positioning; the conversation center rehydrates citations from the completion-time snapshot (v1.0.0 sessions no longer echo them on read-back).
- **Honesty discipline.** Every turn records a structured answerability claim (answerable / confidence / source / abstain reason) on top of the engine's own answer — annotated, never rewritten.
- **Search-app binding foolproofing.** dataset_ids resolve across link_id / link_name / engine_id with tenant-ownership validation, near-miss library suggestions instead of engine error leaks, and contract-as-documentation error codes (40092/40093/40095).

## What's new in v0.5.0

First release since v0.2.0 — it packages three version lines (v0.3.0 knowledge compilation + enterprise
memory + governance/UX, v0.4.0 template single-source-of-truth + trial runs, v0.5.0 RBAC hardening +
upstream RAGFlow v1.0.0 compatibility). See [CHANGELOG.md](./CHANGELOG.md) for the full list.

- **RAGFlow v1.0.0 compatible.** Endpoint pins lifted to `v1.0.0`, five upstream drifts fixed
  (dataset-create schema, `/chat/completions` route, artifact surface, `compilation_template_group`
  rows in `/agents`, mandatory `pipeline_version_id` on document re-binding), and a declarative
  band→artifact-kind map with `422/42261` fail-closed (`page_index` is a structure variant upstream).
- **Knowledge compilation & enterprise memory** shipped end to end (profile → readiness → run →
  dispatch → artifact binding → compiled-first retrieval → improvement gate → recovery).
- **Security hardening.** Custom-role privilege escalation closed, "write implies read" enforced at
  evaluation, `viewer` reduced to the read-only slice of `business_user` (read inversion 19 → 0),
  and the previously inert platform fine-grained actions are now real gates.
- **Governance & evidence.** Release Gate (11 gates), document-level governance projection, precise
  citation locators, multi-dimensional quota buckets, unified notification event stream, gateway v1
  contract, and a frozen D1 regression judge that forbids leaking trial runs into release evidence.
- **Single source of truth for templates**, with trial runs as their own permission resource
  (`trial-run`) that can never become release evidence.
- **Evidence on v1.0.0:** `scripts/upstream-verify.sh --full` → 5 passed / 0 failed / 0 skipped.

## Engine compatibility

| RAGFlow engine | Status | Notes |
|---|---|---|
| `0.27.x` | Verified band | Legacy baseline; `page_index` compilation artifacts supported |
| `1.0.0` | **Verified** (2026-11-10) | Pins and live drills re-run; wiki-only compilation artifacts |
| `> 1.0.0` | `verification_required` | Fail-closed: release creation and capability use require a fresh live contract drill |
| unparseable / unreachable | `blocked` | Fail-closed on release creation; runtime degrades to the last verified band |

Re-verification procedure: `./scripts/upstream-verify.sh --full` (P1 provider-seam
contract tests plus the serial `service` E2E suite, opt-in via `RGX_ALLOW_*` flags),
then archive the evidence. The operator-facing upgrade checklist — including the five
verified upstream behaviours (dataset `embedding_model`, the moved chat endpoint,
wiki-only artifacts, `compilation_template_group` rows, `pipeline_version_id` binding) —
lives in the
[operations runbook](./doc/user/en/operations-runbook-and-failure-drill-sop.md).

### Known upstream issues on v1.0.0

Defects and unannounced contract changes that are **still open upstream** on the
verified `1.0.0` band, as observed while running the v0.5.0 verification round
(2026-11-10). Nothing here is silently degraded: each row is either absorbed by
an explicit platform behaviour or rejected fail-closed. The machine-readable,
per-capability view is served at runtime by `GET /api/v1/system/capabilities`
(`version_issues` + `version_issues_summary`).

| Upstream issue | Effect without a workaround | RAGFlow-X handling |
|---|---|---|
| Structured compilation artifacts have no read surface: `GET /datasets/{id}/artifacts` serves only `type_kwd=wiki_page`, and retrieval returns only `compile_kwd=wiki`, although the Compiler still writes `page_index` / `timeline` / `knowledge_graph` rows | Runs succeed but their products can never be listed, bound, or cited | Declarative `engine version → artifact kind` map; non-`wiki` kinds are rejected at trigger time (`422` / `42261`, “use kind `wiki` instead”) instead of producing unreachable artifacts |
| `POST /auth/login` in the shipped web bundle sends `RSA(base64(password))` while the backend decrypts `RSA(plaintext)` → **code `109` for every browser login** | Engine-admin browser login is unusable | RAGFlow-X never proxies that page: operators authenticate against RAGFlow-X, and engine access uses API keys; the upstream bundle needs a local dist patch if its own UI must be used |
| Dataset creation requires an explicit `embedding_model`; without it the engine resolves the literal `"0"` and parsing fails with `500 model configuration unavailable` | First parse of a new dataset fails | The platform resolves the tenant's default embedding model (`ListDefaultModels` → `ListModels`, 5-minute TTL) and always sends it |
| `GET /agents` mixes `type=compilation_template_group` rows into `data.canvas` **and counts them in `total`** | Shadow sync imports template groups as runnable agents; `GET /agents/{id}/versions` on them returns `103 permission to access the agent` | `ListAgents` filters by `type`, keeps rows with no `type` (older engines), and decrements `total` by the filtered count |
| Re-binding a document to a different canvas fails with `102 pipeline version does not exist / does not belong to canvas X` unless the canvas' published version is named | Pipeline-dataset re-binding and parse-only drills break | `pipeline_version_id` is published (`POST /agents/{id}/publish`) and sent with the binding; older engines ignore the field |
| [`#21095`](https://github.com/infiniflow/ragflow/issues/21095) — the retrieval `_source` projection drops `source_doc_ids` (community fix PR [#21106](https://github.com/infiniflow/ragflow/pull/21106) still unreviewed) | Citation → chunk mapping is lost on read-back | `ref_index` / `positions` are persisted with the answer and citations are rehydrated from the completion-time snapshot, so positioning survives the dropped field |
| [`#21097`](https://github.com/infiniflow/ragflow/issues/21097) — `ResponseHeaderTimeout` (20 min) and the stream timeout (60 s) are hardcoded; merged on `main` via [#21434](https://github.com/infiniflow/ragflow/pull/21434) but **not yet in any release tag** | Long compilations and slow LLMs hit upstream timeouts with no knob | Long-running work is driven by the RAGFlow-X scheduler with explicit deadlines and per-run recovery, never by trusting the engine's header timeout |
| [`#21096`](https://github.com/infiniflow/ragflow/issues/21096) — Windows build breakage in the agent sandbox (`statusFromExitCode`); community fix PR [#21099](https://github.com/infiniflow/ragflow/pull/21099) unreviewed | The upstream sandbox cannot be built on Windows | Not patched: the sandbox runs on Linux only and is governed, never re-implemented, by RAGFlow-X |
| Graph retrieval, the Memory `team-me` permission model, and the agent `sandbox`/`skills` surface differ from the documented form across 1.0.x builds | Behaviour can drift between patch releases | Tracked as `untested` in the version-issue registry: exercised only in dry runs, never promoted to a release gate on an unverified band |

## Quick start

### Run a release archive (UI included)

Every release binary embeds the frontend build, so API and UI come out of one
file:

```bash
# unpack ragflow-x-<version>-<os>-<arch>.tar.gz / .zip, then
cp config/config.example.yaml config/config.yaml   # Windows: copy
./ragflow-x                 # Windows: ragflow-x.exe
```

Open `http://localhost:9191/`. The server reads `config/config.yaml` (or
`config.yaml`) next to the binary without any flag; pass `-config <path>` to use
another file.

If the database in your config is not reachable yet, the server boots the
first-run setup wizard instead of failing: the same URL serves the UI, the
wizard posts to `/api/v1/system/setup/{preflight,apply}` and the router is
swapped in place once it succeeds. The API alone stays at `/api/v1/...`
(`GET /api/v1/health`), and `/metrics` never falls back to the UI.

### Local development

```bash
make start-local
cd web && npm install && npm run dev
make status    # inspect services
make stop      # stop services
```

The local profile uses SQLite and a mock RAGFlow client.

### Connect a real RAGFlow instance

1. Copy `config/config.example.yaml` to `config/config.yaml`.
2. Configure `ragflow.base_url`, `api_key`, PostgreSQL, and Redis. Inject secrets through `RGX_*` environment variables.
3. Start with `make start`.

### Docker Compose

```bash
cd docker
cp .env.example .env
docker compose up -d --build
```

To use a prebuilt image:

```bash
docker pull ghcr.io/davidsche/ragflow-x:v0.5.1
```

Replace the `build:` section in `docker-compose.yml` with:

```yaml
image: ghcr.io/davidsche/ragflow-x:v0.5.1
```

## User documentation

| Document | Purpose |
|---|---|
| [User guide](./doc/user/en/user-guide.md) | Daily workflows for administrators and business users |
| [Built-in permission matrix](./doc/user/en/permission-matrix.md) | Built-in roles, resources, actions, and boundaries |
| [Operations runbook](./doc/user/en/operations-runbook-and-failure-drill-sop.md) | Initialization, upgrade, rollback, degradation, sync, and quota operations |
| [Audit-chain backup and repair SOP](./doc/user/en/audit-chain-backup-and-repair-sop.md) | Verification, backup, repair, and evidence archiving |

Chinese documentation is available in the
[user documentation index](./doc/user/README.md).

## Product video

![RAGFlow-X product overview](https://github.com/user-attachments/assets/a5a7dad3-fd23-4a08-ac64-0abf563a6ad2)

## Development checks

```bash
make build
make test
make vet
cd web && npm run typecheck && npm test
```

## Contributing

Before opening a pull request, read
[`CONTRIBUTING.md`](./CONTRIBUTING.md). Standard flow:

1. Fork and clone the repository:
   ```bash
   git clone git@github.com:DavidSche/ragflow-x.git
   ```
2. Create a feature branch: `git checkout -b feat/my-feature`.
3. Run local quality gates: `make check` and `cd web && npm test`.
4. Submit a pull request using the repository PR template.

## Community

- Discord: [join the community](https://discord.gg/fnc8Ds9nj)
- Issues: [GitHub Issues](https://github.com/DavidSche/ragflow-x/issues)
- Security: follow [`SECURITY.md`](./SECURITY.md); do not report vulnerabilities publicly.

## License

This project is licensed under GPL-3.0. Distributing binaries, modified source,
or derivative works requires distributing the corresponding source under the
same license.

RAGFlow remains an independent project and is integrated only through its
official HTTP API. Follow the upstream license terms when deploying a complete
solution.
