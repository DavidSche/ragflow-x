#!/bin/sh
# Tests docker/entrypoint.sh without Docker.
#
# The image's startup contract has two promises, and both used to be broken:
#   1. a mounted /app/config/config.yaml is used;
#   2. without one, the server still starts (built-in defaults -> setup wizard)
#      instead of dying with `load config failed: ... no such file`;
#   3. operator commands (`reset-admin-password -list`) and explicit flags reach
#      the binary untouched.
#
# The script is copied into a temp dir with CONFIG rewritten to a local path and
# a stub `ragflow-x` first on PATH, so the branches are exercised for real.
#
#   sh docker/test-entrypoint.sh
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
SRC="$HERE/entrypoint.sh"
WORK=$(mktemp -d)
PASS=0
FAIL=0

cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

sed 's#^CONFIG=/app/config/config.yaml#CONFIG=./config.yaml#' "$SRC" > "$WORK/entrypoint.sh"
chmod +x "$WORK/entrypoint.sh"

# Stub binary: prints its argv so the assertions can see exactly what the
# entrypoint handed over.
cat > "$WORK/ragflow-x" <<'STUB'
#!/bin/sh
echo "argv:$*"
STUB
chmod +x "$WORK/ragflow-x"

ok() { PASS=$((PASS + 1)); echo "ok   $1"; }
bad() { FAIL=$((FAIL + 1)); echo "FAIL $1"; }

run() { # run <args...> -> OUT, RC
	OUT=$(cd "$WORK" && PATH="$WORK:$PATH" ./entrypoint.sh "$@" 2>&1)
	RC=$?
}

check() { # check <message> <expected-substring>
	case "$OUT" in
	*"$2"*) ok "$1" ;;
	*) bad "$1 (want substring: $2; got: $OUT)" ;;
	esac
}

check_absent() { # check_absent <message> <unexpected-substring>
	case "$OUT" in
	*"$2"*) bad "$1 (must not contain: $2; got: $OUT)" ;;
	*) ok "$1" ;;
	esac
}

# 1. No config file: say so, then start the binary with no arguments.
rm -f "$WORK/config.yaml"
run
[ "$RC" -eq 0 ] || bad "no config: exit code $RC"
check "no config: explains that it starts on defaults" "no config mounted at ./config.yaml"
check "no config: names the wizard url" "first-run setup wizard on http://localhost:9191/"
check "no config: hands no arguments to the binary" "argv:"
check_absent "no config: does not pass a config flag" "argv:-config"

# 2. Config mounted: the binary is started with it.
printf 'server:\n  port: 9191\n' > "$WORK/config.yaml"
run
check "mounted config: binary receives -config" "argv:-config ./config.yaml"

# 3. Extra CMD arguments are appended, not swallowed.
run extra1 extra2
check "mounted config: extra args are appended" "argv:-config ./config.yaml extra1 extra2"

# 4. Recovery subcommand stays intact (this is how a locked-out operator resets
#    the admin password inside a container).
run reset-admin-password -list
check "recovery: subcommand passes through" "argv:reset-admin-password -list"
check_absent "recovery: no config flag is injected" "argv:-config"

# 5. Explicit flags are the operator's business.
run -config /elsewhere/config.yaml
check "flags: explicit -config passes through" "argv:-config /elsewhere/config.yaml"

echo "entrypoint tests: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
