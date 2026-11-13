# Operations Runbook and Failure Drill SOP

> Updated: 2026-11-13 | Applies to: RAGFlow-X v0.5.3 (schema migration v166 and later)
> Note: `doc/NNN` identifiers refer to engineering-internal design records that are not part of
> this repository; this document itself ships with the release.

## Audience and boundaries

This runbook is for platform administrators, on-call operators, and auditors.
It covers initialization, upgrade, rollback, RAGFlow degradation, resource-sync
failure, gateway quota incidents, notification failure, and audit-chain repair.

Do not bypass approvals to modify production data.

## Daily operations

| Area | Check | Failure action |
|---|---|---|
| Service health | `/system/health` | Inspect DB, Redis, object storage, and RAGFlow |
| Jobs | Task center | Retry after fixing the underlying dependency |
| Approvals | Approval center | Handle overdue or stuck requests |
| Audit | Verify hash chain | Follow the audit repair SOP |
| Quota | Gateway metrics and alerts | Check reservations and finalize/release state |
| RAGFlow sync | Run reports | Resolve conflicts and retry only after diagnosis |
| Notifications | Delivery lifecycle | Check provider credentials and retry policy |

## Initialization

1. Create the database and run migrations with the migration account.
2. Switch the runtime to a least-privilege DML account.
3. Configure JWT secrets, encryption keys, RAGFlow connection, Redis, and
   observability endpoints through environment variables or the secret store.
4. Verify `/system/health` before opening user access.
5. Confirm audit backup scheduling.

## Upgrade

1. Announce the maintenance window.
2. Back up the database and audit table.
3. Obtain the release artifact: v0.5.3 ships as a **binary archive** (frontend embedded); no
   container image is published for this version. To run a container in staging, build it from
   source with `docker compose up -d --build` in `docker/`.
4. Call `POST /api/v1/system/capabilities/verify` and confirm the engine
   version band and capability state before touching storage.
5. Apply migrations with the migration account. v0.5.3 brings the schema to
   **166**; every step is forward-only and re-runnable.
6. Start the new version with the runtime account.
7. Verify health, audit verification, task workers, gateway, and UI login.
8. Keep the previous artifact available for rollback.

### Migration map (150–166)

| Migration | Content | Rollback impact |
|---|---|---|
| 150 | Governance dictionary seed | Data only |
| 151 | Multi-dimensional quota buckets (tenant/model/assistant) |
  Older versions ignore the new buckets |
| 152 | Release version-band preflight | Older versions do not read it |
| 153–156 | Connector base tables (binding/run/actor_enabled/source_identity) |
  Ignored by older versions |
| 157 | `rgx_trial_run`, `rgx_trial_case_result`, two `rgx_chat` columns |
  Never `DROP TABLE` manually: trial evidence is lost |
| 158–161 | RBAC data sync (`business_user` read intent, `team_admin` member
  surfaces, `trial-run` resource, `viewer` read convergence) | Data only and
  idempotent; a restart re-reconciles the built-in matrix |
| 162 | Citation locator columns + per-turn top retrieval similarity |
  Additive; older versions ignore the new columns |
| 163 | `model-default` permission resource backfill (**data only**, idempotent) |
  Older versions do not know the resource; after a rollback its endpoints 403 |
| 164 | `rgx_model_default_recovery_claim` (idempotent claim table for failed
  default-model writes) | Older versions ignore it; never drop it by hand while a
  compensation is in flight |
| 165 | Template baseline lineage (`baseline_key`/`baseline_version`/
  `lineage_state`) plus idempotent stamping of historical rows | Additive columns;
  payloads are never rewritten |
| 166 | Template-instance `idempotency_key`/`assistant_kind`/`instantiation_state`/
  `upstream_refs`, template provenance on trial-run and shadow tables, and the
  `uidx_rgx_template_instance_idempotency` partial unique index | Older versions
  ignore the columns; after a rollback duplicate instantiation on one key can
  reappear (the old version never had the guarantee) |

### Upstream engine upgrade checklist (v1.0.0)

These are verified upstream behaviours, not compatibility switches:

| Item | Constraint | Field note |
|---|---|---|
| D1 | A new dataset must pass `embedding_model` explicitly | Otherwise the
  engine falls back to `"0"` and parsing fails with
  `500 model configuration unavailable` |
| D2 | Chat moved from `POST /chats/{id}/completions` to `POST /chat/completions` |
  The old endpoint returns 404; send `chat_id`/`session_id` |
| D3 | Compilation artifacts are limited to `wiki` | `page_index` is rejected
  at the entry point with `422/42261`, never silently empty |
| D4 | `/agents` mixes in `type=compilation_template_group` rows | Treating a
  template group as a canvas yields `103 permission to access the agent` |
| D5 | Pipeline datasets and document binding need `pipeline_version_id` |
  Versions come only from `POST /agents/{id}/publish` |

Order: upgrade the engine but keep the old instance rollback-ready, verify the
version band, then re-bind compilation and pipeline datasets (most sensitive to
artifact kind and version ids), and only then open business traffic. Engines
above the verified band always require a contract drill first.

#### Known upstream issues (open on v1.0.0, measured 2026-11-10)

U1–U5 are absorbed by platform behaviour, U6–U8 are tracked public issues,
U9–U11 are unverified surfaces, U12 is already fixed upstream. Before
troubleshooting, compare against `GET /api/v1/system/capabilities` — its
`version_issues` field is the machine-readable copy of this table.

| Item | Upstream defect | Symptom in the field | Action / caution |
|---|---|---|---|
| U1 | Structured compilation artifacts have no read surface (`artifacts` is fixed to `type_kwd=wiki_page`, retrieval returns only `compile_kwd=wiki`, yet the Compiler still writes entity/relation rows) | A run reports `state=completed` but no artifact can be bound and retrieval returns nothing | Trigger-time rejection (`422/42261`) suggests `wiki`; do **not** assert `page_index` artifacts exist on `1.0+` |
| U2 | The upstream web bundle sends `RSA(base64(password))` on login while the backend decrypts `RSA(plaintext)` | Browser login always returns `109` (engine's own UI) | Business login goes through RAGFlow-X; use API keys for the engine; the upstream UI needs a dist patch (deployment-side, not in the repo) |
| U3 | Omitting `embedding_model` on dataset creation makes the engine resolve `"0"` | Parsing fails with `500 model configuration unavailable` | The platform resolves the tenant default embedding model (5-minute TTL); switching the default model instance is enough, no restart |
| U4 | `/agents` mixes `type=compilation_template_group` rows into the list and counts them in `total` | Shadow sync imports phantom agents; the version surface returns `103` | Filtered by `type` and `total` corrected; subtract those rows when reconciling resource counts |
| U5 | Re-binding a document to another canvas without that canvas' published version | `102 pipeline version does not exist / does not belong to canvas X` | `POST /agents/{id}/publish` first, then send `pipeline_version_id`; document re-binding needs it too |
| U6 | #21095 — the retrieval `_source` projection drops `source_doc_ids` (PR #21106 unreviewed) | Citations cannot be mapped back to chunks on read-back | Citations are rehydrated from the completion-time snapshot with `ref_index`/`positions` persisted; never rely on the dropped field |
| U7 | #21097 — `ResponseHeaderTimeout` (20 min) and the 60 s stream timeout are hardcoded (merged to `main` via PR #21434, not in a release tag yet) | Long compilations and slow models get cut off upstream | Long work always runs through the RAGFlow-X scheduler (explicit deadline + per-run recovery); never judge failure by the engine timeout |
| U8 | #21096 — Windows build breakage in the agent sandbox (`statusFromExitCode`, PR #21099 unreviewed) | The upstream sandbox cannot be built on Windows | The upstream sandbox runs on Linux only; the platform governs and audits it, never re-implements it |
| U9–U11 | Graph-retrieval scoring form / Memory `team-me` permission model / agent `sandbox`·`skills` surfaces drift between builds | Behaviour differs across versions (`untested`) | Exercised in dry runs only; never used as a release gate on an unverified band; pilot small traffic after an upgrade |
| U12 | The singular search-app `/completion` path regressed with `50265` | Old path errors out | Fixed upstream; this product pins the plural `/searches/{id}/completions` — check contract tests before changing it |
| U13 | Dataset config read/write shapes disagree: writes reject `chunk_method` (`101 extra inputs are not permitted`), reads return only `parser_id`, a no-op `PUT` answers `102 no properties were modified` | Parse-type writes look ignored or rejected | Read/write go through `ParseMethod()` / `{parse_type:1, parser_id:…}`; message 102 is treated as idempotent success |
| U14 | `GET /agents/templates/{id}` returns 404 for templates that exist in the list; `components` is serialised as an object keyed by node id, not an array | Single-template detail looks missing; canvas validation fails on shape | A 404 on single-read falls back to the catalogue; the canvas whitelist accepts both shapes |
| U15 | With no embedding model registered, `POST /datasets` answers **HTTP 200 + `code:500 model configuration unavailable`** (runtime face of U3) | Dataset creation fails with an opaque engine message | Blocked locally with `400/40085` and an actionable hint; upstream business rejections map to 400 with the engine's text |

## Rollback

1. Stop the new version.
2. Restore or roll back the database only when the release procedure explicitly
   requires it. Do not guess schema compatibility.
3. Start the previous version.
4. Run health checks and audit-chain verification.
5. Re-check platform defaults after a rollback: `GET /api/v1/model-providers/defaults`
   (a resource unknown to an older build answers 403) and confirm scenario templates
   still list their published versions before reopening business traffic.
5. Record the rollback cause and evidence.

## v0.5.2 additions: default models and scenario templates

**Default model writes.** `PATCH /api/v1/model-providers/defaults` is a
consistency operation, so the failure codes are operational, not cosmetic:
`409/40912` means the expected value changed (refresh, then retry);
`409/40913` means another request holds the write lock (wait, never hammer it);
`502/50256` means the engine refused or failed the write (the upstream reason is
passed through — fix the model or instance and retry); `502/50257` means the
outcome is unknown because read-back disagreed with the expectation (read the
current default back and hand `operation_ref` to engineering, do not loop). A type showing `unset` blocks dataset creation locally with
`400/40085 no embedding model is registered in the engine…`. The compensator
coordinates through `rgx_model_default_recovery_claim` and never updates audit
rows — if audit verification reports a broken chain, follow the audit SOP
instead of "repairing" it through this path.

**Scenario templates.** Capability verdicts are
`satisfied / repairable / probe_limited / blocked` (priority
`blocked > probe_limited > repairable > satisfied`); `repairable` is a work item,
not an incident. Parse-profile repair has a fixed order: `403` → `409/40915`
(parsed content exists, send `rewrite_existing: true`) → `409/40917` (shared
dataset unacknowledged, send `acknowledge_shared_impact: true`) → approval →
write. `pushdown_enabled` is a platform-side execution policy and is never
pushed upstream. Instantiation accepts only the `Idempotency-Key` header
(`422/42231` when missing), answers `409/40916` while the key is executing, and
**resumes a failed run on the same key** — check `instantiation_state` via
`GET /api/v1/template-instances/{id}`. Imports always create drafts that must
clear the eval gate (`409/40918`) and signature checks (`400/40116`). Metrics
without an evidence source report as unavailable, never as zero.

## RAGFlow degradation

1. Confirm connectivity, API key, model availability, and document engine.
2. Check whether the failure is read-only, write-only, or full outage.
3. Disable new parse or write workflows if RAGFlow cannot accept them safely.
4. Preserve audit and job evidence.
5. Resume only after health and capability probes pass.

## Resource-sync failure

1. Inspect the latest run plan and result.
2. Classify conflicts:
   - `identity`: local shadow missing or inconsistent.
   - `mapping`: external tenant cannot map to a valid workspace.
   - `content`: both upstream and local content changed.
3. Resolve explicitly. Do not allow automatic overwrite.
4. Retry only after the conflicting state is corrected.
5. Confirm there is only one active run per source.

## Gateway quota incident

1. Check request logs, tenant quotas, and reservation state.
2. Confirm whether reservations are active, finalized, released, or stale.
3. Reconcile stale reservations using the worker or admin procedure.
4. Restore quota configuration only after understanding the root cause.
5. Verify `X-Quota-Remaining` behavior and audit records.

## Notification failure

1. Check provider credentials, endpoint reachability, and signing configuration.
2. Inspect `GET /api/v1/alerts/deliveries` and retry counts.
3. Trigger a controlled retry: `POST /api/v1/alerts/:id/deliveries/:channel/retry`.
4. Confirm deduplication and audit records.

## Compilation failure

1. Check the run state with `GET /api/v1/datasets/:id/compilation/runs`.
2. `422/42261` means the engine band does not support the requested artifact
   kind; on v1.0.0 use `wiki`.
3. Truncated artifacts usually mean the dataset compilation token budget is
   too small; validate on a small document before re-running the full set.
4. Contract-drill evidence stays mandatory when the engine version is outside
   the verified band.

## Failure-drill evidence

Record each drill with:

- Trigger time and operator
- Fault injection method
- Expected fail-safe behavior
- Actual result
- Recovery time
- Audit and alert evidence
- Follow-up actions

Do not declare production operational acceptance complete without archived evidence.
