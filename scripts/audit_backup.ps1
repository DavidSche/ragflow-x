#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

$databaseUrl = $env:DATABASE_URL
$backupDir = $env:RGX_AUDIT_BACKUP_DIR

if ([string]::IsNullOrWhiteSpace($databaseUrl)) {
    throw "DATABASE_URL is required"
}
if ([string]::IsNullOrWhiteSpace($backupDir)) {
    throw "RGX_AUDIT_BACKUP_DIR is required"
}

if (-not (Test-Path -LiteralPath $backupDir -PathType Container)) {
    New-Item -Path $backupDir -ItemType Directory -Force | Out-Null
}

$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$backupFile = Join-Path $backupDir "rgx-audit-log-$timestamp.dump"
$manifestFile = "$backupFile.sha256"

& pg_dump --format=custom --serializable-deferrable --table=rgx_audit_log --file=$backupFile $databaseUrl
if ($LASTEXITCODE -ne 0) {
    throw "pg_dump failed with exit code $LASTEXITCODE"
}
if (-not (Test-Path -LiteralPath $backupFile -PathType Leaf)) {
    throw "pg_dump did not create $backupFile"
}

$fileHash = (Get-FileHash -LiteralPath $backupFile -Algorithm SHA256).Hash.ToLowerInvariant()
$fileSize = (Get-Item -LiteralPath $backupFile).Length
$manifest = "sha256:$fileHash`nsize:$fileSize`ntimestamp:$timestamp`n"
Set-Content -LiteralPath $manifestFile -Value $manifest -Encoding utf8NoBOM

Write-Host "backup=$backupFile"
Write-Host "manifest=$manifestFile"
Write-Host "sha256=$fileHash"
