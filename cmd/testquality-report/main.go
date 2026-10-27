// Command testquality-report writes machine-readable test governance snapshots.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

func main() {
	cataloguePath := flag.String("catalogue", "internal/testquality/testdata/scenario_catalogue.json", "path to scenario_catalogue.json")
	outputPath := flag.String("output", "test-results/scenario-quality-report.json", "path to write the JSON report")
	evaluateChanged := flag.Bool("changed-coverage", false, "evaluate Changed Lines Coverage")
	repositoryDir := flag.String("repository", ".", "repository root used for Git and coverage paths")
	baseRef := flag.String("base-ref", "origin/main", "Git base reference")
	headRef := flag.String("head-ref", "", "Git head reference; empty evaluates the working tree")
	goCoverage := flag.String("go-coverage", "", "path to Go coverage profile")
	frontendCoverage := flag.String("frontend-coverage", "", "path to Vite coverage JSON")
	p0Threshold := flag.Float64("p0-threshold", 85, "minimum P0 changed-line coverage percentage")
	p1Threshold := flag.Float64("p1-threshold", 75, "minimum P1 changed-line coverage percentage")
	flag.Parse()

	catalogue, err := testquality.LoadScenarioCatalogue(*cataloguePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load catalogue: %v\n", err)
		os.Exit(1)
	}
	if !*evaluateChanged {
		if err := catalogue.WriteReport(*outputPath, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "write report: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", *outputPath)
		return
	}
	profiles := []string{}
	if strings.TrimSpace(*goCoverage) != "" {
		profiles = append(profiles, *goCoverage)
	}
	if strings.TrimSpace(*frontendCoverage) != "" {
		profiles = append(profiles, *frontendCoverage)
	}
	changed, err := testquality.EvaluateChangedCoverage(testquality.ChangedCoverageConfig{
		RepositoryDir:      *repositoryDir,
		CataloguePath:      *cataloguePath,
		CoverageProfiles:   profiles,
		BaseRef:            *baseRef,
		HeadRef:            *headRef,
		P0ThresholdPercent: *p0Threshold,
		P1ThresholdPercent: *p1Threshold,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluate changed coverage: %v\n", err)
		os.Exit(1)
	}
	if err := writeChangedCoverageReport(*outputPath, changed); err != nil {
		fmt.Fprintf(os.Stderr, "write changed coverage: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *outputPath)
	if failures := testquality.ChangedCoverageFailures(changed); len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "%s\n", strings.Join(failures, "\n"))
		os.Exit(1)
	}
}

func writeChangedCoverageReport(path string, report testquality.ChangedCoverageReport) error {
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
