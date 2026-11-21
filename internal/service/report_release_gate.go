package service

// G-A1 (doc/301 §11.1) — the release gate, as a function.
//
// The gate is twelve rows of §4.2's `硬门槛`, and 301 states four properties it must
// keep, each of which is a way this file could go wrong:
//
//  1. **Every row must be able to fail.** A row whose denominator is missing has not
//     been verified; it must report `unverified` and block, never `pass`. A gate that
//     silently skips a row when its input is zero is the "0/0 = 100%" bug wearing a
//     lab coat, and this build had exactly that bug in `formatRatio` already.
//  2. **`n` and the interval must be printed.** The two decisions that matter
//     (`unsupported_false_positive_rate`, `false_certification_rate`) are stated as
//     *upper bounds*, so a point estimate alone can look fine while the interval says
//     nothing. Each row carries its own `n` and, where 301 demands it, the
//     Clopper–Pearson upper bound at α=0.025 (the one-sided half of a two-sided 95%
//     interval — 0/36 therefore bounds at 9.74%, not 7.98%).
//  3. **The accounting rows need a real denominator.** `report.assurance_monthly_tokens = 0`
//     means "unlimited", which means no bucket, which means no reconciliation — so
//     the gate must be told whether the fixture had a finite allowance instead of
//     inferring it from a zero drift.
//  4. **No row may be added here without a metric behind it.** `Rows` is a closed
//     list built by one switch; there is no way to append a row from outside.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/stats"
)

// Gate verdict words. Three, not two: "not measured" is a different answer from
// "measured and failed", and collapsing them would let an unrun gate ship.
const (
	releaseGatePass       = "pass"
	releaseGateFail       = "fail"
	releaseGateUnverified = "unverified"
)

// releaseGateAlpha is the one-sided tail of a two-sided 95% interval, which is what
// doc/301 §14.A quotes ("0/36 ⇒ 上界 9.74%"). Using 0.05 here would quietly widen the
// gate's claim of what it proved.
//
// It is an alias of stats.Alpha rather than a second literal: the gate and the gold-set
// evaluation must be unable to drift apart, and a duplicated 0.025 is exactly how that
// drift starts.
const releaseGateAlpha = stats.Alpha

// releaseGatePerRevisionTokenCeiling is §4.2's "单修订 token ≤80k".
const releaseGatePerRevisionTokenCeiling int64 = 80000

// releaseGateDriftCeiling is §4.2's `budget_settlement_drift` ≤1%.
const releaseGateDriftCeiling = 0.01

// ReleaseGateRatio is a count with its denominator. Both are required: the gate
// refuses to invent a denominator from the other rows.
type ReleaseGateRatio struct {
	Numerator   int64
	Denominator int64
}

// ReleaseGateInput is what the offline gold-set replay hands the gate. Every field is
// mandatory by construction — a caller with no data leaves zeros and gets `unverified`
// rather than a pass.
type ReleaseGateInput struct {
	// ① unsupported_false_positive_rate — a `supported` verdict on a claim the gold set
	// marks as not supported. Denominator = gold-set claims that must NOT be supported.
	UnsupportedFalsePositive ReleaseGateRatio
	// ② verdict_accuracy@evaluated — denominator = evaluated claims.
	VerdictAccuracy ReleaseGateRatio
	// ③ claim_evaluation_coverage — denominator = claims in scope of the replay.
	ClaimEvaluationCoverage ReleaseGateRatio
	// ④ macro_f1_core over the three core verdicts, plus the class count so a
	// one-class fixture cannot claim a perfect macro F1.
	MacroF1Core    float64
	MacroF1Classes int
	// ⑤ conflicting_recall — denominator = gold-set conflicting claims.
	ConflictingRecall ReleaseGateRatio
	// ⑥ quote_hallucination_rate — denominator = claims carrying a quote.
	QuoteHallucination ReleaseGateRatio
	// ⑦ quote_verified_rate — denominator = claims carrying a quote.
	QuoteVerified ReleaseGateRatio
	// ⑧ false_certification_rate — denominator = all evaluated claims.
	FalseCertification ReleaseGateRatio
	// ⑨ abstain_rate(model_uncertain) — denominator = all claims.
	AbstainModelUncertain ReleaseGateRatio
	// ⑩ cache_replay_consistency — denominator = replays (301 says 50).
	CacheReplayConsistency ReleaseGateRatio
	// ⑪ the largest single-revision token cost in the replay.
	PerRevisionTokens int64
	// ⑫ accounting: a run's leftovers and the settlement drift.
	PendingReservations      int64
	BudgetSettlementDrift    float64
	MonthlyAllowanceIsFinite bool
	// ⑬ `not_evaluated` must be zero under a sufficient offline replay (§4.2).
	NotEvaluatedBudget       int64
	NotEvaluatedPolicy       int64
	NotEvaluatedInputInvalid int64
}

// ReleaseGateRow is one line of the gate's output. `Detail` is written for a human
// reading a CI log: it must state the observed value *and* the denominator, because
// the log is the only artifact that survives the run.
type ReleaseGateRow struct {
	ID        string
	Metric    string
	Observed  string
	Threshold string
	Sample    string
	Verdict   string
	Detail    string
}

// ReleaseGateResult is the whole gate. `Blocking` is what CI reads; `Rows` is what a
// reviewer reads.
type ReleaseGateResult struct {
	Rows []ReleaseGateRow
}

// Blocking returns only the rows that must stop a release, which is every row that is
// not `pass` — unverified included.
func (r ReleaseGateResult) Blocking() []ReleaseGateRow {
	blocking := make([]ReleaseGateRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Verdict != releaseGatePass {
			blocking = append(blocking, row)
		}
	}
	return blocking
}

// String renders the gate as a table for a CI log, one row per line, so a failure is
// readable without opening the JSON.
func (r ReleaseGateResult) String() string {
	lines := make([]string, 0, len(r.Rows)+1)
	lines = append(lines, "G-A1 release gate (doc/301 §11.1)")
	for _, row := range r.Rows {
		lines = append(lines, fmt.Sprintf("%-6s %-9s %-34s observed=%-18s threshold=%-14s n=%s  %s",
			row.ID, row.Verdict, row.Metric, row.Observed, row.Threshold, row.Sample, row.Detail))
	}
	return strings.Join(lines, "\n")
}

// EvaluateReleaseGate runs the twelve §4.2 hard thresholds.
//
// The row list is closed and ordered: a new row means a new §4.2 metric, and that is a
// plan change, not a code change.
func EvaluateReleaseGate(in ReleaseGateInput) ReleaseGateResult {
	rows := make([]ReleaseGateRow, 0, 12)
	add := func(row ReleaseGateRow) { rows = append(rows, row) }

	// ① unsupported_false_positive_rate — CP upper bound ≤10%.
	add(releaseGateUpperBoundRow("A1-01", "unsupported_false_positive_rate",
		in.UnsupportedFalsePositive, 0.10))

	// ② verdict_accuracy@evaluated — point estimate ≥85%.
	add(releaseGateLowerBoundRow("A1-02", "verdict_accuracy@evaluated",
		in.VerdictAccuracy, 0.85))

	// ③ claim_evaluation_coverage — exactly 100%, and the three reasons all zero.
	{
		row := releaseGateLowerBoundRow("A1-03", "claim_evaluation_coverage",
			in.ClaimEvaluationCoverage, 1.0)
		reasons := in.NotEvaluatedBudget + in.NotEvaluatedPolicy + in.NotEvaluatedInputInvalid
		row.Sample = fmt.Sprintf("%d/%d, not_evaluated=%d(budget)+%d(policy)+%d(input_invalid)",
			in.ClaimEvaluationCoverage.Numerator, in.ClaimEvaluationCoverage.Denominator,
			in.NotEvaluatedBudget, in.NotEvaluatedPolicy, in.NotEvaluatedInputInvalid)
		if row.Verdict == releaseGatePass && reasons != 0 {
			row.Verdict = releaseGateFail
			row.Detail = fmt.Sprintf("%d claim(s) were not evaluated: coverage below 100%% even though the ratio rounds to it", reasons)
		}
		add(row)
	}

	// ④ macro_f1_core ≥0.80, over three classes.
	{
		row := ReleaseGateRow{ID: "A1-04", Metric: "macro_f1_core", Threshold: ">=0.80", Sample: "3 classes"}
		switch {
		case in.MacroF1Classes == 0:
			row.Verdict = releaseGateUnverified
			row.Observed = "n/a"
			row.Detail = "no class count reported: a macro F1 over one class is 1.0 by arithmetic"
		case in.MacroF1Classes < 3:
			row.Verdict = releaseGateFail
			row.Observed = fmt.Sprintf("%.4f", in.MacroF1Core)
			row.Sample = fmt.Sprintf("%d classes", in.MacroF1Classes)
			row.Detail = "macro F1 must be computed over the three core verdicts, not a subset"
		default:
			row.Observed = fmt.Sprintf("%.4f", in.MacroF1Core)
			if in.MacroF1Core >= 0.80 {
				row.Verdict = releaseGatePass
			} else {
				row.Verdict = releaseGateFail
				row.Detail = "macro F1 below 0.80"
			}
		}
		add(row)
	}

	// ⑤ conflicting_recall — ≥90%.
	add(releaseGateLowerBoundRow("A1-05", "conflicting_recall", in.ConflictingRecall, 0.90))

	// ⑥ quote_hallucination_rate — must be exactly 0.
	add(releaseGateExactZeroRow("A1-06", "quote_hallucination_rate", in.QuoteHallucination))

	// ⑦ quote_verified_rate — must be exactly 100%.
	add(releaseGateLowerBoundRow("A1-07", "quote_verified_rate", in.QuoteVerified, 1.0))

	// ⑧ false_certification_rate — point estimate ≤5%.
	add(releaseGateUpperBoundRow("A1-08", "false_certification_rate",
		in.FalseCertification, 0.05))

	// ⑨ abstain_rate(model_uncertain) ≤15%.
	add(releaseGateUpperBoundRow("A1-09", "abstain_rate(model_uncertain)",
		in.AbstainModelUncertain, 0.15))

	// ⑩ cache_replay_consistency = 100%.
	add(releaseGateLowerBoundRow("A1-10", "cache_replay_consistency",
		in.CacheReplayConsistency, 1.0))

	// ⑪ single-revision token ceiling.
	{
		row := ReleaseGateRow{ID: "A1-11", Metric: "per_revision_tokens", Threshold: "<=80000",
			Sample: "1 revision", Observed: fmt.Sprintf("%d", in.PerRevisionTokens)}
		switch {
		case in.PerRevisionTokens <= 0:
			row.Verdict = releaseGateUnverified
			row.Detail = "no token cost reported: a zero here means the replay did not run, not that it was free"
		case in.PerRevisionTokens <= releaseGatePerRevisionTokenCeiling:
			row.Verdict = releaseGatePass
		default:
			row.Verdict = releaseGateFail
			row.Detail = "one revision cost more than 80k tokens"
		}
		add(row)
	}

	// ⑫ accounting: pending=0, drift ≤1%, on a finite allowance.
	add(releaseGateAccountingRow(in))

	return ReleaseGateResult{Rows: rows}
}

// releaseGateLowerBoundRow asserts `numerator/denominator ≥ floor`.
func releaseGateLowerBoundRow(id, metric string, ratio ReleaseGateRatio, floor float64) ReleaseGateRow {
	row := ReleaseGateRow{ID: id, Metric: metric,
		Threshold: fmt.Sprintf(">=%.4g", floor),
		Sample:    fmt.Sprintf("%d/%d", ratio.Numerator, ratio.Denominator)}
	if ratio.Denominator <= 0 {
		row.Verdict = releaseGateUnverified
		row.Observed = "n/a"
		row.Detail = "no denominator: 0/0 is not 100%"
		return row
	}
	value := float64(ratio.Numerator) / float64(ratio.Denominator)
	row.Observed = fmt.Sprintf("%.4f", value)
	if value+1e-12 >= floor {
		row.Verdict = releaseGatePass
	} else {
		row.Verdict = releaseGateFail
		row.Detail = fmt.Sprintf("%.4f is below %.4g", value, floor)
	}
	return row
}

// releaseGateUpperBoundRow asserts the Clopper–Pearson upper bound ≤ ceiling.
//
// The point estimate is printed too, because the two disagree exactly when it matters:
// 0/12 is a point estimate of 0 and an upper bound of 26%, and only the second number
// says anything about the next 100 claims.
func releaseGateUpperBoundRow(id, metric string, ratio ReleaseGateRatio, ceiling float64) ReleaseGateRow {
	row := ReleaseGateRow{ID: id, Metric: metric,
		Threshold: fmt.Sprintf("CP upper <=%.4g", ceiling)}
	if ratio.Denominator <= 0 {
		row.Verdict = releaseGateUnverified
		row.Observed = "n/a"
		row.Detail = "no denominator: an interval on zero observations is unbounded, not empty"
		return row
	}
	upper := ClopperPearsonUpper(ratio.Numerator, ratio.Denominator, releaseGateAlpha)
	point := float64(ratio.Numerator) / float64(ratio.Denominator)
	row.Observed = fmt.Sprintf("%.4f (point %.4f)", upper, point)
	row.Sample = fmt.Sprintf("%d/%d, alpha=%.3f", ratio.Numerator, ratio.Denominator, releaseGateAlpha)
	if upper <= ceiling+1e-12 {
		row.Verdict = releaseGatePass
	} else {
		row.Verdict = releaseGateFail
		row.Detail = fmt.Sprintf("upper bound %.4f exceeds %.4g: the point estimate (%.4f) is not the claim being made",
			upper, ceiling, point)
	}
	return row
}

// releaseGateExactZeroRow asserts the numerator is zero, on a non-empty denominator.
func releaseGateExactZeroRow(id, metric string, ratio ReleaseGateRatio) ReleaseGateRow {
	row := ReleaseGateRow{ID: id, Metric: metric, Threshold: "=0",
		Sample: fmt.Sprintf("%d/%d", ratio.Numerator, ratio.Denominator)}
	if ratio.Denominator <= 0 {
		row.Verdict = releaseGateUnverified
		row.Observed = "n/a"
		row.Detail = "no denominator: \"no hallucination found\" and \"no quote was checked\" would print the same 0"
		return row
	}
	row.Observed = fmt.Sprintf("%d", ratio.Numerator)
	if ratio.Numerator == 0 {
		row.Verdict = releaseGatePass
	} else {
		row.Verdict = releaseGateFail
		row.Detail = fmt.Sprintf("%d fabricated quote(s)", ratio.Numerator)
	}
	return row
}

// releaseGateAccountingRow is ⑫. It is the one row that can be *vacuous* by
// configuration rather than by missing data, which is why the allowance is an explicit
// input: `report.assurance_monthly_tokens = 0` builds no bucket, so there is nothing to
// reconcile and the row must say so instead of passing.
func releaseGateAccountingRow(in ReleaseGateInput) ReleaseGateRow {
	row := ReleaseGateRow{ID: "A1-12", Metric: "billing_and_quota_self_consistency",
		Threshold: "pending=0, drift<=0.01"}
	if !in.MonthlyAllowanceIsFinite {
		row.Verdict = releaseGateUnverified
		row.Observed = "n/a"
		row.Sample = "0 runs with a finite allowance"
		row.Detail = "report.assurance_monthly_tokens=0 means unlimited: no bucket is built, so the reconciliation has no denominator"
		return row
	}
	row.Sample = fmt.Sprintf("pending=%d, drift=%.2f%%", in.PendingReservations, in.BudgetSettlementDrift*100)
	row.Observed = fmt.Sprintf("pending=%d, drift=%.4f", in.PendingReservations, in.BudgetSettlementDrift)
	switch {
	case in.PendingReservations > 0:
		row.Verdict = releaseGateFail
		row.Detail = "a run left reservations behind: the quota ledger and the run disagree"
	case in.BudgetSettlementDrift > releaseGateDriftCeiling:
		row.Verdict = releaseGateFail
		row.Detail = "settled tokens drift more than 1% from the metered cost"
	default:
		row.Verdict = releaseGatePass
	}
	return row
}

// ClopperPearsonUpper returns the exact one-sided upper confidence bound for a binomial
// proportion (doc/301 §14.A). The arithmetic lives in `internal/pkg/stats` because the
// gold-set evaluation (`cmd/goldtool eval`) has to certify the *experiment* with the same
// interval this gate certifies a *run* with; two implementations could disagree at the
// fourth decimal while both printing a plausible number, and the gate would then pass a
// run whose experiment failed.
//
// It stays here, as a one-line delegation, because the release gate is the caller that
// has a name in the docs and in `scripts/release-gate/assurance.sh`.
func ClopperPearsonUpper(k, n int64, alpha float64) float64 {
	return stats.Upper(k, n, alpha)
}

// ReleaseGateRowIDs returns the closed row list, used by the test that pins it.
func ReleaseGateRowIDs() []string {
	ids := make([]string, 0, 12)
	for _, row := range EvaluateReleaseGate(ReleaseGateInput{}).Rows {
		ids = append(ids, row.ID)
	}
	sort.Strings(ids)
	return ids
}
