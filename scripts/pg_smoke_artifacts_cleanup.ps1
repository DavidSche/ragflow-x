param(
  [Parameter(Mandatory = $true)][string]$Dsn,
  [string]$Psql = "psql",
  [switch]$Execute
)

# 冒烟副作用清理（doc/239 R-04 = A）
# 默认 dry-run：只打印将执行的语句，不改数据。加 -Execute 才真正提交。
#   .\scripts\pg_smoke_artifacts_cleanup.ps1 -Dsn "host=... dbname=ragflow-x sslmode=disable"
#   .\scripts\pg_smoke_artifacts_cleanup.ps1 -Dsn "..." -Execute

$ErrorActionPreference = "Stop"
$sqlFile = Join-Path $PSScriptRoot "postgres/smoke_artifacts_cleanup.sql"
if (-not (Test-Path $sqlFile)) { throw "cleanup SQL not found: $sqlFile" }

if (-not $Execute) {
  Write-Host "[dry-run] SQL: $sqlFile"
  Write-Host "[dry-run] 预检语句（不改数据）："
  & $psql $Dsn -v ON_ERROR_STOP=1 -c @'
SELECT 'dataset_link' AS t, count(*) FROM rgx_dataset_link WHERE id IN ('916beee8d35a4596aba78141c9c46383','c34smokedataset0000000000000001')
UNION ALL SELECT 'chat', count(*) FROM rgx_chat WHERE id = '0550161dd4df45f08a21391b673a1c44'
UNION ALL SELECT 'trial_run', count(*) FROM rgx_trial_run WHERE id = '09506993c97a44729f95c16c539197ed'
UNION ALL SELECT 'case_result', count(*) FROM rgx_trial_case_result WHERE run_id = '09506993c97a44729f95c16c539197ed';
'@
  if ($LASTEXITCODE -ne 0) { throw "pre-check failed" }
  Write-Host "[dry-run] 确认无误后加 -Execute 重跑。"
  return
}

Write-Host "Executing smoke artifact cleanup on target database..."
& $psql $Dsn -v ON_ERROR_STOP=1 -f $sqlFile
if ($LASTEXITCODE -ne 0) { throw "cleanup failed (transaction rolled back)" }

Write-Host "Cleanup done. 复核建议："
Write-Host "  - GET /api/v1/audit/verify 应为 true（审计行未删）"
Write-Host "  - GET /api/v1/scenario-template-catalog 仍返回本工作区模板"
Write-Host "  - RAGFlow 侧空库 fb92ee2929244752b30ca09f1d223a51 另经 API 删除"
