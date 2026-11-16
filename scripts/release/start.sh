#!/usr/bin/env bash
# ragflow-x launcher — download, unpack, ./start.sh.
#
# The release archive ships a single self-contained binary (API + web UI in one
# process). This script exists so "how do I start it?" is one command instead of
# a paragraph of README: it creates the directories the server expects, tells you
# which mode you are about to enter (first-run wizard vs. configured), starts the
# process, and waits until the port actually answers before printing the URL.
#
#   ./start.sh                 # foreground, default config discovery
#   ./start.sh --daemon        # background, pid file + console log under logs/
#   ./start.sh --sqlite        # first run on SQLite under ./data (no PostgreSQL)
#   ./start.sh --config x.yaml # explicit config file
#   ./start.sh stop | status   # manage the background process
#
# Everything the server itself understands still works: any argument this script
# does not know is passed through to the binary ($ ./start.sh -config conf.yaml).
set -euo pipefail

# /dev/tcp and ${BASH_SOURCE[0]} are bash features; `sh start.sh` would otherwise
# report a broken binary instead of a wrong shell.
if [ -z "${BASH_VERSION:-}" ]; then
	printf 'error: run the launcher with bash: bash start.sh %s
' "$*" >&2
	exit 1
fi

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR" || {
	printf 'error: cannot enter %s
' "$SCRIPT_DIR" >&2
	exit 1
}

readonly PID_FILE="logs/ragflow-x.pid"
readonly PORT_FILE="logs/ragflow-x.port"
readonly CONSOLE_LOG="logs/console.out"
DEFAULT_PORT=9191

usage() {
	cat <<'EOF'
ragflow-x launcher

usage: ./start.sh [action] [options] [-- extra server flags]

actions:
  (none)            run in the foreground (Ctrl-C stops it)
  stop              stop the background process started with --daemon
  status            report whether it runs and whether the port answers

options:
  -d, --daemon      run in the background (pid: logs/ragflow-x.pid, output: logs/console.out)
      --sqlite      first run on SQLite: data/ragflow-x.db, no PostgreSQL needed
      --config PATH use an explicit config file (same as the server's -config)
      --port N      override the listen port (exported as RGX_PORT)
      --no-wait     do not wait for the HTTP port before returning (daemon mode)
  -h, --help        this text

examples:
  ./start.sh --sqlite --daemon     # try the product in one shot, then open the URL
  ./start.sh                       # foreground, reads config/config.yaml if present
  ./start.sh --config /etc/ragflow-x/config.yaml --daemon
EOF
}

log() { printf '%s\n' "$*"; }
# Git Bash / Cygwin report POSIX paths; a native binary needs the native form or
# it silently opens a file called "/e/..." relative to the drive root.
native_path() {
	if command -v cygpath >/dev/null 2>&1; then
		cygpath -w "$1"
	else
		printf '%s' "$1"
	fi
}

warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# --- argument parsing -------------------------------------------------------
ACTION=run
DAEMON=0
SQLITE=0
WAIT=1
CONFIG_PATH=""
PORT=""
SERVER_ARGS=()

while [ $# -gt 0 ]; do
	case "$1" in
	-h | --help)
		usage
		exit 0
		;;
	stop | status)
		ACTION="$1"
		shift
		;;
	-d | --daemon)
		DAEMON=1
		shift
		;;
	--sqlite)
		SQLITE=1
		shift
		;;
	--config)
		[ $# -ge 2 ] || die "--config needs a path"
		CONFIG_PATH="$2"
		shift 2
		;;
	--config=*)
		CONFIG_PATH="${1#*=}"
		shift
		;;
	--port)
		[ $# -ge 2 ] || die "--port needs a number"
		PORT="$2"
		shift 2
		;;
	--port=*)
		PORT="${1#*=}"
		shift
		;;
	--no-wait)
		WAIT=0
		shift
		;;
	--)
		shift
		SERVER_ARGS+=("$@")
		break
		;;
	*)
		# Unknown flags belong to the server (e.g. -config, -config=...).
		SERVER_ARGS+=("$1")
		shift
		;;
	esac
done

# --- binary discovery -------------------------------------------------------
# RAGFLOW_X_BIN lets the test harness substitute a stub; operators never need it.
BIN="${RAGFLOW_X_BIN:-}"
if [ -z "$BIN" ]; then
	for candidate in ragflow-x ragflow-x.exe ../ragflow-x; do
		if [ -f "$candidate" ]; then
			BIN="$candidate"
			break
		fi
	done
fi
[ -n "$BIN" ] && [ -f "$BIN" ] || die "ragflow-x binary not found next to $SCRIPT_DIR (unpack the archive into its own directory)"
if [ ! -x "$BIN" ]; then
	chmod +x "$BIN" 2>/dev/null || die "$BIN is not executable and cannot be made executable"
fi
# Absolute path: nohup/exec look the name up in PATH, not in the working tree.
case "$BIN" in
/*) ;;
*) BIN="$(cd -- "$(dirname -- "$BIN")" && pwd)/$(basename -- "$BIN")" ;;
esac

# --- helpers ----------------------------------------------------------------
resolved_port() {
	local port="${PORT:-${RGX_PORT:-}}"
	if [ -z "$port" ] && [ -f "$PORT_FILE" ]; then
		port="$(cat "$PORT_FILE" 2>/dev/null || true)"
	fi
	if [ -z "$port" ]; then
		# Best effort: read server.port out of the config the server will use.
		local cfg="$CONFIG_PATH"
		if [ -z "$cfg" ] && [ -f config/config.yaml ]; then cfg="config/config.yaml"; fi
		if [ -z "$cfg" ] && [ -f config.yaml ]; then cfg="config.yaml"; fi
		if [ -n "$cfg" ] && [ -f "$cfg" ]; then
			port="$(awk '
				/^[[:space:]]*server:[[:space:]]*$/ { in_server = 1; next }
				in_server && /^[^[:space:]#]/ { in_server = 0 }
				in_server && /^[[:space:]]*port:[[:space:]]*[0-9]+/ {
					sub(/#.*/, "");
					gsub(/[^0-9]/, "", $2);
					print $2;
					exit
				}
			' "$cfg")"
		fi
	fi
	printf '%s' "${port:-$DEFAULT_PORT}"
}

port_open() {
	local port="$1"
	(exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null && exec 3>&- && return 0
	return 1
}

pid_alive() {
	[ -f "$PID_FILE" ] || return 1
	local pid
	pid="$(cat "$PID_FILE" 2>/dev/null || true)"
	[ -n "$pid" ] || return 1
	kill -0 "$pid" 2>/dev/null
}

wait_for_port() {
	local port="$1" tries="${2:-60}"
	local i=0
	while [ "$i" -lt "$tries" ]; do
		if port_open "$port"; then return 0; fi
		if [ "$DAEMON" -eq 1 ] && ! pid_alive; then return 1; fi
		sleep 1
		i=$((i + 1))
	done
	return 1
}

# --- actions ----------------------------------------------------------------
if [ "$ACTION" = stop ]; then
	if pid_alive; then
		pid="$(cat "$PID_FILE")"
		kill "$pid" 2>/dev/null || true
		for _ in $(seq 1 20); do
			kill -0 "$pid" 2>/dev/null || break
			sleep 0.5
		done
		kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
		rm -f "$PID_FILE" "$PORT_FILE"
		log "stopped (pid $pid)"
	else
		rm -f "$PID_FILE"
		log "not running"
	fi
	exit 0
fi

if [ "$ACTION" = status ]; then
	port="$(resolved_port)"
	if pid_alive; then
		log "running (pid $(cat "$PID_FILE"), port $port)"
	else
		log "not running (no live pid in $PID_FILE)"
	fi
	if port_open "$port"; then
		log "port $port: accepting connections -> http://localhost:$port/"
	else
		log "port $port: closed"
	fi
	exit 0
fi

# --- run --------------------------------------------------------------------
mkdir -p logs
[ "$SQLITE" -eq 1 ] && mkdir -p data

# First-run detection: no config file means the server falls back to its built-in
# defaults. Say so out loud — silently landing on a placeholder PostgreSQL is the
# single most confusing first five minutes this product has.
HAVE_CONFIG=0
if [ -n "$CONFIG_PATH" ]; then
	[ -f "$CONFIG_PATH" ] || die "config file not found: $CONFIG_PATH"
	HAVE_CONFIG=1
elif [ -f config/config.yaml ] || [ -f config.yaml ]; then
	HAVE_CONFIG=1
fi

if [ "$SQLITE" -eq 1 ]; then
	# Absolute DSN: a relative one is resolved against the process CWD and turns
	# "my database is gone" into a support ticket.
	export RGX_DB_DRIVER=sqlite
	export RGX_DB_DSN="file:$(native_path "$SCRIPT_DIR/data/ragflow-x.db")?cache=shared"
fi

if [ "$HAVE_CONFIG" -eq 0 ]; then
	# The binary's default is "both" (console + rotating file), but the file path is
	# relative to the *caller's* CWD. Pin it to the archive so the log lands next to
	# the launcher no matter where it was started from; leave the sinks alone so the
	# console keeps streaming too.
	export RGX_LOG_FILE="$(native_path "$SCRIPT_DIR/logs/ragflow-x.log")"
fi
if [ -n "$PORT" ]; then
	export RGX_PORT="$PORT"
fi

set -- "$BIN" "${SERVER_ARGS[@]}"

if [ "$HAVE_CONFIG" -eq 0 ]; then
	log "no config file found: starting on the built-in defaults."
	log "  - the web UI will offer the first-run setup wizard (http://localhost:$(resolved_port)/)"
	log "  - to configure PostgreSQL/RAGFlow by hand: cp config/config.example.yaml config/config.yaml"
	[ "$SQLITE" -eq 1 ] || log "  - or try SQLite first: ./start.sh --sqlite"
else
	log "using config: ${CONFIG_PATH:-$( [ -f config/config.yaml ] && echo config/config.yaml || echo config.yaml )}"
fi

# Refuse to start on top of something that already answers on the port: the
# background wait loop would otherwise call another process's socket "ready".
if port_open "$(resolved_port)"; then
	if pid_alive; then
		die "already running (pid $(cat "$PID_FILE")) on port $(resolved_port) — ./start.sh stop first"
	fi
	die "port $(resolved_port) is already in use by another process — ./start.sh --port <N> or free it"
fi

if [ "$DAEMON" -eq 1 ]; then
	if pid_alive; then
		die "already running (pid $(cat "$PID_FILE")) — ./start.sh stop first"
	fi
	: >"$CONSOLE_LOG"
	if command -v setsid >/dev/null 2>&1; then
		setsid nohup "$@" >>"$CONSOLE_LOG" 2>&1 &
	else
		nohup "$@" >>"$CONSOLE_LOG" 2>&1 &
	fi
	echo $! >"$PID_FILE"
	resolved_port >"$PORT_FILE"
	log "started in the background (pid $(cat "$PID_FILE")), output: $CONSOLE_LOG"
	if [ "$WAIT" -eq 1 ]; then
		if wait_for_port "$(resolved_port)"; then
			log "ready: http://localhost:$(resolved_port)/"
		else
			warn "the port never opened; last lines of $CONSOLE_LOG:"
			tail -n 20 "$CONSOLE_LOG" >&2 || true
			die "startup failed — see $CONSOLE_LOG"
		fi
	fi
	exit 0
fi

log "starting ragflow-x (foreground) — http://localhost:$(resolved_port)/ once it is listening"
exec "$@"
