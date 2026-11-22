# `scripts/probes/` — the two probes whose evidence is in doc/305

The release gate's static tier (`bash scripts/release-gate.sh --tier static`) is
hermetic: no host, no database, no model. The *live* half of the plan's evidence —
T02's candidate pool and T16's drill — needs a deployed ragflow-x, and until v1.28 the
scripts that produced it lived in `localenv/probes/`, which `.gitignore` excludes. A
clone could read doc/305 §2.3 and §4.3 and could not run a single one of the commands
they quote: the evidence was in the repository, the instrument was not
(doc/304 §7 item 12, drift row E82).

Moving them in has one price, and it is the reason this directory has a gate row of its
own: **no tracked file may contain a credential.**

| file | what it is | evidence it produced |
| --- | --- | --- |
| `probe_common.py` | one HTTP client, one exit-code contract, and the credential table | — |
| `t16_live_drill.py` | S1–S11 against a deployed instance (doc/301 §11.1 T16) | doc/305 §4.3, 11/11 PASS |
| `t02_seed_gold_pool.py` | K1–K5: widen the candidate scan, publish one revision per template, restore the knob (doc/304 §5.3) | doc/305 §2.3 |

## The credential contract

The host is configuration; the password is not. `--base` keeps a documented drill
default because a hostname is not a secret, and every credential is read from a flag or
from the environment. A missing one is a **usage** error — exit 2, with a sentence that
names the variable — never a fallback to something that happens to work on the
author's machine:

```bash
export RGX_VERIFY_PASS=…        # console password (required by both probes)
export RGX_DRILL_SSH_PASS=…     # sudo password, only for T16's psql asserts
```

Two consequences worth stating, because both are checks rather than conventions:

* the throwaway tenant account T16's S10 creates gets a password generated per run, so
  no fixed drill string exists anywhere in the tree;
* the ssh password is resolved lazily, inside `psql()`, so a cheap slice
  (`--only S1,S2,S5`) never demands a value it will not use.

## Exit codes

The four sentences are the same everywhere in this repository:

| code | meaning |
| --- | --- |
| 0 | every step PASS |
| 1 | at least one step FAIL (a named assertion) |
| 2 | the environment is unusable, or a required value is missing |
| 3 | ran without failures but **skipped** a step |

`3` is the one that matters: a `--only` slice, or a step whose input an earlier step
never produced, is *not* a pass. A partial run cannot be quoted as "the drill passed".

## Running them

```bash
python scripts/probes/t16_live_drill.py --offline     # the contract, no network, exit 0
python scripts/probes/t16_live_drill.py               # S1–S11
python scripts/probes/t16_live_drill.py --only S1,S3  # cheap slice, exits 3 unless --only covers all

python scripts/probes/t02_seed_gold_pool.py --offline # the contract, no network, exit 0
python scripts/probes/t02_seed_gold_pool.py --dry-run # read the host, change nothing
python scripts/probes/t02_seed_gold_pool.py           # K1–K5 for real
```

`--offline` is the hermetic half: it prints which values are required and which steps
exist, and contacts nothing. That is what `s16.probes-selfcheck` runs, so a change that
adds a step, drops a requirement or reintroduces a default credential is refused by the
gate instead of by the next operator.

The evidence a live run leaves behind (`tmp/t16-drill-*/summary.md`, `summary.json`) is
**untracked**: the run is reproducible from these files plus the environment, and the
reading of one particular run belongs in doc/305, not in the tree.
