// Command testquality-coverage-trend writes statement coverage trend evidence.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

func main() {
	repositoryDir := flag.String("repository", ".", "repository root used to normalize coverage paths")
	goCoveragePath := flag.String("go-coverage", "test-results/go-coverage.out", "path to Go coverage profile")
	frontendPath := flag.String("frontend-coverage", "web/coverage/coverage-final.json", "path to Vite coverage JSON")
	targetsPath := flag.String("targets", "internal/testquality/testdata/coverage_targets.json", "path to coverage_targets.json")
	outputPath := flag.String("output", "test-results/coverage-trend.json", "path to write coverage-trend.json")
	historyPath := flag.String("history", "test-results/coverage-trend-history.json", "optional history JSON used for regression comparison")
	previousPath := flag.String("previous", "internal/testquality/testdata/coverage_trend_baseline.json", "checked-in or prior-run baseline used for regression comparison")
	commit := flag.String("commit", "", "Git commit measured by the snapshot")
	generatedAt := flag.String("generated-at", "", "optional RFC3339 snapshot timestamp")
	flag.Parse()

	timestamp := time.Now()
	if *generatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, *generatedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse generated-at: %v\n", err)
			os.Exit(1)
		}
		timestamp = parsed
	}
	report, err := testquality.EvaluateCoverageTrend(testquality.CoverageTrendConfig{
		RepositoryDir:  *repositoryDir,
		GoCoveragePath: *goCoveragePath,
		FrontendPath:   *frontendPath,
		TargetsPath:    *targetsPath,
		PreviousPath:   *previousPath,
		Commit:         *commit,
		GeneratedAt:    timestamp,
	})
	if writeErr := testquality.WriteCoverageTrendReport(*outputPath, report); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write coverage trend report: %v\n", writeErr)
		os.Exit(1)
	}
	if err == nil && *historyPath != "" {
		if history, historyErr := testquality.LoadCoverageTrendHistory(*historyPath); historyErr == nil {
			history = testquality.AppendCoverageTrendHistory(history, report.Current)
			if writeErr := testquality.WriteCoverageTrendHistory(*historyPath, history); writeErr != nil {
				fmt.Fprintf(os.Stderr, "write coverage trend history: %v\n", writeErr)
				os.Exit(1)
			}
		}
	}
	fmt.Printf("wrote %s\n", *outputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
