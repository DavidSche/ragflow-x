<#
.SYNOPSIS
  ragflow-x launcher for Windows: unpack, then .\start.cmd (or start.ps1).

.DESCRIPTION
  Mirror of scripts/release/start.sh: creates the directories the server expects,
  says which mode you are entering (first-run wizard vs. configured), starts the
  process, and waits until the port answers before printing the URL. The release
  archive ships one self-contained ragflow-x.exe (API + web UI in one file).

.EXAMPLE
  .\start.cmd                    # foreground, default config discovery
  .\start.cmd -Daemon            # background, pid file + console log under logs\
  .\start.cmd -Sqlite            # first run on SQLite under .\data
  .\start.cmd -Config conf.yaml -Port 9191
  .\start.cmd stop
  .\start.cmd status
#>
[CmdletBinding()]
param(
	[Parameter(Position = 0)]
	[ValidateSet('run', 'stop', 'status', '')]
	[string]$Action = 'run',

	[switch]$Daemon,
	[switch]$Sqlite,
	[string]$Config,
	[int]$Port,
	[switch]$NoWait,
	[switch]$Help
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -LiteralPath $root

$pidFile = Join-Path $root 'logs\ragflow-x.pid'
$portFile = Join-Path $root 'logs\ragflow-x.port'
$consoleLog = Join-Path $root 'logs\console.out'
$defaultPort = 9191

function Show-Usage {
	@'
ragflow-x launcher (Windows)

usage: start.cmd [action] [options] [-- extra server flags]

actions:
  (none)          run in the foreground (Ctrl-C stops it)
  stop            stop the background process started with -Daemon
  status          report whether it runs and whether the port answers

options:
  -Daemon         run in the background (pid: logs\ragflow-x.pid, output: logs\console.out)
  -Sqlite         first run on SQLite: data\ragflow-x.db, no PostgreSQL needed
  -Config PATH    use an explicit config file (same as the server's -config)
  -Port N         override the listen port (exported as RGX_PORT)
  -NoWait         do not wait for the HTTP port before returning (daemon mode)
  -Help           this text

examples:
  start.cmd -Sqlite -Daemon     run the product in one shot, then open the URL
  start.cmd                     foreground, reads config\config.yaml if present
'@ | Write-Host
}

if ($Help) { Show-Usage; exit 0 }

$bin = $env:RAGFLOW_X_BIN
if (-not $bin) {
	foreach ($candidate in @('ragflow-x.exe', 'ragflow-x', '..\ragflow-x.exe')) {
		$p = Join-Path $root $candidate
		if (Test-Path -LiteralPath $p) { $bin = $p; break }
	}
}
if (-not $bin -or -not (Test-Path -LiteralPath $bin)) {
	Write-Error "ragflow-x.exe not found next to $root (unpack the archive into its own directory)"
	exit 1
}
$bin = (Resolve-Path -LiteralPath $bin).Path

function Get-ResolvedPort {
	if ($Port -gt 0) { return $Port }
	if ($env:RGX_PORT) { return [int]$env:RGX_PORT }
	if (Test-Path -LiteralPath $portFile) {
		$saved = (Get-Content -LiteralPath $portFile -ErrorAction SilentlyContinue | Select-Object -First 1)
		if ($saved -match '^\d+$') { return [int]$saved }
	}
	$cfg = $Config
	if (-not $cfg) {
		foreach ($candidate in @('config\config.yaml', 'config.yaml')) {
			$p = Join-Path $root $candidate
			if (Test-Path -LiteralPath $p) { $cfg = $p; break }
		}
	}
	if ($cfg -and (Test-Path -LiteralPath $cfg)) {
		$inServer = $false
		foreach ($line in Get-Content -LiteralPath $cfg) {
			if ($line -match '^\s*server:\s*$') { $inServer = $true; continue }
			if ($inServer -and $line -match '^\S') { $inServer = $false }
			if ($inServer -and $line -match '^\s*port:\s*(\d+)') { return [int]$matches[1] }
		}
	}
	return $defaultPort
}

function Test-PortOpen([int]$p) {
	try {
		$client = [System.Net.Sockets.TcpClient]::new()
		$task = $client.ConnectAsync('127.0.0.1', $p)
		if ($task.Wait(400) -and $client.Connected) { $client.Close(); return $true }
		$client.Close()
	} catch { }
	return $false
}

function Test-PidAlive {
	if (-not (Test-Path -LiteralPath $pidFile)) { return $false }
	$procId = (Get-Content -LiteralPath $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
	if (-not $procId) { return $false }
	return $null -ne (Get-Process -Id ([int]$procId) -ErrorAction SilentlyContinue)
}

$listenPort = Get-ResolvedPort

if ($Action -eq 'stop') {
	if (Test-PidAlive) {
		$procId = [int](Get-Content -LiteralPath $pidFile | Select-Object -First 1)
		Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
		Remove-Item -LiteralPath $pidFile, $portFile -ErrorAction SilentlyContinue
		Write-Host "stopped (pid $procId)"
	} else {
		Remove-Item -LiteralPath $pidFile, $portFile -ErrorAction SilentlyContinue
		Write-Host 'not running'
	}
	exit 0
}

if ($Action -eq 'status') {
	if (Test-PidAlive) { Write-Host "running (pid $(Get-Content -LiteralPath $pidFile | Select-Object -First 1), port $listenPort)" }
	else { Write-Host 'not running (no live pid in logs\ragflow-x.pid)' }
	if (Test-PortOpen $listenPort) { Write-Host "port $listenPort`: accepting connections -> http://localhost:$listenPort/" }
	else { Write-Host "port $listenPort`: closed" }
	exit 0
}

New-Item -ItemType Directory -Force -Path (Join-Path $root 'logs') | Out-Null
if ($Sqlite) { New-Item -ItemType Directory -Force -Path (Join-Path $root 'data') | Out-Null }

$haveConfig = $false
if ($Config) {
	if (-not (Test-Path -LiteralPath $Config)) { Write-Error "config file not found: $Config"; exit 1 }
	$haveConfig = $true
} elseif ((Test-Path -LiteralPath (Join-Path $root 'config\config.yaml')) -or (Test-Path -LiteralPath (Join-Path $root 'config.yaml'))) {
	$haveConfig = $true
}

if ($Sqlite) {
	$env:RGX_DB_DRIVER = 'sqlite'
	$env:RGX_DB_DSN = "file:$root\data\ragflow-x.db?cache=shared"
}
if (-not $haveConfig) {
	# Default output is "both" (console + rotating file); the built-in file path is
	# relative to the caller's CWD, so pin it next to the launcher instead.
	$env:RGX_LOG_FILE = Join-Path $root 'logs\ragflow-x.log'
}
if ($Port -gt 0) { $env:RGX_PORT = "$Port" }

if (-not $haveConfig) {
	Write-Host 'no config file found: starting on the built-in defaults.'
	Write-Host "  - the web UI will offer the first-run setup wizard (http://localhost:$listenPort/)"
	Write-Host '  - to configure PostgreSQL/RAGFlow by hand: copy config\config.example.yaml config\config.yaml'
	if (-not $Sqlite) { Write-Host '  - or try SQLite first: start.cmd -Sqlite' }
} else {
	Write-Host "using config: $($(if ($Config) { $Config } else { 'config\config.yaml' }))"
}

$serverArgs = @()
if ($Config) { $serverArgs += @('-config', $Config) }

if (Test-PortOpen $listenPort) {
	if (Test-PidAlive) {
		Write-Error "already running (pid $(Get-Content -LiteralPath $pidFile | Select-Object -First 1)) on port $listenPort — start.cmd stop first"
	} else {
		Write-Error "port $listenPort is already in use by another process — start.cmd -Port <N> or free it"
	}
	exit 1
}

if ($Daemon) {
	if (Test-PidAlive) { Write-Error "already running (pid $(Get-Content -LiteralPath $pidFile | Select-Object -First 1)) — start.cmd stop first"; exit 1 }
	# Start-Process rejects -ArgumentList when the array is empty (a PowerShell
	# quirk: "cannot bind argument to parameter 'ArgumentList'"), so splat only
	# when there is something to pass.
	$startArgs = @{
		FilePath         = $bin
		WorkingDirectory = $root
		RedirectStandardOutput = $consoleLog
		RedirectStandardError  = "$consoleLog.err"
		PassThru         = $true
		WindowStyle      = 'Hidden'
	}
	if ($serverArgs.Count -gt 0) { $startArgs['ArgumentList'] = $serverArgs }
	$proc = Start-Process @startArgs
	Set-Content -LiteralPath $pidFile -Value $proc.Id
	Set-Content -LiteralPath $portFile -Value $listenPort
	Write-Host "started in the background (pid $($proc.Id)), output: $consoleLog"
	if (-not $NoWait) {
		$ok = $false
		for ($i = 0; $i -lt 60; $i++) {
			if (Test-PortOpen $listenPort) { $ok = $true; break }
			if ($proc.HasExited) { break }
			Start-Sleep -Seconds 1
		}
		if ($ok) {
			Write-Host "ready: http://localhost:$listenPort/"
		} else {
			Write-Warning "the port never opened; last lines of $consoleLog`:"
			if (Test-Path -LiteralPath $consoleLog) { Get-Content -LiteralPath $consoleLog -Tail 20 | Write-Host }
			if (Test-Path -LiteralPath "$consoleLog.err") { Get-Content -LiteralPath "$consoleLog.err" -Tail 20 | Write-Host }
			exit 1
		}
	}
	exit 0
}

Write-Host "starting ragflow-x (foreground) — http://localhost:$listenPort/ once it is listening"
$procArgs = $serverArgs
& $bin @procArgs
exit $LASTEXITCODE
