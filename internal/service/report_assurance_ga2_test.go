package service

// G-A2 (doc/301 §10.5) — "no `supported` without a verified quote" as a *scan*, not a
// handful of examples.
//
// The release gate asks for a count over the whole product ("`supported` 且
// `quote_status≠verified` = 0"), and a table of six hand-picked cases cannot produce
// that count: the verdict axis has six values, the quote axis four, the claim type two
// and `requires_evidence` two — the interesting failures live in the cells nobody wrote
// down. This walks the full cross product instead, which is cheap because the clamp is
// pure.
//
// Two invariants are asserted:
//
//   - a surviving `supported` implies `verified` (the gate's own sentence);
//   - the quote layer never *raises* a verdict along the support axis — a claim that
//     contradicts its source must not become partially supported because the quote was
//     fuzzy. That second one is what makes the first one meaningful: without it, a
//     clamp that promoted everything to `supported` on `verified` and left other cells
//     at `supported` by accident could still pass a per-cell test.

import (
	"fmt"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// verdictSupportRank orders the *certainty* axis. `abstained` sits below
// `unsupported` because it asserts nothing; `conflicting` is the strongest negative.
//
// Certainty alone cannot police the clamp (doc/319 R-02): `abstained` ranking below
// `unsupported` meant "a rejection was washed into an abstention" read as a legal
// descent, and the 144-cell sweep stayed green while `reportClampVerdict` rewrote
// every model-judged `unsupported` on a `not_provided` quote. Direction is the
// missing dimension, and it is asserted separately below.
func verdictSupportRank(verdict string) int {
	switch verdict {
	case "":
		return -1
	case model.ReportVerdictAbstained:
		return 0
	case model.ReportVerdictInsufficient:
		return 1
	case model.ReportVerdictUnsupported:
		return 2
	case model.ReportVerdictConflicting:
		return 2
	case model.ReportVerdictPartiallySupported:
		return 3
	case model.ReportVerdictSupported:
		return 4
	}
	// `not_required` / `not_evaluated` never reach the clamp; a new verdict word that
	// does would be ranked here rather than silently compared as 0.
	return 0
}

// verdictDirection is the axis the rank above cannot see: a verdict either rejects
// the claim (-1), certifies it (+1), or declines to say (0).
//
// Sliding *along* the certainty axis is what a weak quote may cause. Crossing from
// -1 to 0 is not a weakening, it is an erasure: the conclusion and the finding that
// `reportAssuranceFindingOfFor` derives from it both disappear (doc/319 R-02).
func verdictDirection(verdict string) int {
	switch verdict {
	case model.ReportVerdictUnsupported, model.ReportVerdictConflicting, model.ReportVerdictOutdated:
		return -1
	case model.ReportVerdictPartiallySupported, model.ReportVerdictSupported:
		return 1
	}
	return 0 // abstained / insufficient_evidence / not_required / not_evaluated
}

func TestNoVerdictReachesSupportedWithoutAVerifiedQuote(t *testing.T) {
	quoteStates := []reportQuoteCheck{
		{Status: reportQuoteVerified},
		{Status: reportQuoteAmbiguous},
		{Status: reportQuoteNotFound},
		{Status: reportQuoteNotProvided},
		// The two lifecycle states of an unreadable snapshot (doc/319 R-01). They are
		// unreachable from the pipeline — the run closes them before the semantic layer
		// is chosen — which is exactly why the clamp's own handling of them has to be
		// asserted here rather than inferred from the pipeline's behaviour.
		{Status: reportQuoteEvidenceExpired},
		{Status: reportQuoteEvidenceMissing},
	}
	verdicts := []string{
		model.ReportVerdictSupported, model.ReportVerdictPartiallySupported,
		model.ReportVerdictUnsupported, model.ReportVerdictConflicting,
		model.ReportVerdictAbstained, model.ReportVerdictInsufficient,
	}
	claimTypes := []string{"fact", "inference", "recommendation"}
	scanned := 0
	for _, check := range quoteStates {
		for _, verdict := range verdicts {
			for _, claimType := range claimTypes {
				for _, requiresEvidence := range []bool{true, false} {
					scanned++
					got, _ := reportClampVerdict(verdict, check, claimType, requiresEvidence)
					name := fmt.Sprintf("%s/%s/%s/%v", check.Status, verdict, claimType, requiresEvidence)
					if got == model.ReportVerdictSupported && check.Status != reportQuoteVerified {
						t.Fatalf("%s: produced `supported` on a %s quote", name, check.Status)
					}
					if verdictSupportRank(got) > verdictSupportRank(verdict) {
						t.Fatalf("%s: clamp raised %s to %s", name, verdict, got)
					}
					if verdictDirection(got) > verdictDirection(verdict) {
						t.Fatalf("%s: clamp moved %s up the direction axis to %s", name, verdict, got)
					}
					// A rejection may weaken towards "declines to say" only by naming a
					// reason; what it may never do is leave the negative side, because that
					// is where the finding lives.
					if verdictDirection(verdict) < 0 && verdictDirection(got) >= 0 {
						t.Fatalf("%s: clamp took %s off the negative side (%s): a quote problem "+
							"may weaken certainty, never erase a conclusion or its finding",
							name, verdict, got)
					}
				}
			}
		}
	}
	// 6 quote states × 6 verdicts × 3 claim types × 2 evidence flags.
	if scanned != 216 {
		t.Fatalf("scanned %d cells, want 216: the cross product shrank and the gate is weaker than it reads", scanned)
	}
}
