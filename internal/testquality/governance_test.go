package testquality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEvaluateGovernancePassesConsolidatedEvidence(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	flake := FlakeReport{
		GeneratedAt: now.Format(time.RFC3339),
		Passed:      true,
		Tests: []FlakeTestReport{
			{Source: flakeSourceGo, Package: "internal/service", TestName: "TestP0_Fast", Risk: coverageRiskP0, Status: flakeStatusPass, Attempts: []FlakeAttempt{{DurationMS: 20}}},
			{Source: flakeSourceVitest, Package: "web/src/example.test.ts", TestName: "works", Risk: flakeRiskOther, Status: flakeStatusPass, Attempts: []FlakeAttempt{{DurationMS: 80}}},
		},
	}
	changed := ChangedCoverageReport{ByRisk: map[string]ChangedCoverageRisk{
		coverageRiskP0: {Coverage: 90, Threshold: 85, Passed: true},
		coverageRiskP1: {Coverage: 80, Threshold: 75, Passed: true},
	}}
	flakePath := filepath.Join(dir, "flake.json")
	changedPath := filepath.Join(dir, "changed.json")
	trendPath := filepath.Join(dir, "trend.json")
	queryPlanTrendPath := filepath.Join(dir, "query-plan-trend.json")
	mutationPath := filepath.Join(dir, "mutation.json")
	if err := writeJSON(flakePath, flake); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(changedPath, changed); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(trendPath, passingCoverageTrend(now, 90)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(queryPlanTrendPath, passingQueryPlanTrend(now)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(mutationPath, mutationTestManifest(now)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateGovernance(GovernanceConfig{
		CataloguePath:             "testdata/scenario_catalogue.json",
		FlakeReportPath:           flakePath,
		ChangedCoverageReportPath: changedPath,
		CoverageTrendPath:         trendPath,
		QueryPlanTrendPath:        queryPlanTrendPath,
		MutationEvidencePath:      mutationPath,
		Now:                       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed {
		t.Fatalf("expected governance gate to pass: %+v", report.Gate)
	}
	if report.Runtime.TotalTests != 2 || report.Runtime.TotalMS != 100 || len(report.Runtime.BySource) != 2 {
		t.Fatalf("unexpected runtime report: %+v", report.Runtime)
	}
	if report.Runtime.SlowestTests[0].TestName != "works" {
		t.Fatalf("expected slowest test first, got %+v", report.Runtime.SlowestTests[0])
	}
	if report.Mutation.Total != 5 || !report.Mutation.Passed {
		t.Fatalf("unexpected mutation evidence: %+v", report.Mutation)
	}
	if !report.QueryPlanTrend.Passed || !report.Gate.QueryPlanTrendPassed {
		t.Fatalf("expected query plan trend to pass, got report=%+v gate=%+v", report.QueryPlanTrend, report.Gate)
	}
	if len(report.NextActions) == 0 || !strings.Contains(report.NextActions[0], "Improve coverage") {
		t.Fatalf("expected coverage actions, got %+v", report.NextActions)
	}
}

func TestEvaluateGovernanceFailsClosedOnFlakeEvidence(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	flake := FlakeReport{
		GeneratedAt: now.Format(time.RFC3339),
		Summary:     FlakeSummary{P0ActiveFlake: 1},
		Tests: []FlakeTestReport{{
			Source: flakeSourceGo, Package: "internal/service", TestName: "TestP0_Flake", Risk: coverageRiskP0, Status: flakeStatusSuspect,
			Attempts: []FlakeAttempt{{Status: flakeStatusFail}, {Status: flakeStatusPass}},
		}},
	}
	changed := ChangedCoverageReport{ByRisk: map[string]ChangedCoverageRisk{
		coverageRiskP0: {Coverage: 90, Threshold: 85, Passed: true},
		coverageRiskP1: {Coverage: 80, Threshold: 75, Passed: true},
	}}
	flakePath := filepath.Join(dir, "flake.json")
	changedPath := filepath.Join(dir, "changed.json")
	trendPath := filepath.Join(dir, "trend.json")
	queryPlanTrendPath := filepath.Join(dir, "query-plan-trend.json")
	mutationPath := filepath.Join(dir, "mutation.json")
	if err := writeJSON(flakePath, flake); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(changedPath, changed); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(trendPath, passingCoverageTrend(now, 90)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(queryPlanTrendPath, passingQueryPlanTrend(now)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(mutationPath, mutationTestManifest(now)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateGovernance(GovernanceConfig{
		CataloguePath:             "testdata/scenario_catalogue.json",
		FlakeReportPath:           flakePath,
		ChangedCoverageReportPath: changedPath,
		CoverageTrendPath:         trendPath,
		QueryPlanTrendPath:        queryPlanTrendPath,
		MutationEvidencePath:      mutationPath,
		Now:                       now,
	})
	if err == nil || !strings.Contains(err.Error(), "Flake Gate failed") {
		t.Fatalf("expected flake gate failure, got %v", err)
	}
	if report.Gate.Passed || report.Runtime.TotalTests != 1 {
		t.Fatalf("expected failed gate with runtime evidence: %+v", report)
	}
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func passingCoverageTrend(now time.Time, coverage float64) CoverageTrendReport {
	return CoverageTrendReport{
		Current: CoverageTrendSnapshot{
			GeneratedAt: now.Format(time.RFC3339),
			Total:       CoverageTrendScope{Path: coverageTrendTotal, Coverage: coverage},
		},
		Collected: true,
		Passed:    true,
		Warnings:  []string{"internal/service coverage 56.22% is below target 75.00%"},
	}
}

func passingQueryPlanTrend(now time.Time) QueryPlanTrendReport {
	return QueryPlanTrendReport{
		Current: QueryPlanTrendSnapshot{
			GeneratedAt: now.Format(time.RFC3339),
			Measurements: []QueryPlanTrendMeasurement{{
				Scale:              queryPlanTrendScaleOne,
				ScenarioID:         queryPlanTrendScenarioPrimary,
				MaxRuntimeMS:       1,
				AllIndexesUsed:     true,
				AllWithinThreshold: true,
				GateStatus:         queryPlanTrendStatusPassed,
				WarningStatus:      queryPlanTrendStatusNone,
				Queries: []QueryPlanTrendQuery{{
					Name:            "active_sync_runs",
					RuntimeMS:       1,
					ExpectedIndex:   "idx_sync_run_active_by_source",
					IndexUsed:       true,
					ThresholdMS:     250,
					WithinThreshold: true,
					GateStatus:      queryPlanTrendStatusPassed,
					WarningStatus:   queryPlanTrendStatusNone,
				}},
			}},
		},
		Collected: true,
		Passed:    true,
	}
}

func TestEvaluateGovernanceKeepsQueryPlanRegressionAsReviewOnly(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	flake := FlakeReport{
		GeneratedAt: now.Format(time.RFC3339),
		Passed:      true,
		Tests:       []FlakeTestReport{{Source: flakeSourceGo, Package: "internal/service", TestName: "TestP0_Fast", Risk: coverageRiskP0, Status: flakeStatusPass, Attempts: []FlakeAttempt{{DurationMS: 20}}}},
	}
	changed := ChangedCoverageReport{ByRisk: map[string]ChangedCoverageRisk{
		coverageRiskP0: {Coverage: 90, Threshold: 85, Passed: true},
		coverageRiskP1: {Coverage: 80, Threshold: 75, Passed: true},
	}}
	queryPlanTrend := passingQueryPlanTrend(now)
	queryPlanTrend.Current.Measurements[0].RuntimeRegression = true
	queryPlanTrend.Current.Measurements[0].WarningStatus = queryPlanTrendWarningRegression
	queryPlanTrend.Warnings = []string{"1x active_sync_runs runtime regression: 0.500ms -> 5.000ms"}
	queryPlanTrend.Regressions = queryPlanTrend.Warnings
	queryPlanTrend.SustainedBaselineReady = true
	queryPlanTrend.SustainedRegressions = []string{"1x active_sync_runs sustained runtime regression over 3 runs: 0.500ms -> 1.500ms -> 5.000ms"}
	queryPlanTrend.ReviewRequired = true
	flakePath := filepath.Join(dir, "flake.json")
	changedPath := filepath.Join(dir, "changed.json")
	trendPath := filepath.Join(dir, "trend.json")
	queryPlanTrendPath := filepath.Join(dir, "query-plan-trend.json")
	mutationPath := filepath.Join(dir, "mutation.json")
	if err := writeJSON(flakePath, flake); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(changedPath, changed); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(trendPath, passingCoverageTrend(now, 90)); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(queryPlanTrendPath, queryPlanTrend); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(mutationPath, mutationTestManifest(now)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateGovernance(GovernanceConfig{
		CataloguePath:             "testdata/scenario_catalogue.json",
		FlakeReportPath:           flakePath,
		ChangedCoverageReportPath: changedPath,
		CoverageTrendPath:         trendPath,
		QueryPlanTrendPath:        queryPlanTrendPath,
		MutationEvidencePath:      mutationPath,
		Now:                       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || !report.QueryPlanTrend.ReviewRequired || !report.Gate.QueryPlanTrendReviewRequired {
		t.Fatalf("expected warning-only review, got report=%+v gate=%+v", report, report.Gate)
	}
	if !slices.ContainsFunc(report.NextActions, func(action string) bool {
		return strings.Contains(action, "Review query plan runtime warning: 1x active_sync_runs")
	}) {
		t.Fatalf("expected query plan review action, got %+v", report.NextActions)
	}
	if !slices.ContainsFunc(report.NextActions, func(action string) bool {
		return strings.Contains(action, "sustained runtime regression over 3 runs")
	}) {
		t.Fatalf("expected sustained regression review action, got %+v", report.NextActions)
	}
}

func TestEvaluateGovernanceFailsClosedWithoutQueryPlanTrend(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	flakePath := filepath.Join(dir, "flake.json")
	changedPath := filepath.Join(dir, "changed.json")
	trendPath := filepath.Join(dir, "trend.json")
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
	if err := writeJSON(trendPath, passingCoverageTrend(now, 90)); err != nil {
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
		QueryPlanTrendPath:        filepath.Join(dir, "missing.json"),
		MutationEvidencePath:      mutationPath,
		Now:                       now,
	})
	if err == nil || !strings.Contains(err.Error(), "Query Plan Trend Gate failed") {
		t.Fatalf("expected query plan trend gate failure, got %v", err)
	}
}
