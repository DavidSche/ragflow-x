package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// evalGold builds a gold file with the plan's distribution, one item per class count.
func evalGold(t *testing.T) []Item {
	t.Helper()
	return goldWithDistribution()
}

// perfectPredictions says what the gold says. It is the control case: every row must be
// able to pass, otherwise a "fail" downstream would mean nothing.
func perfectPredictions(gold []Item) []Prediction {
	out := make([]Prediction, 0, len(gold))
	for _, it := range gold {
		out = append(out, Prediction{ItemID: it.ItemID, Verdict: it.GoldVerdict, QuoteStatus: model.ReportQuoteStatusVerified})
	}
	return out
}

func rowsOf(t *testing.T, rows []evalRow, metric, group string) evalRow {
	t.Helper()
	found := false
	var hit evalRow
	for _, r := range rows {
		if r.Metric == metric && r.Group == group {
			if found {
				t.Fatalf("metric %q group %q appears twice: a report with two answers for one row is unreadable", metric, group)
			}
			hit, found = r, true
		}
	}
	if !found {
		t.Fatalf("metric %q group %q is missing from the receipt", metric, group)
	}
	return hit
}

func runEvalCmd(t *testing.T, args ...string) (int, string, string) {
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
	code := run(append([]string{"eval"}, args...), stdout, stderr)
	outBuf, _ := os.ReadFile(stdout.Name())
	errBuf, _ := os.ReadFile(stderr.Name())
	return code, string(outBuf), string(errBuf)
}

func writeEvalFiles(t *testing.T, gold []Item, preds []Prediction) (string, string) {
	t.Helper()
	dir := t.TempDir()
	goldPath := filepath.Join(dir, "gold-v1.jsonl")
	lines := make([]string, 0, len(gold))
	for _, it := range gold {
		line, err := encodeItem(it)
		if err != nil {
			t.Fatalf("encode gold: %v", err)
		}
		lines = append(lines, line)
	}
	if err := writeJSONL(goldPath, lines); err != nil {
		t.Fatalf("write gold: %v", err)
	}
	predPath := filepath.Join(dir, "pred.jsonl")
	plines := make([]string, 0, len(preds))
	for _, p := range preds {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("encode prediction: %v", err)
		}
		plines = append(plines, string(raw))
	}
	if err := writeJSONL(predPath, plines); err != nil {
		t.Fatalf("write predictions: %v", err)
	}
	return goldPath, predPath
}

// TestEvalControlCasePassesAndNamesItsScope: a perfect replay must reach `pass` on the
// hard rows, and the receipt must still print the row it explicitly does not decide
// (`false_certification_rate` CP 上界 needs n ≥ 72, doc/301 §4.2).
func TestEvalControlCasePassesAndNamesItsScope(t *testing.T) {
	gold := evalGold(t)
	receipt, problems := evaluateExperiment("E1", "control", gold, perfectPredictions(gold))
	if len(problems) != 0 {
		t.Fatalf("control case refused: %v", problems)
	}
	if receipt.Verdict != evalPass {
		t.Fatalf("control verdict = %q, want pass (rows: %s)", receipt.Verdict, describeRows(receipt.Rows))
	}
	for _, metric := range []string{
		"claim_evaluation_coverage", "unsupported_false_positive_rate",
		"macro_f1_core", "false_certification_rate", "abstain_rate(model_uncertain)",
		"quote_hallucination_rate", "quote_verified_rate", "conflicting_recall",
	} {
		found := false
		for _, r := range receipt.Rows {
			if r.Metric == metric {
				found = true
			}
		}
		if !found {
			t.Fatalf("metric %q is missing: a §4.2 row that is model-computable cannot be omitted", metric)
		}
	}
	upper := rowsOf(t, receipt.Rows, "false_certification_rate", "CP 上界")
	if !upper.OutOfScope || upper.Verdict != evalUnverified {
		t.Fatalf("false_certification CP 上界 with n=%d must be unverified + out_of_scope, got %q/%v",
			upper.Denominator, upper.Verdict, upper.OutOfScope)
	}
	if !strings.Contains(upper.Detail, "不在本版承诺范围") {
		t.Fatalf("the deferred row must say so in words, got %q", upper.Detail)
	}
	if !strings.HasPrefix(receipt.Decision, "eligible") {
		t.Fatalf("control decision = %q, want eligible", receipt.Decision)
	}
}

// TestEvalEveryHardRowCanFail is the same discipline the release gate is held to
// (doc/301 §11.1 row 1): a row that cannot fail is decoration. Each case mutates the
// control predictions in exactly one way and asserts the named row fails.
func TestEvalEveryHardRowCanFail(t *testing.T) {
	cases := []struct {
		name    string
		metric  string
		group   string
		mutate  func([]Prediction) []Prediction
		wantSub string
	}{
		{
			name:   "coverage: one claim was never judged",
			metric: "claim_evaluation_coverage",
			mutate: func(ps []Prediction) []Prediction { return ps[1:] },
		},
		{
			name:   "unsupported false positive",
			metric: "unsupported_false_positive_rate",
			mutate: func(ps []Prediction) []Prediction {
				return flipFirst(ps, model.ReportVerdictUnsupported, model.ReportVerdictSupported)
			},
			wantSub: "13.2%",
		},
		{
			// A mutant must *cross the threshold*, not merely be wrong: one wrong claim in
			// 120 is 99.2%, which the ≥85% row is supposed to pass.
			name:   "accuracy",
			metric: "verdict_accuracy@evaluated",
			group:  "点估计",
			mutate: func(ps []Prediction) []Prediction {
				return flipN(ps, model.ReportVerdictSupported, model.ReportVerdictPartiallySupported, 20)
			},
		},
		{
			name:   "macro F1",
			metric: "macro_f1_core",
			mutate: func(ps []Prediction) []Prediction {
				for i := range ps {
					if ps[i].Verdict == model.ReportVerdictConflicting {
						ps[i].Verdict = model.ReportVerdictSupported
					}
				}
				return ps
			},
		},
		{
			name:   "false certification",
			metric: "false_certification_rate",
			group:  "点估计",
			mutate: func(ps []Prediction) []Prediction {
				for i := range ps {
					if ps[i].Verdict == model.ReportVerdictUnsupported {
						ps[i].Verdict = model.ReportVerdictSupported
					}
				}
				return ps
			},
		},
		{
			name:   "model_uncertain abstain",
			metric: "abstain_rate(model_uncertain)",
			mutate: func(ps []Prediction) []Prediction {
				for i := 0; i < 20; i++ {
					ps[i] = Prediction{ItemID: ps[i].ItemID, Verdict: model.ReportVerdictAbstained,
						QuoteStatus: model.ReportQuoteStatusNotProvided, AbstainReason: model.ReportAbstainModelUncertain}
				}
				return ps
			},
		},
		{
			name:   "quote hallucination",
			metric: "quote_hallucination_rate",
			mutate: func(ps []Prediction) []Prediction {
				ps[0] = Prediction{ItemID: ps[0].ItemID, Verdict: model.ReportVerdictAbstained,
					QuoteStatus: model.ReportQuoteStatusNotFound, AbstainReason: model.ReportAbstainQuoteNotFound}
				return ps
			},
			wantSub: "编造原文",
		},
		{
			name:   "quote not verified",
			metric: "quote_verified_rate",
			mutate: func(ps []Prediction) []Prediction {
				ps[0].QuoteStatus = model.ReportQuoteStatusNotProvided
				return ps
			},
		},
		{
			// 21/24 = 87.5% is the first count below the ≥90% floor; 23/24 must still pass,
			// which is why the E2 boundary tests use 22 and 23 explicitly.
			name:   "conflicting recall",
			metric: "conflicting_recall",
			group:  "点估计",
			mutate: func(ps []Prediction) []Prediction {
				return flipN(ps, model.ReportVerdictConflicting, model.ReportVerdictInsufficient, 3)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gold := evalGold(t)
			preds := tc.mutate(perfectPredictions(gold))
			receipt, problems := evaluateExperiment("E1", "mutant", gold, preds)
			if len(problems) != 0 {
				t.Fatalf("refused: %v", problems)
			}
			row := rowsOf(t, receipt.Rows, tc.metric, tc.group)
			if row.Verdict != evalFail {
				t.Fatalf("%s = %q (%d/%d), want fail", tc.metric, row.Verdict, row.Numerator, row.Denominator)
			}
			if receipt.Verdict != evalFail {
				t.Fatalf("receipt verdict = %q, want fail: a failed hard row must fail the receipt", receipt.Verdict)
			}
			if tc.wantSub != "" && !strings.Contains(row.Interval+row.Detail, tc.wantSub) {
				t.Fatalf("%s does not mention %q (interval=%q detail=%q)", tc.metric, tc.wantSub, row.Interval, row.Detail)
			}
		})
	}
}

// TestEvalSeparatesThePointEstimateFromTheLowerBound is §9.2's E2 arithmetic, asserted
// as a decision rather than as a number: 22/24 clears the point estimate and must still
// hold scan_top_k.
func TestEvalSeparatesThePointEstimateFromTheLowerBound(t *testing.T) {
	gold := evalGold(t)
	preds := perfectPredictions(gold)
	// Break exactly two of the 24 conflicting claims: 22/24 = 91.7%, CP 下界 73.0%.
	broken := 0
	for i := range preds {
		if preds[i].Verdict == model.ReportVerdictConflicting && broken < 2 {
			preds[i].Verdict = model.ReportVerdictSupported
			broken++
		}
	}
	receipt, problems := evaluateExperiment("E2", "cited+candidate", gold, preds)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	point := rowsOf(t, receipt.Rows, "conflicting_recall", "点估计")
	lower := rowsOf(t, receipt.Rows, "conflicting_recall", "CP 下界")
	if point.Verdict != evalPass || point.Point != "91.7%" {
		t.Fatalf("point row = %q %s, want pass 91.7%%", point.Verdict, point.Point)
	}
	if lower.Verdict != evalFail || !strings.Contains(lower.Detail, "73.0%") {
		t.Fatalf("lower row = %q (%s), want fail at 73.0%%", lower.Verdict, lower.Detail)
	}
	decision := rowsOf(t, receipt.Rows, "scan_top_k 决策", "")
	if decision.Verdict != evalFail {
		t.Fatalf("scan_top_k row = %q, want fail: 22/24 must not licence a wider scan", decision.Verdict)
	}
	for _, needle := range []string{"hold", "点估计达标、下界不达标", "扩样本到 40 条以上再判"} {
		if !strings.Contains(decision.Point, needle) {
			t.Fatalf("decision %q does not contain %q", decision.Point, needle)
		}
	}
	if receipt.Verdict != evalFail {
		t.Fatalf("receipt = %q, want fail", receipt.Verdict)
	}
}

// TestEvalRaisesScanTopKOnlyWhenTheLowerBoundClears: 23/24 is the smallest movement that
// clears 78% (doc/301 §9.2), so it is the boundary the tool must get right.
func TestEvalRaisesScanTopKOnlyWhenTheLowerBoundClears(t *testing.T) {
	gold := evalGold(t)
	preds := perfectPredictions(gold)
	for i := range preds {
		if preds[i].Verdict == model.ReportVerdictConflicting {
			preds[i].Verdict = model.ReportVerdictSupported
			break
		}
	}
	receipt, problems := evaluateExperiment("E2", "cited+candidate", gold, preds)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	decision := rowsOf(t, receipt.Rows, "scan_top_k 决策", "")
	if decision.Verdict != evalPass {
		t.Fatalf("23/24 decision = %q (%s), want pass", decision.Verdict, decision.Point)
	}
	if !strings.HasPrefix(decision.Point, "raise") || !strings.Contains(decision.Point, "78.9%") {
		t.Fatalf("decision = %q, want raise citing the 78.9%% lower bound", decision.Point)
	}
}

// TestEvalE2EnumeratesEmptyGroups: doc/301 §9.1.2 says an unreachable stratum is reported
// with its actual n and the conclusion covers only the reachable group. A group that
// disappears from the table would read as "never defined".
func TestEvalE2EnumeratesEmptyGroups(t *testing.T) {
	gold := evalGold(t)
	// Every conflicting item rests on a candidate chunk that another section cited.
	for i := range gold {
		it := &gold[i]
		if it.GoldVerdict != model.ReportVerdictConflicting {
			continue
		}
		it.GoldScanScope = "cited+candidate:1"
		it.CandidatePool = []Candidate{{
			ChunkID: fmt.Sprintf("chunk-cand-%d", i), ContentHash: "sha256:bb", ContentExcerpt: "候选原文摘录",
			Truncated: false, PoolRank: 1, CitedElsewhere: true,
		}}
		it.GoldCandidateChunkID = it.CandidatePool[0].ChunkID
	}
	receipt, problems := evaluateExperiment("E2", "cited+candidate", gold, perfectPredictions(gold))
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	yes := rowsOf(t, receipt.Rows, "conflicting_recall", "cited+candidate|cited_elsewhere=true")
	no := rowsOf(t, receipt.Rows, "conflicting_recall", "cited+candidate|cited_elsewhere=false")
	if yes.Denominator != 24 || yes.Verdict != evalObserved {
		t.Fatalf("true group = %d rows / %q, want 24 / observed", yes.Denominator, yes.Verdict)
	}
	if no.Denominator != 0 || no.Verdict != evalUnverified {
		t.Fatalf("false group = %d rows / %q, want 0 / unverified", no.Denominator, no.Verdict)
	}
	if !strings.Contains(no.Detail, "该组无样本") {
		t.Fatalf("empty group detail = %q, want it to say the group has no samples", no.Detail)
	}
	decision := rowsOf(t, receipt.Rows, "scan_top_k 决策", "")
	if !strings.Contains(decision.Point, "结论不覆盖该组") {
		t.Fatalf("decision = %q, want the unreachable-group caveat", decision.Point)
	}
}

// TestEvalRefusesAConflictingLabelWithNoCandidateRow: an unnamed candidate is a data
// defect (nobody can say which group it belongs to), so the row must not silently join a
// bucket.
func TestEvalRefusesAConflictingLabelWithNoCandidateRow(t *testing.T) {
	gold := evalGold(t)
	for i := range gold {
		it := &gold[i]
		if it.GoldVerdict != model.ReportVerdictConflicting {
			continue
		}
		it.GoldScanScope = "cited+candidate:1"
		it.CandidatePool = []Candidate{{
			ChunkID: "chunk-x", ContentHash: "sha256:cc", ContentExcerpt: "候选原文", PoolRank: 1, CitedElsewhere: true,
		}}
		it.GoldCandidateChunkID = "" // deliberately missing
	}
	receipt, problems := evaluateExperiment("E2", "cited+candidate", gold, perfectPredictions(gold))
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	un := rowsOf(t, receipt.Rows, "conflicting_recall", "cited+candidate|cited_elsewhere=unattributed")
	if un.Verdict != evalUnverified || !strings.Contains(un.Detail, "无法归组") {
		t.Fatalf("unattributed group = %q (%s), want unverified + 无法归组", un.Verdict, un.Detail)
	}
	if receipt.Verdict != evalUnverified {
		t.Fatalf("receipt = %q, want unverified", receipt.Verdict)
	}
}

// TestEvalMacroF1IgnoresTheTwoReportOnlyClasses pins §4.2's second-review correction:
// `outdated` and `partially_supported` are reported, never averaged in.
func TestEvalMacroF1IgnoresTheTwoReportOnlyClasses(t *testing.T) {
	gold := evalGold(t)
	base, problems := evaluateExperiment("E1", "base", gold, perfectPredictions(gold))
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	preds := perfectPredictions(gold)
	for i := range preds {
		if preds[i].Verdict == model.ReportVerdictOutdated || preds[i].Verdict == model.ReportVerdictPartiallySupported {
			preds[i].Verdict = model.ReportVerdictInsufficient
		}
	}
	mutated, problems := evaluateExperiment("E1", "mutant", gold, preds)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	if a, b := rowsOf(t, base.Rows, "macro_f1_core", ""), rowsOf(t, mutated.Rows, "macro_f1_core", ""); a.Point != b.Point {
		t.Fatalf("macro F1 changed from %s to %s when the report-only classes changed", a.Point, b.Point)
	}
	if f := rowsOf(t, mutated.Rows, "f1_outdated", ""); f.Point == "1.000" || f.Verdict != evalObserved {
		t.Fatalf("f1_outdated = %q %s, want a degraded number that is still only observed", f.Point, f.Verdict)
	}
}

// TestEvalRefusesAWorksheetAndUnknownPredictions keeps the tool from grading the wrong
// file: a pool/worksheet has no labels, and a prediction for a claim that is not in the
// gold file means the two files describe different populations.
func TestEvalRefusesAWorksheetAndUnknownPredictions(t *testing.T) {
	dir := t.TempDir()
	pool := []Item{sampleItem(1, "")} // no gold_verdict
	poolPath := filepath.Join(dir, "pool.jsonl")
	line, err := encodeItem(pool[0])
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := writeJSONL(poolPath, []string{line}); err != nil {
		t.Fatalf("write: %v", err)
	}
	predPath := filepath.Join(dir, "pred.jsonl")
	if err := writeJSONL(predPath, []string{`{"item_id":"rev00:claim001","verdict":"supported","quote_status":"verified"}`}); err != nil {
		t.Fatalf("write pred: %v", err)
	}
	code, _, stderr := runEvalCmd(t, "--gold", poolPath, "--pred", predPath, "--exp", "E1")
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d", code, exitInvariant)
	}
	if !strings.Contains(stderr, "not a gold file") {
		t.Fatalf("stderr = %q, want it to name the missing labels", stderr)
	}

	gold := evalGold(t)
	gpath, ppath := writeEvalFiles(t, gold, []Prediction{
		{ItemID: "not-in-gold", Verdict: model.ReportVerdictSupported, QuoteStatus: model.ReportQuoteStatusVerified},
	})
	code, _, stderr = runEvalCmd(t, "--gold", gpath, "--pred", ppath, "--exp", "E1")
	if code != exitInvariant || !strings.Contains(stderr, "not in the gold file") {
		t.Fatalf("exit=%d stderr=%q, want a refusal naming the unknown item", code, stderr)
	}
}

// TestEvalRefusesAPredictionWithoutQuoteStatus: an empty quote_status cannot answer §4.2's
// two quote rows, and defaulting it to `not_provided` would manufacture a failure.
func TestEvalRefusesAPredictionWithoutQuoteStatus(t *testing.T) {
	gold := evalGold(t)
	preds := perfectPredictions(gold)
	preds[0].QuoteStatus = ""
	_, problems := evaluateExperiment("E1", "x", gold, preds)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, " "), "quote_status is empty") {
		t.Fatalf("problems = %v, want a named refusal about quote_status", problems)
	}
}

// TestEvalExitCodesMatchTheVerdict keeps the four exit codes meaningful end to end.
func TestEvalExitCodesMatchTheVerdict(t *testing.T) {
	gold := evalGold(t)

	t.Run("pass is 0", func(t *testing.T) {
		gpath, ppath := writeEvalFiles(t, gold, perfectPredictions(gold))
		if code, out, errOut := runEvalCmd(t, "--gold", gpath, "--pred", ppath, "--exp", "E1"); code != exitOK {
			t.Fatalf("exit = %d, want 0 (stderr=%s)", code, errOut)
		} else if !strings.Contains(out, "| 硬门槛 |") {
			t.Fatalf("report has no hard rows: %s", out)
		}
		// `--out` must create the parent directory and leave the table on disk: the
		// report is the deliverable doc/305 quotes, so losing it to a missing dir would
		// only be noticed after the run.
		outPath := filepath.Join(t.TempDir(), "reports", "e1-report.md")
		if code, _, errOut := runEvalCmd(t, "--gold", gpath, "--pred", ppath, "--exp", "E1", "--out", outPath); code != exitOK {
			t.Fatalf("--out exit = %d (stderr=%s)", code, errOut)
		}
		body, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("report not written: %v", err)
		}
		if !strings.Contains(string(body), "macro_f1_core") {
			t.Fatalf("written report is not the table: %s", body)
		}
	})

	t.Run("a failed row is 1", func(t *testing.T) {
		preds := perfectPredictions(gold)
		preds = flipFirst(preds, model.ReportVerdictUnsupported, model.ReportVerdictSupported)
		gpath, ppath := writeEvalFiles(t, gold, preds)
		if code, _, _ := runEvalCmd(t, "--gold", gpath, "--pred", ppath, "--exp", "E1"); code != exitInvariant {
			t.Fatalf("exit = %d, want 1", code)
		}
	})

	t.Run("no denominator is 3, never 0", func(t *testing.T) {
		// Gold with no conflicting claims at all: E2's denominator does not exist.
		var thin []Item
		n := 0
		for _, v := range []string{model.ReportVerdictSupported, model.ReportVerdictUnsupported} {
			for i := 0; i < 20; i++ {
				n++
				thin = append(thin, sampleItem(n, v))
			}
		}
		gpath, ppath := writeEvalFiles(t, thin, perfectPredictions(thin))
		code, out, _ := runEvalCmd(t, "--gold", gpath, "--pred", ppath, "--exp", "E2")
		if code != exitIncomplete {
			t.Fatalf("exit = %d, want 3 (a missing denominator is not a pass)", code)
		}
		if !strings.Contains(out, "无冲突样本") {
			t.Fatalf("report does not say why it could not decide: %s", out)
		}
	})
}

// TestEvalReportPrintsNumeratorDenominatorAndInterval: doc/301 §4.2 — "任何比例都必须同时
// 给出分子/分母，未给 n 的比例不计入验收".
func TestEvalReportPrintsNumeratorDenominatorAndInterval(t *testing.T) {
	gold := evalGold(t)
	receipt, problems := evaluateExperiment("E1", "control", gold, perfectPredictions(gold))
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	receipt.GoldFile, receipt.PredFile = "gold-v1.jsonl", "pred.jsonl"
	md := renderEvalMarkdown(receipt)
	for _, needle := range []string{"分子/分母", "CP 95%", "Clopper–Pearson", "## 本报告的边界", "缺行不等于过得去"} {
		if !strings.Contains(md, needle) {
			t.Fatalf("report is missing %q", needle)
		}
	}
	for _, r := range receipt.Rows {
		if r.Denominator == 0 {
			continue
		}
		cell := fmt.Sprintf("| %d/%d |", r.Numerator, r.Denominator)
		if !strings.Contains(md, cell) {
			t.Fatalf("row %q does not print %s", r.Metric, cell)
		}
	}
	if !strings.Contains(md, "44/44") {
		t.Fatalf("quote_verified_rate should be 44/44 (36 supported + 8 partially_supported): %s", md)
	}
}

// --- helpers ----------------------------------------------------------------------

func flipFirst(ps []Prediction, from, to string) []Prediction {
	return flipN(ps, from, to, 1)
}

func flipN(ps []Prediction, from, to string, n int) []Prediction {
	done := 0
	for i := range ps {
		if done == n {
			break
		}
		if ps[i].Verdict == from {
			ps[i].Verdict = to
			done++
		}
	}
	return ps
}

// TestEvalRefusesATwicePredictedItem: two verdicts for one claim cannot both be scored,
// and the second one must not silently overwrite the first — that is how a re-run
// appended to a file would turn into "the model changed its mind" with no trace.
func TestEvalRefusesATwicePredictedItem(t *testing.T) {
	gold := evalGold(t)
	preds := perfectPredictions(gold)
	doubled := append(append([]Prediction{}, preds...), Prediction{
		ItemID: preds[0].ItemID, Verdict: model.ReportVerdictUnsupported,
		QuoteStatus: model.ReportQuoteStatusNotProvided,
	})
	gpath, ppath := writeEvalFiles(t, gold, doubled)

	out := filepath.Join(t.TempDir(), "report.md")
	code, _, errOut := runCapturingSplit(t, "eval", "--gold", gpath, "--pred", ppath, "--exp", "E1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("a doubly predicted item must exit 1, got %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "predicted twice") {
		t.Fatalf("the refusal must say what happened: %q", errOut)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a refused evaluation must not write a report: a file on disk reads like a finished run")
	}
}

// TestEvalMarksARowWithNoDenominatorUnverified: `conflicting_recall` and `macro_f1_core`
// have no value when the gold file has no `conflicting` class at all. The row must say so
// (`unverified`, `—`), never `0/0 = pass`: "nothing was wrong" and "nothing was measured"
// are the two sentences this tool is built to keep apart (doc/301 §11.1).
func TestEvalMarksARowWithNoDenominatorUnverified(t *testing.T) {
	var thin []Item
	for _, it := range goldWithDistribution() {
		if it.GoldVerdict != model.ReportVerdictConflicting {
			thin = append(thin, it)
		}
	}
	preds := perfectPredictions(thin)

	receipt, problems := evaluateExperiment("E1", "no-conflicting", thin, preds)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	for _, group := range []string{"点估计", "CP 下界"} {
		row := rowsOf(t, receipt.Rows, "conflicting_recall", group)
		if row.Denominator != 0 || row.Verdict != evalUnverified || row.Point != "—" {
			t.Fatalf("conflicting_recall %s with n=%d must be unverified + —, got %q/%q",
				group, row.Denominator, row.Verdict, row.Point)
		}
	}
	macro := rowsOf(t, receipt.Rows, "macro_f1_core", "")
	if macro.Verdict != evalUnverified || !strings.Contains(macro.Detail, "没有分母") {
		t.Fatalf("macro F1 without the conflicting class must say why, got %q (%s)", macro.Verdict, macro.Detail)
	}
	if receipt.Verdict != evalUnverified {
		t.Fatalf("receipt verdict = %q, want unverified (a missing class is not a pass)", receipt.Verdict)
	}

	// The exit code has to follow the rows through the CLI, not only in the pure core.
	gpath, ppath := writeEvalFiles(t, thin, preds)
	code, out, _ := runCapturingSplit(t, "eval", "--gold", gpath, "--pred", ppath, "--exp", "E1", "--method", "no-conflicting")
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3: an unmeasured class is unfinished, not wrong", code)
	}
	if !strings.Contains(out, "没有分母") {
		t.Fatalf("the report must carry the reason into the row, got: %s", firstLines(out, 8))
	}
}

// TestEvalExplainsEveryUnverifiedRow is the readability half of the no-denominator rule:
// `unverified` + `—` is the same picture for "this class has no samples", "the run is out of
// scope" and "nobody filled the table in". Every such row therefore has to carry a reason,
// and the reason has to survive into the markdown a human actually reads.
func TestEvalExplainsEveryUnverifiedRow(t *testing.T) {
	thin := []Item{sampleItem(1, model.ReportVerdictSupported)}
	preds := []Prediction{{ItemID: thin[0].ItemID, Verdict: model.ReportVerdictSupported,
		QuoteStatus: model.ReportQuoteStatusVerified}}
	receipt, problems := evaluateExperiment("E1", "", thin, preds)
	if len(problems) != 0 {
		t.Fatalf("refused: %v", problems)
	}
	sawUnverified := false
	for _, r := range receipt.Rows {
		if r.Verdict != evalUnverified {
			continue
		}
		sawUnverified = true
		if strings.TrimSpace(r.Detail) == "" {
			t.Fatalf("%s / %q is unverified with no reason: a reader cannot tell an empty class from an unfinished one",
				r.Metric, r.Group)
		}
	}
	if !sawUnverified {
		t.Fatal("the fixture is supposed to leave rows with no denominator; it produced none, so this test proves nothing")
	}

	receipt.GoldFile, receipt.PredFile = "gold.jsonl", "pred.jsonl"
	md := renderEvalMarkdown(receipt)
	if !strings.Contains(md, "分母 0") {
		t.Fatalf("the report must carry the reason below the table:\n%s", firstLines(md, 40))
	}
	if strings.Contains(md, "评价报告（）") {
		t.Fatalf("an unnamed method must print as —, not as empty parentheses:\n%s", firstLines(md, 3))
	}
}

func describeRows(rows []evalRow) string {
	var parts []string
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s/%s=%s(%d/%d)", r.Metric, r.Group, r.Verdict, r.Numerator, r.Denominator))
	}
	return strings.Join(parts, " ")
}
