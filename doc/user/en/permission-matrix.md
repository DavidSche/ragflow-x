# Built-In Roles and Permission Matrix

> Version: v1.14 | Updated: 2026-10-08

The authoritative source is `BuiltinPermissionMatrix()` in
`internal/db/migrate.go`. The service reconciles built-in permissions from this
matrix at startup. Built-in roles do not use wildcard grants.

## Terminology

- Product semantics use **workspace**; database and API compatibility layers
  retain the technical identifier `tenant` / `tenant_id`.
- Platform is the control-plane tenant and does not own business resources.
- Permissions are granted as **resource × action**.
- Common actions: `read`, `manage`, `execute`, `append`, `delete:own`,
  `session:create`, and `test`.
- Governance actions include `governance.read`, `governance.manage`,
  `governance.read_sensitive`, `tenant.resource.*`, `tenant.connection.*`,
  `tenant.audit.read`, and `tenant.usage.read`.

## Built-in roles

| Role | Scope | Primary responsibility |
|---|---|---|
| `platform_admin` | Platform | Cross-workspace governance and platform configuration |
| `tenant_admin` | Workspace | Users, roles, teams, projects, datasets, models, keys, approvals |
| `operator` | Workspace | Knowledge operations, content quality, and task handling |
| `business_user` | Workspace | Ask questions and consume approved answers |
| `viewer` | Workspace | Read-only access |
| `team_admin` | Team | Manage the caller's own teams and bound projects/datasets |

## Key boundaries

- `platform_admin` permissions are explicitly enumerated. Cross-workspace
  operations also require governance authorization.
- `tenant_admin` manages resources inside one workspace but cannot implicitly
  write another workspace.
- `operator` can run knowledge operations but cannot manage system settings,
  roles, brands, model providers, API keys, or audit.
- `business_user` can ask questions and read/export answer snapshots within
  granted scope, but cannot manage knowledge operations or infrastructure.
- `viewer` is read-only.
- `team_admin` can manage only teams owned by the caller. Cross-team writes are
  not automatically allowed.

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
