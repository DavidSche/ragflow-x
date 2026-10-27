// Command testquality-flake writes machine-readable flake evidence.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

type inputFlags []string

func (inputs *inputFlags) String() string {
	return strings.Join(*inputs, ",")
}

func (inputs *inputFlags) Set(value string) error {
	*inputs = append(*inputs, value)
	return nil
}

func main() {
	cataloguePath := flag.String("catalogue", "internal/testquality/testdata/scenario_catalogue.json", "path to scenario_catalogue.json")
	outputPath := flag.String("output", "test-results/flake-report.json", "path to write flake-report.json")
	repositoryDir := flag.String("repository", ".", "repository root used to normalize result paths")
	commit := flag.String("commit", "", "Git commit evaluated by the report")
	job := flag.String("job", "", "CI job evaluated by the report")
	environment := flag.String("environment", "", "runner or service environment evaluated by the report")
	var inputs inputFlags
	flag.Var(&inputs, "input", "machine-readable test result input; repeat for Go, Vitest, and Playwright")
	failOnActive := flag.Bool("fail-on-active", true, "fail when P0/P1 active flakes or evidence errors exist")
	flag.Parse()

	report, err := testquality.EvaluateFlakes(testquality.FlakeConfig{
		RepositoryDir: *repositoryDir,
		CataloguePath: *cataloguePath,
		Inputs:        append([]string(nil), inputs...),
		Commit:        *commit,
		Job:           *job,
		Environment:   *environment,
		FailOnActive:  false,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluate flake evidence: %v\n", err)
		os.Exit(1)
	}
	if err := testquality.WriteFlakeReport(*outputPath, report); err != nil {
		fmt.Fprintf(os.Stderr, "write flake report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *outputPath)
	if *failOnActive && report.Failed {
		for _, failure := range append(append([]string(nil), report.Errors...), report.Failures...) {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(1)
	}
}
