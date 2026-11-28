package service

// ScenarioID: SC-DECISION-001
//
// doc/specification/16 §1–§3 freezes the S-1 seam (doc/319 §5): an assurance verdict, an
// approval decision and a publish action are three objects, and none of them may speak for
// another. Three things follow, and each is cheap to get wrong in a way no compiler sees:
//
//   I-1  the report surface's approval can only widen a token envelope — never a verdict;
//   I-2  a blocking condition that holds must not produce a usable tier;
//   I-3  "which sentence does the refusal have to say" is decided by the stronger statement,
//        and `pending` demands nothing from anybody.
//
// The tests below pin the *invariants*, not the current numbers: they enumerate the state
// space (every combination of blocking flags) instead of asserting one example, because an
// example is satisfied by an implementation that special-cases it.

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestReportApprovalCannotCarryAnyActionButExtendBudget is I-1's registry half.
//
// If someone registers a second action on the report surface — "approve-content",
// "override-verdict", anything — this test reds before that action can reach production,
// because the *only* governed action on this surface is a budget extension (`doc/296` §2.2:
// a report's content is never approved, only its budget).
func TestReportApprovalCannotCarryAnyActionButExtendBudget(t *testing.T) {
	// A zero Service is enough: the registry constructor only stores executors, it does not
	// call the store. Passing a nil store keeps a future dependency honest — if building the
	// registry starts needing one, this test panics loudly instead of quietly passing.
	svc := &Service{}
	registry := svc.approvalExecutorRegistry()

	var reportKeys []string
	for key := range registry.executors {
		if strings.HasPrefix(key, model.ApprovalObjectReport+".") {
			reportKeys = append(reportKeys, key)
		}
	}
	if len(reportKeys) != 1 {
		t.Fatalf("the report surface registered %d approval actions (%v); doc/specification/16 §1 "+
			"allows exactly one — a frozen token envelope may only grow through an approval, "+
			"because report content is never an approval object", len(reportKeys), reportKeys)
	}
	want := model.ApprovalObjectReport + "." + model.ApprovalActionExtendBudget
	if reportKeys[0] != want {
		t.Fatalf("the report surface's only action is %q, want %q", reportKeys[0], want)
	}
}

// TestReportApprovalValidatorRefusesEveryOtherAction is I-1's guard half, taken through the
// production validator with a nil Service: the refusal has to happen *before* any store
// access, otherwise an approval written for another action would be held only by the
// registry's shape.
func TestReportApprovalValidatorRefusesEveryOtherAction(t *testing.T) {
	others := []string{
		model.ApprovalActionCreate, model.ApprovalActionUpdate, model.ApprovalActionDelete,
		model.ApprovalActionRelease, model.ApprovalActionResolve,
	}
	for _, action := range others {
		err := validateReportBudgetExtension(context.Background(), nil,
			&model.Approval{Action: action})
		if err == nil {
			t.Fatalf("action %q was accepted on the report surface; only %q is allowed "+
				"(doc/specification/16 §1)", action, model.ApprovalActionExtendBudget)
		}
		if !strings.Contains(err.Error(), "unsupported report approval action") {
			t.Fatalf("action %q was refused with %q, want the documented "+
				"\"unsupported report approval action\"", action, err.Error())
		}
	}
}

// TestReportAssuranceEligibilityRefusesTiersWhenBlockingHold is I-2 over the whole state
// space. Each flag below is one sentence of §3's row I-2; the loop asserts that **any**
// combination containing at least one flag lands on `blocked`, and that the clean
// combination still reaches a usable tier — so the test cannot be satisfied by an
// implementation that simply always blocks.
func TestReportAssuranceEligibilityRefusesTiersWhenBlockingHold(t *testing.T) {
	zero := 0.0
	goodCoverage := 0.95

	flags := []struct {
		name  string
		apply func(*reportAssuranceMetrics)
	}{
		{"coverage_unknown", func(m *reportAssuranceMetrics) { m.Coverage = nil }},
		{"coverage_below_floor", func(m *reportAssuranceMetrics) {
			below := reportAssuranceCoverageFloor - 0.01
			m.Coverage = &below
		}},
		{"quote_hallucination_rate_missing", func(m *reportAssuranceMetrics) {
			m.QuoteHallucinationRate = nil
		}},
		{"quote_hallucination_nonzero", func(m *reportAssuranceMetrics) {
			bad := 0.01
			m.QuoteHallucinationRate = &bad
		}},
		{"quote_not_found", func(m *reportAssuranceMetrics) {
			m.QuoteStatuses[model.ReportQuoteStatusNotFound] = 1
		}},
		{"verified_without_source", func(m *reportAssuranceMetrics) { m.VerifiedWithoutSource = 1 }},
	}

	base := func() reportAssuranceMetrics {
		coverage := goodCoverage
		hallucination := zero
		return reportAssuranceMetrics{
			Status:                 model.ReportAssuranceStatusSucceeded,
			RequestedPolicy:        model.ReportAssurancePolicyAdvisory,
			Coverage:               &coverage,
			QuoteHallucinationRate: &hallucination,
			QuoteStatuses:          map[string]int{},
		}
	}

	for mask := 0; mask < 1<<len(flags); mask++ {
		metrics := base()
		var applied []string
		for i, flag := range flags {
			if mask&(1<<i) == 0 {
				continue
			}
			flag.apply(&metrics)
			applied = append(applied, flag.name)
		}
		tier, basis := reportAssuranceEligibility(metrics)
		if len(applied) == 0 {
			if tier != model.ReportAssuranceEligibilityTier1 {
				t.Fatalf("a clean advisory run produced %q (basis %v); the control case must "+
					"reach a usable tier, otherwise this test would pass on an implementation "+
					"that always blocks", tier, basis)
			}
			if len(basis) != 0 {
				t.Fatalf("a clean run carries a failure basis: %v", basis)
			}
			continue
		}
		if tier != model.ReportAssuranceEligibilityBlocked {
			t.Fatalf("blocking conditions %v produced tier %q (basis %v); doc/specification/16 §3 "+
				"I-2 says a held blocking condition cannot yield a usable tier", applied, tier, basis)
		}
		if len(basis) == 0 {
			t.Fatalf("blocking conditions %v produced no basis: a `blocked` the workbench cannot "+
				"explain is indistinguishable from a bug", applied)
		}
	}
}

// TestReportAssuranceEligibilityPendingNeverOutlivesTheTerminalWrite is I-2's other edge.
//
// `TestReportAssuranceEligibilityIsPendingUntilTerminal` already pins the forward direction
// (queued/running ⇒ pending). This is the reverse: once the run is terminal, `pending` must
// be unreachable — otherwise "we have not measured yet" becomes a permanent excuse, and a
// failed run would look like an unfinished one instead of a failed one.
func TestReportAssuranceEligibilityPendingNeverOutlivesTheTerminalWrite(t *testing.T) {
	for _, status := range []string{
		model.ReportAssuranceStatusSucceeded, model.ReportAssuranceStatusFailed,
		model.ReportAssuranceStatusCancelled,
	} {
		metrics := reportAssuranceMetrics{
			Status: status, RequestedPolicy: model.ReportAssurancePolicyAdvisory,
			QuoteStatuses: map[string]int{},
		}
		if tier, _ := reportAssuranceEligibility(metrics); tier == model.ReportAssuranceEligibilityPending {
			t.Fatalf("terminal status %s produced `pending`; `pending` is reserved for runs that "+
				"have not measured yet", status)
		}
	}
}

// TestReportExportAckNamesTheStrongerStatement is I-3's vocabulary half: the refusal has to
// name the strongest thing that is wrong, and `pending` asks nothing of anybody.
func TestReportExportAckNamesTheStrongerStatement(t *testing.T) {
	cases := []struct {
		name string
		gate reportAssuranceGate
		want string
		why  string
	}{
		{
			name: "conflicts outrank a usable tier",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyRequired,
				Conflicts: 2, Eligibility: model.ReportAssuranceEligibilityTier1},
			want: "conflicts",
			why:  "a conflict names what to fix; the tier only says the run is unusable as an input",
		},
		{
			name: "tier_0 needs acknowledgement",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyRequired,
				Eligibility: model.ReportAssuranceEligibilityTier0},
			want: "release_eligibility",
			why:  "an honest but unusable measurement must be acknowledged, not silently exported",
		},
		{
			name: "blocked needs acknowledgement",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyRequired,
				Eligibility: model.ReportAssuranceEligibilityBlocked},
			want: "release_eligibility",
			why:  "a held blocking condition is the operator's to accept, explicitly",
		},
		{
			name: "tier_1 asks nothing",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyRequired,
				Eligibility: model.ReportAssuranceEligibilityTier1},
			want: "",
			why:  "requiring an acknowledgement for a usable run turns the gate into a formality",
		},
		{
			name: "pending asks nothing",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyRequired,
				Eligibility: model.ReportAssuranceEligibilityPending},
			want: "",
			why:  "a run that has not measured anything has nothing to acknowledge (44122 answers)",
		},
		{
			// The reading this test was written to get right: `advisory` is not "export
			// freely". It only means a run whose verdict is still `pending` may proceed;
			// once the verdict exists, an unusable one must be acknowledged whatever the
			// policy says (doc/specification/16 §2.1 matrix).
			name: "advisory still needs the ack once the verdict exists",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyAdvisory,
				Eligibility: model.ReportAssuranceEligibilityBlocked},
			want: "release_eligibility",
			why:  "the policy decides when the verdict may be deferred, not whether it matters",
		},
		{
			name: "advisory with a pending verdict asks nothing",
			gate: reportAssuranceGate{Policy: model.ReportAssurancePolicyAdvisory,
				Eligibility: model.ReportAssuranceEligibilityPending},
			want: "",
			why:  "`advisory` is exactly the right to proceed before the verdict exists",
		},
	}
	for _, tc := range cases {
		if got := tc.gate.ackReason(); got != tc.want {
			t.Fatalf("%s: ackReason() = %q, want %q\n  %s", tc.name, got, tc.want, tc.why)
		}
	}
}
