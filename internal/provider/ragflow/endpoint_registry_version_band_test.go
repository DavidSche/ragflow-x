package ragflow

import "testing"

// T6.1 Capability Registry version bands (223 T6 / 224 §2.4): the registry
// model carries MinVersion/MaxVerified on each endpoint plus capability-level
// version observations (版本×能力×契约). Data population is T6.2; these tests
// pin the model, the validation, the observation anchors and the gap report
// the reconciliation consumes.

// TestEndpointRegistryVersionDeclarationsWellFormed asserts any version band
// declared on an endpoint is a valid RAGFlow release shape, so a typo in
// T6.2 data cannot silently disable or enable a preflight gate.
func TestEndpointRegistryVersionDeclarationsWellFormed(t *testing.T) {
	for _, e := range endpointRegistry {
		if e.MinVersion != "" && !validVersionString(e.MinVersion) {
			t.Errorf("endpoint %s %s has malformed MinVersion %q", e.Method, e.Path, e.MinVersion)
		}
		if e.MaxVerified != "" && !validVersionString(e.MaxVerified) {
			t.Errorf("endpoint %s %s has malformed MaxVerified %q", e.Method, e.Path, e.MaxVerified)
		}
	}
	// The validator itself must be conservative about accepted shapes.
	for _, bad := range []string{"", "latest", "v1", "1.2.3.4", "rc", "RC1", "v1.0.0-rc1.extra", "0.27.2 "} {
		if validVersionString(bad) {
			t.Errorf("validVersionString must reject %q", bad)
		}
	}
	for _, good := range []string{"0.27.2", "v0.27.2", "v1.0.0-rc1", "rc1", "1.0.0-beta.1"} {
		if !validVersionString(good) {
			t.Errorf("validVersionString must accept %q", good)
		}
	}
}

// TestVersionKnownIssuesRegistered pins the six external version deltas (223
// T6 §方案1) plus the confirmed search-app completion regression, and keeps
// every row audit-able: non-empty capability/version/finding and a legal
// status so the 对账表 cannot silently carry a blank or invented row.
func TestVersionKnownIssuesRegistered(t *testing.T) {
	issues := KnownVersionIssues()
	if len(issues) < 6 {
		t.Fatalf("expected at least the six external version deltas, got %d", len(issues))
	}
	seen := map[string]bool{}
	regressionCount := 0
	for _, i := range issues {
		if i.Capability == "" || i.Version == "" || i.Finding == "" {
			t.Errorf("version issue row must be fully populated: %+v", i)
		}
		if !validVersionIssueStatus(i.Status) {
			t.Errorf("version issue %s has invalid status %q", i.Capability, i.Status)
		}
		if seen[i.Capability] {
			t.Errorf("capability %s appears more than once in the version issues registry", i.Capability)
		}
		seen[i.Capability] = true
		if i.Status == VersionIssueRegression {
			regressionCount++
		}
	}
	if regressionCount == 0 {
		t.Error("version issues registry must carry at least one confirmed regression")
	}
}

// TestKnownVersionIssuesCopyIsDefensive asserts callers cannot mutate the
// authoritative registry through the accessor.
func TestKnownVersionIssuesCopyIsDefensive(t *testing.T) {
	issues := KnownVersionIssues()
	issues[0].Finding = "mutated"
	if versionIssueRegistry[0].Finding == "mutated" {
		t.Fatal("KnownVersionIssues must return a copy, not the backing slice")
	}
}

// TestT62ReconciliationMarkedEveryEndpoint pins the T6.2 收口: every registry
// endpoint carries MaxVerified=v1.0.0-rc1 (evidence: X production gateway on
// the 151 rc1 engine + live contract drills + D1 three-arm measurements), and
// none over-claims a MinVersion floor that was never verified.
func TestT62ReconciliationMarkedEveryEndpoint(t *testing.T) {
	for _, e := range endpointRegistry {
		if e.MaxVerified != "v1.0.0-rc1" {
			t.Errorf("endpoint %s %s missing T6.2 MaxVerified (want v1.0.0-rc1, got %q)", e.Method, e.Path, e.MaxVerified)
		}
		if e.MinVersion != "" {
			t.Errorf("endpoint %s %s must not claim an unverified MinVersion floor yet (got %q)", e.Method, e.Path, e.MinVersion)
		}
	}
}

// TestVersionBandGapReportIsEmptyAfterT62 asserts the reconciliation backlog
// is drained (T6.2 收口), so a future endpoint addition without a version band
// fails the suite and must be classified before it ships.
func TestVersionBandGapReportIsEmptyAfterT62(t *testing.T) {
	gap := VersionBandGapReport()
	if len(gap) != 0 {
		for _, e := range gap {
			t.Errorf("endpoint %s %s still unclassified after T6.2", e.Method, e.Path)
		}
	}
}

// TestEndpointCoverageSummaryTracksUnclassifiedVersion asserts the coverage
// summary exposes the T6.2 backlog count without breaking the existing totals
// consumed by the Capability Matrix report.
func TestEndpointCoverageSummaryTracksUnclassifiedVersion(t *testing.T) {
	summary := EndpointCoverageSummary()
	if summary.Documented+summary.Internal != summary.Total {
		t.Fatalf("coverage totals inconsistent: %+v", summary)
	}
	if summary.UnclassifiedVersion > summary.Total {
		t.Fatalf("unclassified version count exceeds total: %+v", summary)
	}
	gapCount := len(VersionBandGapReport())
	if summary.UnclassifiedVersion != gapCount {
		t.Fatalf("coverage unclassified (%d) does not match gap backlog (%d): %+v", summary.UnclassifiedVersion, gapCount, summary)
	}
	if gapCount != 0 {
		t.Fatalf("T6.2 must have drained the backlog, %d endpoints remain", gapCount)
	}
}

// TestSummarizeVersionIssuesRollsUpStates asserts the preflight rollup
// classifies every row into one of the three states and surfaces regressions
// by capability.
func TestSummarizeVersionIssuesRollsUpStates(t *testing.T) {
	summary := SummarizeVersionIssues()
	if summary.Total != len(versionIssueRegistry) {
		t.Fatalf("rollup total %d != registry %d", summary.Total, len(versionIssueRegistry))
	}
	if summary.Regression+summary.Observed+summary.Untested != summary.Total {
		t.Fatalf("rollup states do not add up: %+v", summary)
	}
	if summary.Regression == 0 {
		t.Fatal("rollup must surface at least one regression")
	}
	if _, ok := summary.RegressionMap["search_app"]; !ok {
		t.Fatalf("search_app completion regression missing from rollup: %+v", summary.RegressionMap)
	}
}
