package testquality

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	coverageRiskP0    = "P0"
	coverageRiskP1    = "P1"
	coverageRiskOther = "Other"
)

var goBlockPattern = regexp.MustCompile(`^([^:]+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// ChangedCoverageConfig configures Changed Lines Coverage attribution.
type ChangedCoverageConfig struct {
	RepositoryDir       string
	CataloguePath       string
	CoverageProfiles    []string
	BaseRef             string
	HeadRef             string
	P0ThresholdPercent  float64
	P1ThresholdPercent  float64
	RequiredRiskDomains []string
}

// ChangedCoverageReport is the machine-readable changed-line gate result.
type ChangedCoverageReport struct {
	BaseRef         string                         `json:"baseRef"`
	HeadRef         string                         `json:"headRef"`
	Profiles        []string                       `json:"profiles"`
	Total           ChangedCoverageSummary         `json:"total"`
	ByRisk          map[string]ChangedCoverageRisk `json:"byRisk"`
	Files           []ChangedCoverageFile          `json:"files"`
	RequiredDomains map[string]bool                `json:"requiredDomains"`
	Errors          []string                       `json:"errors,omitempty"`
}

// ChangedCoverageRisk summarizes one risk bucket.
type ChangedCoverageRisk struct {
	ChangedLines   int      `json:"changedLines"`
	CoveredLines   int      `json:"coveredLines"`
	Coverage       float64  `json:"coverage"`
	Threshold      float64  `json:"threshold"`
	Passed         bool     `json:"passed"`
	PassFiles      []string `json:"passFiles,omitempty"`
	UncoveredFiles []string `json:"uncoveredFiles,omitempty"`
}

// ChangedCoverageFile attributes one changed production file to scenarios.
type ChangedCoverageFile struct {
	Path           string   `json:"path"`
	Risk           string   `json:"risk"`
	ScenarioIDs    []string `json:"scenarioIds"`
	ChangedLines   int      `json:"changedLines"`
	CoveredLines   int      `json:"coveredLines"`
	Coverage       float64  `json:"coverage"`
	UncoveredLines []int    `json:"uncoveredLines,omitempty"`
	LineFilter     string   `json:"lineFilter"`
	MappingMethod  string   `json:"mappingMethod"`
}

// ChangedCoverageSummary is the total gate result.
type ChangedCoverageSummary struct {
	ChangedLines int     `json:"changedLines"`
	CoveredLines int     `json:"coveredLines"`
	Coverage     float64 `json:"coverage"`
}

// EvaluateChangedCoverage computes the diff-based line coverage gate.
func EvaluateChangedCoverage(config ChangedCoverageConfig) (ChangedCoverageReport, error) {
	report := ChangedCoverageReport{
		BaseRef:         config.BaseRef,
		HeadRef:         config.HeadRef,
		Profiles:        append([]string(nil), config.CoverageProfiles...),
		ByRisk:          map[string]ChangedCoverageRisk{},
		RequiredDomains: map[string]bool{},
	}

	catalogue, err := LoadScenarioCatalogue(config.CataloguePath)
	if err != nil {
		return report, fmt.Errorf("load scenario catalogue: %w", err)
	}
	repositoryDir := config.RepositoryDir
	if repositoryDir == "" {
		repositoryDir = catalogue.repositoryDir
	}
	report.RequiredDomains[coverageRiskP0] = true
	report.RequiredDomains[coverageRiskP1] = true
	for _, domain := range config.RequiredRiskDomains {
		report.RequiredDomains[domain] = true
	}

	changedLines, err := runGitDiffLines(repositoryDir, config.BaseRef, config.HeadRef)
	if err != nil {
		return report, err
	}
	coveredLines := make(map[string]map[int]struct{})
	for _, profile := range config.CoverageProfiles {
		profileLines, profileErr := parseCoverageFile(profile)
		if profileErr != nil {
			report.Errors = append(report.Errors, profileErr.Error())
			continue
		}
		for path, lines := range profileLines {
			relativePath, relErr := repositoryRelativePath(repositoryDir, path)
			if relErr != nil {
				report.Errors = append(report.Errors, relErr.Error())
				continue
			}
			merged, exists := coveredLines[relativePath]
			if !exists {
				merged = make(map[int]struct{})
				coveredLines[relativePath] = merged
			}
			for line := range lines {
				merged[line] = struct{}{}
			}
		}
	}
	report.Files = buildChangedCoverageFiles(repositoryDir, changedLines, coveredLines)
	assignChangedCoverageRisk(report.Files, catalogue.RiskCountByFile(), catalogue.ScenarioIDsByFile(), report.RequiredDomains)
	summarizeChangedCoverage(&report, config)
	return report, nil
}

func runGitDiffLines(repositoryDir, baseRef, headRef string) (map[string]map[int]string, error) {
	if strings.TrimSpace(baseRef) == "" {
		return nil, fmt.Errorf("baseRef is required")
	}
	args := []string{"-C", repositoryDir, "diff", "--no-ext-diff", "--no-color", "--unified=0", "--diff-filter=ACM", baseRef}
	if strings.TrimSpace(headRef) != "" {
		args = append(args, headRef)
	}
	command := exec.Command("git", args...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	changed := parseGitDiffLines(output)
	if err := appendUntrackedChangedLines(repositoryDir, changed); err != nil {
		return nil, err
	}
	return changed, nil
}

func appendUntrackedChangedLines(repositoryDir string, changed map[string]map[int]string) error {
	command := exec.Command("git", "-C", repositoryDir, "ls-files", "--others", "--exclude-standard")
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("git list untracked files: %w", err)
	}
	for _, rawPath := range strings.Split(string(output), "\n") {
		relativePath := normalizePath(rawPath)
		if relativePath == "" || !isSourcePath(relativePath) {
			continue
		}
		if isTestAssetPath(relativePath) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repositoryDir, filepath.FromSlash(relativePath)))
		if err != nil {
			return fmt.Errorf("read untracked changed file %s: %w", relativePath, err)
		}
		lines := changed[relativePath]
		if lines == nil {
			lines = make(map[int]string)
			changed[relativePath] = lines
		}
		content := strings.TrimRight(string(data), "\n")
		for index, rawLine := range strings.Split(content, "\n") {
			lines[index+1] = strings.TrimRight(rawLine, "\r")
		}
	}
	return nil
}

func isSourcePath(path string) bool {
	switch strings.ToLower(path[strings.LastIndex(path, ".")+1:]) {
	case "go", "ts", "tsx", "js", "jsx", "json", "yaml", "yml", "md":
		return true
	}
	return strings.EqualFold(path, "makefile")
}

func parseGitDiffLines(raw []byte) map[string]map[int]string {
	changed := map[string]map[int]string{}
	path := ""
	newStart := 0
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.HasPrefix(line, "+++ b/") {
			path = normalizePath(strings.TrimPrefix(line, "+++ b/"))
			changed[path] = map[int]string{}
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			newStart = parseNewHunkStart(line)
			continue
		}
		if path == "" || newStart == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			changed[path][newStart] = strings.TrimPrefix(line, "+")
			newStart++
		case strings.HasPrefix(line, "-"):
		case strings.HasPrefix(line, " ") || line == "":
			newStart++
		}
	}
	return changed
}

func parseNewHunkStart(header string) int {
	parts := strings.Fields(header)
	if len(parts) < 3 {
		return 0
	}
	newRange := strings.TrimPrefix(parts[2], "+")
	number, _, _ := strings.Cut(newRange, ",")
	start, err := strconv.Atoi(number)
	if err != nil || start == 0 {
		return 0
	}
	return start
}

func normalizePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.TrimPrefix(path, "./")
}

func parseCoverageFile(path string) (map[string]map[int]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read coverage profile %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("coverage profile %s is empty", path)
	}
	if strings.HasPrefix(trimmed, "{") {
		return parseViteCoverage(data)
	}
	return parseGoCoverage(strings.Split(trimmed, "\n")), nil
}

func parseGoCoverage(lines []string) map[string]map[int]struct{} {
	covered := make(map[string]map[int]struct{})
	for _, line := range lines {
		matches := goBlockPattern.FindStringSubmatch(strings.TrimSpace(line))
		if matches == nil || strings.TrimSpace(matches[1]) == "mode" {
			continue
		}
		start, err := strconv.Atoi(matches[2])
		end, endErr := strconv.Atoi(matches[4])
		count, countErr := strconv.Atoi(matches[7])
		if err != nil || endErr != nil || countErr != nil || count == 0 || end < start {
			continue
		}
		path := normalizePath(matches[1])
		if covered[path] == nil {
			covered[path] = map[int]struct{}{}
		}
		for lineNumber := start; lineNumber <= end; lineNumber++ {
			covered[path][lineNumber] = struct{}{}
		}
	}
	return covered
}

type viteCoverageFile struct {
	Path  string `json:"path"`
	Lines struct {
		Hits map[string]int `json:"hits"`
	} `json:"lines"`
	StatementMap map[string]viteStatementRange `json:"statementMap"`
	Statements   map[string]int                `json:"s"`
}

type viteStatementRange struct {
	Start vitePosition `json:"start"`
	End   vitePosition `json:"end"`
}

type vitePosition struct {
	Line int `json:"line"`
}

func parseViteCoverage(data []byte) (map[string]map[int]struct{}, error) {
	var report map[string]viteCoverageFile
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode Vite coverage: %w", err)
	}
	covered := make(map[string]map[int]struct{})
	for path, file := range report {
		lines := make(map[int]struct{})
		for statement, count := range file.Statements {
			if count <= 0 {
				continue
			}
			position, exists := file.StatementMap[statement]
			if !exists || position.Start.Line < 1 || position.End.Line < position.Start.Line {
				continue
			}
			for lineNumber := position.Start.Line; lineNumber <= position.End.Line; lineNumber++ {
				lines[lineNumber] = struct{}{}
			}
		}
		for line, hits := range file.Lines.Hits {
			lineNumber, err := strconv.Atoi(line)
			if err == nil && hits > 0 {
				lines[lineNumber] = struct{}{}
			}
		}
		covered[normalizePath(path)] = lines
	}
	return covered, nil
}

func repositoryRelativePath(repositoryDir, path string) (string, error) {
	relative := normalizePath(path)
	if modulePath := modulePathFromRepository(repositoryDir); modulePath != "" && strings.HasPrefix(relative, modulePath+"/") {
		relative = strings.TrimPrefix(relative, modulePath+"/")
	}
	if !strings.HasPrefix(relative, "/") && !strings.Contains(relative, ":") {
		return relative, nil
	}
	absolute, err := filepathAbs(path)
	if err != nil {
		return "", fmt.Errorf("resolve coverage path %q: %w", path, err)
	}
	if cleanRepository := filepathClean(repositoryDir); cleanRepository != "" {
		if relativePath, err := filepathRel(cleanRepository, absolute); err == nil && !strings.HasPrefix(relativePath, "..") {
			return normalizePath(relativePath), nil
		}
	}
	if index := strings.Index(relative, "/web/"); index >= 0 {
		return relative[index+1:], nil
	}
	return "", fmt.Errorf("coverage path %q is outside repository", path)
}

func buildChangedCoverageFiles(repositoryDir string, changedLines map[string]map[int]string, coveredLines map[string]map[int]struct{}) []ChangedCoverageFile {
	files := make([]ChangedCoverageFile, 0, len(changedLines))
	for path := range changedLines {
		if isTestAssetPath(path) {
			continue
		}
		file := ChangedCoverageFile{
			Path:          path,
			Risk:          coverageRiskOther,
			ScenarioIDs:   []string{},
			LineFilter:    lineFilterName(path),
			MappingMethod: "catalogue-mapped-files",
		}
		for lineNumber, text := range changedLines[path] {
			if !isExecutableChangedLine(path, text) {
				continue
			}
			file.ChangedLines++
			if _, covered := coveredLines[path][lineNumber]; covered {
				file.CoveredLines++
			} else {
				file.UncoveredLines = append(file.UncoveredLines, lineNumber)
			}
		}
		slices.Sort(file.UncoveredLines)
		file.Coverage = ratioPercent(file.CoveredLines, file.ChangedLines)
		files = append(files, file)
	}
	slices.SortFunc(files, func(a, b ChangedCoverageFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

func isTestAssetPath(path string) bool {
	path = strings.ReplaceAll(path, "\\", "/")
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.tsx")
}

func lineFilterName(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx"):
		return "typescript"
	default:
		return "text"
	}
}

func isExecutableChangedLine(path, text string) bool {
	line := strings.TrimSpace(text)
	if line == "" {
		return false
	}
	switch lineFilterName(path) {
	case "go":
		return line != "package" && !strings.HasPrefix(line, "package ") &&
			!strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "/*") &&
			line != "*" && !strings.HasPrefix(line, "* ") &&
			!strings.HasPrefix(line, "import ") && !strings.HasPrefix(line, "\"") &&
			!strings.HasPrefix(line, "import (") &&
			line != "{" && line != "}" && line != ")" &&
			!strings.HasPrefix(line, "func ") &&
			!strings.HasPrefix(line, "type ") &&
			!strings.HasPrefix(line, "const ")
	case "typescript":
		return !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "/*") &&
			line != "*" && !strings.HasPrefix(line, "* ") &&
			!strings.HasPrefix(line, "import ") && !strings.HasPrefix(line, "import {") &&
			!strings.HasPrefix(line, "import type") && !strings.HasPrefix(line, "import(") &&
			!strings.HasPrefix(line, "export type") && !strings.HasPrefix(line, "export interface") &&
			!strings.HasPrefix(line, "export *")
	default:
		return true
	}
}

func assignChangedCoverageRisk(files []ChangedCoverageFile, riskCount map[string]map[string]int, scenarios map[string]map[string]struct{}, _ map[string]bool) {
	for fileIndex := range files {
		path := files[fileIndex].Path
		ids := sortedScenarioIDs(scenarios[path])
		files[fileIndex].ScenarioIDs = ids
		switch {
		case riskCount[coverageRiskP0][path] > 0:
			files[fileIndex].Risk = coverageRiskP0
		case riskCount[coverageRiskP1][path] > 0:
			files[fileIndex].Risk = coverageRiskP1
		default:
			files[fileIndex].Risk = coverageRiskOther
			files[fileIndex].MappingMethod = "manual-review-required"
		}
	}
}

func summarizeChangedCoverage(report *ChangedCoverageReport, config ChangedCoverageConfig) {
	riskBuckets := map[string]ChangedCoverageRisk{
		coverageRiskP0:    {Threshold: config.P0ThresholdPercent},
		coverageRiskP1:    {Threshold: config.P1ThresholdPercent},
		coverageRiskOther: {Threshold: 0},
	}
	passed := make(map[string][]string)
	uncovered := make(map[string][]string)
	for fileIndex := range report.Files {
		file := report.Files[fileIndex]
		bucket := riskBuckets[file.Risk]
		bucket.ChangedLines += file.ChangedLines
		bucket.CoveredLines += file.CoveredLines
		riskBuckets[file.Risk] = bucket
		if file.Coverage >= fileThreshold(config, file.Risk) {
			passed[file.Risk] = append(passed[file.Risk], file.Path)
		} else {
			uncovered[file.Risk] = append(uncovered[file.Risk], file.Path)
		}
	}
	for risk, bucket := range riskBuckets {
		bucket.Coverage = ratioPercent(bucket.CoveredLines, bucket.ChangedLines)
		bucket.Passed = bucket.Coverage >= fileThreshold(config, risk)
		bucket.PassFiles = slices.Clone(passed[risk])
		bucket.UncoveredFiles = slices.Clone(uncovered[risk])
		report.ByRisk[risk] = bucket
		report.Total.ChangedLines += bucket.ChangedLines
		report.Total.CoveredLines += bucket.CoveredLines
	}
	report.Total.Coverage = ratioPercent(report.Total.CoveredLines, report.Total.ChangedLines)
}

func fileThreshold(config ChangedCoverageConfig, risk string) float64 {
	switch risk {
	case coverageRiskP0:
		return config.P0ThresholdPercent
	case coverageRiskP1:
		return config.P1ThresholdPercent
	default:
		return 0
	}
}

func sortedScenarioIDs(values map[string]struct{}) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func ratioPercent(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator) * 100
}
