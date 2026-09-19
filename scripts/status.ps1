#requires -Version 5.1

<#
.SYNOPSIS
    Show whether the ragflow-x API server is running.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/status.ps1
#>
[CmdletBinding()]
param([int]$Port = 9191)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root   = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$PidFile = Join-Path $Root 'run\server.pid'

$conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($conn) {
    Write-Host "RUNNING  port=$Port pid=$($conn.OwningProcess)"
    if (Test-Path -LiteralPath $PidFile) {
        Write-Host "pid_file=$(Get-Content -LiteralPath $PidFile -Raw)"
    }
    exit 0
}

Write-Host 'STOPPED  (no listener)'
exit 1
