# `scripts/probes/` — the probes behind the live evidence in doc/305 and doc/304

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
| `w4_artifact_landing.py` | S1–S5: are compiled artifacts **generated / persisted / readable / on the target revision**? (doc/304 §7 item 23, W-4) | doc/304 §5.3, §7 item 23 |

## The credential contract

The host is configuration; the password is not. `--base` keeps a documented drill
default because a hostname is not a secret, and every credential is read from a flag or
from the environment. A missing one is a **usage** error — exit 2, with a sentence that
names the variable — never a fallback to something that happens to work on the
author's machine:

```bash
export RGX_VERIFY_PASS=…        # console password (required by all three probes)
export RGX_DRILL_SSH_PASS=…     # sudo password, only for T16's psql asserts
```

## W-4 is four states, not one

`w4_artifact_landing.py` exists because "compiled artifacts are missing" was a sentence
that could be read as any of four different situations, and only the last one blocks
T11's treatment arm:

| state | what it means | what it is read from |
| --- | --- | --- |
| A 已生成 | a compilation run reached `confirmed` and the engine reported hash-bound artifacts | the dataset's run list (`state`, `detail.artifact_count`) |
| B 已持久化 | rows exist in `rgx_artifact_projection`, `state=ready`, and each row agrees with the JSON it carries | the artifacts endpoint, row by row (a second transport, not the write response) |
| C 可被读取 | `report.locator_hints_enabled` is effective **and** the dataset-scoped ready rows exist | the settings view plus B |
| D 落在目标修订上 | the named revision's sections actually carry `location_hints` | `GET /reports/:id/revisions/:n/sections` |

Only A+B+C+D may print as `W-4 成立` (exit 0). Anything short of that is exit 3, and a
reading that contradicts another (a `ready` row with no content hash, `confirmed` with
`artifact_count=0`, a row whose run id the run list does not know, hints without a
readable source) is exit 1.

Two limits the probe prints rather than hides: it does **not** recompute
`artifact_hash`/`content_hash` (that encoder is Go's `json.Marshal`; a Python look-alike
would agree with a bug), and it does **not** prove the artifact equals the upstream
payload (`content_md_rendered` is not stored in the row).

And one consequence that reorders the plan: `location_hints_json` is frozen in the
**outline** step, so artifacts that land after a revision was generated can never
retro-fill it. W-4 has to complete *before* T11's treatment generation.

Two consequences worth stating, because both are checks rather than conventions:

* the throwaway tenant account T16's S10 creates gets a password generated per run, so
  no fixed drill string exists anywhere in the tree;
* the ssh password is resolved lazily, inside `psql()`, so a cheap slice
  (`--only S1,S2,S5`) never demands a value it will not use.

The walk is wider than this directory. `s16` scans every tracked shell/python under
`scripts/` and `test/` for the same three shapes — a literal assignment, a `${VAR:-literal}`
default, and a literal as the *default argument* of `os.environ.get(ENV, "literal")`. The
last one is why the walk exists: doc/304 §7 item 24 removed two real console passwords from
files that sit outside `scripts/probes/`, and an older version of this check looked only
here. One exemption is allowed, by variable, env name and value together: the e2e workspace
account (`WS_PASS`), which the same file creates and then logs in with — a fixture, not a
credential. The historical-name scan takes no exemption, so a real password pasted behind
that variable name is still refused.

## The writer and the gate are two files, on purpose

`w4_wiki_pilot.py` is the **writer** (S1 dataset → S2 canvas + published version → S3 upload
→ S4 bind `parse_type=2` + ingest → S5 document terminal → S6 profile + run → **S7 STOP**).
`w4_artifact_landing.py` is the **gate**. Neither imports the other, and the writer never
signs off on its own writes: every postcondition is read back from a different endpoint
than the one that wrote it (the upload is confirmed by the documents listing, the binding by
the same listing's `pipeline_id`, the run by the run list, the products by `GET /artifacts`).

Three readings that shaped it, each bought with a real failure on 2026-11-23:

* **`code=102` is not a retry instruction.** Uploading through ragflow-x starts a parse
  immediately; RAGFlow refuses a config write while a task runs. The pilot waits for the
  task *it caused* to finish. Binding underneath it would have left the run reading a
  half-parsed document.
* **`state: completed` is not "products exist".** A merge pass that deletes products and
  rewrites none still reports `completed`. So S5b gates dispatch on
  `GET /artifacts` `total > 0`, and dispatching against a bare `completed` reproduces
  `upstream_idle_after_dispatch` — which is the *correct* answer to "compiled with nothing
  to bind", not a probe defect.
* **Deleting a document is a write with a blast radius.** Removing the one document that
  failed made the merge pass delete the two surviving documents' products and rebuild none
  (`Loading products for 0 document(s)`), because rebuilding happens in the parse stage that
  had not completed. 27 products became 0. The pilot records that instead of retrying it.

### Which copy of "the model" is the one that runs (E107)

The same question — "which `llm_id` does the Compiler use?" — has two stored answers, and only
one of them is load-bearing:

* `user_canvas.dsl` → the Compiler node's `params.llm_id` — **this is what a run uses**;
* `compilation_template.config.llm_id` — only fills in when the caller leaves the param empty
  (`overlayTemplateConfig`, *the caller's per-call override always wins*).

On 151 the template's value (`baaab8cf…`) has **zero rows** in `tenant_model` and nobody noticed,
because the canvas value shadowed it the whole time. A dead id that never runs is not a config
error you can see in the template. So S2 now:

1. asserts the chat model's **instance** is `radeon-external-proxy-155`, not merely that a model
   with that name exists (a name can be republished onto the single machine behind `:18081`);
2. treats "canvas pins another model" as a *config correction*, not a reason to fail: it
   `PUT`s the DSL (`release:true`) and re-reads it, since `UpdateAgent` writes the canvas row and
   its latest version in one transaction. The patch is **field-level** (`repoint_dsl`), never a
   whole-DSL replace — a replace would silently drop anything a human added to that canvas, and
   it would make "what changed?" unanswerable;
3. picks a reusable version by **content**, not by list order — `/versions` has no documented
   ordering, and reusing a version recorded before the repoint would send the batch straight back
   to the single machine. `dsl_contract()` returns `compiler_llm_id` /
   `compiler_embedding_model` / `compiler_template_group_id` so both the canvas and the version
   can be checked against the same expectation.

None of this advances W-4: it removes one known cause of failure (`504 proxy queue wait exceeded`,
`502 … hard deadline (absolute)` — the batch was landing on `gpustack-local`), and A/B/C/D stay
`not_yet` until an authorised run is read through `w4_artifact_landing.py`.

### What the first authorised run read (E108)

One real compile was run after the repoint. It produced **0 products**, but for the first time the
proof of *where the traffic went* came from the wire instead of from config: over the run's window
the proxy logged **13 `amd` chat calls — one `200` at 63.9 s and twelve killed at the 120 s absolute
deadline — and zero `gpustack` chat calls**. So "the batch left the single machine" is now measured,
not inferred from `base_url`.

The cause it exposed is one number, not a defect: `model_proxy.mjs` gives both listeners the same
`120000` ms absolute deadline, and a wiki page rewrite can need more than that (the 63.9 s success is
inside the historical "healthy 1–71 s" band, the slow tail is not). Killing the slow call also holds
one of the two slots for 120 s, which is why the same run's later trial ended `504 proxy queue wait
exceeded` — *slow* and *killed* are two readings of one constraint. Raising it is an operator
decision (restarting that process drops in-flight requests for every consumer), so the probe keeps
reporting the reading and does not touch it. `POST :18082/v1/embeddings` answers `404
model_not_found`, so embeddings cannot follow the batch to AMD either — they stay behind `:18081`.

`w4_wiki_pilot.py` is deliberately **not** wired into `probes.sh`: that gate runs `--offline`
halves, and this file's offline half is a plan printer, while its live half creates datasets
and spends upstream model time. Wiring it in would let a gate run start a compile.

## Waiting is a decision, so it lives where it can be tested

`w4_wait_state.py` is the wait gate: a pure module (no client, no IO, no writer import) with
exactly four states — `idle`, `busy`, `failed`, `unknown`. `w4_wiki_pilot.py` supplies the
read and turns a refusal into its own failure sentence; `test_w4_wait_state.py` decides
whether the classifier is still right, and `probes.sh` runs that test while remaining unable
to reach the writer at all (the sixth selftest mutation injects a line that would run it,
and the gate has to refuse its own mutation).

The rule it replaced was one line: `run in ("1", "RUNNING")`. On engine 1.0.0 `run` is an
optional, legacy field — `ingestion_status` is authoritative (`NormalizeParseStatus` maps
`COMPLETED`→`3`, `FAILED`→`4`) — so that line said "not busy" for a document the engine had
already given up on (`FAILED`, `chunk_count=0`, `run=0`, which is exactly the row the
2026-11-23 run wrote over). Four states, in this order, and the ordering is the argument:

1. `run` says RUNNING → **busy**, even if a stale `COMPLETED` sits next to it;
2. `FAILED` / `CANCELLED` → **failed** (waiting longer cannot make it safe, so the wait stops);
3. `QUEUED` / `RUNNING` / `PENDING` / … → **busy**;
4. progress `1.0` without any status → **unknown** (`done_without_status`), never `idle`.

`unknown` is not a fifth verdict to be resolved later; it is a refusal. A name the listing
forgot (`document_missing`), a row with no state fields, an unrecognised status, and a
timeout all keep the write from happening. `assess_before_write` adds the contradiction the
old code could not see: upstream reporting `compilation: completed` while its own documents
are FAILED or still parsing is not a tie to be broken by preference — it is refused, and the
contradiction is printed.

Two boundaries worth stating, because both are easy to erode:

* **`compilation: completed` never becomes "products exist".** The classifier returns
  `completed`, and S5b still gates dispatch on `GET /artifacts total > 0` (E97).
* **This is safety, not progress.** A safer wait does not move A/B/C/D off `not_yet`; it only
  means the next controlled attempt cannot repeat the loss by this route.

## "The engine never said why" was a reading error, not a missing log

`w4_compiler_evidence.py` exists because of a wrong sentence in this directory's own history.
The 2026-11-23 pilot came back `FAILED` with an empty `progress_msg`, and that was written
down as "the engine never said why". It did say why; the field we read was the wrong one.

`document.progress_msg` is legacy and is **deliberately not rewritten** when a run fails
(`markTerminalProgress` / `recordTerminalPipelineLog` in
`internal/ingestion/service/ingestion_service.go` write to `pipeline_operation_log`), so the
listing shows the old column — empty, or a stale percentage — while the reason sits in the
operation log and the ingestion event stream. The engine serves both read-only:

```
GET /datasets/{id}/ingestions?log_type=file&document_id={doc}   # why one document failed
GET /datasets/{id}/ingestions/{log_id}/messages?limit&after_id  # the run, event by event
GET /datasets/{id}/ingestions/summary                           # the engine's own tally
```

What it found, in one line each: `504 {"error":"proxy queue wait exceeded"}` after 5 retries,
`502 {"error":"proxy upstream error: hard deadline (absolute)"}`, and
`gpustack embeddings API error: 504 Gateway Timeout`. A saturated model proxy, 199s–2234s per
document. That is an upstream capacity answer, not a Compiler defect and not a corpus problem.

Four design notes, each of them a mistake that was available:

* **Four verdicts, and two of them exit 3.** `NAMED` (0) / `CONTRADICTED` (1) / `UNKNOWN` (3)
  / `NO_FAILURE` (3). Exit 3 on `UNKNOWN` is the point: "this tree cannot answer" and "nothing
  failed" are not the same sentence, and neither is a pass. A probe with an `ok/fail` pair
  would have reported `UNKNOWN` as `ok`.
* **Failure has two spellings on one endpoint.** Dataset-level (Wiki) rows say
  `operation_status: "FAIL"`; file-level rows say `operation_status: "4"`. The first live run
  of this probe read a failed file log as healthy and printed `CONTRADICTED` — the honest
  answer to "summary says 2 failed, logs say none". The fix was a spelling, and the case that
  proves it is in `--selftest`, because a false negative here is silent.
* **The `status` field is not a status.** It read `"1"` on the DONE rows, on the FAIL rows and
  on the documents the tally called failed. It is a document code, not a run code, so the probe
  never uses it to decide anything.
* **Contradiction is compared in one unit.** `summary.fail_count` counts *documents*; the Wiki
  list counts *runs*. Comparing them invents failures out of a three-page run history; the
  contradiction rule therefore uses file-level logs only, joined by `document_id`.

And the sentence the probe prints at the end of every live run, because this is the mistake a
root cause invites: **naming a cause does NOT advance W-4's A/B/C/D.** W-4 is "an artifact
landed on a specific target revision and a second reader can see it", which is
`w4_artifact_landing.py`'s question. A confident cause is not an artifact.

The read-only claim is enforced twice: the transport is a single `send()` seam whose first
statement is `assert_read_only(method)` (so a refused method never reaches the socket), and
`probes.sh` refuses the file if a second `client.api(` appears, if a `.post(`/`.put(`/`.patch(`/
`.delete(` call appears anywhere in it, or if the guard is lifted off the seam. Two of the
gate's selftest mutations are exactly those last two, because a guard that still exists but no
longer runs first reads like a harmless refactor.

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

python scripts/probes/w4_artifact_landing.py --offline            # the contract, exit 0
python scripts/probes/w4_artifact_landing.py --selftest           # 22 verdict assertions
python scripts/probes/w4_artifact_landing.py --dataset <id|name>  # A/B/C, exit 3 while open
python scripts/probes/w4_artifact_landing.py --dataset <id>        --report <id> --revision <n>                               # …and D

python scripts/probes/w4_wiki_pilot.py --dry-run        # S1–S7 plan, contacts nothing, needs no password
python scripts/probes/w4_wiki_pilot.py --status         # read the engine: version, wiki groups, dataset state
python scripts/probes/w4_wiki_pilot.py --apply          # S1–S6 for real, then STOP at S7
python scripts/probes/w4_wiki_pilot.py --apply --reingest  # repair "chunks but no products"

python scripts/probes/w4_compiler_evidence.py --offline                  # the contract, exit 0
python scripts/probes/w4_compiler_evidence.py --selftest                 # 10 verdict assertions
python scripts/probes/w4_compiler_evidence.py --dataset <id|name>        # why did the Compiler stage fail
python scripts/probes/w4_compiler_evidence.py --replay tmp/w4diag/compiler-evidence.json  # re-derive offline
```

`--apply` stops at S7 deliberately: it does not generate the T11 target revision and does not
touch a treatment arm. Those are separate steps an operator authorises after A+B+C hold.

`--offline` is the hermetic half: it prints which values are required and which steps
exist, and contacts nothing. That is what `s16.probes-selfcheck` runs, so a change that
adds a step, drops a requirement or reintroduces a default credential is refused by the
gate instead of by the next operator.

The evidence a live run leaves behind (`tmp/t16-drill-*/summary.md`, `summary.json`,
`tmp/w4diag/compiler-evidence.json`) is **untracked**: the run is reproducible from these
files plus the environment, and the reading of one particular run belongs in doc/305, not in
the tree. `--replay` exists so a verdict can be re-derived from a bundle after the host it was
read on is gone — that is what makes "the cause was named" an auditable claim rather than a
screenshot.
