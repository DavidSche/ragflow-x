package testquality

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	DefaultScenarioCataloguePath  = "internal/testquality/testdata/scenario_catalogue.json"
	mutationEvidenceNegativeProof = "negative_proof"
	mutationEvidenceMutationRun   = "mutation_run"
	mutationEvidenceMinSample     = 5
	mutationEvidenceMaxSample     = 10
)

// MutationEvidenceRecord is one machine-readable Mutation Thinking record.
type MutationEvidenceRecord struct {
	ScenarioID       string `json:"scenarioId"`
	TestPackage      string `json:"testPackage,omitempty"`
	TestName         string `json:"testName"`
	EvidenceType     string `json:"evidenceType"`
	Target           string `json:"target"`
	Mutation         string `json:"mutation"`
	ExpectedFailure  string `json:"expectedFailure"`
	ActualResult     string `json:"actualResult"`
	MutationSurvived bool   `json:"mutationSurvived"`
	Action           string `json:"action,omitempty"`
	ReviewedAt       string `json:"reviewedAt"`
}

// MutationEvidenceManifest is the monthly P0 sample ledger.
type MutationEvidenceManifest struct {
	Version      int                      `json:"version"`
	GeneratedAt  string                   `json:"generatedAt"`
	NextReviewAt string                   `json:"nextReviewAt"`
	Records      []MutationEvidenceRecord `json:"records"`
}

// MutationEvidenceReport summarizes manifest validity and follow-up status.
type MutationEvidenceReport struct {
	Total        int                      `json:"total"`
	Valid        int                      `json:"valid"`
	Invalid      int                      `json:"invalid"`
	Survived     int                      `json:"survived"`
	GeneratedAt  string                   `json:"generatedAt"`
	NextReviewAt string                   `json:"nextReviewAt"`
	Records      []MutationEvidenceRecord `json:"records"`
	Errors       []string                 `json:"errors,omitempty"`
	Passed       bool                     `json:"passed"`
}

// LoadMutationEvidence reads and validates the mutation sample against a catalogue.
func LoadMutationEvidence(path, cataloguePath string, now time.Time) (MutationEvidenceReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MutationEvidenceReport{}, fmt.Errorf("read mutation evidence: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	var manifest MutationEvidenceManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return MutationEvidenceReport{}, fmt.Errorf("decode mutation evidence: %w", err)
	}
	catalogue, err := LoadScenarioCatalogue(cataloguePath)
	if err != nil {
		return MutationEvidenceReport{}, fmt.Errorf("load scenario catalogue: %w", err)
	}
	return ValidateMutationEvidenceWithCatalogue(manifest, catalogue, now)
}

// ValidateMutationEvidence enforces catalogue-aligned P0 sampling rules.
func ValidateMutationEvidence(manifest MutationEvidenceManifest, now time.Time) (MutationEvidenceReport, error) {
	catalogue, err := LoadScenarioCatalogue(DefaultScenarioCataloguePath)
	if err != nil {
		return MutationEvidenceReport{}, fmt.Errorf("load scenario catalogue: %w", err)
	}
	return ValidateMutationEvidenceWithCatalogue(manifest, catalogue, now)
}

// ValidateMutationEvidenceWithCatalogue supports package tests and embedded governance flows.
func ValidateMutationEvidenceWithCatalogue(manifest MutationEvidenceManifest, catalogue ScenarioCatalogue, now time.Time) (MutationEvidenceReport, error) {
	report := MutationEvidenceReport{
		Total:        len(manifest.Records),
		GeneratedAt:  manifest.GeneratedAt,
		NextReviewAt: manifest.NextReviewAt,
		Records:      append([]MutationEvidenceRecord(nil), manifest.Records...),
	}
	fail := func(format string, args ...any) (MutationEvidenceReport, error) {
		return report, fmt.Errorf(format, args...)
	}
	if manifest.Version != 1 {
		return fail("mutation evidence version must be 1")
	}
	generatedAt, err := time.Parse(time.RFC3339, manifest.GeneratedAt)
	if err != nil {
		return fail("mutation evidence generatedAt must use RFC3339: %w", err)
	}
	nextReviewAt, err := time.Parse(time.RFC3339, manifest.NextReviewAt)
	if err != nil {
		return fail("mutation evidence nextReviewAt must use RFC3339: %w", err)
	}
	if !nextReviewAt.After(generatedAt) {
		return fail("mutation evidence nextReviewAt must be after generatedAt")
	}
	if len(manifest.Records) < mutationEvidenceMinSample || len(manifest.Records) > mutationEvidenceMaxSample {
		return fail("mutation evidence sample must contain %d-%d records", mutationEvidenceMinSample, mutationEvidenceMaxSample)
	}
	if nextReviewAt.Before(now) {
		report.Errors = append(report.Errors, fmt.Sprintf("mutation evidence review overdue at %s", nextReviewAt.UTC().Format(time.RFC3339)))
	}

	scenarios := make(map[string]Scenario, len(catalogue.Scenarios))
	for _, scenario := range catalogue.Scenarios {
		scenarios[scenario.ID] = scenario
	}
	seen := make(map[string]struct{}, len(manifest.Records))
	for index, record := range manifest.Records {
		validateErr := validateMutationRecord(scenarios, record)
		if validateErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("record %d: %v", index+1, validateErr))
			continue
		}
		key := record.ScenarioID + "\x00" + record.Target
		if _, exists := seen[key]; exists {
			report.Errors = append(report.Errors, fmt.Sprintf("record %d: duplicate scenario/target %s/%s", index+1, record.ScenarioID, record.Target))
			continue
		}
		seen[key] = struct{}{}
		report.Valid++
		if record.MutationSurvived {
			report.Survived++
		}
	}
	report.Invalid = report.Total - report.Valid
	report.Passed = report.Invalid == 0 && len(report.Errors) == 0
	if !report.Passed {
		return report, fmt.Errorf("%s", strings.Join(report.Errors, "; "))
	}
	return report, nil
}

func validateMutationRecord(scenarios map[string]Scenario, record MutationEvidenceRecord) error {
	scenario, exists := scenarios[record.ScenarioID]
	if !exists {
		return fmt.Errorf("unknown scenario %s", record.ScenarioID)
	}
	if scenario.Risk != coverageRiskP0 {
		return fmt.Errorf("mutation sample must select P0 scenarios, got %s", scenario.Risk)
	}
	if !slices.Contains(scenario.ApplicableMutationTargets, record.Target) {
		return fmt.Errorf("target %q is not applicable to %s", record.Target, record.ScenarioID)
	}
	if slices.Contains(scenario.NotApplicableMutationTargets, record.Target) {
		return fmt.Errorf("target %q is explicitly not applicable to %s", record.Target, record.ScenarioID)
	}
	if record.EvidenceType != mutationEvidenceNegativeProof && record.EvidenceType != mutationEvidenceMutationRun {
		return fmt.Errorf("evidenceType must be %s or %s", mutationEvidenceNegativeProof, mutationEvidenceMutationRun)
	}
	if record.TestName == "" {
		return fmt.Errorf("testName is required")
	}
	found := false
	for _, test := range scenario.Tests {
		if test.Name == record.TestName && (record.TestPackage == "" || test.Package == record.TestPackage) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("test %s is not registered with %s", record.TestName, record.ScenarioID)
	}
	if strings.TrimSpace(record.Mutation) == "" {
		return fmt.Errorf("mutation is required")
	}
	if strings.TrimSpace(record.ExpectedFailure) == "" {
		return fmt.Errorf("expectedFailure is required")
	}
	if strings.TrimSpace(record.ActualResult) == "" {
		return fmt.Errorf("actualResult is required")
	}
	if _, err := time.Parse(time.RFC3339, record.ReviewedAt); err != nil {
		return fmt.Errorf("reviewedAt must use RFC3339: %w", err)
	}
	if record.MutationSurvived && strings.TrimSpace(record.Action) == "" {
		return fmt.Errorf("survived mutation requires action")
	}
	return nil
}
