// Command testquality-governance writes the merged quality governance report.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

func main() {
	cataloguePath := flag.String("catalogue", testquality.DefaultScenarioCataloguePath, "path to scenario_catalogue.json")
	flakeReportPath := flag.String("flake-report", "test-results/flake-report.json", "path to flake-report.json")
	changedCoveragePath := flag.String("changed-coverage", "test-results/scenario-quality-report-changed.json", "path to changed coverage report")
	coverageTrendPath := flag.String("coverage-trend", "test-results/coverage-trend.json", "path to coverage-trend.json")
	queryPlanTrendPath := flag.String("query-plan-trend", "test-results/query-plan-trend.json", "path to query-plan-trend.json")
	mutationEvidencePath := flag.String("mutation-evidence", "internal/testquality/testdata/mutation_sample.json", "path to mutation_sample.json")
	outputPath := flag.String("output", "test-results/governance-report.json", "path to write governance-report.json")
	flag.Parse()

	report, err := testquality.EvaluateGovernance(testquality.GovernanceConfig{
		CataloguePath:             *cataloguePath,
		FlakeReportPath:           *flakeReportPath,
		ChangedCoverageReportPath: *changedCoveragePath,
		CoverageTrendPath:         *coverageTrendPath,
		QueryPlanTrendPath:        *queryPlanTrendPath,
		MutationEvidencePath:      *mutationEvidencePath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluate governance: %v\n", err)
	}
	if writeErr := testquality.WriteGovernanceReport(*outputPath, report); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write governance report: %v\n", writeErr)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *outputPath)
	if err != nil {
		os.Exit(1)
	}
}
