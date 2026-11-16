# Scenario Templates: A Hands-On Guide

> Applies to RAGFlow-X v0.6.1｜UI language: English｜Who this is for: workspace admins (`tenant_admin`) and
> build teams with the "authoring" permission

This guide only covers **where to click, what you will see, and what to do about it**. It does not explain
internals and does not require calling the API; every button and badge name below is the real UI text,
and §9 maps the technical vocabulary onto it. Following it end to end takes about ten minutes and leaves you
with an assistant that answers questions.

---

## 0. At a glance: the eight things you actually do

| # | Where | What | You know it worked when |
|---|---|---|---|
| 1 | Left nav → "Dataset" | Create a dataset, upload a document | the document is listed |
| 2 | Dataset → document panel | Select the document → "Parse selected" | the "Chunks" column goes from 0 to >0 |
| 3 | Left nav → "Scenario template" | Pick the scenario that matches the job | the card is highlighted with its description |
| 4 | Wizard step 2 "Knowledge and capability check" | Tick the dataset → "Run capability check" | the capability result panel appears, one badge per dataset |
| 5 | Same step | If a repairable item shows up, repair it (§4) | re-run the check: badge turns "Verified", or stays amber with an explanation |
| 6 | Wizard step 3 "App shape and settings" | Name the app, pick the project, optionally pick a model | "Next" becomes clickable |
| 7 | Wizard step 4 "Verify and create" | Review the trial questions and checklist → "Create app" | "App created."; you land on the app |
| 8 | App detail / "Practice run" | Ask one of the trial questions | it answers, and the citation opens the source passage |

Steps 1–2 matter most: **the capability check only answers "is my dataset good enough", it never fills the
dataset for you.** With an empty dataset nothing further works.

---

## 1. Start with a dataset that has content

1. Left nav → "Dataset" → create one with a name you will recognise later (e.g. "Expense & travel policy").
2. Open it → document panel → "Upload document". md/pdf/docx/txt all work.
3. Select the uploaded document → "Parse selected".
4. Wait until the parse status stops changing, then confirm the **"Chunks" column is greater than 0**.

**Why the chunk count is the number to watch**: a template creates a chat/search application, and that
application answers by retrieving chunks. With zero chunks the create step is rejected by the engine
(the error mentions `doesn't own parsed file`). The chunk column is also the only reliable progress signal
during a later repair.

Parsing is asynchronous and an occasional parse error toast is normal. **Do not trust a single status read**:
refresh the document list — if chunks are above 0, the parse succeeded.

---

## 2. Pick a scenario

"Scenario template" in the left nav *is* the wizard; the first screen is the scenario card list.

Answer two questions before picking:

- **Do I want question answering, or retrieval/review?** Each card lists the app shapes it supports
  (chat / search / assistant). Unsupported shapes cannot be selected in step 3, and the UI says why.
- **Can my dataset meet its requirements?** Step 4 answers that, but the sample questions on the card are
  a quick check that the scenario really matches your job.

Picking wrong only costs a re-selection; it does not pollute data. Built-in platform templates are
**usable as they are** — you do not have to copy them into your workspace first. Copying only matters when
you want to change the template's own prompts/parameters, which is a platform-admin task outside this guide.

---

## 3. Step 2: bind a dataset and run the capability check

1. Under "Bind datasets", tick the dataset from §1 (at least one).
2. Click "Run capability check".
3. The capability result panel appears on the right, one card per dataset, with a badge per capability.

**The four badges mean exactly this**:

| Badge | Meaning | What to do |
|---|---|---|
| Verified | the capability is ready | nothing |
| Repairable | close; the wizard can fix it here | repair it (§4), then re-run the check |
| Unverified | probing was limited (permissions or endpoint unavailable) — **not a pass** | ask an admin about permissions, or try another dataset |
| Blocked | this deployment **cannot** provide it | pick another template, or an engine that supports it; no setting fixes it |

Three reading rules that prevent most misjudgements:

- **"Repairable" is not a failure**: the template wants one level more than your dataset has, and there is
  usually a repair button right there.
- **"Unverified" does not mean "passed"**: when the system cannot obtain evidence it will not declare success
  for you. It also does not stop you from creating — but do not be surprised later.
- **Read the line with text, not just the badge**: `minimum level → actual level` plus a system note tells you
  what is missing, which is more specific than the badge.

Changing the binding (ticking a different dataset) or changing the app shape **voids the result** — the panel
returns to "no check yet" and you must click "Run capability check" again. Different app shapes require
different capabilities.

---

## 4. What "Apply repair" actually changes

Each repairable item has its own button, and the line next to it names the value it is about to write
(e.g. "switch the chunking method to book and re-parse 12 document(s)"). Know which kind you are clicking:

| Repair item (UI text) | What it touches | What you must confirm first |
|---|---|---|
| Switch to the chunking this template requires | the dataset's **chunking method**, and **re-parses existing documents** | already-parsed documents are re-parsed under the new chunking; old chunks are replaced |
| Turn on metadata-filter pushdown and backfill metadata (button: "Turn on and backfill") | the dataset's **pushdown switch**, and starts metadata backfill | the backfill is asynchronous; the check only turns green afterwards |
| Adjust this dataset's parse / retrieval configuration | nothing — the wizard only gives guidance; you change it in the dataset settings | come back and re-run the check |
| This capability is not available in this deployment | nothing — it is an engine ceiling | pick another template or engine |

### Two checkboxes, deliberately separate

If **other applications are already using this dataset**, the panel first lists them and asks you to tick
"I understand the shared impact". If the repair will **re-parse existing documents**, it asks separately for
"I understand these N document(s) will be re-parsed".

The two are intentionally not one checkbox: a single tick must not authorise two different destructive writes.

### Why it is often still amber right after clicking

Because both follow-up jobs are asynchronous:

- **Re-parse**: a successful submit only means the job is queued. The UI says "Re-parse submitted; run the
  check again after parsing finishes". Running the check immediately usually reads "syncing" — that is not a
  failed repair.
- **Metadata backfill**: same story; the badge only moves to "Verified" once the backfill completes.

The correct rhythm is: **repair → read the toast → wait (watch the chunk count / metadata back in the dataset)
→ run the capability check again**.

If the workspace has an approval policy, the repair is submitted for approval first. The UI then says
"Submitted for approval (ticket …)", *not* "done" — re-run the check once it is approved and applied.

### Why the button is greyed out

Three common reasons, all fixable from the panel:

- "I understand the shared impact" is not ticked (other applications use the dataset);
- "I understand these N document(s) will be re-parsed" is not ticked (the dataset has parsed content);
- the note says the server named no target chunking method — change the chunking method in the dataset
  settings yourself and re-run the check (the wizard would rather write nothing than guess).

---

## 5. Step 3: app shape and configuration

- **Application name**: becomes the app name and is searchable afterwards.
- **Project**: required; preselected when you only have one.
- **App shape**: only the shapes the template supports can be chosen; the UI explains the rest.
- **Default model**: leave empty to use the tenant default from Settings → Default models; fill it in only to
  override for this app.
- The system prompt and retrieval parameters below come from the template and are read-only here —
  **do not expect to change wording in this step**. After creation, edit them in the application's own settings.

---

## 6. Step 4: verify and create

This screen gathers the human-readable checks:

- **Trial questions**: the template's own questions; one trial run is started automatically after creation
  (retrieval only, not counted against the publish gate).
- **Publish checklist / permission advice / expected outcome**: what this scenario should look like in
  production, and who should get access.
- **Publish gate**: whether the template requires a passing evaluation before publishing new template
  versions (affects template authoring only, not creating applications).

Click "Create app". Three outcomes:

| Outcome | Meaning | Next |
|---|---|---|
| lands on the app / trial run | created | ask a trial question, open the citation |
| "App created." but the trial run fails | the app exists; the trial run is a bonus step | ask a question in the app manually — the app is fine |
| "N dataset(s) have a hard capability gap; creation is blocked" | one dataset has a "blocked" gap | pick another template, or unbind that dataset and re-run the check |

---

## 7. After go-live: confirm it really works

A successful create only proves resources exist. Three checks:

1. **Ask a real question**: use your business wording (not the template's sample), verify the answer comes
   from your documents and the citation opens the source. If it fails, first check whether the question is
   outside what your documents cover.
2. **Look at "Practice run" and the Q&A history**: each create starts one trial run automatically, and real
   conversations show up there too. "There is history" is closer to usable than "created".
3. **Keep the capability gap on your todo list**: shipping with a gap is allowed ("repairable" does not block
   creation), but an unclosed gap is exactly why some future question will go unanswered — especially
   "metadata filter" style capabilities (filtering by department/year/contract number). Template-level health
   metrics (first-run completion rate, 7-day active) are currently read by platform staff through the
   operations API and are not in the UI, so for day-to-day purposes judge by "is anyone actually using it".

---

## 8. Troubleshooting (search by what you see)

| What you see | Why | What to do |
|---|---|---|
| "No template in this workspace yet" | templates are per workspace and yours has none published | ask a platform admin to publish templates there, or use a workspace that has them |
| the capability check spins / fails | the probe cannot reach the engine, or the engine is down | retry later; if it persists ask an admin about the engine — do not read it as "my dataset is bad" |
| an "Unverified" badge | no evidence obtainable (permissions or endpoint) | ask an admin for permissions, or use a dataset you have access to |
| the repair button is greyed out | a confirmation is missing, or no target value exists | see "Why the button is greyed out" in §4 |
| still "Repairable" after repairing | re-parse or metadata backfill still running | wait (watch chunks/metadata), then re-run the check; do not click repeatedly |
| "Submitted for approval (ticket …)" | the workspace has an approval policy | have the approver approve it, then re-run the check |
| create fails with `doesn't own parsed file` | the bound dataset has no parsed content | back to §1: upload and "Parse selected" until chunks > 0 |
| a parse error toast appeared but documents look fine | parsing is a batch job; status can fall back early | trust the chunk column > 0; no need to re-upload |
| "… creation is blocked" | a bound dataset has a "blocked" gap | pick another template, or unbind that dataset |
| you want to change the template's prompt/parameters | built-in templates belong to the platform | ask a platform admin; the hands-on path here never needs it |

---

## 9. Vocabulary: technical docs ↔ the UI

| Elsewhere | Here / in the UI |
|---|---|
| capability-check / probe | "Run capability check" |
| requirement | a capability line in the result card (vector, metadata filter, citation locator, …) |
| verdict `satisfied` | badge "Verified" / verdict "passed" |
| verdict `repairable` | badge "Repairable" |
| verdict `probe_limited` | badge "Unverified" (**not** a pass) |
| verdict `blocked` | badge "Blocked" (no setting fixes it) |
| `apply_parse_profile` | repair item "Switch to the chunking this template requires" |
| `enable_pushdown_and_sync_meta` | repair item "Turn on metadata-filter pushdown and backfill metadata" |
| `reconfigure_dataset` | repair item "Adjust this dataset's parse / retrieval configuration" (manual) |
| `instantiate` | "Create app" |
| fork / baseline copy | taking a platform template over to change its content; **not needed for normal use**, and not part of this guide |
| eval gate | step 4 "Publish gate" (affects publishing new template versions only) |

When you need to work through the API (bulk dataset creation, pipelines), read the operations runbook and the
API reference instead. This guide deliberately stays on the UI path: anything the UI does not show is what
people most often get wrong.
