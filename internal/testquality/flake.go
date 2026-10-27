package testquality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	flakeSourceGo                    = "go"
	flakeSourceVitest                = "vitest"
	flakeSourcePlaywright            = "playwright"
	flakeStatusPass                  = "PASS"
	flakeStatusFail                  = "FAIL"
	flakeStatusSkip                  = "NOT RUN"
	flakeStatusSuspect               = "SUSPECTED_FLAKY"
	flakeRiskOther                   = "Other"
	flakeClassificationUnclassified  = "UNCLASSIFIED"
	flakeClassificationStableFailure = "STABLE_FAILURE"
)

// FlakeConfig configures flake evaluation from one or more machine-readable reports.
type FlakeConfig struct {
	RepositoryDir string
	CataloguePath string
	Inputs        []string
	Commit        string
	Job           string
	Environment   string
	FailOnActive  bool
}

// FlakeAttempt is one observed result for a test.
type FlakeAttempt struct {
	Source     string  `json:"source"`
	ReportFile string  `json:"reportFile"`
	Package    string  `json:"package,omitempty"`
	TestName   string  `json:"testName"`
	Status     string  `json:"status"`
	Message    string  `json:"message,omitempty"`
	DurationMS float64 `json:"durationMs"`
}

// FlakeTestReport is the grouped flake status for one test.
type FlakeTestReport struct {
	Source         string         `json:"source"`
	File           string         `json:"file"`
	Package        string         `json:"package,omitempty"`
	TestName       string         `json:"testName"`
	ScenarioIDs    []string       `json:"scenarioIds"`
	Risk           string         `json:"risk"`
	Classification string         `json:"classification"`
	Owner          string         `json:"owner"`
	Status         string         `json:"status"`
	Attempts       []FlakeAttempt `json:"attempts"`
}

// FlakeSummary is the P0/P1 gate summary.
type FlakeSummary struct {
	P0ActiveFlake int `json:"p0ActiveFlake"`
	P1ActiveFlake int `json:"p1ActiveFlake"`
	P0Fail        int `json:"p0Fail"`
	P1Fail        int `json:"p1Fail"`
}

// FlakeReport is the machine-readable active flake snapshot.
type FlakeReport struct {
	GeneratedAt    string            `json:"generatedAt"`
	Commit         string            `json:"commit"`
	Job            string            `json:"job,omitempty"`
	Environment    string            `json:"environment,omitempty"`
	Inputs         []string          `json:"inputs"`
	TotalTests     int               `json:"totalTests"`
	CountsByStatus map[string]int    `json:"countsByStatus"`
	Summary        FlakeSummary      `json:"summary"`
	Tests          []FlakeTestReport `json:"tests"`
	Errors         []string          `json:"errors,omitempty"`
	Passed         bool              `json:"passed"`
	Failed         bool              `json:"failed"`
	Failures       []string          `json:"failures,omitempty"`
}

type flakeKey struct {
	Source  string
	Package string
	File    string
	Test    string
}

// EvaluateFlakes reads result reports and classifies stable failures and suspected flakes.
func EvaluateFlakes(config FlakeConfig) (FlakeReport, error) {
	catalogue, err := LoadScenarioCatalogue(config.CataloguePath)
	if err != nil {
		return FlakeReport{}, fmt.Errorf("load scenario catalogue: %w", err)
	}
	report := FlakeReport{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Commit:         config.Commit,
		Job:            config.Job,
		Environment:    config.Environment,
		Inputs:         append([]string(nil), config.Inputs...),
		CountsByStatus: map[string]int{},
	}
	attempts := make([]FlakeAttempt, 0)
	for _, input := range config.Inputs {
		inputAttempts, err := parseFlakeInput(config.RepositoryDir, input)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		attempts = append(attempts, inputAttempts...)
	}
	groups := make(map[flakeKey][]FlakeAttempt)
	for _, attempt := range attempts {
		key := flakeKey{Source: attempt.Source, Package: attempt.Package, File: attempt.Package, Test: attempt.TestName}
		groups[key] = append(groups[key], attempt)
	}
	for key, groupedAttempts := range groups {
		test := FlakeTestReport{
			Source:      key.Source,
			File:        key.File,
			Package:     key.Package,
			TestName:    key.Test,
			ScenarioIDs: []string{},
			Attempts:    groupedAttempts,
		}
		test.Status = aggregateFlakeStatus(groupedAttempts)
		test.Risk, test.ScenarioIDs = catalogue.flakeRisk(key.Source, key.File, key.Package, key.Test)
		test.Classification = flakeClassification(test.Status)
		test.Owner = catalogue.flakeOwner(key.Source, key.File, key.Package, key.Test)
		report.Tests = append(report.Tests, test)
		report.CountsByStatus[test.Status]++
		switch test.Status {
		case flakeStatusFail, flakeStatusSuspect:
			switch test.Risk {
			case coverageRiskP0:
				report.Summary.P0ActiveFlake++
				if test.Status == flakeStatusFail {
					report.Summary.P0Fail++
				}
			case coverageRiskP1:
				report.Summary.P1ActiveFlake++
				if test.Status == flakeStatusFail {
					report.Summary.P1Fail++
				}
			}
		}
	}
	report.TotalTests = len(report.Tests)
	slices.SortFunc(report.Tests, func(a, b FlakeTestReport) int {
		return strings.Compare(a.Source+"/"+a.Package+"/"+a.TestName, b.Source+"/"+b.Package+"/"+b.TestName)
	})
	if report.Summary.P0ActiveFlake > 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("P0 active flake = %d", report.Summary.P0ActiveFlake))
	}
	if report.Summary.P1ActiveFlake > 0 {
		report.Failures = append(report.Failures, fmt.Sprintf("P1 active flake = %d", report.Summary.P1ActiveFlake))
	}
	report.Failed = len(report.Failures) > 0 || len(report.Errors) > 0
	report.Passed = !report.Failed
	if config.FailOnActive && len(report.Failures) > 0 {
		return report, fmt.Errorf("%s", strings.Join(report.Failures, "; "))
	}
	return report, nil
}

// WriteFlakeReport writes a UTF-8 JSON report without a BOM.
func WriteFlakeReport(path string, report FlakeReport) error {
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

func parseFlakeInput(repositoryDir, path string) ([]FlakeAttempt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flake input %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("flake input %s is empty", path)
	}
	if strings.HasPrefix(trimmed, "{") {
		var probe map[string]any
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		if err := decoder.Decode(&probe); err != nil {
			return nil, fmt.Errorf("decode flake input %s: %w", path, err)
		}
		if _, exists := probe["Action"]; exists {
			return parseGoTestJSON(repositoryDir, path, strings.Split(trimmed, "\n"))
		}
		if _, exists := probe["testResults"]; exists {
			return parseVitestJSON(repositoryDir, path, []byte(trimmed))
		}
		if _, exists := probe["suites"]; exists {
			return parsePlaywrightJSON(repositoryDir, path, []byte(trimmed))
		}
	}
	return parseGoTestJSON(repositoryDir, path, strings.Split(trimmed, "\n"))
}

func parseGoTestJSON(repositoryDir string, reportFile string, lines []string) ([]FlakeAttempt, error) {
	attempts := make([]FlakeAttempt, 0)
	outputs := make(map[string][]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Action  string  `json:"Action"`
			Package string  `json:"Package"`
			Test    string  `json:"Test"`
			Elapsed float64 `json:"Elapsed"`
			Output  string  `json:"Output"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode Go test JSON in %s: %w", reportFile, err)
		}
		if event.Test == "" || strings.Contains(event.Test, "/") {
			continue
		}
		eventKey := event.Package + "\x00" + event.Test
		if event.Action == "output" {
			outputs[eventKey] = append(outputs[eventKey], event.Output)
			continue
		}
		status, ok := mapGoAction(event.Action)
		if !ok {
			continue
		}
		attempt := FlakeAttempt{
			Source:     flakeSourceGo,
			ReportFile: reportFile,
			Package:    normalizeGoPackagePath(repositoryDir, event.Package),
			TestName:   event.Test,
			Status:     status,
			Message:    strings.Join(outputs[eventKey], ""),
			DurationMS: event.Elapsed * 1000,
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

func mapGoAction(action string) (string, bool) {
	switch action {
	case "pass":
		return flakeStatusPass, true
	case "fail":
		return flakeStatusFail, true
	case "skip":
		return flakeStatusSkip, true
	default:
		return "", false
	}
}

func normalizeGoPackagePath(repositoryDir, packagePath string) string {
	if modulePath := modulePathFromRepository(repositoryDir); modulePath != "" && strings.HasPrefix(packagePath, modulePath+"/") {
		return strings.TrimPrefix(packagePath, modulePath+"/")
	}
	return packagePath
}

type vitestFlakeFile struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Results []vitestAssertion `json:"assertionResults"`
}

type vitestAssertion struct {
	FullName string   `json:"fullName"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Duration float64  `json:"duration"`
	Messages []string `json:"failureMessages"`
}

func parseVitestJSON(repositoryDir, reportFile string, data []byte) ([]FlakeAttempt, error) {
	var report struct {
		TestResults []vitestFlakeFile `json:"testResults"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode Vitest flake input %s: %w", reportFile, err)
	}
	attempts := make([]FlakeAttempt, 0)
	for _, file := range report.TestResults {
		filePath, err := repositoryRelativePath(repositoryDir, file.Name)
		if err != nil {
			filePath = normalizePath(file.Name)
		}
		for _, result := range file.Results {
			attempts = append(attempts, FlakeAttempt{
				Source:     flakeSourceVitest,
				ReportFile: reportFile,
				Package:    filePath,
				TestName:   result.FullName,
				DurationMS: result.Duration,
				Message:    strings.Join(result.Messages, "\n"),
				Status:     normalizeFlakeStatus(result.Status),
			})
		}
	}
	return attempts, nil
}

type playwrightSuite struct {
	Title  string            `json:"title"`
	File   string            `json:"file"`
	Specs  []playwrightSpec  `json:"specs"`
	Suites []playwrightSuite `json:"suites"`
}

type playwrightSpec struct {
	Title string           `json:"title"`
	File  string           `json:"file"`
	Tests []playwrightTest `json:"tests"`
}

type playwrightTest struct {
	Status  string             `json:"status"`
	Results []playwrightResult `json:"results"`
}

type playwrightResult struct {
	Status   string            `json:"status"`
	Duration float64           `json:"duration"`
	Error    *playwrightError  `json:"error"`
	Errors   []playwrightError `json:"errors"`
}

type playwrightError struct {
	Message string `json:"message"`
}

func parsePlaywrightJSON(repositoryDir, reportFile string, data []byte) ([]FlakeAttempt, error) {
	var report struct {
		Suites []playwrightSuite `json:"suites"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode Playwright flake input %s: %w", reportFile, err)
	}
	attempts := make([]FlakeAttempt, 0)
	for _, suite := range report.Suites {
		attempts = append(attempts, parsePlaywrightSuite(repositoryDir, reportFile, suite)...)
	}
	return attempts, nil
}

func parsePlaywrightSuite(repositoryDir, reportFile string, suite playwrightSuite) []FlakeAttempt {
	attempts := make([]FlakeAttempt, 0)
	for _, spec := range suite.Specs {
		file := spec.File
		if file == "" {
			file = suite.File
		}
		if file != "" && !strings.Contains(file, "/") {
			file = "web/e2e/" + file
		} else {
			if relative, err := repositoryRelativePath(repositoryDir, file); err == nil {
				file = relative
			}
		}
		for _, test := range spec.Tests {
			for _, result := range test.Results {
				attempts = append(attempts, FlakeAttempt{
					Source:     flakeSourcePlaywright,
					ReportFile: reportFile,
					Package:    normalizePath(file),
					TestName:   spec.Title,
					Status:     normalizeFlakeStatus(result.Status),
					Message:    playwrightResultMessage(result),
					DurationMS: result.Duration,
				})
			}
		}
	}
	for _, nested := range suite.Suites {
		attempts = append(attempts, parsePlaywrightSuite(repositoryDir, reportFile, nested)...)
	}
	return attempts
}

func normalizeFlakeStatus(status string) string {
	switch strings.ToLower(status) {
	case "passed", "expected":
		return flakeStatusPass
	case "failed", "unexpected", "timedout", "interrupted":
		return flakeStatusFail
	case "skipped", "pending", "todo":
		return flakeStatusSkip
	default:
		return status
	}
}

func playwrightResultMessage(result playwrightResult) string {
	messages := make([]string, 0)
	if result.Error != nil && result.Error.Message != "" {
		messages = append(messages, result.Error.Message)
	}
	for _, item := range result.Errors {
		if item.Message != "" {
			messages = append(messages, item.Message)
		}
	}
	return strings.Join(messages, "\n")
}

func aggregateFlakeStatus(attempts []FlakeAttempt) string {
	hasPass, hasFail := false, false
	for _, attempt := range attempts {
		switch attempt.Status {
		case flakeStatusPass:
			hasPass = true
		case flakeStatusFail:
			hasFail = true
		}
	}
	switch {
	case hasPass && hasFail:
		return flakeStatusSuspect
	case hasFail:
		return flakeStatusFail
	case hasPass:
		return flakeStatusPass
	default:
		return flakeStatusSkip
	}
}

func (c ScenarioCatalogue) flakeRisk(source, file, packageName, testName string) (string, []string) {
	ids := map[string]struct{}{}
	risk := flakeRiskOther
	for _, scenario := range c.Scenarios {
		if scenario.Risk != coverageRiskP0 && scenario.Risk != coverageRiskP1 {
			continue
		}
		for _, test := range scenario.Tests {
			if !flakeTestMatches(source, file, packageName, testName, test) {
				continue
			}
			ids[scenario.ID] = struct{}{}
			if risk == flakeRiskOther || (scenario.Risk == coverageRiskP0 && risk == coverageRiskP1) {
				risk = scenario.Risk
			}
		}
	}
	return risk, sortedScenarioIDs(ids)
}

func (c ScenarioCatalogue) flakeOwner(source, file, packageName, testName string) string {
	for _, scenario := range c.Scenarios {
		for _, test := range scenario.Tests {
			if flakeTestMatches(source, file, packageName, testName, test) {
				return scenario.TestOwner
			}
		}
	}
	return ""
}

func flakeClassification(status string) string {
	switch status {
	case flakeStatusSuspect:
		return flakeClassificationUnclassified
	case flakeStatusFail:
		return flakeClassificationStableFailure
	default:
		return ""
	}
}

func flakeTestMatches(source, file, packageName, testName string, test ScenarioTest) bool {
	if source == flakeSourceGo {
		return test.Name == testName && (packageName == "" || test.Package == packageName)
	}
	if source != flakeSourceVitest && source != flakeSourcePlaywright {
		return false
	}
	if file != "" && !strings.HasSuffix(normalizePath(file), normalizePath(test.File)) {
		return false
	}
	return strings.Contains(testName, test.Name) || strings.Contains(test.Name, testName)
}
