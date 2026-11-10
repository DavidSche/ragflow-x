# Built-In Roles and Permission Matrix

> Version: v1.16 | Updated: 2026-11-10

The authoritative source is `BuiltinPermissionMatrix()` in
`internal/db/migrate.go`. The service reconciles built-in permissions from this
matrix at startup. Built-in roles do not use wildcard grants.

## Terminology

- Product semantics use **workspace**; database and API compatibility layers
  retain the technical identifier `tenant` / `tenant_id`.
- Platform is the control-plane tenant and does not own business resources.
- Permissions are granted as **resource × action**.
- Implication: `manage`/`execute`/`append`/`test`/`session:create`/`delete:own` **imply `read` on the same
  resource** at authorization time, for built-in and custom roles alike; `deny` rules are literal
  (`deny execute` does not remove read).
- Delegation: a custom role may only be granted permissions the grantor already holds (otherwise
  403/40302). The catalog is the union of the built-in matrix and contains platform-only actions, so
  "in catalog" does not mean "delegable".
- Common actions: `read`, `manage`, `execute`, `append`, `delete:own`,
  `session:create`, and `test`.
- Governance actions include `governance.read`, `governance.manage`,
  `governance.read_sensitive`, `tenant.resource.*`, `tenant.connection.*`,
  `tenant.audit.read`, and `tenant.usage.read`.

## Additional governance and knowledge resources

In addition to the shared resources (`dataset`, `document`, `assistant`, `chat`,
`search-app`, `agent`, `memory`, …), the matrix exposes governance, memory and
compilation sub-resources:

- `document-governance` (document governance: ownership projection, attribution,
  approval and recovery runs; `execute` granted to platform/workspace admins),
  `compilation` (knowledge compilation runs), `knowledge-profile` (knowledge
  profile), `context-decision` (context-decision read, granted to all business
  roles), `query-template` (platform/workspace admins), and `user-clearance`
  (sensitive-content minimal authorization; `read` includes team admins).
- `release-governance` also carries `execute` (evaluation runs and case-version
  triggers) for platform/workspace admins, operators and team admins.
- `connector` (data-source connector bindings and sync runs): `read` and
  `manage` for platform admins and workspace admins; no other built-in role may
  list, preview, sync or rotate connector credentials (doc/225).
- The memory subsystem is split into `memory`, `memory-content`,
  `memory-forget`, `memory-operation`, `memory-policy` and `memory-write`;
  `business_user` can read/write/forget memory content related to themselves but
  cannot manage memory policies. `memory-write` and `memory-forget` are
  action-only resources: they intentionally carry no `read` grant, because there
  is no enumerable "list of writes/forgets" surface (results are read through
  `memory-operation`). Team admins may forget (they are members first), but
  operators deliberately may **not**: an operator must never forget a user's
  memory on their behalf (doc/137 §10.2/§10.3 — withdrawals belong to the owner,
  and governance deletion requires `manage` plus an approval).

## Built-in roles

| Role | Scope | Primary responsibility |
|---|---|---|
| `platform_admin` | Platform | Cross-workspace governance and platform configuration |
| `tenant_admin` | Workspace | Users, roles, teams, projects, datasets, models, keys, approvals |
| `operator` | Workspace | Knowledge operations, content quality, and task handling |
| `business_user` | Workspace | Ask questions and consume approved answers |
| `viewer` | Workspace | Read-only access |
| `team_admin` | Team | `business_user` capabilities plus management of the caller's own teams and bound projects/datasets |

## Key boundaries

- `platform_admin` permissions are explicitly enumerated. Cross-workspace
  operations also require governance authorization.
- The four fine-grained platform actions became executable in doc/248, and each
  keeps the matching `governance.*` permission as a parallel path so existing
  roles behave identically: `tenant.audit.read` reads audits and audit-chain
  anchors across workspaces, `tenant.usage.read` reads usage and usage exports
  across workspaces, `tenant.resource.manage` edits another workspace's
  settings, and `tenant.resource.delete` deletes a workspace (kept separate from
  editing so a platform operator who may change settings cannot delete). Each
  one is a narrower path to an already-existing capability, never a wider one:
  holding `tenant.audit.read` grants no usage or generic governance read.
- Workspace writes are gated in two layers: the **admin surface** (route-level
  `manage tenant`, i.e. may the caller reach the workspace admin endpoints at
  all) and the **operation** (the fine-grained action, i.e. what may they do
  there). `tenant.resource.manage` covers creating and editing a workspace,
  `tenant.resource.delete` covers deleting only, so a delegated platform
  operator gets exactly the combination they were granted. The audit surfaces
  act as one evidence capability: `tenant.audit.read` reads the audit log, the
  audit-chain anchors, and the audit export across workspaces.
- `tenant_admin` manages resources inside one workspace but cannot implicitly
  write another workspace.
- `operator` can run knowledge operations but cannot manage system settings,
  roles, brands, model providers, API keys, or audit.
- `business_user` can ask questions and read/export answer snapshots within
  granted scope, can browse the scenario-template catalog
  (`scenario-template:read` — the only non-403 exit from the empty conversation
  state), can write/forget memory content related to themselves, can read
  practice-run results (`trial-run:read`), but cannot create templates, cannot
  start a practice run (`trial-run:execute`), and cannot manage knowledge
  operations or infrastructure.
- Read access is monotonic across the built-in roles. Since doc/245 there is no
  escape hatch left: `viewer`'s reads are a strict subset of `business_user`'s
  reads (`TestViewerOnlyReadsAreGone`), so a read-only role can never out-read
  the role that does the work. A new resource must be readable by the working
  role first; a rationale no longer substitutes for convergence.
- `viewer` is the **read-only slice of `business_user`** (doc/245): it holds
  `read` on exactly the 14 business surfaces (assistant / chat / search-app /
  agent / dataset / document / logical-document / memory / memory-policy /
  memory-operation / context-decision / scenario-template / trial-run / user)
  and nothing else. It used to also read tenant-wide operational and governance
  ledgers — task queues, cost and dashboard aggregates, `knowledge-ops`,
  `approval`, `eval-set`, `release-governance`, plus the policy/configuration
  resources — which meant a guest could see more of the workspace than a paying
  member. That was the mirror image of the doc/242 inversion and was removed in
  migration 161.
- `team_admin` is `business_user` plus team-scoped management (doc/243): it holds
  the full member surface (`chat` / `search-app` / `agent` / `assistant` /
  `memory` read+execute, including `agent:session:create`) so a team
  administrator can enter the product and supervise its own team's assistants,
  and can browse the template catalog (`scenario-template:read`, which is how the
  product is entered) and can start practice runs on its own templates
  (`trial-run:execute`, doc/244). Practice runs are a **separate resource**
  precisely so this does not require `eval-set:execute`, which also drives
  `POST /eval-cases/:id/revalidate` (tenant-scoped evidence recompute, no team
  ABAC — doc/129 §1178,
  `TestEvalSetExecutePermissionsMatchRevalidationContract`). `manage` stays
  narrowed to owned teams, and cross-team
  writes are not automatically allowed. Team owners are auto-granted this role,
  which is why it must never be weaker than `business_user`.
- The built-in role lattice is declared in `internal/db/role_lattice.go` and
  enforced by `TestRoleSupersetRelationsHoldOnMatrix`: every ordered role pair is
  either a declared superset relation or a declared incomparability with a
  rationale, and `manage`/`execute`/`append`/`test`/`session:create` always
  implies `read` (except for the `memory-write` / `memory-forget` action-only
  resources). `trial-run` carries only `read` and `execute`: a practice run has
  no independent lifecycle to administer (`scenario-template:manage` still gates
  authoring the template it exercises), which keeps the resource from becoming a
  second way to delete run history.

## Custom-role inheritance

Each `(resource, action)` can be configured as:

| State | Meaning |
|---|---|
| `inherit` | Use the nearest explicit rule from the parent chain; deny if none matches |
| `allow` | Explicitly allow |
| `deny` | Explicitly deny; this overrides an inherited allow |

Built-in roles have no parent role. Historical wildcard permissions are removed
during startup reconciliation.

## Navigation mapping

The UI derives navigation from effective permissions returned by `/auth/me`.
Unmapped or unauthorized resources are hidden. The audit view appears only when
the effective permission set includes `audit:read`.

## Reconciliation and compatibility

Migrations 128 and 129 backfill newer built-in permissions into databases that
had already reached the latest schema version. Startup reconciliation replaces
the built-in matrix with current explicit grants and prevents stale wildcard
permissions from remaining active.

As of v0.5.0 (schema 162) the matrix holds **424 rows across 59 resources with
zero read-privilege inversions**. `viewer` keeps read-only access to 14 business
resources, `trial-run` has been its own resource (`read`/`execute`) since
migration 160, and the superset edges `business_user ⊇ viewer` and
`team_admin ⊇ business_user` are declared in `role_lattice.go` and guarded by
tests. `doc/NNN` identifiers in this document refer to engineering-internal
design records that are not part of this repository.
