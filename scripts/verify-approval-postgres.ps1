param(
  [Parameter(Mandatory = $true)][string]$TestDsn,
  [string]$Psql = "psql"
)

$ErrorActionPreference = "Stop"

Write-Host "Running PostgreSQL approval organization-adaptability baseline..."
$env:RGX_TEST_POSTGRES_DSN = $TestDsn
go test ./internal/db -run TestPostgresApprovalOrganizationAdaptability -v
if ($LASTEXITCODE -ne 0) {
  throw "PostgreSQL baseline tests failed"
}

Write-Host "Verifying approval production indexes..."
& $psql $TestDsn -v ON_ERROR_STOP=1 -c @'
SELECT indexname, indexdef
FROM pg_indexes
WHERE indexname IN (
  'idx_rgx_approval_policy_tenant_object',
  'idx_rgx_approval_policy_match',
  'idx_rgx_approval_pending_due',
  'idx_rgx_approval_policy_pending',
  'uk_rgx_approval_step_actor',
  'idx_rgx_approval_delegation_active'
)
ORDER BY indexname;
SELECT relname AS table_name, n_live_tup AS rows
FROM pg_stat_user_tables
WHERE relname IN ('rgx_approval', 'rgx_approval_step', 'rgx_approval_step_actor', 'rgx_approval_delegation')
ORDER BY relname;
'@
if ($LASTEXITCODE -ne 0) {
  throw "PostgreSQL index verification failed"
}

Write-Host "PostgreSQL approval baseline completed."
