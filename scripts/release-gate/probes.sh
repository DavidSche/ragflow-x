#!/usr/bin/env bash
# s16.probes-selfcheck — the live probes are in the tree, and they carry no credential.
#
# Doc/304 §7 item 12: the two scripts that produced T02's candidate pool and T16's drill
# evidence lived in `localenv/probes/`, which `.gitignore` excludes, so a clone could read
# doc/305 §2.3 and §4.3 and could not run one command they quote. v1.28 moved them to
# `scripts/probes/`. That trade has exactly one failure mode worth a gate row: a tracked
# file with a working password in it, which is how a "reproducible" probe becomes a
# credential leak that nobody notices because the file is otherwise useful.
#
# A `go test` cannot cover this (no Go), and grep alone would be a list of forbidden
# strings that ages badly. So this check holds four things instead:
#
#   ① no credential is stored: no non-empty literal assigned to a *PASSWORD*/*SECRET*
#     /*TOKEN* name, and none of the drill's historical strings, in any tracked file of
#     `scripts/probes/`;
#   ② a missing credential is a *usage* sentence (exit 2) that names the variable, and it
#     fires **before** the first HTTP call — so the failure is never blamed on a host;
#   ③ `--offline` still prints the whole contract: every requirement, every step. A step
#     silently deleted from `STEPS_DOC` is a step nobody notices the drill no longer runs;
#   ④ the two probes share `probe_common` and not each other — T02's seed probe importing
#     the T16 drill is the coupling item 12 named, and it is what made the pair
#     un-runnable outside `localenv/` in the first place.
#
# What this does NOT cover, said out loud: it never contacts a host. The live readings are
# doc/305 §2.3 (pool) and §4.3 (11/11 PASS), not this script's.
#
# Usage:
#   bash scripts/release-gate/probes.sh             # the check (hermetic, ~2s)
#   bash scripts/release-gate/probes.sh --selftest  # prove the check can fail
#
# PROBES_DIR exists for `--selftest` only: proving a refusal must not require editing a
# tracked file.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

DIR="${PROBES_DIR:-scripts/probes}"
T16="$DIR/t16_live_drill.py"
T02="$DIR/t02_seed_gold_pool.py"
COMMON="$DIR/probe_common.py"
README="$DIR/README.md"

# `command -v python3` on Windows can resolve to the Store alias; the first one that runs
# `-c pass` wins. Same discipline as `pick_python()` in scripts/release-gate.sh.
PY="${PROBES_PYTHON:-}"
if [ -z "$PY" ]; then
  for c in python3 python py; do
    if command -v "$c" >/dev/null 2>&1 && "$c" -c 'pass' >/dev/null 2>&1; then
      PY="$c"; break
    fi
  done
fi

fail=0
say()  { printf '%s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*"; fail=1; }
good() { printf 'ok    %s\n' "$*"; }

# ── selftest: four mutations, each of which must be refused ──────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a re-added password, a hidden one, a deleted step, a re-coupled pair)"
  cp "$COMMON" "$T16" "$T02" "$README" "$tmp"/ 2>/dev/null

  # ① a credential put back: a name that ends in _PASSWORD with a working literal. This is
  # the exact string v1.28 removed, and nothing about the probe's behaviour changes.
  printf '\nDEFAULT_PASSWORD = "RgxTest#2026a"\n' >> "$tmp/t16_live_drill.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'stores a credential'; then
    say "SELFTEST FAIL: a password literal in a tracked probe was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a password literal in a tracked probe -> refused"
  cp "$T16" "$tmp/t16_live_drill.py"

  # ② a step deleted from the contract. The drill would still run S1–S10 and still print a
  # summary; only the *published* contract would be short one step.
  sed '/("S8", "acknowledge one finding/d' "$T16" > "$tmp/t16_live_drill.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'steps in the contract'; then
    say "SELFTEST FAIL: a deleted step went unnoticed"
    say "$out"
    exit 1
  fi
  say "ok      a step deleted from the printed contract -> refused"
  cp "$T16" "$tmp/t16_live_drill.py"

  # ③ the coupling item 12 named: T02 importing the drill again.
  printf '\nimport t16_live_drill  # noqa: F401\n' >> "$tmp/t02_seed_gold_pool.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'must not import the drill'; then
    say "SELFTEST FAIL: T02 importing the drill was accepted"
    say "$out"
    exit 1
  fi
  say "ok      T02 importing the T16 drill -> refused"
  cp "$T02" "$tmp/t02_seed_gold_pool.py"

  # ④ the hole this check had on its first day: a literal hidden inside a `${VAR:-literal}`
  # default. Its value contains a `$`, so the direct-assignment shape skips it — a probe
  # could have kept a password and still passed. Re-adding one must be refused.
  printf '
DEFAULT_PASS="${RGX_VERIFY_PASS:-RgxTest#2026a}"
' >> "$tmp/t16_live_drill.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'stores a credential'; then
    say "SELFTEST FAIL: a literal hidden in a \${VAR:-literal} default was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a literal hidden in a \${VAR:-literal} default -> refused"

  say "SELFTEST OK"
  exit 0
fi

say "live probes (doc/304 §7 item 12) — tracked, hermetic half only"

# ── ⓪ the files exist and compile ───────────────────────────────────────────
missing=0
for f in "$COMMON" "$T16" "$T02" "$README"; do
  if [ -f "$f" ]; then
    good "$f is in the tree"
  else
    bad "$f is missing: the evidence in doc/305 would be un-runnable again"
    missing=1
  fi
done
if [ "$missing" -ne 0 ]; then
  say ""
  say "probes: FAIL"
  exit 1
fi
if [ -z "$PY" ]; then
  bad "no python on PATH: the probes cannot be compiled, and 'I did not compile' is not 'they compile'"
else
  if out="$("$PY" -m py_compile "$COMMON" "$T16" "$T02" 2>&1)"; then
    good "all three compile ($PY -m py_compile)"
  else
    bad "a probe does not compile"
    printf '%s\n' "$out" | tail -5
  fi
fi

# ── ① no credential is stored ───────────────────────────────────────────────
# The shape, not a word list: `SOMETHING_PASSWORD = "…"` with a non-empty literal. A
# requirement resolved from the environment (`os.environ.get("…", "")`) has no literal and
# is what every credential here must look like.
# Two shapes, because a literal hides in two places: assigned directly, or hidden inside a
# `${VAR:-literal}` default (the value then contains a `$`, so the first pattern skips it —
# that hole was found by re-adding the old default to another script and watching this
# check stay green).
secret_hits="$(
  { grep -nE '^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN)[A-Za-z0-9_]*[[:space:]]*[:=][[:space:]]*"[^"$]+"'       "$COMMON" "$T16" "$T02" 2>/dev/null || true
    grep -nE '^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN)[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*"\$\{[A-Za-z_][A-Za-z0-9_]*:-[^}]'       "$COMMON" "$T16" "$T02" 2>/dev/null || true
  } | sed '/^$/d')"
if [ -z "$secret_hits" ]; then
  good "no credential literal is assigned in any probe (the shape check, not a word list)"
else
  bad "a probe stores a credential"
  printf '%s\n' "$secret_hits" | head -5
fi
# The drill's own history, by name: these are the strings v1.28 removed, and a recurrence
# means someone pasted the old `localenv` copy back in.
hist_hits="$(grep -nE 'RgxTest|RgxDrill|zhixiang@|lictor' "$COMMON" "$T16" "$T02" "$README" 2>/dev/null || true)"
if [ -z "$hist_hits" ]; then
  good "none of the historical drill credentials appear (RgxTest / RgxDrill / zhixiang@)"
else
  bad "a historical drill credential is back in the tree"
  printf '%s\n' "$hist_hits" | head -5
fi
# A credential must not reach the evidence either: the drill writes `summary.json` and
# `summary.md` under `tmp/`, and a password in an artifact is a password in a chat message.
# The shape checked is "the password flows to exactly two places — the login and nothing
# else", which is what a `"password": args.password` added to a summary dict would break.
for f in "$T16" "$T02"; do
  base="$(basename "$f")"
  uses="$(grep -c 'args\.password' "$f" || true)"
  leak="$(grep -n 'args\.password' "$f" | grep -iE 'dump|summary|json\.dumps|evidence' || true)"
  if [ "${uses:-0}" -ge 1 ] && [ -z "$leak" ]; then
    good "$base keeps the password to the login path (${uses} reference(s), none in evidence)"
  else
    bad "$base lets the password reach the evidence, or never uses it (uses=${uses:-0})"
  fi
done

# ── ② a missing credential is a usage sentence, before the network ──────────
for spec in "$T16:RGX_VERIFY_PASS:--only S1" "$T02:RGX_VERIFY_PASS:"; do
  file="${spec%%:*}"; rest="${spec#*:}"; var="${rest%%:*}"; extra="${rest#*:}"
  base="$(basename "$file")"
  # `env -u` on every credential variable: the check must not depend on the operator's shell
  # having exported something (that is how a default silently replaces a requirement).
  # shellcheck disable=SC2086
  out="$(env -u RGX_VERIFY_PASS -u RGX_VERIFY_USER -u RGX_DRILL_SSH_PASS -u RGX_BASE \
           "$PY" "$file" $extra 2>&1)"; rc=$?
  if [ "$rc" -eq 2 ]; then
    good "$base without a password: exit 2 (usage, not a product result)"
  else
    bad "$base without a password: exit $rc, want 2"
    printf '%s\n' "$out" | tail -3
  fi
  if printf '%s' "$out" | grep -q "$var"; then
    good "$base names $var in the refusal: the operator is told what to export"
  else
    bad "$base does not name $var: 'a credential is required' without saying which one"
  fi
  # The refusal must come before the first request, or it is a login failure wearing a
  # usage message — and a wrong host would then look like a missing variable.
  if printf '%s' "$out" | grep -qiE 'environment unavailable|ConnectionError|Max retries'; then
    bad "$base tried the network before refusing"
  else
    good "$base refuses before the first HTTP call"
  fi
done

# ── ③ --offline still prints the whole contract ─────────────────────────────
check_offline() {  # file, want_steps, want_vars
  local file="$1" want_steps="$2" want_vars="$3" base out rc got
  base="$(basename "$file")"
  out="$(env -u RGX_VERIFY_PASS -u RGX_DRILL_SSH_PASS "$PY" "$file" --offline 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    good "$base --offline exits 0 (a laptop with no host can read the contract)"
  else
    bad "$base --offline exits $rc, want 0"
    printf '%s\n' "$out" | tail -3
    return
  fi
  got="$(printf '%s\n' "$out" | grep -cE '^  [SK][0-9]+ ' || true)"
  if [ "${got:-0}" = "$want_steps" ]; then
    good "$base prints its ${want_steps} steps in the contract"
  else
    bad "$base prints ${got:-0} steps in the contract, want ${want_steps}"
  fi
  for v in $want_vars; do
    if printf '%s\n' "$out" | grep -q "$v"; then
      good "$base declares $v"
    else
      bad "$base does not declare $v: the requirement would be discovered at failure time"
    fi
  done
  if printf '%s\n' "$out" | grep -q 'nothing was contacted'; then
    good "$base says it contacted nothing: offline is not a quiet live run"
  else
    bad "$base --offline does not say it contacted nothing"
  fi
  if printf '%s\n' "$out" | grep -q '3 = ran but skipped'; then
    good "$base publishes exit 3 as 'ran but skipped' (a skip is not a pass)"
  else
    bad "$base does not publish exit 3: a partial run could be read as a pass"
  fi
}

check_offline "$T16" 11 "RGX_VERIFY_BASE RGX_VERIFY_PASS RGX_DRILL_SSH_PASS"
check_offline "$T02" 5 "RGX_BASE RGX_VERIFY_PASS"

# ── ④ the pair is decoupled, and both sit on probe_common ───────────────────
for f in "$T16" "$T02"; do
  base="$(basename "$f")"
  if grep -qE '^from probe_common import|^    .*probe_common' "$f"; then
    good "$base imports probe_common"
  else
    bad "$base does not import probe_common: the client would be duplicated"
  fi
done
if grep -qE '(^|[^_a-z])import t16_live_drill|from t16_live_drill' "$T02"; then
  bad "$T02 must not import the drill (doc/304 §7 item 12: that coupling is why the pair could not leave localenv/)"
else
  good "T02 does not import the T16 drill (one shared client, no shared drill)"
fi
# The skip must stay a distinct sentence in the drill: a skipped step that prints as a pass
# is the failure mode exit code 3 exists to prevent.
if grep -q 'CaseSkip' "$T16"; then
  good "the drill still has a SKIP that is not a PASS"
else
  bad "the drill lost CaseSkip: a step whose input was missing would have to be faked"
fi
# …and the reader has to be able to find the live readings from the probes themselves.
for doc in '305' 'item 12' 'RGX_VERIFY_PASS'; do
  if grep -qF -- "$doc" "$README"; then
    good "README names $doc"
  else
    bad "README does not name $doc"
  fi
done

say ""
if [ "$fail" -ne 0 ]; then
  say "probes: FAIL"
  exit 1
fi
say "probes: PASS (tracked + credential-free; the live readings are doc/305 §2.3 and §4.3, not this script's)"
exit 0
