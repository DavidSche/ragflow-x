#requires -Version 5.1

<#
.SYNOPSIS
    Start the ragflow-x API server as a detached background process.

.DESCRIPTION
    The default configuration is the project config (config/config.yaml).
    For local development (SQLite + mock RAGFlow) pass -Local, which selects
    config/config.local.yaml. Any YAML can be supplied with -Config.

    The script:
      * builds the binary first when -Build is given
      * writes a PID file (run/server.pid) so stop.ps1 can stop the right process
      * app logs go to date-rotated files per config `logging.output`
        (default ./logs/ragflow-x.log -> logs/ragflow-x-<date>.log, with
        max_size_mb / max_backups retention) — NOT server.out.log
      * streams stray stdout/stderr to logs/server.out.log / logs/server.err.log
      * waits for the configured port to accept connections and prints the URL
      * is idempotent: starting an already-running instance is a no-op

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/start.ps1 -Local
    powershell -ExecutionPolicy Bypass -File scripts/start.ps1 -Config config\prod.yaml -Build

.PARAMETER Config
    Path to the YAML config file, relative to the repository root.

.PARAMETER Local
    Use config/config.local.yaml (SQLite + mock RAGFlow) for local development.

.PARAMETER Binary
    Path to the server binary. Defaults to bin/ragflow-x (or bin/ragflow-x.exe).
    Rebuilt with -Build.

.PARAMETER Build
    Run `go build` before starting.

.PARAMETER Force
    Stop whatever is currently listening on the port first (use with care).

.PARAMETER Port
    Port to wait for after startup (default 9191).

.PARAMETER TimeoutSec
    How long to wait for the server to come up (default 30s).
#>
[CmdletBinding()]
param(
    [string]$Config,
    [switch]$Local,
    [string]$Binary,
    [switch]$Build,
    [switch]$Force,
    [int]$Port = 9191,
    [int]$TimeoutSec = 30
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Root   = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$RunDir = Join-Path $Root 'run'
$LogDir = Join-Path $Root 'logs'
$PidFile = Join-Path $RunDir 'server.pid'
$OutLog  = Join-Path $LogDir 'server.out.log'
$ErrLog  = Join-Path $LogDir 'server.err.log'

function Resolve-ProjectPath([string]$Path) {
    if (-not $Path) { return '' }
    if ([System.IO.Path]::IsPathRooted($Path)) { return $Path }
    return Join-Path $Root $Path
}

function Test-Port([int]$port) {
    return [bool](Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue)
}

function Get-AlivePid {
    if (-not (Test-Path -LiteralPath $PidFile)) { return $null }
    $raw = (Get-Content -LiteralPath $PidFile -Raw).Trim()
    if (-not $raw) { return $null }
    $proc = Get-Process -Id $raw -ErrorAction SilentlyContinue
    if (-not $proc) { return $null }
    return [int]$raw
}

function Stop-Pid([int]$id) {
    Stop-Process -Id $id -ErrorAction SilentlyContinue
    # Give the process a short grace window for a clean shutdown before forcing.
    Start-Sleep -Milliseconds 800
    if (Get-Process -Id $id -ErrorAction SilentlyContinue) {
        Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
    }
}

# Resolve the config file.
if ($Config -and $Local) { throw 'Choose either -Config or -Local, not both.' }
if (-not $Config) { $Config = if ($Local) { 'config\config.local.yaml' } else { 'config\config.yaml' } }
$ConfigPath = Resolve-ProjectPath $Config
if (-not (Test-Path -LiteralPath $ConfigPath)) { throw "Config file not found: $ConfigPath" }

# Resolve (and optionally build) the binary.
$IsWindowsHost = $env:OS -like '*Windows*'
if ($Build) {
	Push-Location $Root
	if (-not $Binary) { $Binary = if ($IsWindowsHost) { 'bin\ragflow-x.exe' } else { 'bin\ragflow-x' } }
	try { go build -o $Binary ./cmd/server/ } finally { Pop-Location }
	if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }
}
if (-not $Binary) {
	if ($IsWindowsHost) {
		$exePath = Resolve-ProjectPath 'bin\ragflow-x.exe'
		$barePath = Resolve-ProjectPath 'bin\ragflow-x'
		if ((Test-Path -LiteralPath $exePath) -and (Test-Path -LiteralPath $barePath)) {
			$exeTime = (Get-Item -LiteralPath $exePath).LastWriteTimeUtc
			$bareTime = (Get-Item -LiteralPath $barePath).LastWriteTimeUtc
			$Binary = if ($exeTime -ge $bareTime) { 'bin\ragflow-x.exe' } else { 'bin\ragflow-x' }
		} elseif (Test-Path -LiteralPath $exePath) {
			$Binary = 'bin\ragflow-x.exe'
		} else {
			$Binary = 'bin\ragflow-x'
		}
	} else {
		$Binary = 'bin\ragflow-x'
	}
}
$BinaryPath = Resolve-ProjectPath $Binary
if (-not (Test-Path -LiteralPath $BinaryPath)) { throw "Binary not found: $BinaryPath. Pass -Build to compile it." }
if (-not (Test-Path -LiteralPath $BinaryPath)) { $BinaryPath = $BinaryPath + '.exe' }
$BinaryPath = (Resolve-Path -LiteralPath $BinaryPath).Path

# Refuse to double-start our own instance; handle foreign occupiers.
if (Test-Port $Port) {
    $alive = Get-AlivePid
    if ($alive) {
        Write-Host "ragflow-x already running (pid $alive) on port $Port."
        exit 0
    }
    if (-not $Force) {
        throw "Port $Port is already in use by another process. Re-run with -Force to take it over."
    }
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($conn) { Stop-Pid $conn.OwningProcess }
    Start-Sleep -Milliseconds 500
}

# Prepare run/log directories.
New-Item -ItemType Directory -Force -Path $RunDir, $LogDir | Out-Null

Write-Host "Starting ragflow-x from $BinaryPath"
Write-Host "Config: $ConfigPath"

$proc = Start-Process -FilePath $BinaryPath `
    -ArgumentList @('-config', $ConfigPath) `
    -WorkingDirectory $Root `
    -WindowStyle Hidden `
    -RedirectStandardOutput $OutLog `
    -RedirectStandardError $ErrLog `
    -PassThru

$proc.Id | Set-Content -LiteralPath $PidFile -Encoding ascii
Write-Host "Started pid $($proc.Id); waiting for port $Port ..."

$deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSec)
while ([DateTime]::UtcNow -lt $deadline) {
    if (Test-Port $Port) {
        Write-Host "OK  ragflow-x is up: http://localhost:$Port"
        Write-Host "    config: $ConfigPath"
        Write-Host "    logs:   logs\ragflow-x-<date>.log (app rotation, config logging.output)"
        Write-Host "            stdout/stderr fallback: $OutLog / $ErrLog"
        Write-Host "    stop:   powershell -File scripts/stop.ps1"
        exit 0
    }
    if (-not (Get-Process -Id $proc.Id -ErrorAction SilentlyContinue)) {
        break
    }
    Start-Sleep -Milliseconds 500
}

Write-Error "Server did not become ready on port $Port within ${TimeoutSec}s."
if (Test-Path -LiteralPath $ErrLog) { Get-Content -LiteralPath $ErrLog -Tail 30 }
Remove-Item -LiteralPath $PidFile -ErrorAction SilentlyContinue
exit 1
