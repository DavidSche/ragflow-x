// ScenarioID: SC-QUALITY-000
package testquality_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/testquality"
)

func TestP0_QUALITY_000_ScenarioCatalogueIntegrity(t *testing.T) {
	catalogue, err := testquality.LoadScenarioCatalogue(filepath.Join("testdata", "scenario_catalogue.json"))
	if err != nil {
		t.Fatalf("scenario catalogue is not machine-verifiable: %v", err)
	}
	if len(catalogue.Scenarios) < 24 {
		t.Fatalf("catalogue contains %d scenarios; expected the initial 24-risk-domain baseline", len(catalogue.Scenarios))
	}

	ids := make(map[string]struct{}, len(catalogue.Scenarios))
	for _, scenario := range catalogue.Scenarios {
		if _, exists := ids[scenario.ID]; exists {
			t.Errorf("duplicate scenario id: %s", scenario.ID)
		}
		ids[scenario.ID] = struct{}{}
		if strings.HasPrefix(scenario.MachineTrace, "TODO") && scenario.Status != "Missing" && scenario.Status != "Waived" && scenario.Status != "N/A" {
			t.Errorf("%s: status %s requires a real test trace", scenario.ID, scenario.Status)
		}
		if (scenario.Risk == "P0" || scenario.Risk == "P1") && (scenario.Status == "Missing" || scenario.Status == "Partial") {
			t.Errorf("%s: %s closure cannot contain %s", scenario.ID, scenario.Risk, scenario.Status)
		}
	}

	rawClosure, effectiveClosure, activeRisk := catalogue.P0Closure(time.Now())
	if activeRisk == 0 {
		t.Fatalf("P0 active risk denominator must not be zero")
	}
	if effectiveClosure < 1 {
		t.Fatalf("P0 effective closure is %.2f, raw closure is %.2f, active risk is %d", effectiveClosure, rawClosure, activeRisk)
	}

	p1RawClosure, p1EffectiveClosure, p1ActiveRisk := catalogue.P1Closure(time.Now())
	if p1ActiveRisk == 0 {
		t.Fatalf("P1 active risk denominator must not be zero")
	}
	if p1EffectiveClosure < 1 {
		t.Fatalf("P1 effective closure is %.2f, raw closure is %.2f, active risk is %d", p1EffectiveClosure, p1RawClosure, p1ActiveRisk)
	}

	report := catalogue.BuildReport(time.Now())
	if report.TotalScenarios != len(catalogue.Scenarios) {
		t.Fatalf("report total %d does not match catalogue total %d", report.TotalScenarios, len(catalogue.Scenarios))
	}
	if report.P0.ActiveRisk != activeRisk {
		t.Fatalf("report P0 active risk %d does not match closure %d", report.P0.ActiveRisk, activeRisk)
	}
	if report.P1.ActiveRisk != p1ActiveRisk {
		t.Fatalf("report P1 active risk %d does not match closure %d", report.P1.ActiveRisk, p1ActiveRisk)
	}
}

func TestLoadScenarioCatalogueRejectsInvalidRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	invalid := []byte(`{"version":2,"generatedAt":"2026-09-09","source":"doc","scenarios":[]}`)
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := testquality.LoadScenarioCatalogue(path); err == nil {
		t.Fatal("expected invalid catalogue to be rejected")
	}
}
