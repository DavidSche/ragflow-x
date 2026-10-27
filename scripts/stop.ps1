#requires -Version 5.1

<#
.SYNOPSIS
    Stop the ragflow-x API server started by scripts/start.ps1.

.DESCRIPTION
    Stops the process recorded in run/server.pid (PID + simple PID ports) and
    removes the PID file. With -Force it also takes down any process that is
    currently listening on the port even if the PID file is missing.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/stop.ps1
    powershell -ExecutionPolicy Bypass -File scripts/stop.ps1 -Force
#>
[CmdletBinding()]
param(
    [int]$Port = 9191,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root   = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$PidFile = Join-Path $Root 'run\server.pid'

function Stop-Pid([int]$id) {
    Stop-Process -Id $id -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 800
    if (Get-Process -Id $id -ErrorAction SilentlyContinue) {
        Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
    }
}

$stopped = $false

if (Test-Path -LiteralPath $PidFile) {
    $raw = (Get-Content -LiteralPath $PidFile -Raw).Trim()
    if ($raw) {
        if (Get-Process -Id $raw -ErrorAction SilentlyContinue) {
            Write-Host "Stopping ragflow-x (pid $raw)."
            Stop-Pid $raw
            $stopped = $true
        } else {
            Write-Host "Pid $raw is no longer running; removing stale PID file."
        }
    }
    Remove-Item -LiteralPath $PidFile -ErrorAction SilentlyContinue
}

if (-not $stopped) {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($conn) {
        if ($Force) {
            Write-Host "Port $Port occupied by pid $($conn.OwningProcess); force-stopping."
            Stop-Pid $conn.OwningProcess
        } else {
            Write-Warning "Port $Port is in use by pid $($conn.OwningProcess) but no PID file exists. Re-run with -Force to stop it."
        }
    } else {
        Write-Host "ragflow-x is not running (nothing on port $Port)."
    }
}

Write-Host 'Done.'
exit 0
