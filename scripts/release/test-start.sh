#!/usr/bin/env bash
# Regression test for the release launcher (scripts/release/start.sh).
#
# The launcher is the first thing a "download and run" user touches, so its
# promises are tested here instead of by hand: it creates logs/ and data/, turns
# --sqlite into absolute DSN/log paths, refuses to start on a busy port or on
# top of a running instance, remembers the port for status/stop, forwards
# unknown flags to the server, and fails loudly on a bad --config path.
#
# It runs against a stub binary (RAGFLOW_X_BIN), so no ragflow-x build is needed.
# Linux/macOS only: the path assertions are POSIX, and start.ps1 covers Windows.
#   CI: .github/workflows/ci.yml -> job `launcher`
set -uo pipefail

if command -v cygpath >/dev/null 2>&1; then
	echo "skip: start.sh is the linux/darwin launcher (use start.ps1 on Windows); run this test on Linux/macOS"
	exit 0
fi

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
PORT_FG="${PORT_FG:-19731}"
PORT_DAEMON="${PORT_DAEMON:-19732}"
PORT_BUSY="${PORT_BUSY:-19733}"
PORT_CFG="${PORT_CFG:-19734}"

PASS=0
FAIL=0
LISTENER_PID=""

cleanup() {
	[ -n "$LISTENER_PID" ] && kill "$LISTENER_PID" 2>/dev/null
	[ -f "$WORK/logs/ragflow-x.pid" ] && kill "$(cat "$WORK/logs/ragflow-x.pid")" 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

ok() {
	PASS=$((PASS + 1))
	printf 'ok   %s\n' "$1"
}

bad() {
	FAIL=$((FAIL + 1))
	printf 'FAIL %s\n' "$1"
	shift
	for line in "$@"; do printf '     %s\n' "$line"; done
}

OUT=""
RC=0

# run <cmd...>: capture combined output into $OUT and the status into $RC.
run() {
	OUT="$("$@" 2>&1)"
	RC=$?
}

check_rc() { # <want> <label>
	if [ "$RC" -eq "$1" ]; then ok "$2"; else bad "$2" "want exit $1, got $RC" "$OUT"; fi
}

check_out() { # <needle> <label>
	if printf '%s' "$OUT" | grep -qF -- "$1"; then ok "$2"; else bad "$2" "missing: $1" "output: $(printf '%s' "$OUT" | head -c 500)"; fi
}

# Stub server: echoes the environment the launcher injected, then listens on the
# port it was given so the "ready" handshake can be exercised for real.
cat >"$WORK/ragflow-x" <<'STUB'
#!/usr/bin/env bash
echo "STUB driver=[${RGX_DB_DRIVER:-}] dsn=[${RGX_DB_DSN:-}] log=[${RGX_LOG_FILE:-}] output=[${RGX_LOG_OUTPUT:-}] args=[$*]"
python3 -c "
import socket, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', int('${RGX_PORT:-9191}')))
s.listen(8)
time.sleep(120)
"
STUB
chmod +x "$WORK/ragflow-x"
cp "$HERE/start.sh" "$WORK/start.sh"
chmod +x "$WORK/start.sh"
cd "$WORK" || exit 1

# 1. --help is a documentation path, not a server start.
run ./start.sh --help
check_rc 0 "--help exits 0"
check_out "usage: ./start.sh" "--help prints usage"

# 2. A missing binary must fail loudly instead of pretending to run.
run env RAGFLOW_X_BIN=./nope ./start.sh --no-wait
check_rc 1 "missing binary is refused"
check_out "binary not found" "missing binary names the path"

# 3. Foreground --sqlite: absolute DSN/log paths, wizard hint, directories.
run timeout 4 ./start.sh --sqlite --port "$PORT_FG"
check_rc 124 "foreground serves until killed"
check_out "first-run setup wizard" "foreground announces the wizard"
check_out "driver=[sqlite]" "--sqlite sets the driver"
check_out "dsn=[file:$WORK/data/ragflow-x.db?cache=shared]" "--sqlite uses an absolute DSN"
check_out "log=[$WORK/logs/ragflow-x.log]" "--sqlite points the log file at an absolute path"
check_out "output=[]" "--sqlite leaves the sinks at the default (console + file)"
[ -d "$WORK/logs" ] && ok "--sqlite creates logs/" || bad "--sqlite creates logs/"
[ -d "$WORK/data" ] && ok "--sqlite creates data/" || bad "--sqlite creates data/"

# 4. Daemon lifecycle: ready handshake, port memory, status, stop, cleanup.
run ./start.sh --daemon --sqlite --port "$PORT_DAEMON"
check_rc 0 "daemon start reports ready"
check_out "ready: http://localhost:$PORT_DAEMON/" "ready line carries the port"
[ -f "$WORK/logs/ragflow-x.pid" ] && ok "pid file written" || bad "pid file written"
[ "$(cat "$WORK/logs/ragflow-x.port" 2>/dev/null)" = "$PORT_DAEMON" ] &&
	ok "port file remembers --port" || bad "port file remembers --port"

run ./start.sh status
check_rc 0 "status exits 0"
check_out "running (pid" "status reports the pid"
check_out "port $PORT_DAEMON: accepting connections" "status probes the port"

# 5. A second start must refuse instead of silently doing nothing.
run ./start.sh --daemon --sqlite --port "$PORT_DAEMON"
check_rc 1 "second daemon start is refused"
check_out "already running" "refusal names the running instance"

run ./start.sh stop
check_rc 0 "stop exits 0"
check_out "stopped (pid" "stop reports the pid"
[ -f "$WORK/logs/ragflow-x.pid" ] && bad "pid file removed on stop" || ok "pid file removed on stop"
[ -f "$WORK/logs/ragflow-x.port" ] && bad "port file removed on stop" || ok "port file removed on stop"
run ./start.sh status
check_out "not running" "status after stop is clean"

# 6. A foreign process on the port is reported, never mistaken for "ready".
python3 -c "
import socket, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $PORT_BUSY))
s.listen(1)
time.sleep(60)
" &
LISTENER_PID=$!
sleep 1
run ./start.sh --daemon --sqlite --port "$PORT_BUSY"
check_rc 1 "busy port is refused"
check_out "already in use by another process" "busy-port message is actionable"
kill "$LISTENER_PID" 2>/dev/null
LISTENER_PID=""
[ -f "$WORK/logs/ragflow-x.pid" ] && bad "no pid file for a refused start" || ok "no pid file for a refused start"

# 7. Config discovery: server.port is honoured, a bad --config path is not ignored.
mkdir -p config
printf 'server:\n  port: %s\n' "$PORT_CFG" >config/config.yaml
run ./start.sh status
check_rc 0 "status works with a config file"
check_out "port $PORT_CFG" "status reads server.port from config"
run ./start.sh --config nope.yaml
check_rc 1 "missing --config is refused"
check_out "config file not found" "missing --config names the path"

# 8. Unknown flags belong to the server, not to the launcher.
run timeout 4 ./start.sh --port "$PORT_CFG" -- -config other.yaml
check_out "args=[-config other.yaml]" "server receives passthrough args"

printf '\nlauncher tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
