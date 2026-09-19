package testquality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// P0Report is the machine-readable P0 closure snapshot.
type P0Report struct {
	RawClosure       float64 `json:"rawClosure"`
	EffectiveClosure float64 `json:"effectiveClosure"`
	ActiveRisk       int     `json:"activeRisk"`
	Covered          int     `json:"covered"`
	Partial          int     `json:"partial"`
	Missing          int     `json:"missing"`
	ActiveWaived     int     `json:"activeWaived"`
}

// P1Report is the machine-readable P1 closure snapshot.
type P1Report struct {
	RawClosure       float64 `json:"rawClosure"`
	EffectiveClosure float64 `json:"effectiveClosure"`
	ActiveRisk       int     `json:"activeRisk"`
	Covered          int     `json:"covered"`
	Partial          int     `json:"partial"`
	Missing          int     `json:"missing"`
	ActiveWaived     int     `json:"activeWaived"`
}

// ScenarioReport is a compact auditable scenario record.
type ScenarioReport struct {
	ID          string         `json:"id"`
	Risk        string         `json:"risk"`
	Status      string         `json:"status"`
	TestOwner   string         `json:"testOwner"`
	TestLevel   string         `json:"testLevel"`
	CIGate      []string       `json:"ciGate"`
	Tests       []ScenarioTest `json:"tests"`
	MissingTest string         `json:"missingTest"`
}

// CatalogueReport is the CI summary artifact.
type CatalogueReport struct {
	GeneratedAt    string           `json:"generatedAt"`
	Source         string           `json:"source"`
	TotalScenarios int              `json:"totalScenarios"`
	CountsByStatus map[string]int   `json:"countsByStatus"`
	CountsByRisk   map[string]int   `json:"countsByRisk"`
	P0             P0Report         `json:"p0"`
	P1             P1Report         `json:"p1"`
	Scenarios      []ScenarioReport `json:"scenarios"`
}

// BuildReport creates a stable report snapshot for governance.
func (c ScenarioCatalogue) BuildReport(now time.Time) CatalogueReport {
	statusCounts := map[string]int{}
	riskCounts := map[string]int{}
	reports := make([]ScenarioReport, 0, len(c.Scenarios))

	for _, scenario := range c.Scenarios {
		statusCounts[scenario.Status]++
		riskCounts[scenario.Risk]++
		tests := make([]ScenarioTest, 0, len(scenario.Tests))
		tests = append(tests, scenario.Tests...)
		reports = append(reports, ScenarioReport{
			ID:          scenario.ID,
			Risk:        scenario.Risk,
			Status:      scenario.Status,
			TestOwner:   scenario.TestOwner,
			TestLevel:   scenario.TestLevel,
			CIGate:      append([]string(nil), scenario.CIGate...),
			Tests:       tests,
			MissingTest: scenario.MissingTest,
		})
	}

	raw, effective, activeRisk := c.P0Closure(now)
	p1Raw, p1Effective, p1ActiveRisk := c.P1Closure(now)
	return CatalogueReport{
		GeneratedAt:    now.UTC().Format(time.RFC3339),
		Source:         c.Source,
		TotalScenarios: len(c.Scenarios),
		CountsByStatus: statusCounts,
		CountsByRisk:   riskCounts,
		Scenarios:      reports,
		P0: P0Report{
			RawClosure:       raw,
			EffectiveClosure: effective,
			ActiveRisk:       activeRisk,
			Covered:          c.countP0ByStatus(statusCovered),
			Partial:          c.countP0ByStatus(statusPartial),
			Missing:          c.countP0ByStatus(statusMissing),
			ActiveWaived:     c.countActiveP0Waivers(now),
		},
		P1: P1Report{
			RawClosure:       p1Raw,
			EffectiveClosure: p1Effective,
			ActiveRisk:       p1ActiveRisk,
			Covered:          c.countByRiskAndStatus("P1", statusCovered),
			Partial:          c.countByRiskAndStatus("P1", statusPartial),
			Missing:          c.countByRiskAndStatus("P1", statusMissing),
			ActiveWaived:     c.countActiveRiskWaivers("P1", now),
		},
	}
}

// P1Closure returns raw/effective closure and active P1 risk count.
func (c ScenarioCatalogue) P1Closure(now time.Time) (raw, effective float64, activeRisk int) {
	covered := c.countByRiskAndStatus("P1", statusCovered)
	partial := c.countByRiskAndStatus("P1", statusPartial)
	missing := c.countByRiskAndStatus("P1", statusMissing)
	waived := c.countActiveRiskWaivers("P1", now)
	activeRisk = covered + partial + missing + waived
	if activeRisk == 0 {
		return 1, 1, 0
	}
	raw = float64(covered) / float64(activeRisk)
	effective = float64(covered+waived) / float64(activeRisk)
	return raw, effective, activeRisk
}

func (c ScenarioCatalogue) countByRiskAndStatus(risk, status string) int {
	count := 0
	for _, scenario := range c.Scenarios {
		if scenario.Risk == risk && scenario.Status == status {
			count++
		}
	}
	return count
}

func (c ScenarioCatalogue) countActiveRiskWaivers(risk string, now time.Time) int {
	count := 0
	for _, scenario := range c.Scenarios {
		if scenario.Risk != risk || scenario.Status != statusWaived {
			continue
		}
		expiry, err := time.Parse("2006-01-02", scenario.WaiverExpiry)
		if err == nil && !expiry.Before(now) {
			count++
		}
	}
	return count
}

// WriteReport writes the catalogue report as UTF-8 JSON without a BOM.
func (c ScenarioCatalogue) WriteReport(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.BuildReport(now), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
