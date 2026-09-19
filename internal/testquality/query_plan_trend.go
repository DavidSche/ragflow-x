package testquality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	queryPlanTrendHistoryMax        = 90
	queryPlanTrendRuntimeFactor     = 1.5
	queryPlanTrendRuntimeFloorMS    = 1.0
	queryPlanTrendScaleOne          = 1
	queryPlanTrendScaleTen          = 10
	queryPlanTrendScaleHundred      = 100
	queryPlanTrendScenarioPrimary   = "SC-PG-001"
	queryPlanTrendScenarioCapacity  = "SC-PG-002"
	queryPlanTrendStatusPassed      = "PASSED"
	queryPlanTrendStatusFailed      = "FAILED"
	queryPlanTrendStatusNone        = "NONE"
	queryPlanTrendWarningRegression = "RUNTIME_REGRESSION"
	queryPlanTrendWarningThreshold  = "RUNTIME_THRESHOLD"
	queryPlanTrendMaxSourceSkew     = 5 * time.Minute
)

// QueryPlanTrendQuery records one analyzed SQL plan and its runtime decision.
type QueryPlanTrendQuery struct {
	Name                string   `json:"name"`
	RuntimeMS           float64  `json:"runtimeMS"`
	PreviousRuntimeMS   *float64 `json:"previousRuntimeMS,omitempty"`
	RuntimeDeltaMS      *float64 `json:"runtimeDeltaMS,omitempty"`
	RuntimeRegression   bool     `json:"runtimeRegression"`
	SustainedRegression bool     `json:"sustainedRegression"`
	ExpectedIndex       string   `json:"expectedIndex"`
	IndexUsed           bool     `json:"indexUsed"`
	ThresholdMS         float64  `json:"thresholdMS"`
	WithinThreshold     bool     `json:"withinThreshold"`
	GateStatus          string   `json:"gateStatus"`
	WarningStatus       string   `json:"warningStatus"`
}

// QueryPlanTrendMeasurement groups query plan evidence for one scale run.
type QueryPlanTrendMeasurement struct {
	Scale                int                   `json:"scale"`
	ScenarioID           string                `json:"scenarioId"`
	SourcePath           string                `json:"sourcePath"`
	MaxRuntimeMS         float64               `json:"maxRuntimeMS"`
	MaxPreviousRuntimeMS *float64              `json:"maxPreviousRuntimeMS,omitempty"`
	RuntimeDeltaMS       *float64              `json:"runtimeDeltaMS,omitempty"`
	RuntimeRegression    bool                  `json:"runtimeRegression"`
	SustainedRegression  bool                  `json:"sustainedRegression"`
	AllIndexesUsed       bool                  `json:"allIndexesUsed"`
	AllWithinThreshold   bool                  `json:"allWithinThreshold"`
	GateStatus           string                `json:"gateStatus"`
	WarningStatus        string                `json:"warningStatus"`
	Queries              []QueryPlanTrendQuery `json:"queries"`
}

// QueryPlanTrendSnapshot is one point-in-time query plan baseline.
type QueryPlanTrendSnapshot struct {
	GeneratedAt  string                      `json:"generatedAt"`
	Commit       string                      `json:"commit,omitempty"`
	Measurements []QueryPlanTrendMeasurement `json:"measurements"`
}

// QueryPlanTrendHistory stores bounded cross-run snapshots.
type QueryPlanTrendHistory struct {
	Version   int                      `json:"version"`
	Snapshots []QueryPlanTrendSnapshot `json:"snapshots"`
}

// QueryPlanTrendConfig points to the 1x, 10x, 100x, and history evidence.
type QueryPlanTrendConfig struct {
	Report1xPath   string
	Report10xPath  string
	Report100xPath string
	PreviousPath   string
	Commit         string
	GeneratedAt    time.Time
}

// QueryPlanTrendReport is machine-readable cross-run query plan evidence.
type QueryPlanTrendReport struct {
	Current                QueryPlanTrendSnapshot  `json:"current"`
	Previous               *QueryPlanTrendSnapshot `json:"previous,omitempty"`
	Regressions            []string                `json:"regressions,omitempty"`
	SustainedRegressions   []string                `json:"sustainedRegressions,omitempty"`
	Warnings               []string                `json:"warnings,omitempty"`
	Errors                 []string                `json:"errors,omitempty"`
	GateFailures           []string                `json:"gateFailures,omitempty"`
	HistorySize            int                     `json:"historySize"`
	SustainedBaselineReady bool                    `json:"sustainedBaselineReady"`
	Collected              bool                    `json:"collected"`
	ReviewRequired         bool                    `json:"reviewRequired"`
	Passed                 bool                    `json:"passed"`
}

type queryPlanTrendSourceQuery struct {
	Name            string  `json:"name"`
	ExpectedIndex   string  `json:"expectedIndex"`
	IndexUsed       bool    `json:"indexUsed"`
	ExecutionTimeMS float64 `json:"executionTimeMS"`
	ThresholdMS     float64 `json:"thresholdMS"`
	WithinThreshold bool    `json:"withinThreshold"`
	Plan            string  `json:"plan"`
}

type queryPlanTrendSourceReport struct {
	GeneratedAt         string                      `json:"generatedAt"`
	ScenarioID          string                      `json:"scenarioId"`
	Scale               int                         `json:"scale"`
	CompletedSyncRuns   int                         `json:"completedSyncRuns"`
	ActiveSyncRuns      int                         `json:"activeSyncRuns"`
	CompletedSyncItems  int                         `json:"completedSyncItems"`
	ConflictSyncItems   int                         `json:"conflictSyncItems"`
	SucceededDeliveries int                         `json:"succeededDeliveries"`
	FailedDeliveries    int                         `json:"failedDeliveries"`
	PendingDeliveries   int                         `json:"pendingDeliveries"`
	ThresholdMS         float64                     `json:"thresholdMS"`
	AllIndexesUsed      bool                        `json:"allIndexesUsed"`
	AllWithinThreshold  bool                        `json:"allWithinThreshold"`
	Queries             []queryPlanTrendSourceQuery `json:"queries"`
}

// EvaluateQueryPlanTrend collects runtime trends without making a runtime
// regression a merge gate. Index selection and 1x/10x runtime remain hard gates.
func EvaluateQueryPlanTrend(config QueryPlanTrendConfig) (QueryPlanTrendReport, error) {
	report := QueryPlanTrendReport{}
	if config.GeneratedAt.IsZero() {
		config.GeneratedAt = time.Now()
	}
	snapshot := QueryPlanTrendSnapshot{
		GeneratedAt: config.GeneratedAt.UTC().Format(time.RFC3339),
		Commit:      config.Commit,
	}
	sourceGeneratedAts := make([]time.Time, 0, 3)
	inputs := []struct {
		path          string
		expectedScale int
	}{
		{config.Report1xPath, queryPlanTrendScaleOne},
		{config.Report10xPath, queryPlanTrendScaleTen},
		{config.Report100xPath, queryPlanTrendScaleHundred},
	}
	for _, input := range inputs {
		if input.path == "" {
			report.GateFailures = append(report.GateFailures, fmt.Sprintf("%dx query plan report path is empty", input.expectedScale))
			continue
		}
		source, err := readQueryPlanTrendSource(input.path, input.expectedScale)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				report.GateFailures = append(report.GateFailures, fmt.Sprintf("%dx query plan report is missing: %s", input.expectedScale, input.path))
				continue
			}
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		sourceGeneratedAt, parseErr := time.Parse(time.RFC3339, source.GeneratedAt)
		if parseErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("query plan report %s has invalid generatedAt: %v", input.path, parseErr))
			continue
		}
		sourceGeneratedAts = append(sourceGeneratedAts, sourceGeneratedAt)
		snapshot.Measurements = append(snapshot.Measurements, newQueryPlanTrendMeasurement(source, input.path))
	}
	slices.SortFunc(snapshot.Measurements, func(a, b QueryPlanTrendMeasurement) int {
		return a.Scale - b.Scale
	})
	if len(sourceGeneratedAts) > 0 {
		slices.SortFunc(sourceGeneratedAts, func(a, b time.Time) int {
			return a.Compare(b)
		})
		oldest := sourceGeneratedAts[0]
		newest := sourceGeneratedAts[len(sourceGeneratedAts)-1]
		if newest.Sub(oldest) > queryPlanTrendMaxSourceSkew {
			report.Errors = append(report.Errors, fmt.Sprintf(
				"query plan source reports span %s; maximum allowed skew is %s",
				newest.Sub(oldest), queryPlanTrendMaxSourceSkew,
			))
		}
		snapshot.GeneratedAt = newest.UTC().Format(time.RFC3339)
	}
	snapshot.Measurements = validateQueryPlanMeasurements(snapshot.Measurements, &report)
	report.Current = snapshot
	report.Collected = len(snapshot.Measurements) == 3

	if config.PreviousPath != "" {
		history, err := LoadQueryPlanTrendHistory(config.PreviousPath)
		if err == nil {
			report.HistorySize = len(history.Snapshots)
			if previous, exists := latestQueryPlanSnapshotBefore(history.Snapshots, snapshot.GeneratedAt); exists {
				report.Previous = &previous
				compareQueryPlanTrendSnapshots(previous, &snapshot, &report)
				if baseline, hasBaseline := latestQueryPlanSnapshotBefore(history.Snapshots, previous.GeneratedAt); hasBaseline {
					evaluateSustainedQueryPlanRegression(baseline, previous, &snapshot, &report)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			report.Errors = append(report.Errors, err.Error())
		}
	}

	if len(snapshot.Measurements) != 3 {
		report.GateFailures = append(report.GateFailures, fmt.Sprintf("expected 1x, 10x, and 100x evidence, collected %d of 3", len(snapshot.Measurements)))
	}
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "runtime regression") {
			report.Regressions = append(report.Regressions, warning)
		}
	}
	report.Passed = len(report.Errors) == 0 && len(report.GateFailures) == 0
	report.ReviewRequired = !report.Passed || len(report.Warnings) > 0 || len(report.SustainedRegressions) > 0
	if !report.Passed {
		failures := append(append([]string(nil), report.Errors...), report.GateFailures...)
		return report, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return report, nil
}

// LoadQueryPlanTrendHistory reads a bounded cross-run history.
func LoadQueryPlanTrendHistory(path string) (QueryPlanTrendHistory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QueryPlanTrendHistory{}, fmt.Errorf("read query plan trend history: %w", err)
	}
	var history QueryPlanTrendHistory
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&history); err != nil {
		return history, fmt.Errorf("decode query plan trend history: %w", err)
	}
	if history.Version != 1 {
		return history, fmt.Errorf("query plan trend history version must be 1")
	}
	for _, snapshot := range history.Snapshots {
		if _, err := time.Parse(time.RFC3339, snapshot.GeneratedAt); err != nil {
			return history, fmt.Errorf("query plan trend history timestamp is invalid: %w", err)
		}
	}
	return history, nil
}

// AppendQueryPlanTrendHistory replaces the same run and bounds history size.
func AppendQueryPlanTrendHistory(history QueryPlanTrendHistory, snapshot QueryPlanTrendSnapshot) QueryPlanTrendHistory {
	replaced := false
	for index, existing := range history.Snapshots {
		if existing.GeneratedAt == snapshot.GeneratedAt && existing.Commit == snapshot.Commit {
			history.Snapshots[index] = snapshot
			replaced = true
			break
		}
	}
	if !replaced {
		history.Snapshots = append(history.Snapshots, snapshot)
	}
	slices.SortFunc(history.Snapshots, func(a, b QueryPlanTrendSnapshot) int {
		return strings.Compare(a.GeneratedAt, b.GeneratedAt)
	})
	if len(history.Snapshots) > queryPlanTrendHistoryMax {
		history.Snapshots = history.Snapshots[len(history.Snapshots)-queryPlanTrendHistoryMax:]
	}
	return history
}

// WriteQueryPlanTrendHistory writes UTF-8 JSON without a BOM.
func WriteQueryPlanTrendHistory(path string, history QueryPlanTrendHistory) error {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// WriteQueryPlanTrendReport writes UTF-8 JSON without a BOM.
func WriteQueryPlanTrendReport(path string, report QueryPlanTrendReport) error {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// LoadQueryPlanTrendReport reads a previously generated trend report.
func LoadQueryPlanTrendReport(path string) (QueryPlanTrendReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QueryPlanTrendReport{}, fmt.Errorf("read query plan trend report: %w", err)
	}
	var report QueryPlanTrendReport
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return report, fmt.Errorf("decode query plan trend report: %w", err)
	}
	return report, nil
}

func readQueryPlanTrendSource(path string, expectedScale int) (queryPlanTrendSourceReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return queryPlanTrendSourceReport{}, fmt.Errorf("read query plan report %s: %w", path, err)
	}
	var source queryPlanTrendSourceReport
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return source, fmt.Errorf("decode query plan report %s: %w", path, err)
	}
	if _, err := time.Parse(time.RFC3339, source.GeneratedAt); err != nil {
		return source, fmt.Errorf("query plan report %s has invalid generatedAt: %w", path, err)
	}
	if source.Scale != expectedScale {
		return source, fmt.Errorf("query plan report %s has scale %d, expected %d", path, source.Scale, expectedScale)
	}
	if source.ScenarioID != queryPlanTrendScenarioPrimary && source.ScenarioID != queryPlanTrendScenarioCapacity {
		return source, fmt.Errorf("query plan report %s has invalid scenarioId %q", path, source.ScenarioID)
	}
	if source.ThresholdMS <= 0 || len(source.Queries) == 0 {
		return source, fmt.Errorf("query plan report %s has no threshold or queries", path)
	}
	scaleVolumes := []int{
		source.CompletedSyncRuns, source.ActiveSyncRuns, source.CompletedSyncItems,
		source.ConflictSyncItems, source.SucceededDeliveries, source.FailedDeliveries,
		source.PendingDeliveries,
	}
	for _, volume := range scaleVolumes {
		if volume < 0 {
			return source, fmt.Errorf("query plan report %s has negative workload volume", path)
		}
	}
	allIndexesUsed := true
	allWithinThreshold := true
	seen := make(map[string]struct{}, len(source.Queries))
	for _, query := range source.Queries {
		if strings.TrimSpace(query.Name) == "" || strings.TrimSpace(query.ExpectedIndex) == "" {
			return source, fmt.Errorf("query plan report %s has a query with no name or expected index", path)
		}
		if _, exists := seen[query.Name]; exists {
			return source, fmt.Errorf("query plan report %s has duplicate query %s", path, query.Name)
		}
		seen[query.Name] = struct{}{}
		if query.ExecutionTimeMS < 0 || query.ThresholdMS != source.ThresholdMS {
			return source, fmt.Errorf("query plan report %s has invalid runtime or threshold for %s", path, query.Name)
		}
		if query.WithinThreshold != (query.ExecutionTimeMS <= query.ThresholdMS) {
			return source, fmt.Errorf("query plan report %s has inconsistent runtime evidence for %s", path, query.Name)
		}
		allIndexesUsed = allIndexesUsed && query.IndexUsed
		allWithinThreshold = allWithinThreshold && query.WithinThreshold
	}
	if source.AllIndexesUsed != allIndexesUsed || source.AllWithinThreshold != allWithinThreshold {
		return source, fmt.Errorf("query plan report %s has inconsistent aggregate evidence", path)
	}
	return source, nil
}

func newQueryPlanTrendMeasurement(source queryPlanTrendSourceReport, path string) QueryPlanTrendMeasurement {
	measurement := QueryPlanTrendMeasurement{
		Scale:              source.Scale,
		ScenarioID:         source.ScenarioID,
		SourcePath:         filepath.ToSlash(filepath.Clean(path)),
		AllIndexesUsed:     source.AllIndexesUsed,
		AllWithinThreshold: source.AllWithinThreshold,
		GateStatus:         queryPlanTrendStatusPassed,
		WarningStatus:      queryPlanTrendStatusNone,
		Queries:            make([]QueryPlanTrendQuery, 0, len(source.Queries)),
	}
	for _, query := range source.Queries {
		gatePassed := query.IndexUsed
		if source.Scale != queryPlanTrendScaleHundred {
			gatePassed = gatePassed && query.WithinThreshold
		}
		warning := queryPlanTrendStatusNone
		if source.Scale == queryPlanTrendScaleHundred && !query.WithinThreshold {
			warning = queryPlanTrendWarningThreshold
		}
		status := queryPlanTrendStatusPassed
		if !gatePassed {
			status = queryPlanTrendStatusFailed
		}
		measurement.Queries = append(measurement.Queries, QueryPlanTrendQuery{
			Name:            query.Name,
			RuntimeMS:       query.ExecutionTimeMS,
			ExpectedIndex:   query.ExpectedIndex,
			IndexUsed:       query.IndexUsed,
			ThresholdMS:     query.ThresholdMS,
			WithinThreshold: query.WithinThreshold,
			GateStatus:      status,
			WarningStatus:   warning,
		})
		measurement.MaxRuntimeMS = maxFloat64(measurement.MaxRuntimeMS, query.ExecutionTimeMS)
		if status != queryPlanTrendStatusPassed {
			measurement.GateStatus = queryPlanTrendStatusFailed
		}
		if warning != queryPlanTrendStatusNone {
			measurement.WarningStatus = warning
		}
	}
	return measurement
}

func validateQueryPlanMeasurements(measurements []QueryPlanTrendMeasurement, report *QueryPlanTrendReport) []QueryPlanTrendMeasurement {
	expected := []int{queryPlanTrendScaleOne, queryPlanTrendScaleTen, queryPlanTrendScaleHundred}
	seen := make(map[int]struct{}, len(measurements))
	for index, measurement := range measurements {
		if measurement.Scale != expected[index] {
			report.GateFailures = append(report.GateFailures, fmt.Sprintf("expected %dx query plan evidence, got %dx", expected[index], measurement.Scale))
		}
		if _, exists := seen[measurement.Scale]; exists {
			report.GateFailures = append(report.GateFailures, fmt.Sprintf("duplicate %dx query plan evidence", measurement.Scale))
		}
		seen[measurement.Scale] = struct{}{}
		if measurement.GateStatus != queryPlanTrendStatusPassed {
			report.GateFailures = append(report.GateFailures, fmt.Sprintf("%dx query plan hard contract failed", measurement.Scale))
		}
		if measurement.Scale == queryPlanTrendScaleHundred && !measurement.AllWithinThreshold {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%dx runtime threshold breached; warning-only", measurement.Scale))
		}
	}
	return measurements
}

func latestQueryPlanSnapshotBefore(snapshots []QueryPlanTrendSnapshot, generatedAt string) (QueryPlanTrendSnapshot, bool) {
	var selected QueryPlanTrendSnapshot
	found := false
	for _, snapshot := range snapshots {
		if snapshot.GeneratedAt >= generatedAt {
			continue
		}
		if !found || snapshot.GeneratedAt > selected.GeneratedAt {
			selected = snapshot
			found = true
		}
	}
	return selected, found
}

func compareQueryPlanTrendSnapshots(previous QueryPlanTrendSnapshot, current *QueryPlanTrendSnapshot, report *QueryPlanTrendReport) {
	previousByScale := make(map[int]QueryPlanTrendMeasurement, len(previous.Measurements))
	for _, previousMeasurement := range previous.Measurements {
		previousByScale[previousMeasurement.Scale] = previousMeasurement
	}
	for measurementIndex := range current.Measurements {
		measurement := &current.Measurements[measurementIndex]
		previousMeasurement, exists := previousByScale[measurement.Scale]
		if !exists {
			report.Errors = append(report.Errors, fmt.Sprintf("previous snapshot has no %dx query plan measurement", measurement.Scale))
			continue
		}
		previousQueries := make(map[string]float64, len(previousMeasurement.Queries))
		for _, query := range previousMeasurement.Queries {
			previousQueries[query.Name] = query.RuntimeMS
		}
		maxPrevious := 0.0
		for queryIndex := range measurement.Queries {
			query := &measurement.Queries[queryIndex]
			previousRuntime, exists := previousQueries[query.Name]
			if !exists {
				report.Errors = append(report.Errors, fmt.Sprintf("previous %dx snapshot has no %s query", measurement.Scale, query.Name))
				continue
			}
			query.PreviousRuntimeMS = &previousRuntime
			delta := query.RuntimeMS - previousRuntime
			query.RuntimeDeltaMS = &delta
			query.RuntimeRegression = query.RuntimeMS > previousRuntime*queryPlanTrendRuntimeFactor+queryPlanTrendRuntimeFloorMS
			if query.RuntimeRegression {
				query.WarningStatus = queryPlanTrendWarningRegression
				measurement.WarningStatus = queryPlanTrendWarningRegression
				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"%dx %s runtime regression: %.3fms -> %.3fms",
					measurement.Scale, query.Name, previousRuntime, query.RuntimeMS,
				))
			}
			maxPrevious = maxFloat64(maxPrevious, previousRuntime)
		}
		if maxPrevious > 0 {
			previousRuntime := maxPrevious
			measurement.MaxPreviousRuntimeMS = &previousRuntime
			delta := measurement.MaxRuntimeMS - maxPrevious
			measurement.RuntimeDeltaMS = &delta
		}
		measurement.RuntimeRegression = measurement.WarningStatus == queryPlanTrendWarningRegression
	}
}

func evaluateSustainedQueryPlanRegression(baseline, previous QueryPlanTrendSnapshot, current *QueryPlanTrendSnapshot, report *QueryPlanTrendReport) {
	report.SustainedBaselineReady = true
	baselineByScale := make(map[int]QueryPlanTrendMeasurement, len(baseline.Measurements))
	for _, measurement := range baseline.Measurements {
		baselineByScale[measurement.Scale] = measurement
	}
	previousByScale := make(map[int]QueryPlanTrendMeasurement, len(previous.Measurements))
	for _, measurement := range previous.Measurements {
		previousByScale[measurement.Scale] = measurement
	}
	for measurementIndex := range current.Measurements {
		measurement := &current.Measurements[measurementIndex]
		baselineMeasurement, hasBaseline := baselineByScale[measurement.Scale]
		previousMeasurement, hasPrevious := previousByScale[measurement.Scale]
		if !hasBaseline || !hasPrevious {
			report.SustainedBaselineReady = false
			continue
		}
		baselineQueries := make(map[string]float64, len(baselineMeasurement.Queries))
		for _, query := range baselineMeasurement.Queries {
			baselineQueries[query.Name] = query.RuntimeMS
		}
		previousQueries := make(map[string]float64, len(previousMeasurement.Queries))
		for _, query := range previousMeasurement.Queries {
			previousQueries[query.Name] = query.RuntimeMS
		}
		sustainedCount := 0
		for queryIndex := range measurement.Queries {
			query := &measurement.Queries[queryIndex]
			baselineRuntime, hasBaseline := baselineQueries[query.Name]
			previousRuntime, hasPrevious := previousQueries[query.Name]
			if !hasBaseline || !hasPrevious {
				report.SustainedBaselineReady = false
				continue
			}
			regressionThreshold := baselineRuntime*queryPlanTrendRuntimeFactor + queryPlanTrendRuntimeFloorMS
			query.SustainedRegression = query.RuntimeMS > regressionThreshold && previousRuntime > regressionThreshold
			if query.SustainedRegression {
				sustainedCount++
				message := fmt.Sprintf(
					"%dx %s sustained runtime regression over 3 runs: %.3fms -> %.3fms -> %.3fms",
					measurement.Scale, query.Name, baselineRuntime, previousRuntime, query.RuntimeMS,
				)
				report.SustainedRegressions = append(report.SustainedRegressions, message)
			}
		}
		measurement.SustainedRegression = sustainedCount > 0
	}
}

func maxFloat64(left, right float64) float64 {
	if right > left {
		return right
	}
	return left
}
