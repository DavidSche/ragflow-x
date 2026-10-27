#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────
# ragflow-x  —  Start the API server as a background daemon.
#
# Usage:
#   ./scripts/start.sh                 # default config/config.yaml
#   ./scripts/start.sh -Local          # config/config.local.yaml (SQLite + mock)
#   ./scripts/start.sh -Config prod.yaml -Build
#   ./scripts/start.sh -Force          # kill occupant on the port first
#
# The script:
#   • builds the binary when -Build is given
#   • writes a PID file (run/server.pid) so stop.sh can stop it
#   • app logs go to date-rotated files per config logging.output
#     (default ./logs/ragflow-x.log -> logs/ragflow-x-<date>.log,
#     with max_size_mb / max_backups retention) — NOT server.out.log
#   • streams stray stdout/stderr to logs/server.out.log / logs/server.err.log
#   • waits for the port to accept connections and prints the URL
#   • is idempotent: starting an already-running instance is a no-op
# ──────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────
CONFIG=""
LOCAL=false
BINARY=""
BUILD=false
FORCE=false
PORT=9191
TIMEOUT_SEC=30

# ── Parse arguments ───────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    -Config)   CONFIG="$2";   shift 2 ;;
    -Local)    LOCAL=true;     shift   ;;
    -Binary)   BINARY="$2";   shift 2 ;;
    -Build)    BUILD=true;     shift   ;;
    -Force)    FORCE=true;     shift   ;;
    -Port)     PORT="$2";     shift 2 ;;
    -TimeoutSec) TIMEOUT_SEC="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: $0 [-Config file] [-Local] [-Binary path] [-Build] [-Force] [-Port N] [-TimeoutSec N]"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Resolve paths ─────────────────────────────────────────────────────
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUN_DIR="$ROOT/run"
LOG_DIR="$ROOT/logs"
PID_FILE="$RUN_DIR/server.pid"
OUT_LOG="$LOG_DIR/server.out.log"
ERR_LOG="$LOG_DIR/server.err.log"

# ── Helper functions ──────────────────────────────────────────────────
log()  { printf '\033[36m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }
die()  { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }

# Check if a TCP port is in LISTEN state.
port_in_use() {
  if command -v ss &>/dev/null; then
    ss -tlnp "sport = :$PORT" 2>/dev/null | grep -q ":$PORT"
  elif command -v netstat &>/dev/null; then
    netstat -tlnp 2>/dev/null | grep -q ":$PORT "
  elif command -v lsof &>/dev/null; then
    lsof -i :"$PORT" -sTCP:LISTEN &>/dev/null
  else
    # Fallback: try to connect
    (echo >/dev/tcp/127.0.0.1/"$PORT") 2>/dev/null
  fi
}

# Get the PID of the process listening on $PORT.
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

# Read PID from pid file; return empty if stale.
alive_pid() {
  [[ -f "$PID_FILE" ]] || return 1
  local pid
  pid="$(tr -d '[:space:]' < "$PID_FILE")"
  [[ -z "$pid" ]] && return 1
  if kill -0 "$pid" 2>/dev/null; then
    echo "$pid"
  else
    return 1
  fi
}

# Graceful stop, then force if needed.
stop_pid() {
  local pid="$1"
  kill "$pid" 2>/dev/null || true
  # Wait up to 3 seconds for graceful shutdown
  for i in $(seq 1 30); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.1
  done
  kill -9 "$pid" 2>/dev/null || true
}

# ── Resolve config ────────────────────────────────────────────────────
if [[ -n "$CONFIG" && "$LOCAL" == true ]]; then
  die "Choose either -Config or -Local, not both."
fi

if [[ -z "$CONFIG" ]]; then
  if [[ "$LOCAL" == true ]]; then
    CONFIG="config/config.local.yaml"
  else
    CONFIG="config/config.yaml"
  fi
fi

# Resolve relative to repo root
if [[ "$CONFIG" != /* ]]; then
  CONFIG="$ROOT/$CONFIG"
fi

[[ -f "$CONFIG" ]] || die "Config file not found: $CONFIG"

# ── Resolve (and optionally build) binary ─────────────────────────────
if [[ "$BUILD" == true ]]; then
  cd "$ROOT"
  [[ -z "$BINARY" ]] && BINARY="bin/ragflow-x"
  go build -o "$BINARY" ./cmd/server/ || die "go build failed."
fi

if [[ -z "$BINARY" ]]; then
  BINARY="bin/ragflow-x"
fi

if [[ "$BINARY" != /* ]]; then
  BINARY="$ROOT/$BINARY"
fi

[[ -f "$BINARY" ]] || die "Binary not found: $BINARY. Pass -Build to compile it."

# ── Handle port conflicts ─────────────────────────────────────────────
if port_in_use; then
  if alive_pid >/dev/null 2>&1; then
    local_pid="$(alive_pid)"
    log "ragflow-x already running (pid $local_pid) on port $PORT."
    exit 0
  fi
  if [[ "$FORCE" != true ]]; then
    occupant="$(port_pid)"
    die "Port $PORT is already in use by pid ${occupant:-unknown}. Re-run with -Force to take it over."
  fi
  occupant="$(port_pid)"
  if [[ -n "$occupant" ]]; then
    warn "Force-stopping pid $occupant on port $PORT."
    stop_pid "$occupant"
    sleep 0.5
  fi
fi

# ── Prepare directories ──────────────────────────────────────────────
mkdir -p "$RUN_DIR" "$LOG_DIR"

# ── Start the server ─────────────────────────────────────────────────
log "Starting ragflow-x from $BINARY"
log "Config: $CONFIG"

cd "$ROOT"
nohup "$BINARY" -config "$CONFIG" \
  >>"$OUT_LOG" 2>>"$ERR_LOG" &
server_pid=$!

echo "$server_pid" > "$PID_FILE"
log "Started pid $server_pid; waiting for port $PORT ..."

# ── Wait for readiness ────────────────────────────────────────────────
deadline=$(( $(date +%s) + TIMEOUT_SEC ))
while [[ $(date +%s) -lt $deadline ]]; do
  if port_in_use; then
    log "OK  ragflow-x is up: http://localhost:$PORT"
    echo "    config: $CONFIG"
    echo "    logs:   logs/ragflow-x-<date>.log (app rotation, config logging.output)"
    echo "            stdout/stderr fallback: $OUT_LOG / $ERR_LOG"
    echo "    stop:   ./scripts/stop.sh"
    exit 0
  fi
  # Check if process is still alive
  if ! kill -0 "$server_pid" 2>/dev/null; then
    break
  fi
  sleep 0.5
done

# ── Startup failed ────────────────────────────────────────────────────
printf '\033[31mServer did not become ready on port %d within %ds.\033[0m\n' "$PORT" "$TIMEOUT_SEC" >&2
if [[ -f "$ERR_LOG" ]]; then
  echo "--- Last 30 lines of error log ---"
  tail -30 "$ERR_LOG"
fi
rm -f "$PID_FILE"
exit 1
