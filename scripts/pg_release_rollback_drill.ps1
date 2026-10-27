#!/usr/bin/env pwsh
$ErrorActionPreference = "Stop"

$databaseDsn = $env:RGX_PG_RELEASE_DSN
$backupDir = $env:RGX_PG_RELEASE_BACKUP_DIR
$tablesValue = $env:RGX_PG_RELEASE_TABLES
if ([string]::IsNullOrWhiteSpace($tablesValue)) {
    $tablesValue = "rgx_tenant,rgx_user,rgx_schema_version"
}

if ([string]::IsNullOrWhiteSpace($databaseDsn)) {
    throw "RGX_PG_RELEASE_DSN is required"
}
if ([string]::IsNullOrWhiteSpace($backupDir)) {
    throw "RGX_PG_RELEASE_BACKUP_DIR is required"
}

function Resolve-PgTool([string]$environmentVariable, [string]$defaultName) {
    $configured = [Environment]::GetEnvironmentVariable($environmentVariable)
    if ([string]::IsNullOrWhiteSpace($configured)) {
        return $defaultName
    }
    return $configured
}

function Swap-DatabaseName([string]$dsn, [string]$databaseName) {
    $parsed = $null
    if ([Uri]::TryCreate($dsn, [UriKind]::Absolute, [ref]$parsed) -and $parsed.Scheme.StartsWith("postgres")) {
        $builder = [UriBuilder]::new($parsed)
        $builder.Path = "/" + $databaseName
        return $builder.Uri.ToString()
    }

    $fields = $dsn -split "\s+" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    $matched = $false
    for ($index = 0; $index -lt $fields.Count; $index++) {
        if ($fields[$index] -like "dbname=*") {
            $fields[$index] = "dbname=$databaseName"
            $matched = $true
        }
    }
    if (-not $matched) {
        $fields += "dbname=$databaseName"
    }
    return $fields -join " "
}

function Invoke-PgCommand([string]$toolPath, [string[]]$arguments) {
    $output = & $toolPath @arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$([IO.Path]::GetFileName($toolPath)) failed with exit code $LASTEXITCODE"
    }
    return $output
}

function Get-TableRowCount([string]$toolPath, [string]$dsn, [string]$table) {
    if ($table -notmatch "^[A-Za-z_][A-Za-z0-9_]*$") {
        throw "invalid table name: $table"
    }
    $output = Invoke-PgCommand $toolPath @(
        "--no-password",
        "--tuples-only",
        "--no-align",
        "--dbname=$dsn",
        "--command=SELECT COUNT(*) FROM $table"
    )
    return [int64]($output | Select-Object -Last 1)
}

$tables = $tablesValue -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ }
if (-not $tables) {
    throw "RGX_PG_RELEASE_TABLES resolved to no tables"
}

$dumpTool = Resolve-PgTool "RGX_PG_RELEASE_PG_DUMP_BIN" "pg_dump"
$restoreTool = Resolve-PgTool "RGX_PG_RELEASE_PG_RESTORE_BIN" "pg_restore"
$psqlTool = Resolve-PgTool "RGX_PG_RELEASE_PSQL_BIN" "psql"
$maintenanceDsn = Swap-DatabaseName $databaseDsn "postgres"

New-Item -Path $backupDir -ItemType Directory -Force | Out-Null
$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$cloneName = "rgx_release_rollback_$($timestamp.ToLower())"
$backupFile = Join-Path $backupDir "rgx-release-baseline-$timestamp.dump"
$cloneDsn = Swap-DatabaseName $databaseDsn $cloneName
$cloneCreated = $false
$keepBackup = $env:RGX_PG_RELEASE_KEEP_BACKUP -notin @($null, "", "0", "false")

try {
    foreach ($table in $tables) {
        $sourceCount = Get-TableRowCount $psqlTool $databaseDsn $table
        Write-Host "source table=$table rows=$sourceCount"
    }

    Invoke-PgCommand $dumpTool @(
        "--format=custom",
        "--serializable-deferrable",
        "--file=$backupFile",
        $databaseDsn
    ) | Out-Null
    $backupInfo = Get-Item -LiteralPath $backupFile
    if ($backupInfo.Length -le 0) {
        throw "pg_dump created an empty backup"
    }
    Invoke-PgCommand $restoreTool @("--list", $backupFile) | Out-Null
    Write-Host "backup=$backupFile size=$($backupInfo.Length)"

    Invoke-PgCommand $psqlTool @(
        "--no-password",
        "--dbname=$maintenanceDsn",
        "--command=CREATE DATABASE $cloneName TEMPLATE template0 ENCODING 'UTF8'"
    ) | Out-Null
    $cloneCreated = $true

    Invoke-PgCommand $restoreTool @(
        "--exit-on-error",
        "--clean",
        "--if-exists",
        "--no-owner",
        "--no-privileges",
        "--dbname=$cloneDsn",
        $backupFile
    ) | Out-Null

    foreach ($table in $tables) {
        $sourceCount = Get-TableRowCount $psqlTool $databaseDsn $table
        $restoredCount = Get-TableRowCount $psqlTool $cloneDsn $table
        if ($sourceCount -ne $restoredCount) {
            throw "row count mismatch table=$table source=$sourceCount restored=$restoredCount"
        }
        Write-Host "restored table=$table rows=$restoredCount"
    }

    Write-Host "release rollback drill PASS"
}
finally {
    if ($cloneCreated) {
        $null = Invoke-PgCommand $psqlTool @(
            "--no-password",
            "--dbname=$maintenanceDsn",
            "--command=DROP DATABASE IF EXISTS $cloneName WITH (FORCE)"
        ) 2>$null
    }
    if (-not $keepBackup -and (Test-Path -LiteralPath $backupFile -PathType Leaf)) {
        Remove-Item -LiteralPath $backupFile -Force
    }
}
