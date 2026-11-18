package service

// Quality gate verdicts and the metrics that carry them (doc/288 §14.1/§14.2).
//
// A live run published a revision whose stored metrics said
//
//	{"quality_gate_pass_rate":0,"quality_not_checked":4,"quality_flag":"ok"}
//
// and all three statements were about the same report. The ratio said "nothing
// passed", the counter said "nothing was ever judged", the flag said "fine". Every
// one of them was individually defensible and together they were useless: the first
// trend endpoint written against this JSON would have drawn a 0% quality line for a
// report whose gate had never run.
//
// The rule these tests pin: **a number that was never measured is `null`, not 0**,
// and a verdict that was never reached is `not_checked`, not `passed` or `failed`.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func metricsOf(t *testing.T, revisionJSON string) reportQualityMetrics {
	t.Helper()
	var metrics reportQualityMetrics
	if err := json.Unmarshal([]byte(revisionJSON), &metrics); err != nil {
		t.Fatalf("metrics_json must decode: %v (%s)", err, revisionJSON)
	}
	return metrics
}

// TestQualityGateVerdictFollowsTheEvidenceStatus pins the mapping itself. It is the
// single place where "the gate reached a verdict" is decided, so every branch is
// named here instead of being inferred from a generated report.
func TestQualityGateVerdictFollowsTheEvidenceStatus(t *testing.T) {
	cases := []struct {
		evidence string
		want     string
		why      string
	}{
		{model.ReportEvidenceSupported, model.ReportQualityPassed,
			"cited evidence the retriever actually supports"},
		{model.ReportEvidencePartiallySupported, model.ReportQualityNeedsReview,
			"cited fewer chunks than min_evidence: did not meet the template's bar"},
		{model.ReportEvidenceUnsupported, model.ReportQualityNeedsReview,
			"no supported citation: disclosure, never a failure"},
		{model.ReportEvidenceOutdated, model.ReportQualityNeedsReview, "stale snapshot"},
		{model.ReportEvidenceConflicting, model.ReportQualityNeedsReview, "sources disagree"},
		{model.ReportEvidenceNotRequired, model.ReportQualityNotChecked,
			"the template never asked for evidence: no verdict to give"},
	}
	for _, tc := range cases {
		if got := reportSectionQualityStatus(tc.evidence); got != tc.want {
			t.Fatalf("evidence %q must yield quality %q (%s), got %q",
				tc.evidence, tc.want, tc.why, got)
		}
	}
}

// TestQualityGateCountsOnlyJudgedSections is the regression the live metrics exposed:
// `not_checked` sections must leave the denominator instead of counting as failures.
func TestQualityGateCountsOnlyJudgedSections(t *testing.T) {
	fx := newReportFixture(t, "gate")
	seeds := []reportSectionSeed{
		{Key: "background", Status: model.ReportSectionSucceeded},
		{Key: "applicable_rules", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 2, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "compliance", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 3, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "gaps", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidencePartiallySupported, QualityStatus: model.ReportQualityNeedsReview},
		{Key: "remediation", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceNotRequired, QualityStatus: model.ReportQualityNotChecked},
	}
	revision := publishReport(t, fx, seeds)
	metrics := metricsOf(t, revision.MetricsJSON)

	if metrics.QualityJudged != 3 {
		t.Fatalf("only verdicts reached belong in the denominator: judged=%d, want 3 (%s)",
			metrics.QualityJudged, revision.MetricsJSON)
	}
	if metrics.QualityNeedsReview != 1 || metrics.QualityNotChecked != 1 {
		t.Fatalf("verdict counts wrong: needs_review=%d not_checked=%d (%s)",
			metrics.QualityNeedsReview, metrics.QualityNotChecked, revision.MetricsJSON)
	}
	if metrics.QualityGatePassRate == nil {
		t.Fatalf("a judged revision must report a rate, got null (%s)", revision.MetricsJSON)
	}
	if got := *metrics.QualityGatePassRate; got != 0.6667 {
		t.Fatalf("pass rate must be passed/judged = 2/3, got %v (%s)", got, revision.MetricsJSON)
	}
	if passed := int(*metrics.QualityGatePassRate * float64(metrics.QualityJudged)); passed != 2 {
		t.Fatalf("stored rate and counts must agree: rate=%v judged=%d implies %d passed",
			*metrics.QualityGatePassRate, metrics.QualityJudged, passed)
	}
}

// TestQualityGateStoresNullWhenNothingWasJudged is the exact live shape: sections
// exist, none was judged, and the JSON must not claim a 0% pass rate.
func TestQualityGateStoresNullWhenNothingWasJudged(t *testing.T) {
	fx := newReportFixture(t, "gate-null")
	// The live shape: every evidence section cites real chunks (so citation coverage
	// is 1.0 and the flag is `ok`) but none of them carries a verdict — which is what
	// a row written before the gate existed looks like, and exactly the data the live
	// run stored as `rate: 0 / not_checked: 4 / flag: ok`.
	seeds := []reportSectionSeed{
		{Key: "background", Status: model.ReportSectionSucceeded},
		{Key: "applicable_rules", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 2, EvidenceStatus: model.ReportEvidenceSupported},
		{Key: "compliance", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 3, EvidenceStatus: model.ReportEvidenceSupported},
		{Key: "gaps", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported},
		{Key: "remediation", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported},
	}
	revision := publishReport(t, fx, seeds)
	if !strings.Contains(revision.MetricsJSON, `"quality_gate_pass_rate":null`) {
		t.Fatalf("an unjudged revision must store null, not 0.0 (%s)", revision.MetricsJSON)
	}
	metrics := metricsOf(t, revision.MetricsJSON)
	if metrics.QualityGatePassRate != nil || metrics.QualityJudged != 0 {
		t.Fatalf("nothing judged: rate must stay nil and judged 0 (%s)", revision.MetricsJSON)
	}
	if metrics.QualityNotChecked != 4 {
		t.Fatalf("every evidence section must be counted as not_checked, got %d (%s)",
			metrics.QualityNotChecked, revision.MetricsJSON)
	}
	// The flag is about citation coverage, not about the gate verdict (§14.2). Here
	// coverage is 1.0, so the flag is `ok` — and that is precisely why the old stored
	// `0` was unreadable: `ok` next to `0` next to `4 not checked`.
	if metrics.QualityFlag != model.ReportQualityFlagOK {
		t.Fatalf("flag must follow citation coverage only, got %q (%s)",
			metrics.QualityFlag, revision.MetricsJSON)
	}
	if metrics.CitationCoverage != 1 {
		t.Fatalf("fixture must reproduce the live coverage=1 shape, got %v", metrics.CitationCoverage)
	}
}

// TestQualityGateNeverReportsPassRateWithoutAVerdict generalises the two cases above
// into the invariant a trend endpoint can rely on: a non-nil rate implies judged > 0,
// and judged implies the counts add up.
func TestQualityGateNeverReportsPassRateWithoutAVerdict(t *testing.T) {
	fixtures := []struct {
		name  string
		seeds []reportSectionSeed
	}{
		{"mixed", successSeeds()},
		{"all-not-checked", []reportSectionSeed{
			{Key: "background", Status: model.ReportSectionSucceeded},
			{Key: "applicable_rules", Status: model.ReportSectionSucceeded},
			{Key: "compliance", Status: model.ReportSectionSucceeded},
			{Key: "gaps", Status: model.ReportSectionSucceeded},
			{Key: "remediation", Status: model.ReportSectionSucceeded},
		}},
	}
	for index, fixture := range fixtures {
		fx := newReportFixture(t, "gate-invariant-"+string(rune('a'+index)))
		revision := publishReport(t, fx, fixture.seeds)
		metrics := metricsOf(t, revision.MetricsJSON)
		if metrics.QualityGatePassRate != nil && metrics.QualityJudged == 0 {
			t.Fatalf("%s: a rate without a denominator is unreadable (%s)",
				fixture.name, revision.MetricsJSON)
		}
		if metrics.QualityGatePassRate == nil && metrics.QualityJudged != 0 {
			t.Fatalf("%s: judged sections must produce a rate (%s)",
				fixture.name, revision.MetricsJSON)
		}
		if metrics.QualityJudged < metrics.QualityNeedsReview {
			t.Fatalf("%s: needs_review cannot exceed the judged count (%s)",
				fixture.name, revision.MetricsJSON)
		}
	}
}

// TestQualityGatePassRateIsRecordedForPublishedRevision keeps the metrics attached to
// the published row, where the trend query reads them — not only on a job payload that
// retention will delete first.
func TestQualityGatePassRateIsRecordedForPublishedRevision(t *testing.T) {
	fx := newReportFixture(t, "gate-publish")
	revision := publishReport(t, fx, successSeeds())
	if revision.Status != model.ReportRevisionPublished {
		t.Fatalf("fixture must publish, got %q", revision.Status)
	}
	stored := fx.reportRevision(t, revision.ID)
	if stored.PublishedAt == nil {
		t.Fatal("published revision must carry published_at for a day-bucketed trend")
	}
	if stored.CreatedAt.IsZero() || stored.PublishedAt.Before(stored.CreatedAt.Add(-time.Minute)) {
		t.Fatalf("published_at cannot precede created_at: created=%s published=%s",
			stored.CreatedAt, stored.PublishedAt)
	}
	if metrics := metricsOf(t, stored.MetricsJSON); metrics.EvidenceSections == 0 {
		t.Fatal("a published report must record its evidence-section denominator")
	}
}

// TestQualityGateDoesNotGatePublishing is the P0 contract (§11 半交付): the verdict is
// recorded, never enforced. A revision whose every section says `needs_review` is
// still `published`/`full`, because P0 promises disclosure, not a gate.
func TestQualityGateDoesNotGatePublishing(t *testing.T) {
	fx := newReportFixture(t, "gate-no-block")
	seeds := successSeeds()
	for index := range seeds {
		if seeds[index].RequiresEvi {
			seeds[index].QualityStatus = model.ReportQualityNeedsReview
		}
	}
	revision := publishReport(t, fx, seeds)
	if revision.Status != model.ReportRevisionPublished || revision.Completeness != model.ReportCompletenessFull {
		t.Fatalf("needs_review must not block publishing: %+v", revision)
	}
	metrics := metricsOf(t, revision.MetricsJSON)
	if metrics.QualityGatePassRate == nil || *metrics.QualityGatePassRate != 0 {
		t.Fatalf("every judged section flagged: rate must be 0, got %v (%s)",
			metrics.QualityGatePassRate, revision.MetricsJSON)
	}
	// `background` is not part of the evidence set (E in §14.1), so it is outside the
	// gate's denominator entirely — not passed, not flagged, not "not checked": the
	// gate never looked at it.
	if metrics.QualityJudged != 4 || metrics.QualityNotChecked != 0 {
		t.Fatalf("judged sections must all be flagged: judged=%d not_checked=%d (%s)",
			metrics.QualityJudged, metrics.QualityNotChecked, revision.MetricsJSON)
	}
}
