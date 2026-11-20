package service

import (
	"encoding/json"
	"math"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Assurance metrics and release eligibility (doc/301 §4.3 / §5.5).
//
// Everything in this file is pure: no database, no clock, no network. That is a
// gate, not a style choice — G-A8 asserts that `release_eligibility` is produced
// by a pure function with table-driven tests and has no manual write path, because
// the one thing a quality gate must never allow is "somebody set the field".

const (
	// reportAssuranceCoverageFloor is the runtime floor for a production run
	// (doc/301 §4.2). The gold-set replay uses a *different* threshold (100%): with
	// a fixed fixture and no budget pressure, 85% means "we skipped a fifth of the
	// claims", not "we did well".
	reportAssuranceCoverageFloor = 0.85
	// reportAssuranceNotRequiredCeiling is §6.3 invariant 9: above this share the
	// coverage figure is marked 「分母偏小」 and the revision may not be used for an
	// E1/E2 threshold decision.
	reportAssuranceNotRequiredCeiling = 0.40
)

// reportAssuranceMetrics is the §4.3 metric set of one run. Pointers on every
// rate are what encode "`null` is not `0`": an empty denominator is `nil`, and a
// `nil` rate never satisfies a threshold (it fails it with an explicit reason).
type reportAssuranceMetrics struct {
	Status          string `json:"status"`
	RequestedPolicy string `json:"requested_policy"`

	TotalClaims int `json:"total_claims"`
	// NotRequired is excluded from the coverage denominator (§4.3 status table):
	// whether an inference/recommendation claim *needs* a support verdict is a
	// template-semantics question, and mixing it in would make coverage
	// incomparable between templates.
	NotRequired      int      `json:"not_required"`
	NotRequiredShare *float64 `json:"not_required_share"`
	// DenominatorSmall is the §6.3 invariant 9 marker, written into metrics_json
	// by the aggregator rather than trusted to a reader.
	DenominatorSmall bool `json:"denominator_small"`

	// Evaluated counts every claim with a support verdict, cache hits included:
	// a cached verdict was still evaluated, once, by someone (§4.3).
	Evaluated int `json:"evaluated"`
	// Denominator is `total_claims − not_required`, the coverage denominator.
	Denominator int `json:"denominator"`

	Coverage *float64 `json:"coverage"`
	// Accuracy is verdict accuracy *conditioned on coverage*: it is reported next
	// to coverage and never alone, because abstaining would otherwise raise it.
	Accuracy *float64 `json:"accuracy"`

	Verdicts            map[string]int `json:"verdicts"`
	AbstainReasons      map[string]int `json:"abstain_reasons"`
	NotEvaluatedReasons map[string]int `json:"not_evaluated_reasons"`
	QuoteStatuses       map[string]int `json:"quote_statuses"`

	QuoteNeeded            int      `json:"quote_needed"`
	QuoteVerifiedRate      *float64 `json:"quote_verified_rate"`
	QuoteHallucinationRate *float64 `json:"quote_hallucination_rate"`
	AbstainUncertainRate   *float64 `json:"abstain_model_uncertain_rate"`
	SemanticSupportRate    *float64 `json:"semantic_support_rate"`
	StructurePassRate      *float64 `json:"structure_pass_rate"`

	// VerifiedWithoutSource is 「显示已核实但无来源」 (G-A2). Non-zero is a lie in the
	// UI, so it blocks regardless of every other number.
	VerifiedWithoutSource int `json:"verified_without_source"`

	// AssuredClaims is the count of claims that carry both a source and a verified
	// quote; it is 0 for a run of an empty revision, which is why coverage — not
	// this — decides the tier.
	AssuredClaims int `json:"assured_claims"`

	// BlockingPrerequisitesMet is the v0.7.1 evidence flag (§4.2 `Tier-2 前置`).
	// It is always false in v0.7.0: tier_2 exists in the vocabulary so v0.7.1 can
	// open blocking without a schema change, not so it can be claimed early.
	BlockingPrerequisitesMet bool `json:"blocking_prerequisites_met"`
}

// assuranceMetricsFromEvaluations aggregates one run's evaluation rows.
//
// `goldVerdicts` maps claim_id to the gold label; it is nil outside the gold-set
// replay, and only the gold-set numbers (accuracy, false certification) are filled
// from it. Deriving accuracy from the run's own verdicts would be circular.
func assuranceMetricsFromEvaluations(
	status, requestedPolicy string,
	evals []model.ReportClaimEvaluation,
	goldVerdicts map[string]string,
) reportAssuranceMetrics {
	m := reportAssuranceMetrics{
		Status:              status,
		RequestedPolicy:     requestedPolicy,
		TotalClaims:         len(evals),
		Verdicts:            map[string]int{},
		AbstainReasons:      map[string]int{},
		NotEvaluatedReasons: map[string]int{},
		QuoteStatuses:       map[string]int{},
	}
	for _, evaluation := range evals {
		m.Verdicts[evaluation.SupportVerdict]++
		switch evaluation.SupportVerdict {
		case model.ReportVerdictNotRequired:
			m.NotRequired++
		case model.ReportVerdictNotEvaluated:
			m.NotEvaluatedReasons[evaluation.NotEvaluatedReason]++
		case model.ReportVerdictAbstained:
			m.AbstainReasons[evaluation.AbstainReason]++
		}
		if model.ReportVerdictScored(evaluation.SupportVerdict) {
			m.Evaluated++
		}
		if evaluation.QuoteStatus != "" {
			m.QuoteStatuses[evaluation.QuoteStatus]++
		}
		// A claim whose UI renders 「已核实」 must have both a source and a verified
		// quote. `supported` with no evidence id is the exact failure G-A2 scans for.
		if evaluation.SupportVerdict == model.ReportVerdictSupported ||
			evaluation.SupportVerdict == model.ReportVerdictPartiallySupported {
			m.QuoteNeeded++
			if evaluation.QuoteStatus == model.ReportQuoteStatusVerified &&
				evidenceIDsCount(evaluation.EvidenceIDsJSON) > 0 {
				m.AssuredClaims++
			} else if evaluation.QuoteStatus == model.ReportQuoteStatusVerified {
				m.VerifiedWithoutSource++
			}
		}
	}

	m.Denominator = m.TotalClaims - m.NotRequired
	m.Coverage = ratioPtr(m.Evaluated, m.Denominator)
	m.NotRequiredShare = ratioPtr(m.NotRequired, m.TotalClaims)
	if m.NotRequiredShare != nil && *m.NotRequiredShare > reportAssuranceNotRequiredCeiling {
		m.DenominatorSmall = true
	}
	m.QuoteVerifiedRate = ratioPtr(m.QuoteStatuses[model.ReportQuoteStatusVerified], m.QuoteNeeded)
	m.QuoteHallucinationRate = ratioPtr(
		m.AbstainReasons[model.ReportAbstainQuoteNotFound]+m.AbstainReasons[model.ReportAbstainNoQuote],
		m.TotalClaims)
	m.AbstainUncertainRate = ratioPtr(m.AbstainReasons[model.ReportAbstainModelUncertain], m.TotalClaims)

	factual := m.Verdicts[model.ReportVerdictSupported] + m.Verdicts[model.ReportVerdictPartiallySupported] +
		m.Verdicts[model.ReportVerdictUnsupported] + m.Verdicts[model.ReportVerdictConflicting]
	m.SemanticSupportRate = ratioPtr(m.Verdicts[model.ReportVerdictSupported], factual)

	structural := countByStructure(evals)
	m.StructurePassRate = ratioPtr(structural[model.ReportStructurePassed],
		structural[model.ReportStructurePassed]+structural[model.ReportStructureFailed])

	if goldVerdicts != nil {
		correct := 0
		scored := 0
		for _, evaluation := range evals {
			gold, ok := goldVerdicts[evaluation.ClaimID]
			if !ok || !model.ReportVerdictScored(gold) {
				continue
			}
			scored++
			if gold == evaluation.SupportVerdict {
				correct++
			}
		}
		m.Accuracy = ratioPtr(correct, scored)
	}
	return m
}

func countByStructure(evals []model.ReportClaimEvaluation) map[string]int {
	out := map[string]int{}
	for _, evaluation := range evals {
		if evaluation.StructureVerdict != "" {
			out[evaluation.StructureVerdict]++
		}
	}
	return out
}

// evidenceIDsCount counts the non-empty ids in an `evidence_ids_json` column
// without trusting the string: `"[]"`, `""` and `"null"` all mean "no source".
func evidenceIDsCount(raw string) int {
	if raw == "" {
		return 0
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return 0
	}
	n := 0
	for _, id := range ids {
		if id != "" {
			n++
		}
	}
	return n
}

// ratioPtr returns nil when the denominator is empty. This is the whole "null ≠ 0"
// discipline in one function: 0/0 is unknown, not zero percent.
func ratioPtr(numerator, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

// reportAssuranceEligibility is the ONLY producer of `release_eligibility`
// (doc/301 §5.5). It returns the tier and, when the tier is not tier_2/tier_1, the
// list of conditions that failed — the UI needs the reasons, and a bare `tier_0`
// would make the reviewer guess which number was short.
func reportAssuranceEligibility(m reportAssuranceMetrics) (string, []string) {
	if !model.ReportAssuranceTerminal(m.Status) {
		// While a run is queued or running there are no metrics yet. Returning
		// anything other than `pending` here would announce a conclusion before the
		// measurement (second review P0-5).
		return model.ReportAssuranceEligibilityPending, nil
	}
	if m.Status == model.ReportAssuranceStatusFailed || m.Status == model.ReportAssuranceStatusCancelled {
		return model.ReportAssuranceEligibilityBlocked, []string{"run_" + m.Status}
	}

	var failures []string
	if m.Coverage == nil {
		// Every claim was `not_required` (or there were none): coverage is unknown,
		// and unknown never satisfies a floor. It is not "100% because we skipped
		// nothing that mattered".
		failures = append(failures, "coverage_unknown")
	} else if *m.Coverage < reportAssuranceCoverageFloor {
		failures = append(failures, "coverage_below_floor")
	}
	if m.QuoteHallucinationRate == nil || *m.QuoteHallucinationRate > 0 {
		failures = append(failures, "quote_hallucination")
	}
	if m.QuoteStatuses[model.ReportQuoteStatusNotFound] > 0 {
		failures = append(failures, "quote_not_found")
	}
	if m.VerifiedWithoutSource > 0 {
		failures = append(failures, "verified_without_source")
	}
	if len(failures) > 0 {
		return model.ReportAssuranceEligibilityBlocked, failures
	}

	// Tier-1 conditions hold. Tier-2 additionally needs the operator to have asked
	// for blocking *and* the v0.7.1 evidence to exist; `required` is rejected by the
	// settings validator in v0.7.0, so this branch is unreachable until T18.
	if m.RequestedPolicy == model.ReportAssurancePolicyRequired && m.BlockingPrerequisitesMet {
		return model.ReportAssuranceEligibilityTier2, nil
	}
	if m.DenominatorSmall {
		// Coverage passed, but on a denominator this revision shrank by marking
		// claims `not_required` (invariant 9). It stays tier_0: the run is honest,
		// the coverage conclusion is not usable for a release decision.
		return model.ReportAssuranceEligibilityTier0, []string{"not_required_share_too_high"}
	}
	return model.ReportAssuranceEligibilityTier1, nil
}

// reportAssuranceEligibilityBasis renders the failures for `metrics_json` so the
// workbench can explain a `blocked` without a second query.
func reportAssuranceEligibilityBasis(failures []string) string {
	if len(failures) == 0 {
		return "[]"
	}
	payload, err := json.Marshal(failures)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

// reportAssuranceDrift is `budget_settlement_drift` (§4.2, single-run form):
// (settled − actual) / settled. A nil result means "nothing was settled", which
// must not be read as a 0% drift.
func reportAssuranceDrift(settled, actual int64) *float64 {
	if settled <= 0 {
		return nil
	}
	drift := math.Abs(float64(settled-actual)) / float64(settled)
	return &drift
}
