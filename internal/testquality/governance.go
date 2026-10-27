package testquality

import (
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

// RuntimeSourceReport summarizes one execution source.
type RuntimeSourceReport struct {
	Source          string  `json:"source"`
	TestCount       int     `json:"testCount"`
	TotalDurationMS float64 `json:"totalDurationMs"`
	MaxDurationMS   float64 `json:"maxDurationMs"`
}

// RuntimeTestReport identifies one slow observed test.
type RuntimeTestReport struct {
	Source       string  `json:"source"`
	Package      string  `json:"package"`
	TestName     string  `json:"testName"`
	Risk         string  `json:"risk"`
	DurationMS   float64 `json:"durationMs"`
	AttemptCount int     `json:"attemptCount"`
}

// RuntimeReport turns FlakeReport attempts into runtime trend evidence.
type RuntimeReport struct {
	Collected    bool                  `json:"collected"`
	TotalTests   int                   `json:"totalTests"`
	TotalMS      float64               `json:"totalMs"`
	BySource     []RuntimeSourceReport `json:"bySource"`
	SlowestTests []RuntimeTestReport   `json:"slowestTests"`
}

// GovernanceGate is the merged release-facing quality gate.
type GovernanceGate struct {
	Passed                       bool     `json:"passed"`
	Failures                     []string `json:"failures,omitempty"`
	CoveragePassed               bool     `json:"coveragePassed"`
	TrendPassed                  bool     `json:"trendPassed"`
	FlakePassed                  bool     `json:"flakePassed"`
	MutationPassed               bool     `json:"mutationPassed"`
	CataloguePassed              bool     `json:"cataloguePassed"`
	RuntimeCollected             bool     `json:"runtimeCollected"`
	QueryPlanTrendPassed         bool     `json:"queryPlanTrendPassed"`
	QueryPlanTrendReviewRequired bool     `json:"queryPlanTrendReviewRequired"`
}

// GovernanceReport consolidates all machine-readable quality evidence.
type GovernanceReport struct {
	GeneratedAt     string                 `json:"generatedAt"`
	Catalogue       CatalogueReport        `json:"catalogue"`
	ChangedCoverage ChangedCoverageReport  `json:"changedCoverage"`
	CoverageTrend   CoverageTrendReport    `json:"coverageTrend"`
	Flake           FlakeReport            `json:"flake"`
	Runtime         RuntimeReport          `json:"runtime"`
	Mutation        MutationEvidenceReport `json:"mutation"`
	QueryPlanTrend  QueryPlanTrendReport   `json:"queryPlanTrend"`
	NextActions     []string               `json:"nextActions"`
	Gate            GovernanceGate         `json:"gate"`
}

// GovernanceConfig points to the artifacts that must already exist.
type GovernanceConfig struct {
	CataloguePath             string
	FlakeReportPath           string
	ChangedCoverageReportPath string
	CoverageTrendPath         string
	QueryPlanTrendPath        string
	MutationEvidencePath      string
	Now                       time.Time
}

// LoadFlakeReport reads a previously generated flake report.
func LoadFlakeReport(path string) (FlakeReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FlakeReport{}, fmt.Errorf("read flake report: %w", err)
	}
	var report FlakeReport
	if err := json.Unmarshal(data, &report); err != nil {
		return FlakeReport{}, fmt.Errorf("decode flake report: %w", err)
	}
	return report, nil
}

// LoadChangedCoverageReport reads a previously generated changed coverage report.
func LoadChangedCoverageReport(path string) (ChangedCoverageReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ChangedCoverageReport{}, fmt.Errorf("read changed coverage report: %w", err)
	}
	var report ChangedCoverageReport
	if err := json.Unmarshal(data, &report); err != nil {
		return ChangedCoverageReport{}, fmt.Errorf("decode changed coverage report: %w", err)
	}
	return report, nil
}

// EvaluateGovernance builds the merged quality report and enforces the Gate.
func EvaluateGovernance(config GovernanceConfig) (GovernanceReport, error) {
	if config.Now.IsZero() {
		config.Now = time.Now()
	}
	catalogue, err := LoadScenarioCatalogue(config.CataloguePath)
	if err != nil {
		return GovernanceReport{}, fmt.Errorf("load scenario catalogue: %w", err)
	}
	flake, err := LoadFlakeReport(config.FlakeReportPath)
	if err != nil {
		return GovernanceReport{}, err
	}
	changed, err := LoadChangedCoverageReport(config.ChangedCoverageReportPath)
	if err != nil {
		return GovernanceReport{}, err
	}
	coverageTrend, err := LoadCoverageTrendReport(config.CoverageTrendPath)
	if err != nil {
		return GovernanceReport{}, err
	}
	queryPlanTrend, err := LoadQueryPlanTrendReport(config.QueryPlanTrendPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return GovernanceReport{}, err
		}
		queryPlanTrend = QueryPlanTrendReport{}
	}
	mutation, err := LoadMutationEvidence(config.MutationEvidencePath, config.CataloguePath, config.Now)
	if err != nil {
		return GovernanceReport{}, err
	}

	report := GovernanceReport{
		GeneratedAt:     config.Now.UTC().Format(time.RFC3339),
		Catalogue:       catalogue.BuildReport(config.Now),
		ChangedCoverage: changed,
		CoverageTrend:   coverageTrend,
		Flake:           flake,
		Runtime:         buildRuntimeReport(flake),
		Mutation:        mutation,
		QueryPlanTrend:  queryPlanTrend,
	}
	queryPlanWarnings := append(append([]string(nil), report.QueryPlanTrend.Warnings...), report.QueryPlanTrend.SustainedRegressions...)
	report.NextActions = governanceNextActions(catalogue, report.Catalogue, mutation, report.CoverageTrend.Warnings, queryPlanWarnings)
	report.Gate = buildGovernanceGate(report)
	if !report.Gate.Passed {
		return report, fmt.Errorf("%s", strings.Join(report.Gate.Failures, "; "))
	}
	return report, nil
}

// WriteGovernanceReport writes UTF-8 JSON without a BOM.
func WriteGovernanceReport(path string, report GovernanceReport) error {
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

func buildRuntimeReport(flake FlakeReport) RuntimeReport {
	report := RuntimeReport{TotalTests: len(flake.Tests)}
	sourceOrder := map[string]int{flakeSourceGo: 0, flakeSourceVitest: 1, flakeSourcePlaywright: 2}
	sources := make(map[string]*RuntimeSourceReport)
	runtimes := make([]RuntimeTestReport, 0, len(flake.Tests))
	for _, test := range flake.Tests {
		duration := 0.0
		maxDuration := 0.0
		for _, attempt := range test.Attempts {
			duration += attempt.DurationMS
			if attempt.DurationMS > maxDuration {
				maxDuration = attempt.DurationMS
			}
		}
		source, exists := sources[test.Source]
		if !exists {
			source = &RuntimeSourceReport{Source: test.Source}
			sources[test.Source] = source
		}
		source.TestCount++
		source.TotalDurationMS += duration
		source.MaxDurationMS = max(source.MaxDurationMS, maxDuration)
		report.TotalMS += duration
		runtimes = append(runtimes, RuntimeTestReport{
			Source:       test.Source,
			Package:      test.Package,
			TestName:     test.TestName,
			Risk:         test.Risk,
			DurationMS:   duration,
			AttemptCount: len(test.Attempts),
		})
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		left, right := sourceOrder[a], sourceOrder[b]
		if left == right {
			return strings.Compare(a, b)
		}
		return left - right
	})
	for _, name := range names {
		report.BySource = append(report.BySource, *sources[name])
	}
	slices.SortFunc(runtimes, func(a, b RuntimeTestReport) int {
		if a.DurationMS == b.DurationMS {
			return strings.Compare(a.Source+"/"+a.Package+"/"+a.TestName, b.Source+"/"+b.Package+"/"+b.TestName)
		}
		if a.DurationMS > b.DurationMS {
			return -1
		}
		return 1
	})
	if len(runtimes) > 10 {
		runtimes = runtimes[:10]
	}
	report.SlowestTests = runtimes
	report.Collected = report.TotalTests > 0
	return report
}

func governanceNextActions(catalogue ScenarioCatalogue, catalogueReport CatalogueReport, mutation MutationEvidenceReport, coverageWarnings, queryPlanWarnings []string) []string {
	actions := make([]string, 0)
	for _, scenario := range catalogue.Scenarios {
		if scenario.Status == statusCovered || scenario.Status == statusNA {
			continue
		}
		action := fmt.Sprintf("Close %s %s risk", scenario.ID, scenario.Risk)
		if scenario.Status == statusWaived {
			action += " before waiver expiry"
		} else {
			action += ": " + scenario.MissingTest
		}
		actions = append(actions, action)
	}
	if catalogueReport.P0.ActiveWaived > 0 {
		actions = append(actions, "Review active P0 waivers and expiry dates")
	}
	if mutation.Survived > 0 {
		actions = append(actions, "Follow up on survived mutation records")
	}
	for _, warning := range coverageWarnings {
		actions = append(actions, "Improve coverage: "+warning)
	}
	for _, warning := range queryPlanWarnings {
		actions = append(actions, "Review query plan runtime warning: "+warning)
	}
	if len(actions) == 0 {
		actions = append(actions, "Maintain current P0/P1 coverage and rotate the monthly mutation sample")
	}
	return actions
}

func buildGovernanceGate(report GovernanceReport) GovernanceGate {
	gate := GovernanceGate{
		CoveragePassed:               report.ChangedCoverage.ByRisk[coverageRiskP0].Passed && report.ChangedCoverage.ByRisk[coverageRiskP1].Passed && len(report.ChangedCoverage.Errors) == 0,
		TrendPassed:                  report.CoverageTrend.Passed,
		FlakePassed:                  report.Flake.Passed,
		MutationPassed:               report.Mutation.Passed,
		RuntimeCollected:             report.Runtime.Collected,
		QueryPlanTrendPassed:         report.QueryPlanTrend.Passed,
		QueryPlanTrendReviewRequired: report.QueryPlanTrend.ReviewRequired,
		CataloguePassed:              report.Catalogue.P0.EffectiveClosure >= 1 && report.Catalogue.P0.ActiveWaived == 0 && report.Catalogue.P1.EffectiveClosure >= 1,
	}
	if !gate.CoveragePassed {
		gate.Failures = append(gate.Failures, "Changed Lines Coverage Gate failed")
	}
	if !gate.TrendPassed {
		gate.Failures = append(gate.Failures, "Coverage Trend Gate failed")
	}
	if !gate.QueryPlanTrendPassed {
		gate.Failures = append(gate.Failures, "Query Plan Trend Gate failed")
	}
	if !gate.FlakePassed {
		gate.Failures = append(gate.Failures, "Flake Gate failed")
	}
	if !gate.MutationPassed {
		gate.Failures = append(gate.Failures, "Mutation/Negative Proof Gate failed")
	}
	if !gate.CataloguePassed {
		gate.Failures = append(gate.Failures, "Scenario Closure Gate failed")
	}
	if !gate.RuntimeCollected {
		gate.Failures = append(gate.Failures, "Runtime evidence is empty")
	}
	gate.Passed = len(gate.Failures) == 0
	return gate
}
