# Audit-Chain Backup and Repair SOP

> Applies to: RAGFlow-X v0.5.3 (schema migration v166 and later) | Updated: 2026-11-13

## Scope

This SOP applies to RAGFlow-X PostgreSQL production deployments. The object
table is `rgx_audit_log`.

- Verify: `GET /api/v1/audit/verify`
- Repair: `POST /api/v1/audit/repair`

Repair requires a platform administrator. Tenant administrators with
`audit:read` cannot call it. Repair re-anchors existing records for the named
tenant; it does not restore deleted records.

## Principles

1. Verify before repairing.
2. Take an independent physical backup before any repair.
3. Repair only confirmed abnormal tenants.
4. Never hand-edit `prev_hash`, `hash`, `seq`, or `at` through SQL.
5. Re-verify after repair and require `valid=true`.

## Daily backup

### Environment variables

Export in the operations shell:

- `DATABASE_URL`: PostgreSQL connection URL using a least-privilege operations
  account.
- `RGX_AUDIT_BACKUP_DIR`: output directory on independent storage.

### Run backup

Windows:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/audit_backup.ps1
```

Linux/macOS:

```bash
./scripts/audit_backup.sh
```

The script writes a PostgreSQL custom-format dump and a SHA-256 manifest. The
manifest records size, checksum, and timestamp, but not credentials.

### Retention

| Environment | Minimum retention |
|---|---|
| Production | 180 days |
| Staging | 30 days |

Before deleting an expired backup, confirm that an off-site or object-storage
copy exists.

## Incident procedure

1. **Capture evidence.** Call verify with an account that has `audit:read`:

   ```powershell
   Invoke-RestMethod -Method Get -Uri "http://localhost:9191/api/v1/audit/verify" -Headers @{ Authorization = "Bearer <access-token>" }
   ```

   If `valid=false`, do not delete or rewrite audit data.

2. **Back up immediately.** If backup fails, stop and involve the database
   administrator.

3. **Identify the tenant.** Confirm the tenant from business changes, approvals,
   and the error timeline. Do not batch-guess tenants.

4. **Repair as platform administrator.**

   ```powershell
   $body = @{ tenant_id = "<tenant-id>" } | ConvertTo-Json
   Invoke-RestMethod -Method Post -Uri "http://localhost:9191/api/v1/audit/repair" -ContentType "application/json" -Headers @{ Authorization = "Bearer <platform-admin-token>" } -Body $body
   ```

5. **Archive evidence.**

   - Pre-repair dump and manifest
   - Pre-repair `valid=false` response
   - Post-repair `valid=true` response
   - Repair ticket ID

To inspect the original chain, restore the backup to an isolated PostgreSQL
instance.

## Acceptance

- The backup script fails fast when required variables are empty.
- The dump uses PostgreSQL custom format.
- The manifest checksum verifies successfully.
- Repair is preceded by a backup and followed by `valid=true`.
- Every repair creates an `audit.repair` audit record.
