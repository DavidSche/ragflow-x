#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────
# ragflow-x  —  Stop the API server started by start.sh.
#
# Usage:
#   ./scripts/stop.sh          # stop by PID file
#   ./scripts/stop.sh -Force   # also kill by port if PID file is missing
#   ./scripts/stop.sh -Port 9191
# ──────────────────────────────────────────────────────────────────────
set -euo pipefail

PORT=9191
FORCE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    -Port)  PORT="$2"; shift 2 ;;
    -Force) FORCE=true; shift ;;
    -h|--help)
      echo "Usage: $0 [-Force] [-Port N]"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PID_FILE="$ROOT/run/server.pid"

log()  { printf '\033[36m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }

# Graceful stop, then force.
stop_pid() {
  local pid="$1"
  kill "$pid" 2>/dev/null || true
  for i in $(seq 1 30); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.1
  done
  kill -9 "$pid" 2>/dev/null || true
}

# Get PID listening on port.
port_pid() {
  if command -v ss &>/dev/null; then
    ss -tlnp "sport = :$PORT" 2>/dev/null \
      | grep -oP 'pid=\K[0-9]+' | head -1
  elif command -v lsof &>/dev/null; then
    lsof -i :"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1
  elif command -v netstat &>/dev/null; then
    netstat -ano 2>/dev/null | grep ":$PORT " | grep "LISTENING" \
      | awk '{print $NF}' | head -1
  else
    echo ""
  fi
}

stopped=false

# ── Try PID file first ───────────────────────────────────────────────
if [[ -f "$PID_FILE" ]]; then
  pid="$(tr -d '[:space:]' < "$PID_FILE")"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    log "Stopping ragflow-x (pid $pid)."
    stop_pid "$pid"
    stopped=true
  elif [[ -n "$pid" ]]; then
    log "Pid $pid is no longer running; removing stale PID file."
  fi
  rm -f "$PID_FILE"
fi

# ── Fallback: kill by port ────────────────────────────────────────────
if [[ "$stopped" != true ]]; then
  occupant="$(port_pid)"
  if [[ -n "$occupant" ]]; then
    if [[ "$FORCE" == true ]]; then
      log "Port $PORT occupied by pid $occupant; force-stopping."
      stop_pid "$occupant"
    else
      warn "Port $PORT is in use by pid $occupant but no PID file exists."
      warn "Re-run with -Force to stop it."
    fi
  else
    log "ragflow-x is not running (nothing on port $PORT)."
  fi
fi

log "Done."
exit 0
