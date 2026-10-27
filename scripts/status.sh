#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────
# ragflow-x  —  Show whether the API server is running.
#
# Usage:
#   ./scripts/status.sh
#   ./scripts/status.sh -Port 9191
# ──────────────────────────────────────────────────────────────────────
set -euo pipefail

PORT=9191

while [[ $# -gt 0 ]]; do
  case "$1" in
    -Port) PORT="$2"; shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PID_FILE="$ROOT/run/server.pid"

# Find PID listening on port
listen_pid=""
if command -v ss &>/dev/null; then
  listen_pid="$(ss -tlnp "sport = :$PORT" 2>/dev/null \
    | grep -oP 'pid=\K[0-9]+' | head -1)"
elif command -v lsof &>/dev/null; then
  listen_pid="$(lsof -i :"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1)"
elif command -v netstat &>/dev/null; then
  listen_pid="$(netstat -ano 2>/dev/null | grep ":$PORT " \
    | grep "LISTENING" | awk '{print $NF}' | head -1)"
fi

if [[ -n "$listen_pid" ]]; then
  printf '\033[32mRUNNING\033[0m  port=%d pid=%s\n' "$PORT" "$listen_pid"
  if [[ -f "$PID_FILE" ]]; then
    echo "pid_file=$(cat "$PID_FILE")"
  fi
  exit 0
fi

printf '\033[31mSTOPPED\033[0m  (no listener on port %d)\n' "$PORT"
exit 1
