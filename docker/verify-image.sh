#!/usr/bin/env bash
# Verifies the container contract of the image built from docker/Dockerfile.
#
# The archive is the supported install path, so its container twin has no CI job
# (the Go + web build is minutes); `docker/test-entrypoint.sh` covers the part
# that needs no Docker. Run this before publishing an image, or against a
# published tag:
#
#   bash docker/verify-image.sh                                  # build + verify
#   IMAGE=ghcr.io/owner/repo:v0.6.1 SKIP_BUILD=1 bash docker/verify-image.sh
#                                                               # (VERSION follows the tag)
#   VERSION=v1.0.0 bash docker/verify-image.sh                    # build stamping v1.0.0
#   GOPROXY_ARG=https://goproxy.cn,direct bash docker/verify-image.sh
#
# Requires: docker (with compose), python3 nowhere — the stub engine runs in a
# python:3.12 container. Windows: run from Git Bash with MSYS_NO_PATHCONV=1.
set -uo pipefail

# Git Bash rewrites arguments that look like POSIX paths into C:/Program Files/Git/...
# — which turns `-v <host dir>:/stub:ro` into a mount of a directory that does not
# exist, and the stub engine then dies with "can't open file". The result looks like
# a broken image (engine unreachable -> readyz 503 -> unhealthy), so do not rely on
# the reader noticing the line in the header: re-run ourselves with the fix.
case "$(uname -s)" in
MSYS* | MINGW* | CYGWIN*)
	if [ "${MSYS_NO_PATHCONV:-0}" != "1" ]; then
		echo "note: re-running with MSYS_NO_PATHCONV=1 (Git Bash mangles /paths in docker arguments)" >&2
		MSYS_NO_PATHCONV=1 exec bash "$0" "$@"
	fi
	;;
esac

REPO="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${IMAGE:-ragflow-x:verify}"
HOSTPORT="${HOSTPORT:-9931}"
VERSION="${VERSION:-}"
# Against a published image the stamp is already inside the binary, so demanding a
# hand-set VERSION only produces a false FAIL (`missing ["version":"v0.0.0-verify"]`
# while the image correctly reports its own tag). Read it off the tag instead; an
# explicit VERSION still wins, which is what a local build needs.
if [ -z "$VERSION" ]; then
	case "$IMAGE" in
	*:*) VERSION="${IMAGE##*:}" ;;
	*) VERSION="latest" ;;
	esac
	case "$VERSION" in
	*digest*) VERSION="latest" ;;
	esac
fi
GOARG="${GOPROXY_ARG:-https://proxy.golang.org,direct}"
FAILED=0
WORK=$(mktemp -d)

cleanup() {
	docker rm -f rgx-verify-bare rgx-verify-stub >/dev/null 2>&1
	compose_override down -v --remove-orphans >/dev/null 2>&1
	rm -rf "$WORK"
}
trap cleanup EXIT

step() { printf '\n===== %s =====\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }
fail() { FAILED=$((FAILED + 1)); printf 'FAIL  %s\n' "$*"; }
check() { if [ "$2" = "$3" ]; then pass "$1 ($2)"; else fail "$1: got [$2] want [$3]"; fi; }
contains() { case "$2" in *"$3"*) pass "$1" ;; *) fail "$1: missing [$3] in [${2:0:300}]" ;; esac; }
absent() { case "$2" in *"$3"*) fail "$1: unexpected [$3]" ;; *) pass "$1" ;; esac; }

# docker.exe on Windows turns "/e/study-place/..." into "E:\e\study-place\..."
# when MSYS_NO_PATHCONV=1, so compose is always called from docker/ with
# relative paths and host paths go through `pwd -W`.
compose_plain() { (cd "$REPO/docker" && docker compose -f docker-compose.yml "$@"); }
compose_override() {
	(cd "$REPO/docker" && docker compose --env-file "$(winpath "$WORK")/verify.env" \
		-f docker-compose.yml -f "$(winpath "$WORK")/override.yml" "$@")
}
winpath() { (cd "$1" && pwd -W 2>/dev/null || pwd); }

wait_healthy() { # wait_healthy <container> <seconds>
	local name=$1 limit=$2 i=0 status
	[ -n "$name" ] || { echo "no container id"; return 1; }
	while [ "$i" -lt "$limit" ]; do
		status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name" 2>/dev/null)
		case "$status" in
		healthy) echo "healthy after ${i}s"; return 0 ;;
		unhealthy) echo "unhealthy after ${i}s"; docker inspect -f '{{range .State.Health.Log}}{{.Output}}{{end}}' "$name"; return 1 ;;
		esac
		sleep 2
		i=$((i + 2))
	done
	echo "still ${status:-gone} after ${limit}s"
	return 1
}

# Anything left from an earlier run holds the published port and makes the next
# stage fail with "port is already allocated", which reads like a broken image.
write_scratch() {
	cat > "$WORK/verify.env" <<EOF
RGX_ADMIN_USER=admin
RGX_ADMIN_PASSWORD=
RGX_MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
RGX_JWT_SECRET=fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210
RGX_DB_PASSWORD=verify-only-pw
GOPROXY=$GOARG
RGX_RAGFLOW_BASE_URL=http://rgx-verify-stub:9380
RGX_RAGFLOW_API_KEY=verify-key
EOF
	cat > "$WORK/override.yml" <<EOF
services:
  ragflow-x:
    ports: !override
      - "$HOSTPORT:9191"
    environment:
      RGX_RAGFLOW_BASE_URL: http://rgx-verify-stub:9380
      RGX_RAGFLOW_API_KEY: verify-key
EOF
}

preclean() {
	docker rm -f rgx-verify-bare rgx-verify-stub >/dev/null 2>&1
	# Override files, not the base file: `down` interpolates the same env
	# file, and a bare base-file call fails on the required variables and
	# leaves containers holding the published port.
	write_scratch
	compose_override down -v --remove-orphans >/dev/null 2>&1
	sleep 1
}

stage_build() {
	step "build image ($IMAGE, VERSION=$VERSION, GOPROXY=$GOARG)"
	# Relative paths from the repo root: docker.exe on Windows would read the
	# POSIX absolute path as "E:\\e\\study-place\\...".
	(cd "$REPO" && docker build -f docker/Dockerfile \
		--build-arg VERSION="$VERSION" \
		--build-arg "GOPROXY=$GOARG" \
		-t "$IMAGE" .) || { fail "docker build"; return 1; }
	pass "image built"
}

stage_metadata() {
	step "image metadata and contents"
	local entry cmd health user listing
	entry=$(docker image inspect -f '{{json .Config.Entrypoint}}' "$IMAGE")
	cmd=$(docker image inspect -f '{{json .Config.Cmd}}' "$IMAGE")
	health=$(docker image inspect -f '{{json .Config.Healthcheck.Test}}' "$IMAGE")
	user=$(docker image inspect -f '{{.Config.User}}' "$IMAGE")
	contains "entrypoint is the container entrypoint" "$entry" "docker-entrypoint.sh"
	case "$cmd" in null | "[]") pass "cmd is empty (entrypoint decides)" ;; *) fail "cmd: got [$cmd]" ;; esac
	contains "healthcheck curls readyz" "$health" "/api/v1/readyz"
	check "runs as root (pid 1 + volumes)" "$user" ""

	listing=$(docker run --rm --entrypoint sh "$IMAGE" -c \
		'ls /app/config.example.yaml /usr/local/bin/ragflow-x /usr/local/bin/docker-entrypoint.sh; ls -d /app/data /app/secrets /app/config' 2>&1)
	contains "example config shipped" "$listing" "/app/config.example.yaml"
	contains "entrypoint installed" "$listing" "/usr/local/bin/docker-entrypoint.sh"
	contains "volume mount points exist" "$listing" "/app/config"
}

stage_bare() {
	step "bare docker run, no config mounted"
	preclean
	docker run -d --name rgx-verify-bare -p "$HOSTPORT:9191" "$IMAGE" >/dev/null || { fail "bare run start"; return 1; }
	if wait_healthy rgx-verify-bare 120; then pass "container healthy in wizard mode"; else fail "container health"; fi

	local logs code body
	logs=$(docker logs rgx-verify-bare 2>&1)
	contains "entrypoint says no config is mounted" "$logs" "no config mounted at /app/config/config.yaml"
	contains "entrypoint points at the wizard" "$logs" "first-run setup wizard"
	# The build-arg has to survive -ldflags: image and archive report the same tag.
	contains "binary reports the stamped version" "$logs" "\"version\":\"$VERSION\""

	code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$HOSTPORT/api/v1/healthz")
	check "GET /api/v1/healthz" "$code" "200"
	body=$(curl -s "http://127.0.0.1:$HOSTPORT/api/v1/readyz")
	contains "readyz marks setup as required" "$body" '"setup_required":true'
	body=$(curl -s "http://127.0.0.1:$HOSTPORT/api/v1/system/setup/status")
	contains "status reports mode=wizard" "$body" '"mode":"wizard"'
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$HOSTPORT/")
	check "GET / serves the embedded SPA" "$code" "200"
	body=$(curl -s "http://127.0.0.1:$HOSTPORT/")
	contains "SPA html has a root node" "$body" 'id="root"'
	# The admin-only step needs the full service graph, which the bootstrap router
	# does not have: it must answer 409/40009 pointing at apply, not a bare 404.
	body=$(curl -s -X POST "http://127.0.0.1:$HOSTPORT/api/v1/system/setup/admin")
	contains "admin step explains itself on the bootstrap router" "$body" '"code":40009'
	docker rm -f rgx-verify-bare >/dev/null 2>&1

	step "recovery subcommand reaches the binary"
	local out
	out=$(MSYS_NO_PATHCONV=1 docker run --rm -e RGX_DB_DRIVER=sqlite \
		-e 'RGX_DB_DSN=file:/app/data/rec.db?cache=shared' "$IMAGE" reset-admin-password -list 2>&1)
	printf '%s\n' "$out" | tail -3
	# The CLI deliberately does not migrate, so an empty DB answers with either
	# "no accounts" or a missing-table error; both prove the subcommand ran.
	case "$out" in
	*"no accounts"* | *"list users"*) pass "reset-admin-password reached the binary" ;;
	*) fail "reset-admin-password: unexpected output" ;;
	esac
	absent "entrypoint did not inject its own arguments" "$out" "no config mounted"
}

stage_compose() {
	step "compose stack: postgres + redis + mounted config + stub engine"
	preclean
	compose_override up -d --build || { fail "compose up"; return 1; }

	# A stand-in engine on the stack network. Without one the product correctly
	# stays in the wizard, so only a reachable engine exercises the full router
	# together with the mounted config file.
	docker rm -f rgx-verify-stub >/dev/null 2>&1
	docker run -d --name rgx-verify-stub --network docker_default \
		-v "$(winpath "$REPO/docker")/:/stub:ro" python:3.12 python3 /stub/stub-engine.py 9380 >/dev/null ||
		fail "stub engine start"
	sleep 3
	compose_override up -d --force-recreate ragflow-x >/dev/null 2>&1

	local cid body logs
	cid=$(compose_override ps -q ragflow-x 2>/dev/null | head -1)
	if wait_healthy "$cid" 200; then pass "compose service healthy"; else fail "compose service health"; fi
	body=$(curl -s "http://127.0.0.1:$HOSTPORT/api/v1/system/setup/status")
	printf '%s\n' "$body" | head -c 240; echo
	contains "status reports the database is up" "$body" '"db_up":true'
	contains "status reports the engine is up" "$body" '"ragflow_up":true'
	contains "full router answers (mode=engine)" "$body" '"mode":"engine"'
	logs=$(docker logs "$cid" 2>&1)
	contains "mounted config was used" "$logs" '"/app/config/config.yaml"'
}

if [ "${SKIP_BUILD:-0}" != "1" ]; then stage_build || exit 1; fi
stage_metadata
stage_bare
stage_compose

printf '\n===== summary: %s =====\n' "$([ "$FAILED" -eq 0 ] && echo ALL PASS || echo "$FAILED FAILED")"
[ "$FAILED" -eq 0 ]
