package service

// doc/301 T10 ② — `locator_assisted_section_rate`.
//
// The metric exists to answer exactly one question: "was the compiled locator used".
// It must not be able to answer the other one ("did a compiled artifact end up in the
// citation list") — A54 forbids that shape from existing at all. So the two properties
// worth testing are the denominator's definition and the `null ≠ 0` rule:
//
//   - a section that cited nothing is not "unassisted", it is not in the denominator;
//   - a revision where nothing cited anything stores `null`, not `0.0`, so a quiet
//     week cannot be read as "the locator never helped".

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// locatorMetricsFacts builds the minimum a structural derivation needs.
func locatorMetricsFacts(keys ...string) reportFinalizeFacts {
	evidence := make(map[string]bool, len(keys))
	for _, key := range keys {
		evidence[key] = true
	}
	return reportFinalizeFacts{TemplateKeys: keys, EvidenceKeys: evidence}
}

func locatorSection(ord int, citations int, hints string) model.ReportSection {
	return model.ReportSection{
		Ord: ord, SectionKey: "s", Status: model.ReportSectionSucceeded,
		ContentMD: "正文", CitationCount: citations, LocationHintsJSON: hints,
		EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed,
	}
}

func TestLocatorAssistedRateCountsOnlySectionsThatCitedSomething(t *testing.T) {
	present := map[string]model.ReportSection{
		"a": locatorSection(1, 2, `[{"kind":"page_index","title":"第 3 页"}]`),
		"b": locatorSection(2, 1, ``),
		// A section with a hint but no citation had nothing for the locator to assist:
		// counting it would make the rate move when citation quality moved.
		"c": locatorSection(3, 0, `[{"kind":"wiki","title":"制度总览"}]`),
	}
	metrics := deriveReportQualityMetrics(locatorMetricsFacts("a", "b", "c"), present, 0)
	if metrics.WithCitationSections != 2 || metrics.LocatorAssistedSections != 1 {
		t.Fatalf("expected 1/2, got %d/%d",
			metrics.LocatorAssistedSections, metrics.WithCitationSections)
	}
	if metrics.LocatorAssistedSectionRate == nil || *metrics.LocatorAssistedSectionRate != 0.5 {
		t.Fatalf("unexpected rate: %v", metrics.LocatorAssistedSectionRate)
	}
}

func TestLocatorAssistedRateIsNullWhenNothingCited(t *testing.T) {
	present := map[string]model.ReportSection{
		"a": locatorSection(1, 0, `[{"kind":"page_index","title":"第 3 页"}]`),
	}
	metrics := deriveReportQualityMetrics(locatorMetricsFacts("a"), present, 0)
	if metrics.LocatorAssistedSectionRate != nil {
		t.Fatalf("0/0 must store null, got %v", *metrics.LocatorAssistedSectionRate)
	}
	if metrics.WithCitationSections != 0 {
		t.Fatalf("denominator must be reported as 0, got %d", metrics.WithCitationSections)
	}
}

// TestLocatorAssistedRateTreatsAnEmptyHintArrayAsNoHint: `AutoMigrate` writes `[]` for
// a section that was frozen with no locators, and `[]` is not a hint.
func TestLocatorAssistedRateTreatsAnEmptyHintArrayAsNoHint(t *testing.T) {
	present := map[string]model.ReportSection{
		"a": locatorSection(1, 1, `[]`),
		"b": locatorSection(2, 1, `  `),
	}
	metrics := deriveReportQualityMetrics(locatorMetricsFacts("a", "b"), present, 0)
	if metrics.LocatorAssistedSections != 0 {
		t.Fatalf("empty hint payloads must not count, got %d", metrics.LocatorAssistedSections)
	}
	if metrics.LocatorAssistedSectionRate == nil || *metrics.LocatorAssistedSectionRate != 0 {
		t.Fatalf("0/2 is a real zero and must be stored as 0, got %v", metrics.LocatorAssistedSectionRate)
	}
}

// TestMetricsTrendPoolsTheLocatorRate: the rollup pools counts instead of averaging
// per-revision ratios, so a 1-section report cannot outweigh a 20-section one, and the
// denominator stays visible beside the rate.
func TestMetricsTrendPoolsTheLocatorRate(t *testing.T) {
	fx := newReportFixture(t, "metrics-locator")
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)

	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-loc-1", "rev-loc-1",
		model.ReportRevisionPublished, model.ReportCompletenessFull, at,
		`{"structure_completeness":1.0,"citation_coverage":1.0,"quality_flag":"ok",
		  "with_citation_sections":1,"locator_assisted_sections":1}`)
	seedMetricsRevision(t, fx.gdb, fx.tenantID, fx.owner.ID, "rep-loc-2", "rev-loc-2",
		model.ReportRevisionPublished, model.ReportCompletenessFull, at,
		`{"structure_completeness":1.0,"citation_coverage":1.0,"quality_flag":"ok",
		  "with_citation_sections":9,"locator_assisted_sections":0}`)

	totals, err := fx.svc.ReportMetricsTrend(ctx, fx.owner.ID, fx.tenantID, ReportMetricsTrendRequest{Days: 2, View: "own"})
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if totals.Totals.WithCitationSections != 10 || totals.Totals.LocatorAssistedSections != 1 {
		t.Fatalf("expected pooled 1/10, got %d/%d",
			totals.Totals.LocatorAssistedSections, totals.Totals.WithCitationSections)
	}
	if totals.Totals.LocatorAssistedSectionRate == nil || *totals.Totals.LocatorAssistedSectionRate != 0.1 {
		t.Fatalf("expected 0.1, got %v", totals.Totals.LocatorAssistedSectionRate)
	}
	// Averaging the two per-revision ratios would have answered 0.5; that number is
	// what this test exists to keep out.
	raw, err := json.Marshal(totals.Totals)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytesContain(raw, `"locator_assisted_section_rate":0.1`) {
		t.Fatalf("rate must be stored as a pooled ratio: %s", raw)
	}
}

func bytesContain(haystack []byte, needle string) bool {
	return strings.Contains(string(haystack), needle)
}
