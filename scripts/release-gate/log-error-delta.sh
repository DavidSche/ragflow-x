#!/usr/bin/env bash
# log-error-delta.sh — did this run leave an error line behind in the deployment log?
#
# Why this exists
# ---------------
# On 2026-11-28 a live run was green 9/9 (doc/324 §7) and the container had logged one
# `level=ERROR` during it: a probe deliberately sent a malformed request, the handler
# recorded it as a delivery failure, and nothing in the gate looked at the log. `l02`
# compares a bundle name; every other live row compares an HTTP outcome. A refusal, a
# panic recovered into a 500, a bookkeeping failure that only reaches the log — all of
# them pass every HTTP-shaped check by construction.
#
# So this reads the deployment log itself, twice: once before the suites and once after.
# What it asserts is the **delta**, not the count. A deployment that has been running for
# weeks carries old errors and is not the subject; "this run added none" is.
#
# Two things it refuses to do
# ---------------------------
#   * it will not treat "nothing was measured" as a pass. No log command, a log command
#     that fails, or a log that shrank between the two reads (rotation, restart) all end
#     in a non-zero exit with a sentence saying which one happened.
#   * it will not count what it did not define. `RGX_GATE_LOG_ERROR_PATTERN` is a fixed
#     string (not a regexp): the gate does not get to decide that a WARN is an error, and
#     a shipped default that quietly matches nothing would be the worst outcome here —
#     the `--selftest` therefore proves a matching pattern *does* match.
#
# Usage
#   bash scripts/release-gate/log-error-delta.sh baseline <outdir>
#   bash scripts/release-gate/log-error-delta.sh compare  <outdir>
#   bash scripts/release-gate/log-error-delta.sh --selftest
#
# Environment
#   RGX_GATE_LOG_CMD             command whose stdout is the deployment log
#                                (e.g. `docker logs --since 30m rgx-test-app`). It is a
#                                command, not a path: the gate runs on a workstation while
#                                the log lives on the host, in a container, or in a store.
#   RGX_GATE_LOG_ERROR_PATTERN   fixed string counted as an error line
#                                (default: `"level":"ERROR"`, this service's JSON logs)
#
# Exit codes: 0 the delta is zero / baseline written, 1 the delta is not zero or is not
# measurable, 2 usage error or missing environment.
set -uo pipefail

MODE="${1:-}"
OUT="${2:-}"
PATTERN="${RGX_GATE_LOG_ERROR_PATTERN:-\"level\":\"ERROR\"}"
BASELINE_FILE="log_error_baseline"
DELTA_FILE="log_error_delta.tsv"

usage() {
  printf 'usage: %s baseline <outdir> | compare <outdir> | --selftest\n' "$(basename "$0")" >&2
}

# scan writes the log to a file and echoes how many lines carry PATTERN.
# Echoes "-1" when the log itself could not be read: the caller decides what that means,
# and no caller is allowed to read it as "zero errors".
scan() { # scan <dest> -> prints count or -1
  local dest="$1" rc
  bash -c "$RGX_GATE_LOG_CMD" > "$dest" 2>&1
  rc=$?
  if [ "$rc" != "0" ]; then
    printf -- '-1\n'
    printf 'log command exited %s; its output so far is in %s\n' "$rc" "$dest" >&2
    return 0
  fi
  # `grep -c` prints 0 and exits 1 when nothing matches; the count is what we want either way.
  { grep -F -c -- "$PATTERN" "$dest" || true; }
}

require_env() {
  if [ -z "${RGX_GATE_LOG_CMD:-}" ]; then
    printf 'RGX_GATE_LOG_CMD is not set: this check reads the deployment log and cannot guess where it is\n' >&2
    printf '  set it to a command that prints the log, e.g. RGX_GATE_LOG_CMD="docker logs --since 30m <container>"\n' >&2
    return 2
  fi
  return 0
}

do_baseline() {
  [ -n "$OUT" ] || { usage; return 2; }
  require_env || return 2
  local dest="$OUT/log_error_scan.tsv" count
  mkdir -p "$OUT" || return 2
  count=$(scan "$dest")
  if [ "$count" = "-1" ]; then
    printf 'baseline not written: the log could not be read, so there is nothing to compare against\n' >&2
    return 1
  fi
  printf '%s\n' "$count" > "$OUT/$BASELINE_FILE"
  printf 'baseline: %s line(s) carry the pattern; log captured in %s\n' "$count" "$dest"
  return 0
}

do_compare() {
  [ -n "$OUT" ] || { usage; return 2; }
  require_env || return 2
  local base_file="$OUT/$BASELINE_FILE"
  if [ ! -f "$base_file" ]; then
    printf 'no baseline at %s: run the baseline step before the suites (a delta without a before is not a measurement)\n' "$base_file" >&2
    return 1
  fi
  local before after delta dest="$OUT/log_error_scan_after.tsv"
  before=$(tr -dc '0-9' < "$base_file")
  if [ -z "$before" ]; then
    printf 'the baseline at %s carries no number: it was not written by this script
' "$base_file" >&2
    return 1
  fi
  after=$(scan "$dest")
  if [ "$after" = "-1" ]; then
    printf 'the delta is not measurable: the log could not be read on the second pass\n' >&2
    return 1
  fi
  if [ "$after" -lt "$before" ]; then
    # Rotation, restart, or a shorter `--since`: the two reads no longer describe the same
    # log, so their difference describes nothing. Saying "no new errors" here would be a
    # measurement that does not exist.
    printf 'the log shrank between the two reads (%s -> %s lines): it was truncated, rotated or restarted\n' "$before" "$after" >&2
    printf '  the delta cannot be interpreted; re-run with a log window that covers the whole gate run\n' >&2
    printf '%s\t%s\t%s\n' "$before" "$after" "shrank" > "$OUT/$DELTA_FILE"
    return 1
  fi
  delta=$((after - before))
  printf '%s\t%s\t%s\t%s\n' "$before" "$after" "$delta" "$PATTERN" > "$OUT/$DELTA_FILE"
  if [ "$delta" -eq 0 ]; then
    printf 'ERROR lines: before=%s after=%s (delta 0)\n' "$before" "$after"
    return 0
  fi
  printf 'ERROR lines: before=%s after=%s (delta %s) -- this run added them\n' "$before" "$after" "$delta" >&2
  printf -- '---- matching lines (tail) ----\n' >&2
  grep -F -- "$PATTERN" "$dest" | tail -n 5 | cut -c1-400 | sed 's/^/  /' >&2
  return 1
}

# ── selftest ────────────────────────────────────────────────────────────────
# Four states, because the interesting failures here are not "it counted wrong":
#   (1) an unchanged log is a pass                     → exit 0
#   (2) one added error line is a failure, and the offending line is printed → exit 1
#   (3) a log that shrank is NOT a pass                → exit 1, says "shrank"
#   (4) a log command that fails / is unset is NOT a pass → exit 1 / exit 2
selftest() {
  local tmp fails=0
  tmp=$(mktemp -d 2>/dev/null) || { printf 'selftest: cannot create a temp dir
' >&2; return 2; }
  local self
  self="$PWD/scripts/release-gate/log-error-delta.sh"

  run_case() { # run_case <name> <expected-exit> <expected-substring> -- <function>
    local name="$1" want_rc="$2" want_text="$3"
    shift 4
    local out rc
    out=$("$@" 2>&1)
    rc=$?
    if [ "$rc" != "$want_rc" ]; then
      printf 'FAIL  %s: exit %s, want %s
%s
' "$name" "$rc" "$want_rc" "$out"
      fails=$((fails + 1))
      return
    fi
    if [ -n "$want_text" ] && ! printf '%s' "$out" | grep -F -q -- "$want_text"; then
      printf 'FAIL  %s: output does not contain %s
%s
' "$name" "$want_text" "$out"
      fails=$((fails + 1))
      return
    fi
    printf 'ok    %s
' "$name"
  }

  # A log with no error line, and the same log plus one.
  local stable
  stable='echo "2026-11-28T00:00:00Z INFO started"; echo "2026-11-28T00:00:01Z INFO done"'

  case_1() { # unchanged log → pass
    mkdir -p "$tmp/1"
    RGX_GATE_LOG_CMD="$stable" bash "$self" baseline "$tmp/1" >/dev/null 2>&1 || return 9
    RGX_GATE_LOG_CMD="$stable" bash "$self" compare "$tmp/1"
  }

  # The counted string is the subject, so the fixtures below choose one the log actually
  # carries (`ERROR`), and case 7 proves the shipped default matches this service's shape.
  case_2() { # one added error line → fail, and the line is printed
    mkdir -p "$tmp/2"
    ( export RGX_GATE_LOG_ERROR_PATTERN=ERROR
      RGX_GATE_LOG_CMD="$stable" bash "$self" baseline "$tmp/2" >/dev/null 2>&1 || return 9
      RGX_GATE_LOG_CMD="$stable; echo '2026-11-28T00:00:02Z ERROR boom'" bash "$self" compare "$tmp/2" )
  }

  case_3() { # the log shrank → unmeasurable, NOT a pass
    mkdir -p "$tmp/3"
    ( export RGX_GATE_LOG_ERROR_PATTERN=ERROR
      RGX_GATE_LOG_CMD="$stable; echo 'a ERROR b'; echo 'c ERROR d'" bash "$self" baseline "$tmp/3" >/dev/null 2>&1 || return 9
      RGX_GATE_LOG_CMD='echo "a ERROR b"' bash "$self" compare "$tmp/3" )
  }

  case_4() { # the log command fails → not a silent zero
    mkdir -p "$tmp/4"
    RGX_GATE_LOG_CMD='echo "half a log"; exit 7' bash "$self" baseline "$tmp/4"
  }

  case_5() { # no log command → usage error naming the variable
    mkdir -p "$tmp/5"
    ( unset RGX_GATE_LOG_CMD; bash "$self" baseline "$tmp/5" )
  }

  case_6() { # a compare without a baseline measures nothing
    mkdir -p "$tmp/6"
    RGX_GATE_LOG_CMD="$stable" bash "$self" compare "$tmp/6"
  }

  case_7() { # the shipped default pattern really matches this service's log shape
    mkdir -p "$tmp/7"
    ( unset RGX_GATE_LOG_ERROR_PATTERN
      RGX_GATE_LOG_CMD='echo "{\"level\":\"INFO\"}"; echo "{\"level\":\"ERROR\"}"'         bash "$self" baseline "$tmp/7" >/dev/null 2>&1 || return 9
      cat "$tmp/7/$BASELINE_FILE" )
  }

  run_case "an unchanged log has delta 0"                        0 "delta 0" -- case_1
  run_case "one new error line fails the delta"                  1 "ERROR boom" -- case_2
  run_case "a log that shrank is unmeasurable"                   1 "shrank" -- case_3
  run_case "a failing log command is not a zero"                 1 "could not be read" -- case_4
  run_case "no RGX_GATE_LOG_CMD is a usage error"                2 "RGX_GATE_LOG_CMD is not set" -- case_5
  run_case "a compare without a baseline is refused"             1 "no baseline" -- case_6
  run_case "the default pattern counts this service's ERROR line" 0 "1" -- case_7

  rm -rf "$tmp"
  if [ "$fails" -ne 0 ]; then
    printf 'selftest: %s case(s) failed
' "$fails" >&2
    return 1
  fi
  printf 'selftest: 7 cases, the delta can fail
'
  return 0
}

case "$MODE" in
  baseline) do_baseline ;;
  compare) do_compare ;;
  --selftest) selftest ;;
  *) usage; exit 2 ;;
esac
