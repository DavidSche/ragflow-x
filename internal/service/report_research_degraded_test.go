package service

// The writing-time retrieval pool and the research-degradation count (doc/301 §5.8
// items 1–2, T23).
//
// Two disclosures, and both are about *what the writer had*, not about what it wrote:
//
//   - the pool width and whether it was thin against the template's `min_evidence`,
//     frozen next to the body so "this claim is weak" can be answered with "the
//     corpus had three chunks" instead of a guess;
//   - the `fallback_*` plan state, counted so the team can watch §5.8 item 3's
//     threshold (thin/degraded > 20% for two consecutive versions) instead of
//     commissioning planning v2 on a hunch.
//
// The nullable shape is the point of the first half: `NULL` ("this revision never
// recorded it") is not `0` ("the pool was empty"), and a `bool` that reads `false`
// for both "adequate" and "not measured" would turn an absence into a verdict.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestSectionCommitFreezesTheWritingPool drives one real section through the runner
// so the columns are written by the production path, not by a fixture.
func TestSectionCommitFreezesTheWritingPool(t *testing.T) {
	fx, engine := newReportRunFixture(t, "pool-freeze")
	created := fx.outlineJob(t, fx.datasetLink, "req-pool-freeze-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(context.Background(), fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程 [1]。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"住宿标准为每天 600 元。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"审批由部门负责人完成。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	sections, err := fx.svc.Store.ListReportSections(context.Background(), fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatalf("list sections: %v", err)
	}
	written := 0
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded {
			continue
		}
		written++
		// A section that required evidence and could not retrieve still records a
		// pool (possibly 0): "the query matched nothing" is a fact about the corpus.
		if section.RequiresEvidence {
			if section.PoolHitChunks == nil {
				t.Fatalf("section %d required evidence but recorded no pool width", section.Ord)
			}
			if section.PoolInsufficient == nil {
				t.Fatalf("section %d required evidence but recorded no sufficiency flag", section.Ord)
			}
			if section.PoolCandidateCount == nil {
				t.Fatalf("section %d required evidence but recorded no uncited count", section.Ord)
			}
			if *section.PoolCandidateCount > *section.PoolHitChunks {
				t.Fatalf("section %d: %d uncited chunks in a pool of %d", section.Ord,
					*section.PoolCandidateCount, *section.PoolHitChunks)
			}
		} else if section.PoolHitChunks != nil {
			t.Fatalf("section %d required no evidence, so its pool must stay NULL, got %d",
				section.Ord, *section.PoolHitChunks)
		}
	}
	if written == 0 {
		t.Fatal("no section was written, so nothing above was actually asserted")
	}
}

// poolSection builds a section row for the pure derivation test.
func poolSection(ord int, requiresEvidence bool, planning string) model.ReportSection {
	section := model.ReportSection{
		Ord: ord, SectionKey: "s" + string(rune('0'+ord)), Status: model.ReportSectionSucceeded,
		RequiresEvidence: requiresEvidence, ContentMD: "正文",
	}
	if planning != "" {
		section.ResearchPlanJSON = researchPlanJSON(reportResearchPlan{Depth: "deep", Planning: planning})
	}
	return section
}

func TestResearchDegradationIsCountedNotGuessed(t *testing.T) {
	present := map[string]model.ReportSection{
		"s1": poolSection(1, true, reportResearchPlanningPlanned),
		"s2": poolSection(2, true, reportResearchPlanningBudget),
		"s3": poolSection(3, true, reportResearchPlanningPlannerError),
		// A section with no plan at all (a standard-depth run) is neither planned nor
		// degraded: counting it as healthy would inflate the denominator.
		"s4": poolSection(4, true, ""),
		// A failed section is not a research outcome; it is a gap, disclosed elsewhere.
		"s5": func() model.ReportSection {
			section := poolSection(5, true, reportResearchPlanningUnparseable)
			section.Status = model.ReportSectionFailed
			return section
		}(),
	}
	facts := reportFinalizeFacts{
		TemplateKeys: []string{"s1", "s2", "s3", "s4", "s5"},
		EvidenceKeys: map[string]bool{"s1": true, "s2": true, "s3": true, "s4": true, "s5": true},
	}
	metrics := deriveReportQualityMetrics(facts, present, 0)

	if metrics.ResearchPlannedSections != 3 {
		t.Fatalf("planned sections = %d, want 3 (the failed section's plan does not count)",
			metrics.ResearchPlannedSections)
	}
	if metrics.ResearchDegradedSections != 2 {
		t.Fatalf("degraded sections = %d, want 2 (fallback_budget + fallback_planner_error)",
			metrics.ResearchDegradedSections)
	}
}

func TestResearchDegradedTotalsArePooledAcrossRevisions(t *testing.T) {
	// Two revisions, different sizes. The totals must be sums, not an average of two
	// per-revision ratios: §5.8 item 3 asks "how much of the delivered text is
	// degraded", and a mean of ratios answers a question about revisions instead.
	encode := func(planned, degraded int) string {
		t.Helper()
		raw, err := json.Marshal(reportQualityMetrics{
			ResearchPlannedSections: planned, ResearchDegradedSections: degraded,
		})
		if err != nil {
			t.Fatalf("marshal metrics: %v", err)
		}
		return string(raw)
	}
	first, second := encode(4, 1), encode(40, 20)
	var accumulator reportMetricsAccumulator
	for _, raw := range []string{first, second} {
		accumulateReportMetric(&accumulator, &model.ReportRevision{
			Status: model.ReportRevisionPublished, Completeness: model.ReportCompletenessFull,
			MetricsJSON: raw,
		})
	}
	totals := accumulator.view()
	if totals.ResearchPlannedSections != 44 {
		t.Fatalf("pooled planned sections = %d, want 44", totals.ResearchPlannedSections)
	}
	if totals.ResearchDegradedSections != 21 {
		t.Fatalf("pooled degraded sections = %d, want 21", totals.ResearchDegradedSections)
	}
}
