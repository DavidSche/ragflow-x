package service

import "testing"

// TestAssuranceBudgetPinsTheApprovedCeiling pins the numbers approved in
// doc/develop/313 §7 C3 (2026-11-26): 5M tokens per month, and an unchanged 80k
// ceiling for a single run. The monthly number lives in quota buckets and is
// enforced there; this test guards the *per-run* one, because that is the number a
// single bad input can spend in one shot.
//
// Why a test and not a comment: the three places that hold this number (the run
// default here, the tenant settings default snapshot, and the release-gate row for
// per-revision tokens) are edited by different people at different times. A silent
// drift turns "approved 80k" into "whatever was last pasted", and nothing else in
// the suite would notice — the budget is a ceiling, so a bigger number simply stops
// refusing.
func TestAssuranceBudgetPinsTheApprovedCeiling(t *testing.T) {
	const approved = int64(80000)
	if reportAssuranceDefaultBudgetTokens != approved {
		t.Fatalf("per-run budget = %d, doc/develop/313 §7 C3 approved %d; changing it needs a new approval, not a new constant",
			reportAssuranceDefaultBudgetTokens, approved)
	}

	// The tenant-facing default must agree with the run default: a tenant that never
	// edited the key reads this snapshot, so a drift here is a different budget for
	// tenants that touched nothing versus tenants that saved the same value.
	snapshot := defaultSystemSettingsSnapshot()
	groups, ok := snapshot["groups"].(map[string]interface{})
	if !ok {
		t.Fatalf("default snapshot has no groups map")
	}
	report, ok := groups["report"].(map[string]interface{})
	if !ok {
		t.Fatalf("default snapshot has no groups.report group")
	}
	got, ok := report["assurance_budget_tokens"]
	if !ok {
		t.Fatalf("default snapshot has no report.assurance_budget_tokens")
	}
	if asInt(got) != approved {
		t.Fatalf("settings default report.assurance_budget_tokens = %v, want %d (doc/develop/313 §7 C3)", got, approved)
	}

	// 44119 has to stay a *gap* code, not a failure code: a run that hits the ceiling
	// publishes what it has and discloses the rest (doc/301 §7.3). If someone reuses
	// this number for a hard failure, the "budget hit" story changes meaning without
	// the number changing.
	if codeReportAssuranceBudgetExhausted != 44119 {
		t.Fatalf("budget-exhausted code = %d, want 44119 (doc/301 §7.3 keeps 44119 and 44123 apart)", codeReportAssuranceBudgetExhausted)
	}
}

func asInt(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return -1
}
