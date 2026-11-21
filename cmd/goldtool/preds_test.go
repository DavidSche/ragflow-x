package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The tests below exercise `preds` without a database: `planPredictions` is the whole
// decision surface (selection, validation, disclosure), so a test can enumerate every
// way an export could lie about what the run did.

func predsRun(id, status string, at time.Time) predRow {
	return predRow{RunID: id, RunStatus: status, RunMode: model.ReportAssuranceModeFull,
		RunCreatedAt: at, Method: model.ReportEvaluationMethodRule}
}

// predsRowsFor says what the gold says, as the deterministic method's output would: this
// is the control case for the exporter (and the input that must make `eval` pass).
func predsRowsFor(gold []Item) []predRow {
	at := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	out := make([]predRow, 0, len(gold))
	for _, it := range gold {
		r := predsRun("run-"+it.Source.RevisionID, model.ReportAssuranceStatusSucceeded, at)
		r.RevisionID, r.ClaimID = it.Source.RevisionID, it.ClaimID
		r.Verdict, r.QuoteStatus, r.Attempt = it.GoldVerdict, model.ReportQuoteStatusVerified, 1
		out = append(out, r)
	}
	return out
}

func TestPredsWritesOneRowPerItemInFileOrder(t *testing.T) {
	gold := evalGold(t)
	preds, receipt, problems := planPredictions(gold, predsRowsFor(gold), model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("control export refused: %v", problems)
	}
	if len(preds) != len(gold) || receipt.Written != len(gold) {
		t.Fatalf("wrote %d rows / receipt %d, want %d", len(preds), receipt.Written, len(gold))
	}
	if !receipt.ItemsHasLabels {
		t.Fatal("a gold population must be reported as labelled, otherwise the operator cannot tell whether eval will accept the file")
	}
	if receipt.MissingCount != 0 {
		t.Fatalf("missing = %d, want 0", receipt.MissingCount)
	}
	for i := range gold {
		if preds[i].ItemID != gold[i].ItemID {
			t.Fatalf("row %d is %q, want %q: the file order must follow the items file so two exports diff cleanly",
				i, preds[i].ItemID, gold[i].ItemID)
		}
	}
	if predsExitCode(receipt) != exitOK {
		t.Fatalf("a complete export must reach exit 0, got %d (receipt %+v)", predsExitCode(receipt), receipt)
	}
	if receipt.ByVerdict[model.ReportVerdictSupported] != GoldDistribution[model.ReportVerdictSupported] {
		t.Fatalf("by_verdict[%s] = %d, want %d", model.ReportVerdictSupported,
			receipt.ByVerdict[model.ReportVerdictSupported], GoldDistribution[model.ReportVerdictSupported])
	}
}

// TestPredsFeedsEval: the two commands must close the loop. A predictions file this
// command writes, read back by `eval`, has to reproduce the control case — otherwise
// "preds → eval" is a documented pipeline nobody has run.
func TestPredsFeedsEval(t *testing.T) {
	gold := evalGold(t)
	preds, _, problems := planPredictions(gold, predsRowsFor(gold), model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("export refused: %v", problems)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "pred.jsonl")
	lines := make([]string, 0, len(preds))
	for _, p := range preds {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatalf("write: %v", err)
	}
	back, err := readPredictions(path)
	if err != nil {
		t.Fatalf("eval cannot read what preds writes: %v", err)
	}
	receipt, problems := evaluateExperiment("E1", model.ReportEvaluationMethodRule, gold, back)
	if len(problems) != 0 {
		t.Fatalf("eval refused the exported file: %v", problems)
	}
	if receipt.Verdict != evalPass {
		t.Fatalf("a perfect export must score pass, got %q (%s)", receipt.Verdict, describeRows(receipt.Rows))
	}
}

// TestPredsNewestRowWinsAndSaysSo: a claim judged twice (re-run, or a retry) must have
// exactly one row in the file, and the receipt must say that a choice was made.
func TestPredsNewestRowWinsAndSaysSo(t *testing.T) {
	gold := evalGold(t)[:1]
	old := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	newer := old.Add(24 * time.Hour)

	stale := predsRun("run-old", model.ReportAssuranceStatusSucceeded, old)
	stale.RevisionID, stale.ClaimID = gold[0].Source.RevisionID, gold[0].ClaimID
	stale.Verdict, stale.QuoteStatus, stale.Attempt = model.ReportVerdictUnsupported, model.ReportQuoteStatusVerified, 1

	fresh := stale
	fresh.RunID, fresh.RunCreatedAt = "run-new", newer
	fresh.Verdict, fresh.Attempt = model.ReportVerdictSupported, 1

	preds, receipt, problems := planPredictions(gold, []predRow{stale, fresh}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if len(preds) != 1 || preds[0].Verdict != model.ReportVerdictSupported {
		t.Fatalf("got %+v, want the newest run's verdict", preds)
	}
	if receipt.MultiSource != 1 {
		t.Fatalf("items_with_multiple_candidate_rows = %d, want 1: a silent choice makes two exports disagree",
			receipt.MultiSource)
	}
	if len(receipt.Runs) != 1 || receipt.Runs[0].RunID != "run-new" {
		t.Fatalf("runs = %+v, want only run-new", receipt.Runs)
	}
}

// TestPredsValidatesEveryCompetingRow is the anti-cherry-pick rule: if the newest row is
// malformed, the export must fail rather than fall back to the older, well-formed one.
func TestPredsValidatesEveryCompetingRow(t *testing.T) {
	gold := evalGold(t)[:1]
	old := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)

	good := predsRun("run-good", model.ReportAssuranceStatusSucceeded, old)
	good.RevisionID, good.ClaimID = gold[0].Source.RevisionID, gold[0].ClaimID
	good.Verdict, good.QuoteStatus = model.ReportVerdictSupported, model.ReportQuoteStatusVerified

	bad := good
	bad.RunID, bad.RunCreatedAt = "run-bad", old.Add(time.Hour)
	bad.Verdict = "definitely_supported" // not in the closed set

	preds, _, problems := planPredictions(gold, []predRow{good, bad}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) == 0 {
		t.Fatalf("a malformed competing row must fail the export, got rows %+v", preds)
	}
	if !strings.Contains(problems[0], "definitely_supported") || !strings.Contains(problems[0], "run-bad") {
		t.Fatalf("the problem must name the verdict and the run, got %q", problems[0])
	}
	if preds != nil {
		t.Fatalf("no file may be written when any row is rejected, got %d rows", len(preds))
	}
}

// TestPredsRefusesEachVocabularyViolation enumerates the transcribing rules. Each case is
// one way a file could look complete while carrying a value the runtime never means.
func TestPredsRefusesEachVocabularyViolation(t *testing.T) {
	gold := evalGold(t)[:1]
	cases := []struct {
		name   string
		mutate func(*predRow)
		want   string
	}{
		{"unknown verdict", func(r *predRow) { r.Verdict = "mostly_true" }, "runtime vocabulary"},
		{"abstained without reason", func(r *predRow) {
			r.Verdict, r.QuoteStatus, r.AbstainReason = model.ReportVerdictAbstained, model.ReportQuoteStatusVerified, ""
		}, "abstain_reason is empty"},
		{"abstain reason on a concluded verdict", func(r *predRow) {
			r.AbstainReason = model.ReportAbstainModelUncertain
		}, "cannot belong to a row that concluded"},
		{"not_evaluated without reason", func(r *predRow) {
			r.Verdict, r.QuoteStatus, r.NotEvalReason = model.ReportVerdictNotEvaluated, model.ReportQuoteStatusNotProvided, ""
		}, "not_evaluated_reason is empty"},
		{"not_evaluated reason on a concluded verdict", func(r *predRow) {
			r.NotEvalReason = model.ReportNotEvaluatedBudget
		}, "not_evaluated_reason"},
		{"supported without quote status", func(r *predRow) {
			r.Verdict, r.QuoteStatus = model.ReportVerdictSupported, ""
		}, "empty quote_status"},
		{"quote status outside the set", func(r *predRow) { r.QuoteStatus = "probably_fine" }, "closed set"},
		{"unknown abstain reason", func(r *predRow) {
			r.Verdict, r.AbstainReason = model.ReportVerdictAbstained, "too_hard"
		}, "closed set"},
		{"another method's row", func(r *predRow) { r.Method = model.ReportEvaluationMethodSemantic }, "belongs to one method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := predsRowsFor(gold)[0]
			tc.mutate(&row)
			_, _, problems := planPredictions(gold, []predRow{row}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
			if len(problems) == 0 {
				t.Fatalf("expected a refusal mentioning %q", tc.want)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("problems = %v, want something about %q", problems, tc.want)
			}
		})
	}
}

// TestPredsFillsNotProvidedOnlyWhereLegal: an empty quote_status is legal for the verdicts
// that do not assert support, and it is counted — `not_provided` is the runtime's own word
// for "no quote", not a default the exporter invented silently.
func TestPredsFillsNotProvidedOnlyWhereLegal(t *testing.T) {
	gold := evalGold(t)[:1]
	row := predsRowsFor(gold)[0]
	row.Verdict, row.QuoteStatus = model.ReportVerdictInsufficient, ""

	preds, receipt, problems := planPredictions(gold, []predRow{row}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if preds[0].QuoteStatus != model.ReportQuoteStatusNotProvided {
		t.Fatalf("quote_status = %q, want not_provided", preds[0].QuoteStatus)
	}
	if receipt.EmptyQuote != 1 || receipt.ByQuoteStatus[model.ReportQuoteStatusNotProvided] != 1 {
		t.Fatalf("empties must be counted (receipt %d / %d)", receipt.EmptyQuote,
			receipt.ByQuoteStatus[model.ReportQuoteStatusNotProvided])
	}
}

func TestPredsRefusesAHalfLabelledPopulation(t *testing.T) {
	gold := evalGold(t)[:4]
	gold[0].GoldVerdict = "" // a worksheet row mixed into a gold file
	_, receipt, problems := planPredictions(gold, predsRowsFor(gold), model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) == 0 || !strings.Contains(problems[0], "half-labelled") {
		t.Fatalf("a half-labelled population must be refused, got %v", problems)
	}
	if receipt.ItemsHasLabels {
		t.Fatal("items_has_labels must be false when any item lacks a label")
	}
}

// TestPredsNamesEveryMissingItem: the file must be smaller than the population when the
// run judged fewer claims, and the receipt must be able to say which claims — the whole
// point of the command is that this is a fact, not a rounding error.
func TestPredsNamesEveryMissingItem(t *testing.T) {
	gold := evalGold(t)
	rows := predsRowsFor(gold)
	dropped := rows[len(rows)-12:] // keep the last 12 out
	_ = dropped
	preds, receipt, problems := planPredictions(gold, rows[:len(rows)-12], model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if receipt.MissingCount != 12 {
		t.Fatalf("missing = %d, want 12", receipt.MissingCount)
	}
	if len(preds) != len(gold)-12 {
		t.Fatalf("wrote %d rows, want %d", len(preds), len(gold)-12)
	}
	if len(receipt.Missing) != 10 {
		t.Fatalf("printed %d missing ids, want the list capped at 10", len(receipt.Missing))
	}
	if predsExitCode(receipt) != exitIncomplete {
		t.Fatalf("a file that cannot cover the population is incomplete, got exit %d", predsExitCode(receipt))
	}
	for _, p := range preds {
		for _, m := range receipt.Missing {
			if p.ItemID == m {
				t.Fatalf("%q is both written and reported missing", m)
			}
		}
	}
}

// TestPredsRefusesHalfFinishedRuns: `queued`/`running` wrote partial rows, `failed` and
// `cancelled` runs wrote rows their own status disowned. Only `succeeded` and `degraded`
// are a finished method's answer.
func TestPredsRefusesHalfFinishedRuns(t *testing.T) {
	gold := evalGold(t)[:1]
	for _, status := range []string{
		model.ReportAssuranceStatusQueued, model.ReportAssuranceStatusRunning,
		model.ReportAssuranceStatusFailed, model.ReportAssuranceStatusCancelled,
	} {
		row := predsRowsFor(gold)[0]
		row.RunStatus = status
		_, _, problems := planPredictions(gold, []predRow{row}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
		if len(problems) == 0 || !strings.Contains(problems[0], status) {
			t.Fatalf("status %q must be refused by name, got %v", status, problems)
		}
	}

	row := predsRowsFor(gold)[0]
	row.RunStatus = model.ReportAssuranceStatusDegraded
	_, receipt, problems := planPredictions(gold, []predRow{row}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("degraded rows are exportable (the rule layer did answer), got %v", problems)
	}
	if len(receipt.Degraded) != 1 || receipt.Degraded[0] != row.RunID {
		t.Fatalf("degraded runs must be listed, got %v", receipt.Degraded)
	}
	if predsExitCode(receipt) != exitIncomplete {
		t.Fatalf("a degraded run's export must exit incomplete, got %d", predsExitCode(receipt))
	}
}

// TestPredsEmptyResultIsIncompleteNotPass: no rows is not "0 rows written, all good". The
// command ran, so it is exit 3 with the population named as missing, never exit 0.
func TestPredsEmptyResultIsIncompleteNotPass(t *testing.T) {
	gold := evalGold(t)
	preds, receipt, problems := planPredictions(gold, nil, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("no rows is a finding, not an invariant failure: %v", problems)
	}
	if len(preds) != 0 || receipt.Written != 0 {
		t.Fatalf("wrote %d rows from no evaluations", len(preds))
	}
	if receipt.MissingCount != len(gold) {
		t.Fatalf("missing = %d, want %d", receipt.MissingCount, len(gold))
	}
	if predsExitCode(receipt) != exitIncomplete {
		t.Fatalf("exit = %d, want 3 (an empty file must not read as success)", predsExitCode(receipt))
	}
}

// TestPredsCLIFlagDiscipline covers the arguments: `--method` has no default, and a typo
// is a usage error that prints the closed set rather than writing an empty file.
func TestPredsCLIFlagDiscipline(t *testing.T) {
	dir := t.TempDir()
	itemsPath := filepath.Join(dir, "items.jsonl")
	lines, err := encodeItems(evalGold(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := writeJSONL(itemsPath, lines); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no items", []string{"--out", filepath.Join(dir, "p.jsonl"), "--method", "deterministic"}, "--items"},
		{"no out", []string{"--items", itemsPath, "--method", "deterministic"}, "--out"},
		{"no method", []string{"--items", itemsPath, "--out", filepath.Join(dir, "p.jsonl")}, "--method is required"},
		{"bad method", []string{"--items", itemsPath, "--out", filepath.Join(dir, "p.jsonl"), "--method", "vibes"}, "not in the closed set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runPredsCmd(t, tc.args...)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to mention %q", stderr, tc.want)
			}
		})
	}
}

func runPredsCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("stderr temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"preds"}, args...), stdout, stderr)
	outBuf, _ := os.ReadFile(stdout.Name())
	errBuf, _ := os.ReadFile(stderr.Name())
	return code, string(outBuf), string(errBuf)
}

func TestPredsReceiptIsJSONAndNamesTheMethod(t *testing.T) {
	gold := evalGold(t)
	_, receipt, _ := planPredictions(gold, predsRowsFor(gold), model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("receipt does not encode: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("receipt is not valid JSON: %v", err)
	}
	for _, key := range []string{"items_file", "items_has_labels", "method", "mode", "rows_written",
		"items_with_multiple_candidate_rows", "missing_items_count", "by_verdict", "runs"} {
		if _, ok := back[key]; !ok {
			t.Fatalf("receipt is missing key %q: %s", key, raw)
		}
	}
	if back["method"] != model.ReportEvaluationMethodRule {
		t.Fatalf("receipt method = %v, want %q — a file that does not name its method cannot enter E1",
			back["method"], model.ReportEvaluationMethodRule)
	}
}

func TestPredsReportsTheRunsItRead(t *testing.T) {
	gold := evalGold(t)
	preds, receipt, problems := planPredictions(gold, predsRowsFor(gold), model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	seen := map[string]int{}
	for _, p := range preds {
		_ = p
	}
	total := 0
	for _, r := range receipt.Runs {
		total += r.Rows
		seen[r.RunID]++
	}
	if total != len(preds) {
		t.Fatalf("runs account for %d rows, file has %d", total, len(preds))
	}
	if len(receipt.Runs) != 6 {
		t.Fatalf("runs = %d, want 6 (one per revision in the fixture); got %+v", len(receipt.Runs), receipt.Runs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("run %q appears %d times in the receipt", id, n)
		}
	}
}

func TestPredsNeverWritesNotRequiredAsAScoredJudgement(t *testing.T) {
	// A `not_required` row is legal and must be transcribed as-is; `eval` is what decides
	// it shrinks coverage. The exporter must not turn it into `insufficient_evidence`
	// (a guess about *why* nothing was asked).
	gold := evalGold(t)[:1]
	row := predsRowsFor(gold)[0]
	row.Verdict, row.QuoteStatus = model.ReportVerdictNotRequired, ""
	preds, receipt, problems := planPredictions(gold, []predRow{row}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if preds[0].Verdict != model.ReportVerdictNotRequired {
		t.Fatalf("verdict = %q, want not_required transcribed verbatim", preds[0].Verdict)
	}
	if receipt.NotRequired != 1 {
		t.Fatalf("not_required rows = %d, want 1", receipt.NotRequired)
	}
}

func TestPredsSelectionTiebreakIsDeterministic(t *testing.T) {
	gold := evalGold(t)[:1]
	a := predsRowsFor(gold)[0]
	a.RunID = "run-a"
	b := a
	b.RunID = "run-b"
	a.Verdict, b.Verdict = model.ReportVerdictSupported, model.ReportVerdictUnsupported

	first, _, problems := planPredictions(gold, []predRow{a, b}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	second, _, _ := planPredictions(gold, []predRow{b, a}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if first[0].Verdict != second[0].Verdict {
		t.Fatalf("row order in the query changed the answer: %q vs %q (same-second runs need a total order)",
			first[0].Verdict, second[0].Verdict)
	}
}

func TestPredsAttemptTiebreakPrefersTheRetry(t *testing.T) {
	gold := evalGold(t)[:1]
	first := predsRowsFor(gold)[0]
	first.RunID, first.Attempt = "run-1", 1
	first.Verdict = model.ReportVerdictInsufficient
	retry := first
	retry.Attempt, retry.Verdict = 2, model.ReportVerdictSupported

	preds, _, problems := planPredictions(gold, []predRow{retry, first}, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if preds[0].Verdict != model.ReportVerdictSupported {
		t.Fatalf("verdict = %q, want the higher attempt (a retry is the row the run stands behind)",
			preds[0].Verdict)
	}
}

func TestPredsByAbstainReasonIsCounted(t *testing.T) {
	gold := evalGold(t)
	rows := predsRowsFor(gold)
	rows[0].Verdict, rows[0].AbstainReason = model.ReportVerdictAbstained, model.ReportAbstainInputInvalid
	rows[1].Verdict, rows[1].AbstainReason = model.ReportVerdictAbstained, model.ReportAbstainNoQuote
	_, receipt, problems := planPredictions(gold, rows, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if receipt.ByAbstainReason[model.ReportAbstainInputInvalid] != 1 ||
		receipt.ByAbstainReason[model.ReportAbstainNoQuote] != 1 {
		t.Fatalf("by_abstain_reason = %v", receipt.ByAbstainReason)
	}
	if receipt.ByVerdict[model.ReportVerdictAbstained] != 2 {
		t.Fatalf("by_verdict[abstained] = %d, want 2", receipt.ByVerdict[model.ReportVerdictAbstained])
	}
}

func TestPredsCacheHitRowsAreCounted(t *testing.T) {
	gold := evalGold(t)[:2]
	rows := predsRowsFor(gold)
	rows[0].CacheHit = true
	_, receipt, problems := planPredictions(gold, rows, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if receipt.CacheHitRows != 1 {
		t.Fatalf("cache_hit_rows = %d, want 1", receipt.CacheHitRows)
	}
}

func TestPredsRejectsAnItemWithNoRevision(t *testing.T) {
	// A pool item without a revision cannot be matched to any evaluation row; it will be
	// reported missing, which is the honest outcome (and `validate` is what refuses such a
	// file outright).
	gold := evalGold(t)[:1]
	gold[0].Source.RevisionID = ""
	rows := predsRowsFor(evalGold(t)[:1])
	_, receipt, problems := planPredictions(gold, rows, model.ReportEvaluationMethodRule, model.ReportAssuranceModeFull)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if receipt.MissingCount != 1 {
		t.Fatalf("missing = %d, want 1 (no revision id means no match)", receipt.MissingCount)
	}
}
