#!/usr/bin/env bash
# image-spec.sh — is the contract inside the *image* the one we are shipping?
#
# doc/304 §4 E164, doc/310 §22.4 T310-12. `spec-artifact.sh` proves three equalities
# (git index == package == binary bytes). It cannot prove anything about the image:
# `.github/workflows/release.yml`'s `docker` job runs `docker/Dockerfile`, which
# **compiles Go a second time**. A container user therefore pulls a binary that no
# earlier check has ever looked at, and "it was built from the same commit" is an
# intention, not a reading.
#
# So this file answers the fourth question by running the image and asking it:
#
#     git index spec  ==  /docs/openapi.json  served by the image
#
# Why over HTTP instead of reading the binary out of the image: the HTTP surface is
# what a client actually receives. `//go:embed` could hold the right bytes while the
# handler serves something else (wrong var, wrong content-type, gzip, a route that
# never registers and falls through to the SPA shell — E159). The served bytes are
# the deliverable; anything cheaper is a proxy for it.
#
# The four ways this must go red (never SKIP, never PASS):
#   1. the image cannot be found / cannot start / never becomes ready;
#   2. `/docs/openapi.json` answers with anything but 200 + application/json
#      (a 200 text/html SPA shell is the classic false positive here);
#   3. the served bytes differ from the expected bytes;
#   4. the comparison itself cannot be made (missing expected file, no python).
#
# Usage
#   bash scripts/release/image-spec.sh verify <image-ref> [options]
#   bash scripts/release/image-spec.sh compare <served-json> <served-yaml> \
#                                               <expect-json> <expect-yaml>
#   bash scripts/release/image-spec.sh --selftest
#   bash scripts/release/image-spec.sh gate [<version>]
#
# Options for `verify`:
#   --json FILE / --yaml FILE   expected bytes (default: the git *index* copy of
#                               internal/docs/swagger.{json,yaml} — the committed
#                               bytes, not this machine's working tree)
#   --platform P                repeatable; each platform is started and asked
#                               (default: the host platform). Verifying only one
#                               platform of a multi-arch push leaves the others
#                               unexamined, which is how this file gets used in CI:
#                               one run per published platform digest.
#   --port N                    host port for 127.0.0.1 (default 9937, +1 on clash)
#   --timeout S                 seconds to wait for /api/v1/readyz (default 240)
#   --keep                      leave the container behind for diagnosis
#
# Controlled configuration: the container is started with sqlite (no external
# database), a placeholder engine URL that is never dialled, and docs exposed to
# anonymous callers *for this throwaway container only*. None of that is a
# recommendation for production; it is the smallest configuration in which the
# docs routes are registered.
set -uo pipefail

# Windows consoles default to GBK. Without this the circled-digit markers the
# verdict lines are grepped on arrive as '??', so the check would fail for a reason
# that has nothing to do with the artifact.
export PYTHONIOENCODING=utf-8

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO" || exit 1

JSON_PATH="internal/docs/swagger.json"
YAML_PATH="internal/docs/swagger.yaml"
HOST_PORT=9937
WAIT_SECONDS=240
KEEP=0
PLATFORMS=()
EXPECT_JSON=""
EXPECT_YAML=""
FAILED=0
WORK=""

say() { printf '%s\n' "$*" >&2; }
ok() { printf 'ok    %s\n' "$*"; }
bad() { FAILED=$((FAILED + 1)); printf 'FAIL  %s\n' "$*" >&2; }
die() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

cleanup() {
	local c
	for c in $(docker ps -aq --filter "name=rgx-spec-" 2>/dev/null); do
		[ "$KEEP" = "1" ] || docker rm -f "$c" >/dev/null 2>&1
	done
	[ -n "${WORK:-}" ] && rm -rf "$WORK"
}
trap cleanup EXIT

sha_of() { sha256sum "$1" 2>/dev/null | cut -c1-16; }
bytes_of() { wc -c <"$1" 2>/dev/null | tr -d ' \n'; }

require_docker() {
	command -v docker >/dev/null 2>&1 || die "docker is not on PATH: this row cannot look, and 'cannot look' is not a pass"
	docker info >/dev/null 2>&1 || die "docker is installed but the daemon is not reachable: 'cannot look' is not a pass"
}

# ── compare (no docker) ────────────────────────────────────────────────────
# Split out so the refusals can be exercised without building an image, and so the
# HTTP path and the file path share the exact same verdict logic.
_compare_one() { # _compare_one <name> <served> <expect>
	local name=$1 served=$2 expect=$3 sb eb
	[ -f "$expect" ] || { bad "$name: expected file not readable ($expect) — nothing to compare against"; return 1; }
	[ -f "$served" ] || { bad "$name: served bytes not readable ($served)"; return 1; }
	sb=$(bytes_of "$served")
	eb=$(bytes_of "$expect")
	if [ "$sb" = "0" ]; then bad "$name: served 0 bytes"; return 1; fi
	# The NoRoute trap (E159): an unregistered path answers 200 with the SPA shell.
	# Sniffing the first byte catches it even when a caller forgot the content type.
	if [ "$name" = "json" ] && [ "$(head -c 1 "$served")" != "{" ]; then
		bad "$name: served content is not a JSON document (first byte '$(head -c 1 "$served")') — a 200 is not a route"
		return 1
	fi
	if [ "$sb" = "$eb" ] && cmp -s "$served" "$expect"; then
		ok "served == expected  $name  $(printf '%9s' "$sb") B  sha256 $(sha_of "$served")"
		return 0
	fi
	bad "$name: served != expected (served $sb B $(sha_of "$served"), expected $eb B $(sha_of "$expect"))"
	return 1
}

compare_files() { # <served-json> <served-yaml> <expect-json> <expect-yaml>
	# Passed as four arguments rather than packed into one string: Windows paths
	# carry a colon (`E:/tmpgo/...`), so any `a:b:c` encoding splits them wrongly.
	_compare_one json "$1" "$3"
	_compare_one yaml "$2" "$4"
}

# ── expected bytes ─────────────────────────────────────────────────────────
# Default is the *index*, not the working tree (E140: a working tree is one
# machine's reading; the image was built from a commit). `git show` failing is a
# refusal, not a fallback.
materialize_expect() {
	WORK=$(mktemp -d)
	if [ -z "$EXPECT_JSON" ]; then
		EXPECT_JSON="$WORK/expected.json"
		git show ":$JSON_PATH" >"$EXPECT_JSON" 2>/dev/null ||
			die "cannot read $JSON_PATH from the git index (nothing committed to compare against)"
		say "index:   $JSON_PATH (git show :…, the committed bytes)"
	fi
	if [ -z "$EXPECT_YAML" ]; then
		EXPECT_YAML="$WORK/expected.yaml"
		git show ":$YAML_PATH" >"$EXPECT_YAML" 2>/dev/null ||
			die "cannot read $YAML_PATH from the git index (nothing committed to compare against)"
		say "index:   $YAML_PATH (git show :…, the committed bytes)"
	fi
}

# ── verify ────────────────────────────────────────────────────────────────
free_port() { # free_port <start>
	local p=$1 i=0
	while [ "$i" -lt 40 ]; do
		if ! docker ps --format '{{.Ports}}' | grep -q ":$p->"; then printf '%s' "$p"; return 0; fi
		p=$((p + 1)); i=$((i + 1))
	done
	return 1
}

# Readiness is "the contract surface answers", not "the product is ready".
# `/api/v1/readyz` is 503 whenever RAGFlow is unreachable, and this container points
# at a deliberately dead engine (a docs check must not need an engine, a database
# server, or a token). So poll the thing being verified: 200 + application/json.
wait_docs() { # wait_docs <name> <port> <out-json> ; echoes the last "code ctype"
	local name=$1 port=$2 out=$3 i=0 obs=""
	while [ "$i" -lt "$WAIT_SECONDS" ]; do
		obs=$(curl -s -o "$out" -m 5 -w '%{http_code} %{content_type}' 			"http://127.0.0.1:$port/docs/openapi.json" 2>/dev/null)
		case "$obs" in
		"200 "*application/json*) LAST_OBS=$obs; return 0 ;;
		esac
		if ! docker ps --format '{{.Names}}' | grep -qx "$name"; then LAST_OBS=$obs; return 1; fi
		sleep 2
		i=$((i + 2))
	done
	LAST_OBS=$obs
	return 1
}

verify_image() { # verify_image <ref>
	local ref=$1 digest ga id
	require_docker
	# Absent locally is not a refusal by itself — a CI row is supposed to pull the
	# published digest. "Cannot pull it" is (see the loop), and so is "it started but
	# served something else". The local-build case is what `gate` passes.
	if docker image inspect "$ref" >/dev/null 2>&1; then
		digest=$(docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "$ref")
		id=$(docker image inspect --format '{{.Id}}' "$ref" | cut -c1-19)
		say "image:   $ref"
		say "digest:  ${digest:-<not from a registry: local build $id>}"
	else
		say "image:   $ref (not present locally; will pull per platform)"
	fi
	say "expect:  json $(bytes_of "$EXPECT_JSON") B $(sha_of "$EXPECT_JSON") / yaml $(bytes_of "$EXPECT_YAML") B $(sha_of "$EXPECT_YAML")"

	local plats=("${PLATFORMS[@]}")
	[ "${#plats[@]}" -eq 0 ] && plats=("local")

	local p idx=0
	for p in "${plats[@]}"; do
		idx=$((idx + 1))
		local name="rgx-spec-$idx-$RANDOM" port cand
		cand=$HOST_PORT
		port=$(free_port "$cand") || die "no free host port near $HOST_PORT"
		if ! docker image inspect "$ref" >/dev/null 2>&1 ||
			[ "$p" != "local" ] && ! docker image inspect --format '{{.Os}}/{{.Architecture}}' "$ref" 2>/dev/null | grep -qx "${p#linux/}"; then
			# A pinned manifest-list digest plus `--platform` is the published bytes for
			# that platform, in the sense a caller pulling it would get them. Nothing is
			# rebuilt and nothing is resolved by name, so a re-tagged `latest` cannot make
			# this row look at a different image than the one that was pushed.
			say "pulling $ref (platform $p)…"
			if ! MSYS_NO_PATHCONV=1 docker pull ${p:+--platform "$p"} "$ref" >"$WORK/pull-$idx.log" 2>&1; then
				tail -5 "$WORK/pull-$idx.log" >&2
				die "image cannot be pulled: $ref (platform $p) — 'cannot find it' is a refusal, not a skip"
			fi
		fi
		local args=(-d --name "$name" -p "127.0.0.1:$port:9191")
		[ "$p" != "local" ] && args+=(--platform "$p")
		args+=(
			-e RGX_DB_DRIVER=sqlite
			-e 'RGX_DB_DSN=file:/app/data/spec.db?cache=shared'
			-e RGX_RAGFLOW_BASE_URL=http://127.0.0.1:1
			-e RGX_RAGFLOW_API_KEY=image-spec-probe-not-a-key
			-e RGX_DOCS_ENABLED=true
			-e RGX_DOCS_REQUIRE_ADMIN=false
			"$ref"
		)
		say ""
		say "== $ref (platform $p, 127.0.0.1:$port) =="
		MSYS_NO_PATHCONV=1 docker run "${args[@]}" >/dev/null 2>&1 || die "container did not start ($ref, platform $p)"
		local served="$WORK/served-$idx.json" servedy="$WORK/served-$idx.yaml" code
		if ! wait_docs "$name" "$port" "$served"; then
			# The E159 shape is the one that looks fine: 200 text/html, 1286 B SPA
			# shell. Say which one happened instead of only printing logs.
			case "$LAST_OBS" in
			"200 "*) bad "GET /docs/openapi.json answered '$LAST_OBS' — a 200 with a non-JSON body is the SPA shell, not a route (docs not enabled in this image?)" ;;
			*) bad "GET /docs/openapi.json never answered 200 + application/json within ${WAIT_SECONDS}s (last: '${LAST_OBS:-no answer}')" ;;
			esac
			docker logs "$name" 2>&1 | tail -8 >&2
			[ "$KEEP" = "1" ] || docker rm -f "$name" >/dev/null 2>&1
			die "image started but did not serve the contract ($ref, platform $p)"
		fi
		code=$(curl -s -o "$servedy" -m 60 -w '%{http_code}' "http://127.0.0.1:$port/docs/openapi.yaml")
		[ "$code" = "200" ] || bad "GET /docs/openapi.yaml answered $code (want 200)"
		compare_files "$served" "$servedy" "$EXPECT_JSON" "$EXPECT_YAML"
		if [ "$KEEP" = "1" ]; then
			ok "container kept for diagnosis: $name"
		else
			docker rm -f "$name" >/dev/null 2>&1
		fi
	done
}

# ── selftest ──────────────────────────────────────────────────────────────
selftest() {
	local d
	d=$(mktemp -d)
	WORK=$d
	printf '{"openapi":"3.0.0"}\n' >"$d/good.json"
	printf 'openapi: 3.0.0\n' >"$d/good.yaml"
	printf '{"openapi":"3.0.0"}\n' >"$d/served.json"
	printf 'openapi: 3.0.0\n' >"$d/served.yaml"

	say "-- M0 control: identical pairs pass"
	FAILED=0
	compare_files "$d/served.json" "$d/served.yaml" "$d/good.json" "$d/good.yaml" >/dev/null 2>&1
	[ "$FAILED" -eq 0 ] && ok "M0 control passes (so the refusals below are not 'always red')" || bad "M0 control should have passed"

	say "-- M1 one flipped byte is refused"
	printf '{"openapi":"3.0.1"}\n' >"$d/flip.json"
	FAILED=0
	compare_files "$d/flip.json" "$d/served.yaml" "$d/good.json" "$d/good.yaml" >/dev/null 2>&1
	[ "$FAILED" -gt 0 ] && ok "M1 flipped byte refused" || bad "M1 flipped byte was accepted"

	say "-- M2 an unreadable expected file is refused (not treated as clean)"
	FAILED=0
	compare_files "$d/served.json" "$d/served.yaml" "$d/missing.json" "$d/good.yaml" >/dev/null 2>&1
	[ "$FAILED" -gt 0 ] && ok "M2 missing expected refused" || bad "M2 missing expected accepted"

	say "-- M3 the SPA shell behind a 200 is refused even if lengths matched"
	printf '<!DOCTYPE html><html></html>\n' >"$d/shell.json"
	FAILED=0
	compare_files "$d/shell.json" "$d/served.yaml" "$d/shell.json" "$d/good.yaml" >/dev/null 2>&1
	[ "$FAILED" -gt 0 ] && ok "M3 HTML shell refused (a 200 is not a route)" || bad "M3 HTML shell accepted"

	say "-- M4 zero bytes is refused"
	: >"$d/empty.json"
	FAILED=0
	compare_files "$d/empty.json" "$d/served.yaml" "$d/empty.json" "$d/good.yaml" >/dev/null 2>&1
	[ "$FAILED" -gt 0 ] && ok "M4 empty served refused" || bad "M4 empty served accepted"

	say "-- M5 a missing image is a refusal (proves 'cannot start' is not a skip)"
	FAILED=0
	( EXPECT_JSON="$d/good.json" EXPECT_YAML="$d/good.yaml" verify_image "ragx-spec-does-not-exist:0.0.0" ) >/dev/null 2>&1
	[ $? -ne 0 ] && ok "M5 missing image refused" || bad "M5 missing image accepted"

	say "-- M6 a platform that cannot be pulled is refused, and the platform is named"
	local out
	out=$( EXPECT_JSON="$d/good.json" EXPECT_YAML="$d/good.yaml" PLATFORMS=(linux/arm64) 		verify_image "ragx-spec-does-not-exist:0.0.0" 2>&1 )
	if printf '%s' "$out" | grep -q "cannot be pulled" && printf '%s' "$out" | grep -q "linux/arm64"; then
		ok "M6 unpullable platform refused and named (so --platform is not decoration)"
	else
		bad "M6 unpullable platform was not refused with its platform named"
	fi


	rm -rf "$d"
	WORK=""
	say ""
	if [ "$FAILED" -ne 0 ]; then printf 'SELFTEST FAILED\n' >&2; exit 1; fi
	ok "6/6 refusals behave (1 control + 5 refusals)"
}

# ── gate: build the host image from this tree, then verify it ──────────────
gate() {
	local version=${1:-v0.0.0-image}
	local ref=${RGX_IMAGE_SPEC_IMAGE:-ragflow-x:gate-image}
	require_docker
	# `gate` runs before any `verify` in the same process, so the work dir has to be
	# created here too (it also holds the build log, which has to be readable when the
	# build fails — a redirect into "" is the kind of bug that only shows up on Windows).
	[ -n "${WORK:-}" ] && [ -d "$WORK" ] || WORK=$(mktemp -d)
	if [ -z "${RGX_IMAGE_SPEC_IMAGE:-}" ]; then
		# Built from the working tree on purpose: the point is "the image a
		# person would build right now carries the committed bytes". A dirty
		# tree shows up as a mismatch, not as a silent pass.
		say "building $ref from this tree (docker/Dockerfile)…"
		MSYS_NO_PATHCONV=1 docker build -f docker/Dockerfile \
			--build-arg "GOPROXY=${RGX_IMAGE_SPEC_GOPROXY:-https://proxy.golang.org,direct}" \
			--build-arg "VERSION=$version" -t "$ref" . >"$WORK/build.log" 2>&1 ||
			{ tail -25 "$WORK/build.log" >&2; die "docker build failed (see above)"; }
	fi
	verify_image "$ref"
}

main() {
	local cmd=${1:-}
	[ $# -gt 0 ] && shift
	case "$cmd" in
	compare)
		[ $# -eq 4 ] || die "compare needs <served-json> <served-yaml> <expect-json> <expect-yaml>"
		WORK=$(mktemp -d)
		compare_files "$1" "$2" "$3" "$4"
		[ "$FAILED" -eq 0 ] || exit 1
		;;
	verify)
		local ref=""
		while [ $# -gt 0 ]; do
			case "$1" in
			--json) EXPECT_JSON=$2; shift 2 ;;
			--yaml) EXPECT_YAML=$2; shift 2 ;;
			--platform) PLATFORMS+=("$2"); shift 2 ;;
			--port) HOST_PORT=$2; shift 2 ;;
			--timeout) WAIT_SECONDS=$2; shift 2 ;;
			--keep) KEEP=1; shift ;;
			-*) die "unknown option: $1" ;;
			*) [ -z "$ref" ] || die "verify takes one image reference"; ref=$1; shift ;;
			esac
		done
		[ -n "$ref" ] || die "verify needs an image reference"
		require_docker
		materialize_expect
		verify_image "$ref"
		[ "$FAILED" -eq 0 ] || exit 1
		ok "image carries the committed contract ($ref)"
		;;
	gate) gate "${1:-}" ;;
	--selftest | selftest) selftest ;;
	*)
		sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
		exit 2
		;;
	esac
}

main "$@"
