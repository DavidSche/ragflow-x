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
#     `scripts/probes/` — and the same two shapes over every tracked shell/python in
#     `scripts/` and `test/`, because item 24's tail found that this row only ever looked
#     inside `scripts/probes/` while the two files that *had* a credential lived outside it;
#   ② a missing credential is a *usage* sentence (exit 2) that names the variable, and it
#     fires **before** the first HTTP call — so the failure is never blamed on a host;
#   ③ `--offline` still prints the whole contract: every requirement, every step. A step
#     silently deleted from `STEPS_DOC` is a step nobody notices the drill no longer runs;
#   ④ the probes share `probe_common` and not each other — T02's seed probe importing
#     the T16 drill is the coupling item 12 named, and it is what made the pair
#     un-runnable outside `localenv/` in the first place.
#
# v1.41 adds a third probe (`w4_artifact_landing.py`) to the same contract. It measures
# W-4 (doc/304 §7 item 23) and its whole point is that "not yet" must stay distinguishable
# from "done": the four states A/B/C/D are separate readings, and only A+B+C+D together may
# print as 成立 (exit 0). So this check holds one more thing for it — ⑤ its own
# `--selftest` passes, because a verdict nobody has proved can fail is a verdict that
# reads green on the day the writer starts lying.
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
W4="$DIR/w4_artifact_landing.py"
# The W-4 *writer* is deliberately not a variable here: this check must never gain a
# path to it (doc/304 E97 — a gate that can start a compile can lose a dataset). What
# it runs instead is the writer's pure wait classifier, whose tests cannot write.
WAIT="$DIR/w4_wait_state.py"
WAITTEST="$DIR/test_w4_wait_state.py"
# The W-4 *diagnosis* probe. Also read-only, also not a writer: it reads the engine's
# ingestion logs, which is where the Compiler failure reason actually lives (doc/304 E103).
EVID="$DIR/w4_compiler_evidence.py"
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

# ── selftest: fifteen mutations, each of which must be refused ───────────────────
if [ "${1:-}" = "--selftest" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  say "selftest: can this check refuse? (a re-added password, a hidden one, a deleted step, a re-coupled pair, a gate that runs the writer, a diagnosis probe that can write, a repair predicate that widens into 'safe')"
  cp "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" "$README" "$tmp"/ 2>/dev/null

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

  # ⑤ the new probe's own verdict rule: 'not_yet' must never print as 成立. The mutation
  # makes `verdict_d` call an empty revision established — exactly the shape a tired
  # patch takes when someone wants the W-4 row to turn green.
  sed 's/if hinted <= 0:/if False:/' "$W4" > "$tmp/w4_artifact_landing.py"
  out="$("$PY" "$tmp/w4_artifact_landing.py" --selftest 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'failed'; then
    say "SELFTEST FAIL: a W-4 verdict that reports 'not yet' as established was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a W-4 verdict that reports 'not yet' as established -> refused"
  cp "$W4" "$tmp/w4_artifact_landing.py"

  # ⑥ the separation requirement 6 names: a gate line that could start a compile. The
  # mutation makes probes.sh *look like* it runs the writer; nothing is executed (the copy
  # is only read), and the wait-state tests must refuse it.
  # The name is assembled from two pieces on purpose: the test below refuses any single
  # line of *this file* that carries the writer's executable token, so the mutation text
  # itself must not look like such a line.
  W="w4_wiki_"; W="${W}pilot.py"
  { cat "$0"; printf '
%s
' "out=\"\$(\"\$PY\" \"\$DIR/$W\" --status 2>&1)\""; }     > "$tmp/probes-executes-writer.sh"
  out="$(PROBES_SH="$tmp/probes-executes-writer.sh" "$PY" "$WAITTEST" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'must not run the writer'; then
    say "SELFTEST FAIL: a gate line that runs the W-4 writer was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a gate line that would run the W-4 writer -> refused"

  # ⑦ a diagnosis probe that gained a writing verb: the read-only claim is the only reason
  # this file may point at a live dataset, so a `.post(` added anywhere must be refused.
  { cat "$EVID"; printf '

def _post_that_should_not_exist(client, path):
    return client.session.post(path)
'; }     > "$tmp/w4_compiler_evidence.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'can write'; then
    say "SELFTEST FAIL: a diagnosis probe that can write was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a diagnosis probe with a .post( call -> refused"
  cp "$EVID" "$tmp/w4_compiler_evidence.py"

  # ⑧ the guard lifted off the seam: the method is still checked *somewhere* (the function
  # is still defined and still tested), it just no longer runs before the socket. This is
  # the mutation that reads as a harmless refactor.
  sed 's/^        assert_read_only(method) .*$/        pass  # guard removed/'     "$EVID" > "$tmp/w4_compiler_evidence.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'not called above the request seam'; then
    say "SELFTEST FAIL: a read-only guard lifted off the request seam was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a read-only guard lifted off the request seam -> refused"
  cp "$EVID" "$tmp/w4_compiler_evidence.py"

  # ⑨ doc/304 §7 item 34's forbidden downgrade, in its most plausible shape. Delete the
  # "there must be something to repair" clause and the repair predicate authorises a write
  # on a healthy set: it becomes `safe` under a second name, and the S6 dispatch loses the
  # contradiction check that is the whole reason the bind gate exists. The test that must
  # go red is the one named below — not "some test failed".
  sed 's/^    if not failed:$/    if False:/' "$WAIT" > "$tmp/w4_wait_state.py"
  out="$(PROBES_SH="$0" "$PY" "$tmp/test_w4_wait_state.py" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'test_a_clean_set_has_nothing_to_repair'; then
    say "SELFTEST FAIL: a repair predicate that authorises a write with nothing to repair was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a repair predicate that authorises a write with nothing to repair -> refused"
  cp "$WAIT" "$tmp/w4_wait_state.py"

  # ⑩ the other direction, and the one that looks helpful: keep every rule but let a *busy*
  # set be repaired. A second dispatch queues behind the parse that is already running, and
  # the config write in the same action is refused by the engine (code 102). The point of a
  # client-side gate is to refuse before the socket does.
  sed 's/^    if busy:$/    if False:/' "$WAIT" > "$tmp/w4_wait_state.py"
  out="$(PROBES_SH="$0" "$PY" "$tmp/test_w4_wait_state.py" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'test_busy_refuses_the_repair'; then
    say "SELFTEST FAIL: a repair predicate that permits a second dispatch over a running parse was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a repair predicate that permits a write over a running parse -> refused"
  cp "$WAIT" "$tmp/w4_wait_state.py"

  # ⑪ the gap item 24 left open: the credential shapes were only ever looked for inside
  # `scripts/probes/`, so a `${VAR:-literal}` default in `scripts/` or `test/e2e/` had no
  # reader. The mutation is a scratch tree instead of a tracked file (proving a refusal must
  # not require editing one); the walk's *scope* is asserted separately in the normal run —
  # by name, for the two files item 24 names. Mutation plus scope assertion, not either alone.
  mkdir -p "$tmp/scan"
  printf '#!/usr/bin/env bash\nAX_PASS="${RAGRFLOW_X_PASS:-RgxTest#2026a}"\n' > "$tmp/scan/compare.sh"
  out="$(PROBES_SCAN_ROOT="$tmp/scan" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'a tracked script stores a credential'; then
    say "SELFTEST FAIL: a literal hidden in a \${VAR:-literal} default outside scripts/probes was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a literal hidden outside scripts/probes/ -> refused"

  # ⑫ the other half of ⑪: the scope assertion. A walk that quietly stops covering a file
  # is a check that keeps printing `ok` while looking at less — the same failure as an
  # `--only` that selects nothing. Pointing the walk at a list that omits one of item 24's
  # two files must be refused, and by name.
  git ls-files scripts test 2>/dev/null | grep -E '\.(sh|py|mjs|js)$' \
    | grep -vE '^scripts/probes/|^scripts/release-gate/probes\.sh$' \
    | grep -vF 'test/e2e/scenario-templates/e2e_scenario_templates.py' > "$tmp/scan-list.txt"
  out="$(PROBES_SCAN_LIST="$tmp/scan-list.txt" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'no longer covers a file item 24 names'; then
    say "SELFTEST FAIL: a credential walk that stopped covering an e2e script was accepted"
    say "$out"
    exit 1
  fi
  say "ok      a credential walk that lost one of item 24's files -> refused"

  # ⑬ the exemption's width. `WS_PASS` is allowed by variable *and* env name *and* value
  # together; changing the value must stop the exemption matching. Without this mutation the
  # exemption is a name-only allowlist, and a real password could hide behind a fixture's name.
  mkdir -p "$tmp/scan2/scenario-templates"
  sed 's/RGX_E2E_WS_PASS", "E2e#2026pass"/RGX_E2E_WS_PASS", "E2e#2026sealed"/' \
    test/e2e/scenario-templates/e2e_scenario_templates.py > "$tmp/scan2/scenario-templates/e2e_scenario_templates.py"
  if grep -qF 'E2e#2026sealed' "$tmp/scan2/scenario-templates/e2e_scenario_templates.py"; then
    out="$(PROBES_SCAN_ROOT="$tmp/scan2" bash "$0" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'a tracked script stores a credential'; then
      say "SELFTEST FAIL: the WS_PASS exemption accepted a changed value"
      say "$out"
      exit 1
    fi
    say "ok      the WS_PASS exemption after its value changed -> refused"
  else
    say "SELFTEST FAIL: the WS_PASS exemption mutation could not be built (the line moved)"
    exit 1
  fi

  # ⑭ the relaxation E133 measured, widened into the check it replaced. Dropping the
  # "nothing overtook it" filter turns every failure in the log into a contradiction with
  # `fail_count=0`, which is exactly what the probe printed on 2026-11-24 for a dataset
  # whose two documents had both succeeded since: four named failures, all of them ours,
  # reported as someone else's inconsistency. The case that must go red is the overturned
  # pair — not "some case failed".
  sed 's/^                      if id(log) not in overtaken\]$/                      ]/'     "$EVID" > "$tmp/w4_compiler_evidence.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'a later success overtook are historical'; then
    say "SELFTEST FAIL: a probe that reads every logged failure as a live contradiction was accepted"
    say "$out"
    exit 1
  fi
  say "ok      every logged failure read as a live contradiction -> refused"
  cp "$EVID" "$tmp/w4_compiler_evidence.py"

  # ⑮ and the other direction, the one that reads as mercy: call *every* failure historical.
  # Then nothing can ever contradict the summary, and the reading is a wall of tolerance —
  # a dataset that is FAILED right now with `fail_count=0` stops being an inconsistency.
  sed 's/^        return int(chunks or 0) > 0$/        return True/'     "$EVID" > "$tmp/w4_compiler_evidence.py"
  out="$(PROBES_DIR="$tmp" bash "$0" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ] || ! printf '%s' "$out" | grep -q 'a failure nothing overtook still contradicts'; then
    say "SELFTEST FAIL: a probe that treats every failure as overtaken was accepted"
    say "$out"
    exit 1
  fi
  say "ok      every failure treated as overtaken -> refused"
  cp "$EVID" "$tmp/w4_compiler_evidence.py"

  say "SELFTEST OK"
  exit 0
fi

say "live probes (doc/304 §7 item 12) — tracked, hermetic half only"

# ── ⓪ the files exist and compile ───────────────────────────────────────────
missing=0
for f in "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" "$README"; do
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
  if out="$("$PY" -m py_compile "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" 2>&1)"; then
    good "all seven compile ($PY -m py_compile)"
  else
    bad "a probe does not compile"
    printf '%s\n' "$out" | tail -5
  fi
fi

# ── ① no credential is stored ───────────────────────────────────────────────
# The shape, not a word list: `SOMETHING_PASSWORD = "…"` with a non-empty literal. A
# requirement resolved from the environment (`os.environ.get("…", "")`) has no literal and
# is what every credential here must look like.
# Three shapes, because a literal hides in three places: assigned directly, hidden inside a
# `${VAR:-literal}` default (the value then contains a `$`, so the first pattern skips it —
# that hole was found by re-adding the old default to another script and watching this check
# stay green), and hidden as the **default argument** of `os.environ.get(ENV, "literal")`
# (doc/304 E111 — the same hole one language over: the literal sits in the call's second
# argument, which neither of the other two patterns resembles).
# Defined once, here, and reused by (1b) so the two walks cannot disagree about what a
# literal is.
PYHIDDEN='^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN|KEY)[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*os\.environ\.get\([[:space:]]*"[^"]+"[[:space:]]*,[[:space:]]*"[^"$]+'
secret_hits="$(
  { grep -nE '^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN)[A-Za-z0-9_]*[[:space:]]*[:=][[:space:]]*"[^"$]+"'       "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" 2>/dev/null || true
    grep -nE '^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN)[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*"\$\{[A-Za-z_][A-Za-z0-9_]*:-[^}]'       "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" 2>/dev/null || true
    grep -nE '^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(API_KEY|APIKEY|SECRET_KEY|ACCESS_KEY|ENGINE_KEY)[[:space:]]*[:=][[:space:]]*"[^"$]+"'       "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" 2>/dev/null || true
    grep -nE "$PYHIDDEN" "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" 2>/dev/null || true
  } | sed '/^$/d')"
if [ -z "$secret_hits" ]; then
  good "no credential literal is assigned in any probe (the shape check, not a word list)"
else
  bad "a probe stores a credential"
  printf '%s\n' "$secret_hits" | head -5
fi
# The drill's own history, by name: these are the strings v1.28 removed, and a recurrence
# means someone pasted the old `localenv` copy back in.
hist_hits="$(grep -nE 'RgxTest|RgxDrill|zhixiang@|lictor|MPZMtiIw' "$COMMON" "$T16" "$T02" "$W4" "$WAIT" "$WAITTEST" "$EVID" "$README" 2>/dev/null || true)"
if [ -z "$hist_hits" ]; then
  good "none of the historical drill credentials appear (RgxTest / RgxDrill / zhixiang@)"
else
  bad "a historical drill credential is back in the tree"
  printf '%s\n' "$hist_hits" | head -5
fi

# ── ①b the same shapes outside `scripts/probes/` (doc/304 §7 item 24) ────────
# Item 24 removed two literals and said out loud what it did not cover: the shape check
# only walks `scripts/probes/`, so the two files that actually carried a credential —
# `scripts/template-capability-compare.sh` and the e2e scenario script — were guarded by a
# hand grep rather than by this row. Two halves close it, and neither alone would:
#   · the walk covers every tracked shell/python under `scripts/` and `test/`, and *asserts
#     by name* that the two files item 24 names are still in it (a walk that silently shrinks
#     is how a check stops checking);
#   · `--selftest` mutation ⑪ puts a `${VAR:-literal}` default into a scratch tree and
#     requires a refusal.
# One exemption, by exact line shape: `WS_PASS` in the e2e scripts is created *and* used by
# the same file, which item 24 calls a fixture and not a credential. Allowed by variable, env
# name and value all together, so changing any of the three stops the exemption matching. The
# historical-name scan below takes no exemption at all.
SCAN_SKIP='^scripts/probes/|^scripts/release-gate/probes\.sh$'
scan_files() {
  if [ -n "${PROBES_SCAN_LIST:-}" ]; then   # `--selftest` only: prove the scope assertion
    cat "$PROBES_SCAN_LIST"
  elif [ -n "${PROBES_SCAN_ROOT:-}" ]; then   # `--selftest` only: read a scratch tree
    find "$PROBES_SCAN_ROOT" -type f \( -name '*.sh' -o -name '*.py' -o -name '*.mjs' -o -name '*.js' \)
  else
    git ls-files scripts test 2>/dev/null | grep -E '\.(sh|py|mjs|js)$' | grep -vE "$SCAN_SKIP"
  fi
}
SCAN_LIST="$(scan_files)"
if [ -z "${PROBES_SCAN_ROOT:-}" ]; then
  scan_n="$(printf '%s\n' "$SCAN_LIST" | grep -c . || true)"
  scan_lost=""
  for f in scripts/template-capability-compare.sh test/e2e/scenario-templates/e2e_scenario_templates.py; do
    printf '%s\n' "$SCAN_LIST" | grep -qxF "$f" || scan_lost="$scan_lost $f"
  done
  if [ -n "$scan_lost" ]; then
    bad "the credential walk no longer covers a file item 24 names:$scan_lost"
  elif [ "$scan_n" -lt 20 ]; then
    bad "the credential walk covers only $scan_n tracked file(s): a shrinking scope is how this row stops checking"
  else
    good "the credential walk covers $scan_n tracked scripts under scripts/ and test/ (item 24's two files by name)"
  fi
fi
# The three shapes, as in ①, plus the exempt line shape. `grep -n` prints `line:content`, so
# the exemption is matched against that, and the file is prefixed afterwards.
DIRECT='^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN|API_KEY|APIKEY|SECRET_KEY|ACCESS_KEY|ENGINE_KEY)[A-Za-z0-9_]*[[:space:]]*[:=][[:space:]]*"[^"$]+'
HIDDEN='^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*(PASS|PASSWD|PASSWORD|SECRET|TOKEN|API_KEY|APIKEY|SECRET_KEY|ACCESS_KEY|ENGINE_KEY)[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*"\$\{[A-Za-z_][A-Za-z0-9_]*:-[^}]'
EXEMPT='^[0-9]+:WS_PASS = os\.environ\.get\("RGX_E2E_WS_PASS", "E2e#2026pass"\)$'
tree_hits=""
while IFS= read -r f; do
  [ -n "$f" ] || continue
  hits="$( { grep -nE "$DIRECT" "$f" 2>/dev/null || true; grep -nE "$HIDDEN" "$f" 2>/dev/null || true; grep -nE "$PYHIDDEN" "$f" 2>/dev/null || true; } )"
  case "$f" in
    */scenario-templates/*|test/e2e/scenario-templates/*) hits="$(printf '%s\n' "$hits" | grep -vE "$EXEMPT" || true)" ;;
  esac
  [ -n "$hits" ] && tree_hits="$tree_hits$(printf '%s\n' "$hits" | sed "s|^|$f:|")"$'\n'
done <<< "$SCAN_LIST"
if [ -z "$tree_hits" ]; then
  good "no credential literal outside scripts/probes/ either (the same three shapes, item 24's tail)"
else
  bad "a tracked script stores a credential"
  printf '%s\n' "$tree_hits" | sed '/^$/d' | head -5
fi
# The historical names, over the same walk and with no exemption: this is the check that
# would catch `WS_PASS = "RgxTest#2026a"` even though the shape exemption above allows the
# variable name. Two mechanisms, two different questions.
tree_hist=""
while IFS= read -r f; do
  [ -n "$f" ] || continue
  h="$(grep -nE 'RgxTest|RgxDrill|zhixiang@|lictor|MPZMtiIw' "$f" 2>/dev/null || true)"
  [ -n "$h" ] && tree_hist="$tree_hist$(printf '%s\n' "$h" | sed "s|^|$f:|")"$'\n'
done <<< "$SCAN_LIST"
if [ -z "$tree_hist" ]; then
  good "no historical drill credential anywhere in the walk (no exemption applies here)"
else
  bad "a historical drill credential is outside scripts/probes/ too"
  printf '%s\n' "$tree_hist" | sed '/^$/d' | head -5
fi
# A credential must not reach the evidence either: the drill writes `summary.json` and
# `summary.md` under `tmp/`, and a password in an artifact is a password in a chat message.
# The shape checked is "the password flows to exactly two places — the login and nothing
# else", which is what a `"password": args.password` added to a summary dict would break.
if grep -q 'client.login_with_key(api_key)' "$EVID" && ! grep -qE 'json\.dumps\([^)]*api_key|"api_key":' "$EVID"; then
  good "w4_compiler_evidence.py sends the key to the login seam and never into the evidence"
else
  bad "w4_compiler_evidence.py does not keep the API key out of the evidence"
fi
for f in "$T16" "$T02" "$W4"; do
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
for spec in "$T16:RGX_VERIFY_PASS:--only S1" "$T02:RGX_VERIFY_PASS:" "$W4:RGX_VERIFY_PASS:--dataset ds-placeholder" \
            "$EVID:RGX_ENGINE_KEY:--dataset ds-placeholder"; do
  file="${spec%%:*}"; rest="${spec#*:}"; var="${rest%%:*}"; extra="${rest#*:}"
  base="$(basename "$file")"
  # `env -u` on every credential variable: the check must not depend on the operator's shell
  # having exported something (that is how a default silently replaces a requirement).
  # shellcheck disable=SC2086
  out="$(env -u RGX_VERIFY_PASS -u RGX_VERIFY_USER -u RGX_DRILL_SSH_PASS -u RGX_BASE \
           -u RGX_ENGINE_KEY -u RGX_ENGINE_BASE \
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
  out="$(env -u RGX_VERIFY_PASS -u RGX_DRILL_SSH_PASS -u RGX_ENGINE_KEY "$PY" "$file" --offline 2>&1)"; rc=$?
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
check_offline "$W4" 5 "RGX_BASE RGX_VERIFY_PASS"
check_offline "$EVID" 7 "RGX_ENGINE_BASE RGX_ENGINE_KEY"

# ── ④ the pair is decoupled, and both sit on probe_common ───────────────────
for f in "$T16" "$T02" "$W4" "$EVID"; do
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
# ── ⑤ the W-4 probe can prove its own verdicts fail ─────────────────────────
out="$("$PY" "$W4" --selftest 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q 'SELFTEST OK'; then
  good "w4_artifact_landing.py --selftest: $(printf '%s' "$out" | head -1)"
else
  bad "w4_artifact_landing.py --selftest failed (rc=$rc): the W-4 verdicts are unproven"
  printf '%s
' "$out" | tail -5
fi

# ── ⑥ the wait classifier decides, and it can refuse ────────────────────────
# Why here: the pilot may not write under a document whose own state is not idle, and the
# 2026-11-23 loss happened because the old check read `run` and nothing else. The tests are
# a separate file that imports only the pure module, so running them here cannot start a
# compile — that separation is itself one of the tests (`test_probes_sh_never_executes_the_writer`).
out="$(PROBES_SH="$0" "$PY" "$WAITTEST" 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q 'SELFTEST OK'; then
  good "test_w4_wait_state.py: $(printf '%s' "$out" | sed -n 's/.*wait-state selftest: \([0-9]*\) case.*/\1/p') case(s), idle/busy/failed/unknown, timeout fails closed"
else
  bad "test_w4_wait_state.py failed (rc=$rc): the wait classifier's refusals are unproven"
  printf '%s
' "$out" | tail -8
fi

# ── ⑦ the W-4 diagnosis probe can be wrong, and says which way ──────────────
# Its verdict is a sentence about *someone else's* system, so the failure mode that matters
# is confident, not cautious: calling an unclassified message `named` would hand W-4 a
# root cause it does not have. `--selftest` asserts the exact verdict *and* exit code for
# each case, so a permissive classifier turns it red.
out="$("$PY" "$EVID" --selftest 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q 'SELFTEST OK'; then
  good "w4_compiler_evidence.py --selftest: $(printf '%s' "$out" | grep -c '^ok ' ) case(s), named/unknown/no-failure/contradicted kept apart"
else
  bad "w4_compiler_evidence.py --selftest failed (rc=$rc): its verdicts are unproven"
  printf '%s
' "$out" | tail -6
fi

# ── ⑧ the diagnosis probe reads, by construction ────────────────────────────
# doc/304 E103: this probe is allowed to *read why* a compile failed and is not allowed to
# do anything else — no re-ingest, no re-trigger, no delete, no config change. Three
# properties make that checkable without trusting the docstring: exactly one request seam,
# the guard called above it, and no verb-taking helpers anywhere in the file.
seams="$(grep -c 'client\.api(' "$EVID" || true)"
if [ "${seams:-0}" = "1" ]; then
  good "w4_compiler_evidence.py has exactly one request seam (client.api appears once)"
else
  bad "w4_compiler_evidence.py has ${seams:-0} request seams, want 1: the write surface is no longer one place"
fi
if grep -qE '^[[:space:]]+assert_read_only\(method\)' "$EVID"; then
  good "the guard sits above the seam, so a refused method never reaches the socket"
else
  bad "the read-only guard is not called above the request seam"
fi
verbs="$(grep -nE '\.(post|put|patch|delete)\(' "$EVID" || true)"
if [ -z "$verbs" ]; then
  good "no .post/.put/.patch/.delete call exists in the diagnosis probe"
else
  bad "the diagnosis probe can write:"
  printf '%s
' "$verbs" | head -5
fi
if grep -q 'does NOT advance W-4' "$EVID"; then
  good "the probe says out loud that a named cause does not advance A/B/C/D"
else
  bad "the probe does not separate 'cause named' from 'artifact landed'"
fi

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
