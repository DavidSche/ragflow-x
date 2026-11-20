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

// verdictSupportRank orders the support axis. `abstained` sits below `unsupported`
// because it asserts nothing; `conflicting` is the strongest negative.
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

func TestNoVerdictReachesSupportedWithoutAVerifiedQuote(t *testing.T) {
	quoteStates := []reportQuoteCheck{
		{Status: reportQuoteVerified},
		{Status: reportQuoteAmbiguous},
		{Status: reportQuoteNotFound},
		{Status: reportQuoteNotProvided},
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
				}
			}
		}
	}
	// 4 quote states × 6 verdicts × 3 claim types × 2 evidence flags.
	if scanned != 144 {
		t.Fatalf("scanned %d cells, want 144: the cross product shrank and the gate is weaker than it reads", scanned)
	}
}
