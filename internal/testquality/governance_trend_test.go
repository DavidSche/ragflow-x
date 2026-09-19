package testquality

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateGovernanceFailsClosedOnCoverageTrendRegression(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	flakePath := filepath.Join(dir, "flake.json")
	changedPath := filepath.Join(dir, "changed.json")
	trendPath := filepath.Join(dir, "trend.json")
	queryPlanTrendPath := filepath.Join(dir, "query-plan-trend.json")
	mutationPath := filepath.Join(dir, "mutation.json")
	if err := writeJSON(flakePath, FlakeReport{GeneratedAt: now.Format(time.RFC3339), Passed: true, Tests: []FlakeTestReport{{
		Source: flakeSourceGo, TestName: "TestP0_Fast", Risk: coverageRiskP0, Status: flakeStatusPass,
		Attempts: []FlakeAttempt{{DurationMS: 10}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(changedPath, ChangedCoverageReport{ByRisk: map[string]ChangedCoverageRisk{
		coverageRiskP0: {Coverage: 90, Threshold: 85, Passed: true},
		coverageRiskP1: {Coverage: 80, Threshold: 75, Passed: true},
	}}); err != nil {
		t.Fatal(err)
	}
	trend := passingCoverageTrend(now, 80)
	trend.Regressions = []string{"internal/service coverage regressed from 75.00% to 60.00%"}
	trend.Passed = false
	if err := writeJSON(trendPath, trend); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(queryPlanTrendPath, passingQueryPlanTrend(now)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(mutationPath, mutationTestManifest(now)); err != nil {
		t.Fatal(err)
	}

	_, err := EvaluateGovernance(GovernanceConfig{
		CataloguePath:             "testdata/scenario_catalogue.json",
		FlakeReportPath:           flakePath,
		ChangedCoverageReportPath: changedPath,
		CoverageTrendPath:         trendPath,
		QueryPlanTrendPath:        queryPlanTrendPath,
		MutationEvidencePath:      mutationPath,
		Now:                       now,
	})
	if err == nil || !strings.Contains(err.Error(), "Coverage Trend Gate failed") {
		t.Fatalf("expected coverage trend gate failure, got %v", err)
	}
}
