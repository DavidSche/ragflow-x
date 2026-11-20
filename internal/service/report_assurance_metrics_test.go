package service

import (
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// These tests are the executable form of doc/301 §5.5 (eligibility) and §6.3
// invariants 3/4/9. G-A8 asserts that eligibility is a pure function with
// table-driven tests; this file is that test.

func evalWith(claimID, verdict, quoteStatus, evidenceIDs string) model.ReportClaimEvaluation {
	return model.ReportClaimEvaluation{
		ClaimID:            claimID,
		SupportVerdict:     verdict,
		QuoteStatus:        quoteStatus,
		EvidenceIDsJSON:    evidenceIDs,
		NotEvaluatedReason: "",
	}
}

// goodRun builds n fully assured claims: supported, verified quote, real source.
func goodRun(n int) []model.ReportClaimEvaluation {
	out := make([]model.ReportClaimEvaluation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, evalWith("claim-"+string(rune('a'+i%26)), model.ReportVerdictSupported,
			model.ReportQuoteStatusVerified, `["snap-1"]`))
	}
	return out
}

func TestReportAssuranceEligibilityIsPendingUntilTerminal(t *testing.T) {
	// A queued run has no metrics, so it must not claim any tier. This is the
	// regression that second review P0-5 caught: `tier` set at creation time.
	for _, status := range []string{model.ReportAssuranceStatusQueued, model.ReportAssuranceStatusRunning} {
		tier, failures := reportAssuranceEligibility(reportAssuranceMetrics{
			Status: status, RequestedPolicy: model.ReportAssurancePolicyAdvisory,
			Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.0),
		})
		if tier != model.ReportAssuranceEligibilityPending {
			t.Fatalf("%s must be pending, got %s", status, tier)
		}
		if len(failures) != 0 {
			t.Fatalf("%s must report no failures, got %v", status, failures)
		}
	}
}

func TestReportAssuranceEligibilityBlockedOnFailedRun(t *testing.T) {
	// `failed` is not a soft state: a run that did not complete cannot certify
	// anything, even if the partial counters look fine.
	for _, status := range []string{model.ReportAssuranceStatusFailed, model.ReportAssuranceStatusCancelled} {
		tier, failures := reportAssuranceEligibility(reportAssuranceMetrics{
			Status: status, Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.0),
		})
		if tier != model.ReportAssuranceEligibilityBlocked {
			t.Fatalf("%s must be blocked, got %s", status, tier)
		}
		if len(failures) == 0 {
			t.Fatalf("%s must name a failure reason", status)
		}
	}
}

func TestReportAssuranceEligibilityThresholds(t *testing.T) {
	cases := []struct {
		name     string
		metrics  reportAssuranceMetrics
		wantTier string
		wantCode string
	}{
		{
			name: "clean run reaches tier_1",
			metrics: reportAssuranceMetrics{
				Status:          model.ReportAssuranceStatusSucceeded,
				RequestedPolicy: model.ReportAssurancePolicyAdvisory,
				Coverage:        ptr(0.92), QuoteHallucinationRate: ptr(0.0),
				QuoteStatuses: map[string]int{model.ReportQuoteStatusVerified: 92},
			},
			wantTier: model.ReportAssuranceEligibilityTier1,
		},
		{
			name: "coverage exactly at the floor passes",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(reportAssuranceCoverageFloor), QuoteHallucinationRate: ptr(0.0),
			},
			wantTier: model.ReportAssuranceEligibilityTier1,
		},
		{
			name: "coverage just under the floor blocks",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(0.84), QuoteHallucinationRate: ptr(0.0),
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "coverage_below_floor",
		},
		{
			// The dangerous case: accuracy is 100% because the run abstained on the
			// hard half. Coverage is the counterweight, and it blocks here.
			name: "abstaining to raise accuracy does not reach tier_1",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(0.50), QuoteHallucinationRate: ptr(0.0),
				AbstainReasons: map[string]int{model.ReportAbstainModelUncertain: 50},
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "coverage_below_floor",
		},
		{
			name: "unknown coverage never satisfies the floor",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: nil, QuoteHallucinationRate: ptr(0.0),
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "coverage_unknown",
		},
		{
			name: "a fabricated quote blocks even at full coverage",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.01),
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "quote_hallucination",
		},
		{
			name: "one not_found quote blocks",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.0),
				QuoteStatuses: map[string]int{model.ReportQuoteStatusNotFound: 1},
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "quote_not_found",
		},
		{
			// 「显示已核实但无来源」 is not a metric to weigh, it is a false statement in
			// the UI: it blocks regardless of everything else.
			name: "verified badge without a source blocks",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.0), VerifiedWithoutSource: 1,
			},
			wantTier: model.ReportAssuranceEligibilityBlocked,
			wantCode: "verified_without_source",
		},
		{
			// Invariant 9: coverage passed on a shrunken denominator. The run is
			// honest, the conclusion is not usable for a release decision.
			name: "coverage on a shrunken denominator stays tier_0",
			metrics: reportAssuranceMetrics{
				Status:   model.ReportAssuranceStatusSucceeded,
				Coverage: ptr(1.0), QuoteHallucinationRate: ptr(0.0), DenominatorSmall: true,
			},
			wantTier: model.ReportAssuranceEligibilityTier0,
			wantCode: "not_required_share_too_high",
		},
		{
			// `required` alone is not enough: without the v0.7.1 blocking evidence
			// the run may not claim tier_2 (else "set a config flag" would replace
			// "meet the bar").
			name: "required policy without blocking evidence stops at tier_1",
			metrics: reportAssuranceMetrics{
				Status:          model.ReportAssuranceStatusSucceeded,
				RequestedPolicy: model.ReportAssurancePolicyRequired,
				Coverage:        ptr(1.0), QuoteHallucinationRate: ptr(0.0),
			},
			wantTier: model.ReportAssuranceEligibilityTier1,
		},
		{
			name: "required policy with blocking evidence reaches tier_2",
			metrics: reportAssuranceMetrics{
				Status:          model.ReportAssuranceStatusSucceeded,
				RequestedPolicy: model.ReportAssurancePolicyRequired,
				Coverage:        ptr(1.0), QuoteHallucinationRate: ptr(0.0),
				BlockingPrerequisitesMet: true,
			},
			wantTier: model.ReportAssuranceEligibilityTier2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, failures := reportAssuranceEligibility(tc.metrics)
			if tier != tc.wantTier {
				t.Fatalf("want %s, got %s (failures %v)", tc.wantTier, tier, failures)
			}
			if tc.wantCode == "" {
				if len(failures) != 0 {
					t.Fatalf("want no failures, got %v", failures)
				}
				return
			}
			found := false
			for _, failure := range failures {
				if failure == tc.wantCode {
					found = true
				}
			}
			if !found {
				t.Fatalf("want failure %q, got %v", tc.wantCode, failures)
			}
		})
	}
}

func TestReportAssuranceMetricsCountCoverageAndNotRequiredSeparately(t *testing.T) {
	evals := []model.ReportClaimEvaluation{
		evalWith("c1", model.ReportVerdictSupported, model.ReportQuoteStatusVerified, `["s1"]`),
		evalWith("c2", model.ReportVerdictUnsupported, model.ReportQuoteStatusNotProvided, `["s2"]`),
		evalWith("c3", model.ReportVerdictNotRequired, "", ""),
		{ClaimID: "c4", SupportVerdict: model.ReportVerdictNotEvaluated,
			NotEvaluatedReason: model.ReportNotEvaluatedBudget},
		{ClaimID: "c5", SupportVerdict: model.ReportVerdictAbstained,
			AbstainReason: model.ReportAbstainQuoteNotFound},
	}
	m := assuranceMetricsFromEvaluations(model.ReportAssuranceStatusSucceeded,
		model.ReportAssurancePolicyAdvisory, evals, nil)

	if m.TotalClaims != 5 || m.NotRequired != 1 || m.Denominator != 4 {
		t.Fatalf("want 5/1/4, got %d/%d/%d", m.TotalClaims, m.NotRequired, m.Denominator)
	}
	if m.Evaluated != 2 {
		t.Fatalf("evaluated must count only scored verdicts (supported+unsupported), got %d", m.Evaluated)
	}
	if m.Coverage == nil || *m.Coverage != 0.5 {
		t.Fatalf("coverage must be 2/4 (not_required excluded from the denominator), got %v", m.Coverage)
	}
	if m.NotRequiredShare == nil || *m.NotRequiredShare != 0.2 {
		t.Fatalf("not_required_share must be 1/5, got %v", m.NotRequiredShare)
	}
	if m.DenominatorSmall {
		t.Fatal("20% is under the 40% ceiling")
	}
	// quote_hallucination_rate counts quote_not_found + no_quote over ALL claims:
	// it measures "how often did we cite text that is not there", which is a
	// statement about the report, not about the evaluated subset.
	if m.QuoteHallucinationRate == nil || *m.QuoteHallucinationRate != 0.2 {
		t.Fatalf("want 1/5, got %v", m.QuoteHallucinationRate)
	}
	// quote_verified_rate's denominator is "claims that need a quote", i.e.
	// supported + partially_supported. An `unsupported` claim has no quote to
	// verify, so it is out — 1/1 here, not 1/2.
	if m.QuoteNeeded != 1 || m.QuoteVerifiedRate == nil || *m.QuoteVerifiedRate != 1 {
		t.Fatalf("want quote_needed=1 and rate=1, got %d / %v", m.QuoteNeeded, m.QuoteVerifiedRate)
	}
}

func TestReportAssuranceNotRequiredShareCeiling(t *testing.T) {
	// Exactly 40% is allowed; 41% is 「分母偏小」. A ceiling that trips at the boundary
	// it documents would make the documented number wrong.
	atCeiling := make([]model.ReportClaimEvaluation, 5)
	for i := range atCeiling {
		atCeiling[i] = evalWith("c", model.ReportVerdictNotRequired, "", "")
	}
	atCeiling[0] = evalWith("c0", model.ReportVerdictSupported, model.ReportQuoteStatusVerified, `["s"]`)
	atCeiling[1] = evalWith("c1", model.ReportVerdictSupported, model.ReportQuoteStatusVerified, `["s"]`)
	atCeiling[2] = evalWith("c2", model.ReportVerdictSupported, model.ReportQuoteStatusVerified, `["s"]`)
	m := assuranceMetricsFromEvaluations("succeeded", "advisory", atCeiling, nil)
	if m.DenominatorSmall {
		t.Fatalf("2/5 = 40%% is at the ceiling and must not trip it (share %v)", m.NotRequiredShare)
	}

	overCeiling := append(atCeiling, evalWith("c5", model.ReportVerdictNotRequired, "", ""))
	over := assuranceMetricsFromEvaluations("succeeded", "advisory", overCeiling, nil)
	if !over.DenominatorSmall {
		t.Fatalf("3/6 = 50%% must trip the ceiling (share %v)", over.NotRequiredShare)
	}
}

func TestReportAssuranceNoVerifiedQuoteMeansNoSupported(t *testing.T) {
	// §6.3 invariant 3, as a unit: `supported` demands BOTH a verified quote and a
	// source id. The two halves fail separately so the test says which one.
	noSource := assuranceMetricsFromEvaluations("succeeded", "advisory",
		[]model.ReportClaimEvaluation{evalWith("c1", model.ReportVerdictSupported,
			model.ReportQuoteStatusVerified, `[]`)}, nil)
	if noSource.VerifiedWithoutSource != 1 || noSource.AssuredClaims != 0 {
		t.Fatalf("an empty evidence list must count as verified-without-source (got %d/%d)",
			noSource.VerifiedWithoutSource, noSource.AssuredClaims)
	}
	notVerified := assuranceMetricsFromEvaluations("succeeded", "advisory",
		[]model.ReportClaimEvaluation{evalWith("c1", model.ReportVerdictSupported,
			model.ReportQuoteStatusNotFound, `["s1"]`)}, nil)
	if notVerified.AssuredClaims != 0 || notVerified.QuoteStatuses[model.ReportQuoteStatusNotFound] != 1 {
		t.Fatal("a not_found quote must not count as assured")
	}
	ok := assuranceMetricsFromEvaluations("succeeded", "advisory",
		[]model.ReportClaimEvaluation{evalWith("c1", model.ReportVerdictSupported,
			model.ReportQuoteStatusVerified, `["s1"]`)}, nil)
	if ok.AssuredClaims != 1 || ok.VerifiedWithoutSource != 0 {
		t.Fatal("a verified quote with a source must count as assured")
	}
}

func TestReportAssuranceMetricsJSONCarriesDenominator(t *testing.T) {
	// §4.3 discipline: the number and its denominator travel together, so a
	// dashboard cannot print "coverage 92%" without "92/100".
	evals := goodRun(10)
	m := assuranceMetricsFromEvaluations("succeeded", "advisory", evals, nil)
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("metrics must marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("metrics must unmarshal: %v", err)
	}
	for _, key := range []string{"coverage", "denominator", "total_claims", "not_required_share",
		"quote_verified_rate", "quote_hallucination_rate", "abstain_reasons",
		"not_evaluated_reasons", "denominator_small"} {
		if _, exists := decoded[key]; !exists {
			t.Fatalf("metrics_json must carry %q", key)
		}
	}
	if decoded["denominator"].(float64) != 10 || decoded["coverage"].(float64) != 1 {
		t.Fatalf("want denominator 10 and coverage 1, got %v / %v", decoded["denominator"], decoded["coverage"])
	}
}

func TestReportAssuranceMetricsExcludeCacheHitFromNotEvaluated(t *testing.T) {
	// Second review P0-3: `cache_hit` is a source marker, not a reason for not
	// evaluating. A cached verdict must land in `evaluated` and in no
	// not_evaluated bucket, or the gold-set "not_evaluated = 0" gate would fail on
	// a perfectly healthy cached replay.
	cached := evalWith("c1", model.ReportVerdictSupported, model.ReportQuoteStatusVerified, `["s1"]`)
	cached.CacheHit = true
	m := assuranceMetricsFromEvaluations("succeeded", "advisory",
		[]model.ReportClaimEvaluation{cached}, nil)
	if m.Evaluated != 1 {
		t.Fatalf("a cache hit is still evaluated, got %d", m.Evaluated)
	}
	if len(m.NotEvaluatedReasons) != 0 {
		t.Fatalf("cache_hit must not appear in not_evaluated_reasons, got %v", m.NotEvaluatedReasons)
	}
	for _, reason := range model.ReportNotEvaluatedReasons() {
		if reason == model.ReportNotEvaluatedCacheHit {
			t.Fatal("cache_hit must not be part of the not_evaluated vocabulary")
		}
	}
}

func TestReportAssuranceDriftIsNilWithoutSettlement(t *testing.T) {
	if got := reportAssuranceDrift(0, 1200); got != nil {
		t.Fatalf("no settlement means no denominator: want nil, got %v", *got)
	}
	if got := reportAssuranceDrift(1000, 1005); got == nil || *got != 0.005 {
		t.Fatalf("want 0.5%%, got %v", got)
	}
}

func ptr[T any](v T) *T { return &v }
