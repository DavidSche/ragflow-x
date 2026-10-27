// Package testquality provides machine-verifiable test governance assets.
package testquality

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	statusCovered = "Covered"
	statusPartial = "Partial"
	statusMissing = "Missing"
	statusWaived  = "Waived"
	statusNA      = "N/A"
	waiverNone    = "NONE"
)

var (
	scenarioIDPattern = regexp.MustCompile(`^SC-[A-Z0-9]+(?:-[A-Z0-9]+)*-\d{3}$`)
	allowedRisks      = map[string]bool{"P0": true, "P1": true, "P2": true}
	allowedStatuses   = map[string]bool{
		statusCovered: true,
		statusPartial: true,
		statusMissing: true,
		statusWaived:  true,
		statusNA:      true,
	}
	allowedCIGates = map[string]bool{
		"PR Fast":        true,
		"PR Contract":    true,
		"PR Integration": true,
		"Nightly":        true,
		"Release":        true,
	}
)

// ScenarioTest records one executable test that proves part of a scenario.
type ScenarioTest struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	File    string `json:"file"`
}

// Scenario is one executable test-risk record from the Scenario Catalogue.
type Scenario struct {
	ID                           string         `json:"id"`
	Risk                         string         `json:"risk"`
	Domain                       string         `json:"domain"`
	ContractSource               string         `json:"contractSource"`
	Preconditions                string         `json:"preconditions"`
	Input                        string         `json:"input"`
	ExpectedResult               string         `json:"expectedResult"`
	SecurityInvariant            string         `json:"securityInvariant"`
	StateInvariant               string         `json:"stateInvariant"`
	SideEffects                  string         `json:"sideEffects"`
	Oracle                       string         `json:"oracle"`
	TestLevel                    string         `json:"testLevel"`
	MappedFiles                  []string       `json:"mappedFiles"`
	Tests                        []ScenarioTest `json:"tests"`
	MissingTest                  string         `json:"missingTest"`
	TestOwner                    string         `json:"testOwner"`
	CIGate                       []string       `json:"ciGate"`
	Status                       string         `json:"status"`
	Waiver                       string         `json:"waiver"`
	WaiverExpiry                 string         `json:"waiverExpiry"`
	ApplicableMutationTargets    []string       `json:"applicableMutationTargets"`
	NotApplicableMutationTargets []string       `json:"notApplicableMutationTargets"`
	MachineTrace                 string         `json:"machineTrace"`
}

// ScenarioCatalogue is the root document in testdata/scenario_catalogue.json.
type ScenarioCatalogue struct {
	Version       int        `json:"version"`
	GeneratedAt   string     `json:"generatedAt"`
	Source        string     `json:"source"`
	Scenarios     []Scenario `json:"scenarios"`
	repositoryDir string
}

// LoadScenarioCatalogue reads, decodes, and validates a catalogue.
func LoadScenarioCatalogue(path string) (ScenarioCatalogue, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ScenarioCatalogue{}, fmt.Errorf("read scenario catalogue: %w", err)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))

	var catalogue ScenarioCatalogue
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalogue); err != nil {
		return ScenarioCatalogue{}, fmt.Errorf("decode scenario catalogue %s: %w", path, err)
	}

	if err := catalogue.validate(path); err != nil {
		return ScenarioCatalogue{}, err
	}
	return catalogue, nil
}

// P0Closure returns the raw and effective closure percentages and active risk counts.
func (c ScenarioCatalogue) P0Closure(now time.Time) (raw, effective float64, activeRisk int) {
	covered := c.countP0ByStatus(statusCovered)
	partial := c.countP0ByStatus(statusPartial)
	missing := c.countP0ByStatus(statusMissing)
	activeWaived := c.countActiveP0Waivers(now)

	activeRisk = covered + partial + missing + activeWaived
	if activeRisk == 0 {
		return 1, 1, 0
	}
	raw = float64(covered) / float64(activeRisk)
	effective = float64(covered+activeWaived) / float64(activeRisk)
	return raw, effective, activeRisk
}

func (c ScenarioCatalogue) countP0ByStatus(status string) int {
	count := 0
	for _, scenario := range c.Scenarios {
		if scenario.Risk == "P0" && scenario.Status == status {
			count++
		}
	}
	return count
}

func (c ScenarioCatalogue) countActiveP0Waivers(now time.Time) int {
	count := 0
	for _, scenario := range c.Scenarios {
		if scenario.Risk != "P0" || scenario.Status != statusWaived {
			continue
		}
		expiry, err := time.Parse("2006-01-02", scenario.WaiverExpiry)
		if err == nil && !expiry.Before(now) {
			count++
		}
	}
	return count
}

func (c *ScenarioCatalogue) validate(path string) error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported catalogue version %d", c.Version)
	}
	if strings.TrimSpace(c.GeneratedAt) == "" {
		return fmt.Errorf("generatedAt is required")
	}
	if _, err := time.Parse("2006-01-02", c.GeneratedAt); err != nil {
		return fmt.Errorf("generatedAt must use YYYY-MM-DD: %w", err)
	}
	if strings.TrimSpace(c.Source) == "" {
		return fmt.Errorf("source is required")
	}
	if len(c.Scenarios) == 0 {
		return fmt.Errorf("scenarios must not be empty")
	}

	c.repositoryDir = repositoryRoot(path)
	seenIDs := make(map[string]bool, len(c.Scenarios))
	p0Count := 0
	for index := range c.Scenarios {
		scenario := &c.Scenarios[index]
		if err := c.validateScenario(scenario); err != nil {
			return fmt.Errorf("scenario %d: %w", index+1, err)
		}
		if seenIDs[scenario.ID] {
			return fmt.Errorf("duplicate scenario id %s", scenario.ID)
		}
		seenIDs[scenario.ID] = true
		if scenario.Risk == "P0" {
			p0Count++
		}
	}
	if p0Count == 0 {
		return fmt.Errorf("catalogue must contain at least one P0 scenario")
	}
	return nil
}

func (c *ScenarioCatalogue) validateScenario(scenario *Scenario) error {
	if !scenarioIDPattern.MatchString(scenario.ID) {
		return fmt.Errorf("invalid scenario id %q", scenario.ID)
	}
	if !allowedRisks[scenario.Risk] {
		return fmt.Errorf("%s: risk must be P0, P1, or P2", scenario.ID)
	}
	if strings.TrimSpace(scenario.Domain) == "" || scenario.Domain != strings.ToLower(scenario.Domain) {
		return fmt.Errorf("%s: domain must be a non-empty lowercase identifier", scenario.ID)
	}
	for _, field := range []string{
		scenario.ContractSource,
		scenario.Preconditions,
		scenario.Input,
		scenario.ExpectedResult,
		scenario.SecurityInvariant,
		scenario.StateInvariant,
		scenario.SideEffects,
		scenario.Oracle,
		scenario.TestLevel,
		scenario.MissingTest,
		scenario.TestOwner,
		scenario.MachineTrace,
	} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("%s: required narrative field is empty", scenario.ID)
		}
	}
	if len(scenario.MappedFiles) == 0 {
		return fmt.Errorf("%s: at least one mapped file is required", scenario.ID)
	}
	if len(scenario.CIGate) == 0 {
		return fmt.Errorf("%s: at least one CI gate is required", scenario.ID)
	}
	for _, gate := range scenario.CIGate {
		if !allowedCIGates[gate] {
			return fmt.Errorf("%s: unknown CI gate %q", scenario.ID, gate)
		}
	}
	if err := c.validateFiles(scenario); err != nil {
		return err
	}
	if err := c.validateScenarioStatus(scenario); err != nil {
		return err
	}
	if scenario.Risk == "P0" && (len(scenario.ApplicableMutationTargets) == 0 || len(scenario.NotApplicableMutationTargets) == 0) {
		return fmt.Errorf("%s: P0 requires both applicable and not-applicable mutation targets", scenario.ID)
	}
	if hasDuplicate(scenario.ApplicableMutationTargets) || hasDuplicate(scenario.NotApplicableMutationTargets) {
		return fmt.Errorf("%s: mutation targets must be unique within each list", scenario.ID)
	}
	return c.validateScenarioTrace(scenario)
}

func (c *ScenarioCatalogue) validateFiles(scenario *Scenario) error {
	for _, file := range scenario.MappedFiles {
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("%s: mapped file is empty", scenario.ID)
		}
		if err := c.ensureRepositoryFile(file); err != nil {
			return fmt.Errorf("%s: mapped file %q does not exist: %w", scenario.ID, file, err)
		}
	}
	if strings.TrimSpace(scenario.ContractSource) != "" {
		if err := c.ensureRepositoryFile(scenario.ContractSource); err != nil {
			return fmt.Errorf("%s: contract source %q does not exist: %w", scenario.ID, scenario.ContractSource, err)
		}
	}
	for _, test := range scenario.Tests {
		if strings.TrimSpace(test.Package) == "" || strings.TrimSpace(test.Name) == "" || strings.TrimSpace(test.File) == "" {
			return fmt.Errorf("%s: each test requires package, name, and file", scenario.ID)
		}
		if err := c.ensureRepositoryFile(test.File); err != nil {
			return fmt.Errorf("%s: test file %q does not exist: %w", scenario.ID, test.File, err)
		}
	}
	return nil
}

func (c *ScenarioCatalogue) validateScenarioStatus(scenario *Scenario) error {
	if !allowedStatuses[scenario.Status] {
		return fmt.Errorf("%s: unknown status %q", scenario.ID, scenario.Status)
	}

	switch scenario.Status {
	case statusCovered, statusPartial:
		if len(scenario.Tests) == 0 {
			return fmt.Errorf("%s: %s requires at least one test", scenario.ID, scenario.Status)
		}
	case statusMissing:
		if len(scenario.Tests) != 0 {
			return fmt.Errorf("%s: Missing must not reference executable tests", scenario.ID)
		}
		if strings.TrimSpace(scenario.MissingTest) == "" {
			return fmt.Errorf("%s: Missing requires missingTest", scenario.ID)
		}
	case statusWaived, statusNA:
		if scenario.Status == statusWaived && strings.TrimSpace(scenario.MissingTest) == "" {
			return fmt.Errorf("%s: Waived requires missingTest or alternative evidence", scenario.ID)
		}
	}

	if scenario.Status == statusWaived || scenario.Status == statusNA {
		if scenario.Waiver == waiverNone || scenario.WaiverExpiry == waiverNone {
			return fmt.Errorf("%s: %s requires a waiver and expiry", scenario.ID, scenario.Status)
		}
		expiry, err := time.Parse("2006-01-02", scenario.WaiverExpiry)
		if err != nil {
			return fmt.Errorf("%s: waiverExpiry must use YYYY-MM-DD", scenario.ID)
		}
		if !expiry.After(time.Now()) {
			return fmt.Errorf("%s: waiver expired on %s", scenario.ID, scenario.WaiverExpiry)
		}
		return nil
	}

	if scenario.Waiver != waiverNone || scenario.WaiverExpiry != waiverNone {
		return fmt.Errorf("%s: non-waived status must use NONE waiver metadata", scenario.ID)
	}
	return nil
}

func (c *ScenarioCatalogue) validateScenarioTrace(scenario *Scenario) error {
	if scenario.Status != statusCovered && scenario.Status != statusPartial {
		return nil
	}
	if !strings.Contains(scenario.MachineTrace, scenario.ID) {
		return fmt.Errorf("%s: machineTrace must contain the scenario ID", scenario.ID)
	}
	for _, test := range scenario.Tests {
		fullPath := filepath.Join(c.repositoryDir, filepath.FromSlash(test.File))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("%s: read test metadata: %w", scenario.ID, err)
		}
		source := string(data)
		if !strings.Contains(source, "// ScenarioID: "+scenario.ID) {
			return fmt.Errorf("%s: test file %q is missing ScenarioID metadata", scenario.ID, test.File)
		}
		if strings.EqualFold(filepath.Ext(test.File), ".go") {
			if scenario.Risk == "P0" && !strings.HasPrefix(test.Name, "TestP0_") {
				return fmt.Errorf("%s: P0 test %q must use TestP0_ naming", scenario.ID, test.Name)
			}
			if !strings.Contains(source, "func "+test.Name+"(") {
				return fmt.Errorf("%s: test function %q is missing from %q", scenario.ID, test.Name, test.File)
			}
		} else if !strings.Contains(source, test.Name) {
			return fmt.Errorf("%s: frontend test %q is missing from %q", scenario.ID, test.Name, test.File)
		}
	}
	return nil
}

// RiskCountByFile maps each production file to active P0/P1 scenario counts.
func (c ScenarioCatalogue) RiskCountByFile() map[string]map[string]int {
	counts := map[string]map[string]int{"P0": {}, "P1": {}}
	for _, scenario := range c.Scenarios {
		if scenario.Risk != coverageRiskP0 && scenario.Risk != coverageRiskP1 {
			continue
		}
		for _, file := range scenario.MappedFiles {
			counts[scenario.Risk][file]++
		}
	}
	return counts
}

// ScenarioIDsByFile maps production files to their Catalogue scenario IDs.
func (c ScenarioCatalogue) ScenarioIDsByFile() map[string]map[string]struct{} {
	ids := map[string]map[string]struct{}{}
	for _, scenario := range c.Scenarios {
		if scenario.Risk != coverageRiskP0 && scenario.Risk != coverageRiskP1 {
			continue
		}
		for _, file := range scenario.MappedFiles {
			if ids[file] == nil {
				ids[file] = map[string]struct{}{}
			}
			ids[file][scenario.ID] = struct{}{}
		}
	}
	return ids
}

func (c *ScenarioCatalogue) ensureRepositoryFile(relativePath string) error {
	cleanPath := filepath.ToSlash(filepath.Clean(relativePath))
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
		return fmt.Errorf("path escapes repository")
	}
	fullPath := filepath.Join(c.repositoryDir, filepath.FromSlash(cleanPath))
	info, err := os.Stat(fullPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%w: path is a directory", fs.ErrInvalid)
	}
	return nil
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func repositoryRoot(cataloguePath string) string {
	absolutePath, err := filepath.Abs(cataloguePath)
	if err != nil {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(absolutePath), "..", "..", ".."))
}
