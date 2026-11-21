package service

// Tests for G-A1 (doc/301 §11.1) — the release gate as a function.
//
// Two things are asserted here that a happy-path gate cannot assert about itself:
//
//   - **every row can fail**, including the three "policy" shapes (coverage = 100%,
//     quote_hallucination = 0, quote_verified = 100%). A row that passes on an empty
//     input is the failure this file exists to prevent.
//   - **the interval, not the point estimate**, is what the two risky rows compare
//     against: 0/12 has a point estimate of 0 and still fails a `≤5%` ceiling by CP
//     upper bound. That asymmetry is the whole reason §4.2 states those rows as bounds.

import (
	"math"
	"strings"
	"testing"
)

// passingGateInput is a fixture that passes all twelve rows. Each negative case below
// starts from it and breaks exactly one row, so a failure cannot be blamed on the
// neighbours.
func passingGateInput() ReleaseGateInput {
	return ReleaseGateInput{
		UnsupportedFalsePositive: ReleaseGateRatio{Numerator: 0, Denominator: 36},
		VerdictAccuracy:          ReleaseGateRatio{Numerator: 34, Denominator: 36},
		ClaimEvaluationCoverage:  ReleaseGateRatio{Numerator: 120, Denominator: 120},
		MacroF1Core:              0.86,
		MacroF1Classes:           3,
		ConflictingRecall:        ReleaseGateRatio{Numerator: 23, Denominator: 24},
		QuoteHallucination:       ReleaseGateRatio{Numerator: 0, Denominator: 96},
		QuoteVerified:            ReleaseGateRatio{Numerator: 96, Denominator: 96},
		FalseCertification:       ReleaseGateRatio{Numerator: 1, Denominator: 120},
		AbstainModelUncertain:    ReleaseGateRatio{Numerator: 6, Denominator: 120},
		CacheReplayConsistency:   ReleaseGateRatio{Numerator: 50, Denominator: 50},
		PerRevisionTokens:        61200,
		PendingReservations:      0,
		BudgetSettlementDrift:    0.003,
		MonthlyAllowanceIsFinite: true,
	}
}

func TestReleaseGatePassesOnAClaimedFixture(t *testing.T) {
	result := EvaluateReleaseGate(passingGateInput())
	if blocking := result.Blocking(); len(blocking) != 0 {
		t.Fatalf("a healthy fixture must pass every row, got:\n%s", result.String())
	}
	if len(result.Rows) != 12 {
		t.Fatalf("gate has %d rows, want 12 (§4.2 hard thresholds)", len(result.Rows))
	}
}

func TestEveryReleaseGateRowCanFail(t *testing.T) {
	type breaker struct {
		name   string
		rowID  string
		mutate func(*ReleaseGateInput)
	}
	breakers := []breaker{
		{"unsupported false positive too common", "A1-01", func(in *ReleaseGateInput) {
			in.UnsupportedFalsePositive = ReleaseGateRatio{Numerator: 3, Denominator: 36}
		}},
		{"verdict accuracy below floor", "A1-02", func(in *ReleaseGateInput) {
			in.VerdictAccuracy = ReleaseGateRatio{Numerator: 28, Denominator: 36}
		}},
		{"coverage below 100%", "A1-03", func(in *ReleaseGateInput) {
			in.ClaimEvaluationCoverage = ReleaseGateRatio{Numerator: 119, Denominator: 120}
		}},
		{"a claim was skipped but the ratio rounds to 100%", "A1-03", func(in *ReleaseGateInput) {
			in.NotEvaluatedPolicy = 1
		}},
		{"macro F1 below floor", "A1-04", func(in *ReleaseGateInput) { in.MacroF1Core = 0.71 }},
		{"macro F1 over fewer than three classes", "A1-04", func(in *ReleaseGateInput) { in.MacroF1Classes = 2 }},
		{"conflicting recall below floor", "A1-05", func(in *ReleaseGateInput) {
			in.ConflictingRecall = ReleaseGateRatio{Numerator: 18, Denominator: 24}
		}},
		{"one fabricated quote", "A1-06", func(in *ReleaseGateInput) {
			in.QuoteHallucination = ReleaseGateRatio{Numerator: 1, Denominator: 96}
		}},
		{"a quote went unverified", "A1-07", func(in *ReleaseGateInput) {
			in.QuoteVerified = ReleaseGateRatio{Numerator: 95, Denominator: 96}
		}},
		{"false certification above ceiling", "A1-08", func(in *ReleaseGateInput) {
			in.FalseCertification = ReleaseGateRatio{Numerator: 4, Denominator: 40}
		}},
		{"too many model-uncertain abstains", "A1-09", func(in *ReleaseGateInput) {
			in.AbstainModelUncertain = ReleaseGateRatio{Numerator: 20, Denominator: 120}
		}},
		{"cache replay diverged once", "A1-10", func(in *ReleaseGateInput) {
			in.CacheReplayConsistency = ReleaseGateRatio{Numerator: 49, Denominator: 50}
		}},
		{"revision too expensive", "A1-11", func(in *ReleaseGateInput) { in.PerRevisionTokens = 81000 }},
		{"a run left a reservation behind", "A1-12", func(in *ReleaseGateInput) { in.PendingReservations = 1 }},
		{"settlement drift above 1%", "A1-12", func(in *ReleaseGateInput) { in.BudgetSettlementDrift = 0.02 }},
	}
	for _, tc := range breakers {
		t.Run(tc.name, func(t *testing.T) {
			in := passingGateInput()
			tc.mutate(&in)
			result := EvaluateReleaseGate(in)
			row := releaseGateRow(t, result, tc.rowID)
			if row.Verdict != releaseGateFail {
				t.Fatalf("%s must fail, got %s (%s)\n%s", tc.rowID, row.Verdict, row.Observed, result.String())
			}
			if len(result.Blocking()) == 0 {
				t.Fatalf("a failing row must block the release")
			}
		})
	}
}

func TestMissingDenominatorsAreUnverifiedNotPassing(t *testing.T) {
	// An empty input is the shape a half-wired run produces. Every row must refuse to
	// answer rather than report a clean bill of health.
	result := EvaluateReleaseGate(ReleaseGateInput{})
	if len(result.Blocking()) != 12 {
		t.Fatalf("an empty input produced %d blocking row(s), want 12:\n%s",
			len(result.Blocking()), result.String())
	}
	for _, row := range result.Rows {
		if row.Verdict != releaseGateUnverified {
			t.Fatalf("%s answered %s on no data", row.ID, row.Verdict)
		}
	}
}

func TestTheFalseCertificationCeilingIsAnIntervalNotAPointEstimate(t *testing.T) {
	in := passingGateInput()
	// 0 out of 12 is a point estimate of 0% and an upper bound of 26%: the gate must
	// refuse it against a 5% ceiling. This is the case doc/301 §14.A is built around.
	in.FalseCertification = ReleaseGateRatio{Numerator: 0, Denominator: 12}
	row := releaseGateRow(t, EvaluateReleaseGate(in), "A1-08")
	if row.Verdict != releaseGateFail {
		t.Fatalf("0/12 must fail a 5%% CP ceiling, got %s (%s)", row.Verdict, row.Observed)
	}
	if !strings.Contains(row.Observed, "point 0.0000") {
		t.Fatalf("the row must print both the point estimate and the bound, got %q", row.Observed)
	}
}

func TestTheAccountingRowRefusesToBeVacuouslyGreen(t *testing.T) {
	in := passingGateInput()
	// `report.assurance_monthly_tokens = 0` builds no bucket. With no allowance there
	// is no reconciliation, and a zero drift is not evidence of correctness.
	in.MonthlyAllowanceIsFinite = false
	in.BudgetSettlementDrift = 0
	row := releaseGateRow(t, EvaluateReleaseGate(in), "A1-12")
	if row.Verdict != releaseGateUnverified {
		t.Fatalf("an unlimited allowance must leave the row unverified, got %s", row.Verdict)
	}
}

func TestTheCoverageRowCatchesARoundingHundredPercent(t *testing.T) {
	in := passingGateInput()
	in.ClaimEvaluationCoverage = ReleaseGateRatio{Numerator: 120, Denominator: 120}
	in.NotEvaluatedInputInvalid = 1
	row := releaseGateRow(t, EvaluateReleaseGate(in), "A1-03")
	if row.Verdict != releaseGateFail {
		t.Fatalf("a skipped claim must fail coverage even at 120/120, got %s", row.Verdict)
	}
	if !strings.Contains(row.Detail, "not evaluated") {
		t.Fatalf("the row must say why, got %q", row.Detail)
	}
}

func TestTheGatePrintsEveryRowWithItsDenominator(t *testing.T) {
	text := EvaluateReleaseGate(passingGateInput()).String()
	for _, id := range ReleaseGateRowIDs() {
		if !strings.Contains(text, id) {
			t.Fatalf("row %s is missing from the log output", id)
		}
	}
	if !strings.Contains(text, "alpha=0.025") {
		t.Fatalf("the interval's alpha must be printed: %q", text)
	}
	if !strings.Contains(text, "n=") {
		t.Fatalf("every row must print its sample size")
	}
}

// TestClopperPearsonUpperMatchesThePlannedFigures pins the interval itself, because a
// gate whose bound is quietly too narrow certifies with the wrong arithmetic. The
// reference values come from `scipy.stats.beta.ppf` at the same alpha.
func TestClopperPearsonUpperMatchesKnownValues(t *testing.T) {
	cases := []struct {
		k, n int64
		want float64
	}{
		{0, 36, 0.097394}, // doc/301 §14.A: "0/36 ⇒ 上界 9.74%"
		{5, 36, 0.294975},
		{1, 36, 0.145289},
		{2, 20, 0.316983},
		{0, 12, 0.264648},
		{36, 36, 1.0},
	}
	for _, tc := range cases {
		got := ClopperPearsonUpper(tc.k, tc.n, releaseGateAlpha)
		if math.Abs(got-tc.want) > 5e-4 {
			t.Fatalf("CP upper(%d/%d) = %.6f, want %.6f", tc.k, tc.n, got, tc.want)
		}
	}
}

func releaseGateRow(t *testing.T, result ReleaseGateResult, id string) ReleaseGateRow {
	t.Helper()
	for _, row := range result.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("row %s not found in gate output", id)
	return ReleaseGateRow{}
}

// TestReleaseGatePrintsTheTableForTheGateScript is the seam between the Go gate and
// `scripts/release-gate/assurance.sh` (doc/301 §11.1: "验收 = 门禁里有一条会红的检查").
//
// The script cannot import Go, so the table is emitted twice in two shapes: the human
// table (above) and one `gate-row:` line per row, which the script parses. Two rules
// make that parse trustworthy rather than decorative:
//
//   - the line carries `n=` and `denom=` for every row, so "this row passed" can never
//     be printed by a row that had no denominator;
//   - the count is asserted here, not only in the script: if someone deletes a row from
//     the switch, this test fails before the script has a chance to print a shorter,
//     greener table.
func TestReleaseGatePrintsTheTableForTheGateScript(t *testing.T) {
	result := EvaluateReleaseGate(passingGateInput())
	if len(result.Rows) != 12 {
		t.Fatalf("gate has %d rows, want 12 (the script parses exactly twelve gate-row lines)", len(result.Rows))
	}
	for _, row := range result.Rows {
		// `observed` carries the Clopper-Pearson bound on the two upper-bound rows and
		// the bare count elsewhere; both are printed so the log can be audited without
		// re-running anything.
		t.Logf("gate-row: id=%s verdict=%s sample=%s observed=%s", row.ID, row.Verdict, row.Sample, row.Observed)
	}
	t.Logf("gate-alpha: alpha=%.3f", releaseGateAlpha)
}
