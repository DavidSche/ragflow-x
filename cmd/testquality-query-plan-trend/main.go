// Command testquality-query-plan-trend writes cross-run query plan trend evidence.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

func main() {
	report1xPath := flag.String("report-1x", "test-results/postgres-query-plan-report-1x.json", "path to 1x query plan report")
	report10xPath := flag.String("report-10x", "test-results/postgres-query-plan-report-10x.json", "path to 10x query plan report")
	report100xPath := flag.String("report-100x", "test-results/postgres-query-plan-report-100x.json", "path to 100x query plan report")
	outputPath := flag.String("output", "test-results/query-plan-trend.json", "path to write query-plan-trend.json")
	historyPath := flag.String("history", "test-results/query-plan-trend-history.json", "cross-run history JSON")
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
	report, err := testquality.EvaluateQueryPlanTrend(testquality.QueryPlanTrendConfig{
		Report1xPath:   *report1xPath,
		Report10xPath:  *report10xPath,
		Report100xPath: *report100xPath,
		PreviousPath:   *historyPath,
		Commit:         *commit,
		GeneratedAt:    timestamp,
	})
	if writeErr := testquality.WriteQueryPlanTrendReport(*outputPath, report); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write query plan trend report: %v\n", writeErr)
		os.Exit(1)
	}
	if err == nil && *historyPath != "" {
		history, historyErr := testquality.LoadQueryPlanTrendHistory(*historyPath)
		if errors.Is(historyErr, fs.ErrNotExist) {
			history = testquality.QueryPlanTrendHistory{Version: 1}
		}
		if historyErr == nil || errors.Is(historyErr, fs.ErrNotExist) {
			history = testquality.AppendQueryPlanTrendHistory(history, report.Current)
			if writeErr := testquality.WriteQueryPlanTrendHistory(*historyPath, history); writeErr != nil {
				fmt.Fprintf(os.Stderr, "write query plan trend history: %v\n", writeErr)
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
