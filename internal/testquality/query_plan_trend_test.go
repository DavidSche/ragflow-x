package testquality

import (
	"path/filepath"
	"testing"
	"time"
)

type queryPlanTrendFixtureQuery struct {
	Name            string  `json:"name"`
	ExpectedIndex   string  `json:"expectedIndex"`
	IndexUsed       bool    `json:"indexUsed"`
	ExecutionTimeMS float64 `json:"executionTimeMS"`
	ThresholdMS     float64 `json:"thresholdMS"`
	WithinThreshold bool    `json:"withinThreshold"`
}

type queryPlanTrendFixtureReport struct {
	GeneratedAt         string                       `json:"generatedAt"`
	ScenarioID          string                       `json:"scenarioId"`
	Scale               int                          `json:"scale"`
	CompletedSyncRuns   int                          `json:"completedSyncRuns"`
	ActiveSyncRuns      int                          `json:"activeSyncRuns"`
	CompletedSyncItems  int                          `json:"completedSyncItems"`
	ConflictSyncItems   int                          `json:"conflictSyncItems"`
	SucceededDeliveries int                          `json:"succeededDeliveries"`
	FailedDeliveries    int                          `json:"failedDeliveries"`
	PendingDeliveries   int                          `json:"pendingDeliveries"`
	ThresholdMS         float64                      `json:"thresholdMS"`
	AllIndexesUsed      bool                         `json:"allIndexesUsed"`
	AllWithinThreshold  bool                         `json:"allWithinThreshold"`
	Queries             []queryPlanTrendFixtureQuery `json:"queries"`
}

func queryPlanTrendFixture(t *testing.T, path, generatedAt, scenarioID string, scale int, runtime float64, withinThreshold bool) {
	t.Helper()
	threshold := 250.0
	report := queryPlanTrendFixtureReport{
		GeneratedAt:         generatedAt,
		ScenarioID:          scenarioID,
		Scale:               scale,
		CompletedSyncRuns:   1200 * scale,
		ActiveSyncRuns:      25 * scale,
		CompletedSyncItems:  1500 * scale,
		ConflictSyncItems:   25 * scale,
		SucceededDeliveries: 1500 * scale,
		FailedDeliveries:    30 * scale,
		PendingDeliveries:   30 * scale,
		ThresholdMS:         threshold,
		AllIndexesUsed:      true,
		AllWithinThreshold:  withinThreshold,
		Queries: []queryPlanTrendFixtureQuery{{
			Name:            "active_sync_runs",
			ExpectedIndex:   "idx_sync_run_active_by_source",
			IndexUsed:       true,
			ExecutionTimeMS: runtime,
			ThresholdMS:     threshold,
			WithinThreshold: withinThreshold,
		}},
	}
	if err := osWriteFile(path, mustJSON(report)); err != nil {
		t.Fatal(err)
	}
}

func queryPlanTrendHistoryFixture(generatedAt string, runtimes map[int]float64) QueryPlanTrendHistory {
	measurements := make([]QueryPlanTrendMeasurement, 0, len(runtimes))
	for _, scale := range []int{1, 10, 100} {
		runtime := runtimes[scale]
		measurements = append(measurements, QueryPlanTrendMeasurement{
			Scale:         scale,
			ScenarioID:    "SC-PG-001",
			MaxRuntimeMS:  runtime,
			GateStatus:    "PASSED",
			WarningStatus: "NONE",
			Queries: []QueryPlanTrendQuery{{
				Name:            "active_sync_runs",
				RuntimeMS:       runtime,
				IndexUsed:       true,
				ExpectedIndex:   "idx_sync_run_active_by_source",
				ThresholdMS:     250,
				WithinThreshold: true,
				GateStatus:      "PASSED",
				WarningStatus:   "NONE",
			}},
		})
	}
	return QueryPlanTrendHistory{
		Version:   1,
		Snapshots: []QueryPlanTrendSnapshot{{GeneratedAt: generatedAt, Measurements: measurements}},
	}
}

func TestEvaluateQueryPlanTrendCollectsScaleEvidenceAndWarnsOnRegression(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	previousPath := filepath.Join(dir, "history.json")
	queryPlanTrendFixture(t, firstPath, now.Add(-time.Minute).Format(time.RFC3339), "SC-PG-001", 1, 1, true)
	queryPlanTrendFixture(t, tenPath, now.Add(-time.Minute).Format(time.RFC3339), "SC-PG-001", 10, 2, true)
	queryPlanTrendFixture(t, hundredPath, now.Add(-time.Minute).Format(time.RFC3339), "SC-PG-002", 100, 3, true)
	if err := osWriteFile(previousPath, mustJSON(queryPlanTrendHistoryFixture(now.Add(-24*time.Hour).Format(time.RFC3339), map[int]float64{1: 1, 10: 1, 100: 1}))); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath:   firstPath,
		Report10xPath:  tenPath,
		Report100xPath: hundredPath,
		PreviousPath:   previousPath,
		Commit:         "abc123",
		GeneratedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Collected || !report.Passed || !report.ReviewRequired || len(report.Warnings) == 0 {
		t.Fatalf("expected collected passing report with runtime warning, got %+v", report)
	}
	hundred := report.Current.Measurements[2]
	if hundred.Scale != 100 || !hundred.RuntimeRegression || hundred.WarningStatus != "RUNTIME_REGRESSION" {
		t.Fatalf("unexpected 100x measurement: %+v", hundred)
	}
	if report.Current.Measurements[0].GateStatus != "PASSED" || hundred.GateStatus != "PASSED" {
		t.Fatalf("runtime regression must remain warning-only, got %+v", report.Current.Measurements)
	}
}

func TestEvaluateQueryPlanTrendKeepsHundredTimesThresholdAsWarning(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	firstPath := filepath.Join(t.TempDir(), "1x.json")
	tenPath := filepath.Join(t.TempDir(), "10x.json")
	hundredPath := filepath.Join(t.TempDir(), "100x.json")
	queryPlanTrendFixture(t, firstPath, now.Format(time.RFC3339), "SC-PG-001", 1, 1, true)
	queryPlanTrendFixture(t, tenPath, now.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 300, false)

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !report.ReviewRequired || len(report.GateFailures) != 0 || len(report.Warnings) == 0 {
		t.Fatalf("expected warning-only threshold report, got %+v", report)
	}
	if report.Current.Measurements[2].GateStatus != "PASSED" || report.Current.Measurements[2].WarningStatus != "RUNTIME_THRESHOLD" {
		t.Fatalf("unexpected 100x warning statuses: %+v", report.Current.Measurements[2])
	}
}

func TestEvaluateQueryPlanTrendFailsInvalidOrMissingEvidence(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{GeneratedAt: now})
	if err == nil || report.Passed || report.Collected || len(report.GateFailures) == 0 {
		t.Fatalf("expected missing evidence to fail, got report=%+v err=%v", report, err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := osWriteFile(invalidPath, []byte("{")); err != nil {
		t.Fatal(err)
	}
	report, err = EvaluateQueryPlanTrend(QueryPlanTrendConfig{Report1xPath: invalidPath, GeneratedAt: now})
	if err == nil || report.Passed || len(report.Errors) != 1 || len(report.GateFailures) != 3 {
		t.Fatalf("expected invalid 1x evidence and incomplete scale evidence to fail, got report=%+v err=%v", report, err)
	}
}

func TestEvaluateQueryPlanTrendRejectsMixedSourceBatches(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	queryPlanTrendFixture(t, firstPath, now.Add(-10*time.Minute).Format(time.RFC3339), "SC-PG-001", 1, 1, true)
	queryPlanTrendFixture(t, tenPath, now.Add(-10*time.Minute).Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 1, true)

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
	})
	if err == nil || report.Passed || len(report.Errors) != 1 {
		t.Fatalf("expected mixed source batch rejection, got report=%+v err=%v", report, err)
	}
}

func TestEvaluateQueryPlanTrendDetectsMissingPreviousQuery(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	firstPath := filepath.Join(t.TempDir(), "1x.json")
	tenPath := filepath.Join(t.TempDir(), "10x.json")
	hundredPath := filepath.Join(t.TempDir(), "100x.json")
	previousPath := filepath.Join(t.TempDir(), "history.json")
	queryPlanTrendFixture(t, firstPath, now.Format(time.RFC3339), "SC-PG-001", 1, 1, true)
	queryPlanTrendFixture(t, tenPath, now.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 1, true)
	history := queryPlanTrendHistoryFixture(now.Add(-time.Hour).Format(time.RFC3339), map[int]float64{1: 1, 10: 1, 100: 1})
	history.Snapshots[0].Measurements[0].Queries = nil
	if err := osWriteFile(previousPath, mustJSON(history)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		PreviousPath: previousPath, GeneratedAt: now,
	})
	if err == nil || report.Passed || len(report.Errors) == 0 {
		t.Fatalf("expected missing previous query to fail, got report=%+v err=%v", report, err)
	}
}

func TestEvaluateQueryPlanTrendFlagsSustainedRegressionOnlyAfterThreeRuns(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	historyPath := filepath.Join(dir, "history.json")
	queryPlanTrendFixture(t, firstPath, now.Format(time.RFC3339), "SC-PG-001", 1, 8, true)
	queryPlanTrendFixture(t, tenPath, now.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 1, true)
	history := queryPlanTrendHistoryFixture(now.Add(-2*time.Hour).Format(time.RFC3339), map[int]float64{1: 1, 10: 1, 100: 1})
	previous := queryPlanTrendHistoryFixture(now.Add(-time.Hour).Format(time.RFC3339), map[int]float64{1: 4, 10: 1, 100: 1})
	history.Snapshots = append(history.Snapshots, previous.Snapshots...)
	if err := osWriteFile(historyPath, mustJSON(history)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		PreviousPath: historyPath, GeneratedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.SustainedBaselineReady || len(report.SustainedRegressions) != 1 {
		t.Fatalf("expected one sustained regression, got ready=%+v regressions=%+v", report.SustainedBaselineReady, report.SustainedRegressions)
	}
	if !report.ReviewRequired || !report.Passed {
		t.Fatalf("expected sustained warning to stay review-only, got %+v", report)
	}
	if !report.Current.Measurements[0].SustainedRegression || !report.Current.Measurements[0].Queries[0].SustainedRegression {
		t.Fatalf("expected measurement and query sustained flags, got %+v", report.Current.Measurements[0])
	}
}

func TestEvaluateQueryPlanTreatsSingleRunRegressionAsTransient(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	historyPath := filepath.Join(dir, "history.json")
	queryPlanTrendFixture(t, firstPath, now.Format(time.RFC3339), "SC-PG-001", 1, 8, true)
	queryPlanTrendFixture(t, tenPath, now.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 1, true)
	history := queryPlanTrendHistoryFixture(now.Add(-time.Hour).Format(time.RFC3339), map[int]float64{1: 1, 10: 1, 100: 1})
	if err := osWriteFile(historyPath, mustJSON(history)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		PreviousPath: historyPath, GeneratedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SustainedBaselineReady || len(report.SustainedRegressions) != 0 || !report.Current.Measurements[0].RuntimeRegression {
		t.Fatalf("expected transient regression only, got %+v", report)
	}
	if !report.ReviewRequired || !report.Passed {
		t.Fatalf("expected transient warning to stay review-only, got %+v", report)
	}
}

func TestEvaluateQueryPlanFlagsFlatSustainedRegressionAgainstOriginalBaseline(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	historyPath := filepath.Join(dir, "history.json")
	queryPlanTrendFixture(t, firstPath, now.Format(time.RFC3339), "SC-PG-001", 1, 10, true)
	queryPlanTrendFixture(t, tenPath, now.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, now.Format(time.RFC3339), "SC-PG-002", 100, 1, true)
	history := queryPlanTrendHistoryFixture(now.Add(-2*time.Hour).Format(time.RFC3339), map[int]float64{1: 1, 10: 1, 100: 1})
	previous := queryPlanTrendHistoryFixture(now.Add(-time.Hour).Format(time.RFC3339), map[int]float64{1: 10, 10: 1, 100: 1})
	history.Snapshots = append(history.Snapshots, previous.Snapshots...)
	if err := osWriteFile(historyPath, mustJSON(history)); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		PreviousPath: historyPath, GeneratedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Current.Measurements[0].Queries[0].SustainedRegression || len(report.SustainedRegressions) != 1 {
		t.Fatalf("expected flat sustained regression against original baseline, got %+v", report)
	}
}

func TestAppendQueryPlanTrendHistoryReplacesAndBounds(t *testing.T) {
	current := QueryPlanTrendSnapshot{GeneratedAt: "2026-09-10T12:00:00Z"}
	history := AppendQueryPlanTrendHistory(QueryPlanTrendHistory{Version: 1}, current)
	history = AppendQueryPlanTrendHistory(history, QueryPlanTrendSnapshot{GeneratedAt: "2026-09-10T11:00:00Z"})
	history = AppendQueryPlanTrendHistory(history, current)
	if len(history.Snapshots) != 2 || history.Snapshots[0].GeneratedAt != "2026-09-10T11:00:00Z" {
		t.Fatalf("expected replacement and sorting, got %+v", history.Snapshots)
	}

	for index := 0; index < queryPlanTrendHistoryMax+1; index++ {
		current := QueryPlanTrendSnapshot{GeneratedAt: time.Date(2026, 9, 10, 12, 0, index, 0, time.UTC).Format(time.RFC3339)}
		history = AppendQueryPlanTrendHistory(history, current)
	}
	if len(history.Snapshots) != queryPlanTrendHistoryMax {
		t.Fatalf("expected bounded history, got %d", len(history.Snapshots))
	}
}

func TestAppendQueryPlanTrendHistoryIsIdempotentForSameSourceReports(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "1x.json")
	tenPath := filepath.Join(dir, "10x.json")
	hundredPath := filepath.Join(dir, "100x.json")
	reportTime := now.Add(-time.Minute)
	queryPlanTrendFixture(t, firstPath, reportTime.Format(time.RFC3339), "SC-PG-001", 1, 1, true)
	queryPlanTrendFixture(t, tenPath, reportTime.Format(time.RFC3339), "SC-PG-001", 10, 1, true)
	queryPlanTrendFixture(t, hundredPath, reportTime.Format(time.RFC3339), "SC-PG-002", 100, 1, true)
	first, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		GeneratedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateQueryPlanTrend(QueryPlanTrendConfig{
		Report1xPath: firstPath, Report10xPath: tenPath, Report100xPath: hundredPath,
		GeneratedAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Current.GeneratedAt != second.Current.GeneratedAt {
		t.Fatalf("expected source report timestamp, got %s and %s", first.Current.GeneratedAt, second.Current.GeneratedAt)
	}
	history := AppendQueryPlanTrendHistory(QueryPlanTrendHistory{Version: 1}, first.Current)
	history = AppendQueryPlanTrendHistory(history, second.Current)
	if len(history.Snapshots) != 1 {
		t.Fatalf("expected idempotent history append, got %d snapshots", len(history.Snapshots))
	}
}
