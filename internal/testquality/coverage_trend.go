package testquality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	coverageTrendSourceGo     = "go"
	coverageTrendSourceVitest = "vitest"
	coverageTrendTotal        = "total"
	coverageTrendHistoryMax   = 90
	coverageTrendPrecision    = 0.05
)

type coverageTrendAccumulator struct {
	statements        int
	coveredStatements int
	target            float64
}

// CoverageTrendFile records statement coverage for one source file.
type CoverageTrendFile struct {
	Source            string  `json:"source"`
	Path              string  `json:"path"`
	Statements        int     `json:"statements"`
	CoveredStatements int     `json:"coveredStatements"`
	Coverage          float64 `json:"coverage"`
}

// CoverageTrendScope aggregates one configured package or frontend scope.
type CoverageTrendScope struct {
	Path              string  `json:"path"`
	Statements        int     `json:"statements"`
	CoveredStatements int     `json:"coveredStatements"`
	Coverage          float64 `json:"coverage"`
	Target            float64 `json:"target,omitempty"`
	TargetPassed      bool    `json:"targetPassed"`
}

// CoverageTrendSnapshot is one point-in-time statement coverage baseline.
type CoverageTrendSnapshot struct {
	GeneratedAt string               `json:"generatedAt"`
	Commit      string               `json:"commit,omitempty"`
	Total       CoverageTrendScope   `json:"total"`
	Scopes      []CoverageTrendScope `json:"scopes"`
	Files       []CoverageTrendFile  `json:"files"`
}

// CoverageTrendDelta compares a scope with the prior snapshot.
type CoverageTrendDelta struct {
	Path             string   `json:"path"`
	PreviousCoverage *float64 `json:"previousCoverage,omitempty"`
	CurrentCoverage  float64  `json:"currentCoverage"`
	Delta            *float64 `json:"delta,omitempty"`
	Regression       bool     `json:"regression"`
}

// CoverageTargets is the auditable target registry for trend reporting.
type CoverageTargets struct {
	Version int              `json:"version"`
	Targets []CoverageTarget `json:"targets"`
}

// CoverageTarget is one package or frontend scope target.
type CoverageTarget struct {
	Path   string  `json:"path"`
	Target float64 `json:"target"`
}

// CoverageTrendHistory stores bounded snapshot history.
type CoverageTrendHistory struct {
	Version   int                     `json:"version"`
	Snapshots []CoverageTrendSnapshot `json:"snapshots"`
}

// CoverageTrendConfig points to profile, target, and history inputs.
type CoverageTrendConfig struct {
	RepositoryDir  string
	GoCoveragePath string
	FrontendPath   string
	TargetsPath    string
	PreviousPath   string
	Commit         string
	GeneratedAt    time.Time
}

// CoverageTrendReport is the machine-readable monthly coverage trend.
type CoverageTrendReport struct {
	Current     CoverageTrendSnapshot  `json:"current"`
	Previous    *CoverageTrendSnapshot `json:"previous,omitempty"`
	Deltas      []CoverageTrendDelta   `json:"deltas"`
	Regressions []string               `json:"regressions,omitempty"`
	Warnings    []string               `json:"warnings,omitempty"`
	Errors      []string               `json:"errors,omitempty"`
	HistorySize int                    `json:"historySize"`
	Collected   bool                   `json:"collected"`
	Passed      bool                   `json:"passed"`
}

// LoadCoverageTargets reads and validates the trend target registry.
func LoadCoverageTargets(path string) (CoverageTargets, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CoverageTargets{}, fmt.Errorf("read coverage targets: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	var targets CoverageTargets
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&targets); err != nil {
		return CoverageTargets{}, fmt.Errorf("decode coverage targets: %w", err)
	}
	if targets.Version != 1 {
		return targets, fmt.Errorf("coverage targets version must be 1")
	}
	seen := make(map[string]struct{}, len(targets.Targets))
	for _, target := range targets.Targets {
		if strings.TrimSpace(target.Path) == "" || strings.HasSuffix(target.Path, "/") {
			return targets, fmt.Errorf("coverage target path is invalid")
		}
		if target.Target < 0 || target.Target > 100 {
			return targets, fmt.Errorf("coverage target for %s must be between 0 and 100", target.Path)
		}
		if _, exists := seen[target.Path]; exists {
			return targets, fmt.Errorf("duplicate coverage target %s", target.Path)
		}
		seen[target.Path] = struct{}{}
	}
	return targets, nil
}

// EvaluateCoverageTrend builds statement coverage trend evidence from profiles.
func EvaluateCoverageTrend(config CoverageTrendConfig) (CoverageTrendReport, error) {
	report := CoverageTrendReport{}
	if config.GeneratedAt.IsZero() {
		config.GeneratedAt = time.Now()
	}
	files := make([]CoverageTrendFile, 0)
	if config.GoCoveragePath != "" {
		goFiles, err := parseGoCoverageStatements(config.GoCoveragePath, config.RepositoryDir)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			files = append(files, goFiles...)
		}
	}
	if config.FrontendPath != "" {
		viteFiles, err := parseViteCoverageStatements(config.FrontendPath, config.RepositoryDir)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			files = append(files, viteFiles...)
		}
	}

	var targets CoverageTargets
	if config.TargetsPath != "" {
		loaded, err := LoadCoverageTargets(config.TargetsPath)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			targets = loaded
		}
	}

	current := CoverageTrendSnapshot{
		GeneratedAt: config.GeneratedAt.UTC().Format(time.RFC3339),
		Commit:      config.Commit,
		Files:       files,
	}
	current.Total = aggregateCoverageTrendFiles(coverageTrendTotal, files, nil)
	current.Scopes = aggregateCoverageTrendScopes(files, targets.Targets)
	report.Current = current
	report.Collected = current.Total.Statements > 0 && len(report.Errors) == 0
	for _, scope := range current.Scopes {
		if scope.Target > 0 && !scope.TargetPassed {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"%s coverage %.2f%% is below target %.2f%%",
				scope.Path, scope.Coverage, scope.Target,
			))
		}
	}

	var history CoverageTrendHistory
	if config.PreviousPath != "" {
		loaded, err := LoadCoverageTrendHistory(config.PreviousPath)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				report.Errors = append(report.Errors, err.Error())
			}
		} else {
			history = loaded
			if previous, exists := latestCoverageSnapshotBefore(history.Snapshots, current.GeneratedAt); exists {
				report.Previous = &previous
				report.Deltas = compareCoverageSnapshots(previous, current)
				for _, delta := range report.Deltas {
					if delta.Regression {
						report.Regressions = append(report.Regressions, fmt.Sprintf(
							"%s coverage regressed from %.2f%% to %.2f%%",
							delta.Path, *delta.PreviousCoverage, delta.CurrentCoverage,
						))
					}
				}
			}
		}
	}
	report.HistorySize = len(history.Snapshots)
	report.Passed = report.Collected && len(report.Errors) == 0 && len(report.Regressions) == 0
	if !report.Passed {
		failures := append(append([]string(nil), report.Errors...), report.Regressions...)
		return report, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return report, nil
}

// LoadCoverageTrendHistory reads bounded snapshot history.
func LoadCoverageTrendHistory(path string) (CoverageTrendHistory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CoverageTrendHistory{}, fmt.Errorf("read coverage trend history: %w", err)
	}
	var history CoverageTrendHistory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&history); err != nil {
		return history, fmt.Errorf("decode coverage trend history: %w", err)
	}
	if history.Version != 1 {
		return history, fmt.Errorf("coverage trend history version must be 1")
	}
	return history, nil
}

// AppendCoverageTrendHistory replaces same-commit snapshots and bounds history.
func AppendCoverageTrendHistory(history CoverageTrendHistory, snapshot CoverageTrendSnapshot) CoverageTrendHistory {
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
	slices.SortFunc(history.Snapshots, func(a, b CoverageTrendSnapshot) int {
		return strings.Compare(a.GeneratedAt, b.GeneratedAt)
	})
	if len(history.Snapshots) > coverageTrendHistoryMax {
		history.Snapshots = history.Snapshots[len(history.Snapshots)-coverageTrendHistoryMax:]
	}
	return history
}

// WriteCoverageTrendHistory writes UTF-8 JSON without a BOM.
func WriteCoverageTrendHistory(path string, history CoverageTrendHistory) error {
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

// LoadCoverageTrendReport reads a generated report.
func LoadCoverageTrendReport(path string) (CoverageTrendReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CoverageTrendReport{}, fmt.Errorf("read coverage trend report: %w", err)
	}
	var report CoverageTrendReport
	if err := json.Unmarshal(data, &report); err != nil {
		return report, fmt.Errorf("decode coverage trend report: %w", err)
	}
	return report, nil
}

// WriteCoverageTrendReport writes UTF-8 JSON without a BOM.
func WriteCoverageTrendReport(path string, report CoverageTrendReport) error {
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

func parseGoCoverageStatements(path, repositoryDir string) ([]CoverageTrendFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Go coverage profile %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("Go coverage profile %s is empty", path)
	}
	accumulators := make(map[string]*coverageTrendAccumulator)
	seenBlocks := make(map[string]bool)
	for _, line := range strings.Split(trimmed, "\n") {
		matches := goBlockPattern.FindStringSubmatch(strings.TrimSpace(line))
		if matches == nil || strings.TrimSpace(matches[1]) == "mode" {
			continue
		}
		statementCount, err := parseNonNegativeInt(matches[6])
		if err != nil {
			return nil, fmt.Errorf("parse Go coverage profile %s: %w", path, err)
		}
		executionCount, err := parseNonNegativeInt(matches[7])
		if err != nil {
			return nil, fmt.Errorf("parse Go coverage profile %s: %w", path, err)
		}
		relativePath, err := repositoryRelativePath(repositoryDir, matches[1])
		if err != nil {
			return nil, err
		}
		item := accumulators[relativePath]
		if item == nil {
			item = &coverageTrendAccumulator{}
			accumulators[relativePath] = item
		}
		blockKey := strings.Join(matches[1:6], ":")
		if covered, duplicate := seenBlocks[blockKey]; duplicate {
			if executionCount > 0 && !covered {
				item.coveredStatements += statementCount
				seenBlocks[blockKey] = true
			}
			continue
		}
		seenBlocks[blockKey] = executionCount > 0
		item.statements += statementCount
		if executionCount > 0 {
			item.coveredStatements += statementCount
		}
	}
	return coverageTrendFileList(coverageTrendSourceGo, accumulators), nil
}

func parseViteCoverageStatements(path, repositoryDir string) ([]CoverageTrendFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Vite coverage profile %s: %w", path, err)
	}
	var report map[string]viteCoverageFile
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode Vite coverage profile %s: %w", path, err)
	}
	accumulators := make(map[string]*coverageTrendAccumulator)
	for key, file := range report {
		sourcePath := file.Path
		if sourcePath == "" {
			sourcePath = key
		}
		relativePath, err := repositoryRelativePath(repositoryDir, sourcePath)
		if err != nil {
			return nil, err
		}
		item := accumulators[relativePath]
		if item == nil {
			item = &coverageTrendAccumulator{}
			accumulators[relativePath] = item
		}
		for _, count := range file.Statements {
			item.statements++
			if count > 0 {
				item.coveredStatements++
			}
		}
	}
	return coverageTrendFileList(coverageTrendSourceVitest, accumulators), nil
}

func coverageTrendFileList(source string, accumulators map[string]*coverageTrendAccumulator) []CoverageTrendFile {
	files := make([]CoverageTrendFile, 0, len(accumulators))
	for path, item := range accumulators {
		files = append(files, CoverageTrendFile{
			Source:            source,
			Path:              path,
			Statements:        item.statements,
			CoveredStatements: item.coveredStatements,
			Coverage:          coveragePercent(item.coveredStatements, item.statements),
		})
	}
	slices.SortFunc(files, func(a, b CoverageTrendFile) int {
		return strings.Compare(a.Source+"/"+a.Path, b.Source+"/"+b.Path)
	})
	return files
}

func aggregateCoverageTrendScopes(files []CoverageTrendFile, targets []CoverageTarget) []CoverageTrendScope {
	accumulators := make(map[string]*coverageTrendAccumulator)
	for _, target := range targets {
		accumulators[target.Path] = &coverageTrendAccumulator{target: target.Target}
	}
	for _, file := range files {
		scopePath := coverageScopeForPath(file.Path, targets)
		item := accumulators[scopePath]
		if item == nil {
			item = &coverageTrendAccumulator{}
			accumulators[scopePath] = item
		}
		item.statements += file.Statements
		item.coveredStatements += file.CoveredStatements
	}
	scopes := make([]CoverageTrendScope, 0, len(accumulators))
	for path, item := range accumulators {
		scopes = append(scopes, CoverageTrendScope{
			Path:              path,
			Statements:        item.statements,
			CoveredStatements: item.coveredStatements,
			Coverage:          coveragePercent(item.coveredStatements, item.statements),
			Target:            item.target,
			TargetPassed:      item.target == 0 || coveragePercent(item.coveredStatements, item.statements)+coverageTrendPrecision >= item.target,
		})
	}
	slices.SortFunc(scopes, func(a, b CoverageTrendScope) int {
		return strings.Compare(a.Path, b.Path)
	})
	return scopes
}

func aggregateCoverageTrendFiles(path string, files []CoverageTrendFile, _ []CoverageTarget) CoverageTrendScope {
	total := CoverageTrendScope{Path: path}
	for _, file := range files {
		total.Statements += file.Statements
		total.CoveredStatements += file.CoveredStatements
	}
	total.Coverage = coveragePercent(total.CoveredStatements, total.Statements)
	total.TargetPassed = true
	return total
}

func coverageScopeForPath(path string, targets []CoverageTarget) string {
	selected := "other"
	selectedLength := -1
	for _, target := range targets {
		if path == target.Path || strings.HasPrefix(path, target.Path+"/") {
			if len(target.Path) > selectedLength {
				selected = target.Path
				selectedLength = len(target.Path)
			}
		}
	}
	return selected
}

func compareCoverageSnapshots(previous, current CoverageTrendSnapshot) []CoverageTrendDelta {
	previousScopes := make(map[string]CoverageTrendScope, len(previous.Scopes)+1)
	previousScopes[previous.Total.Path] = previous.Total
	for _, scope := range previous.Scopes {
		previousScopes[scope.Path] = scope
	}
	currentScopes := make(map[string]CoverageTrendScope, len(current.Scopes)+1)
	currentScopes[current.Total.Path] = current.Total
	for _, scope := range current.Scopes {
		currentScopes[scope.Path] = scope
	}
	paths := make([]string, 0, len(currentScopes))
	for path := range currentScopes {
		paths = append(paths, path)
	}
	for path := range previousScopes {
		if _, exists := currentScopes[path]; !exists {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	deltas := make([]CoverageTrendDelta, 0, len(paths))
	for _, path := range paths {
		currentScope, currentExists := currentScopes[path]
		previousScope, previousExists := previousScopes[path]
		delta := CoverageTrendDelta{Path: path, Regression: false}
		if currentExists {
			delta.CurrentCoverage = currentScope.Coverage
		}
		if previousExists && currentExists {
			previousCoverage := previousScope.Coverage
			difference := currentScope.Coverage - previousScope.Coverage
			delta.PreviousCoverage = &previousCoverage
			delta.Delta = &difference
			delta.Regression = difference < -coverageTrendPrecision
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func latestCoverageSnapshotBefore(snapshots []CoverageTrendSnapshot, generatedAt string) (CoverageTrendSnapshot, bool) {
	var selected CoverageTrendSnapshot
	found := false
	for _, snapshot := range snapshots {
		if snapshot.GeneratedAt < generatedAt && (!found || snapshot.GeneratedAt > selected.GeneratedAt) {
			selected = snapshot
			found = true
		}
	}
	return selected, found
}

func coveragePercent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	value := float64(covered) / float64(total) * 100
	return math.Round(value*10000) / 10000
}

func parseNonNegativeInt(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("value %d is negative", parsed)
	}
	return parsed, nil
}
