package service

// R17 / R18 contract tests: A54–A57 (doc/295 §5).
//
// The theme of all four is the same: an *enhancement* must never be able to change
// what the report claims. R17 locators may steer an outline but must not reach the
// citation chain; R18's planner may gather more evidence but its own failures must
// not cost a section. Both are asserted structurally — by comparing rows — rather
// than by reading the wording of a prompt.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// seedReportLocatorArtifact writes one `ready` page-index projection for a logical
// dataset. Its payload declares `doc-a`, which is the document the mock engine's
// chunks come from, so the hint is inside the section's own source scope.
func seedReportLocatorArtifact(
	t *testing.T, fx *reportFixture, datasetID, artifactID, title string,
) {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"title": title, "source_document_ids": []string{"doc-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := &model.ArtifactProjection{
		ID: id.New(), TenantID: fx.tenantID, DatasetID: datasetID, RunID: "run-locator",
		ArtifactID: artifactID, Kind: model.CompilationKindPageIndex,
		State: model.ArtifactProjectionReady, ArtifactHash: "hash-" + artifactID,
		ContentHash: "content-" + artifactID, ArtifactJSON: string(payload),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := fx.gdb.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

// runReportToSections creates a report and drives it through outline confirmation
// and one generate slice, returning the written sections, their citations and their
// claims — the three things A54 compares.
func runReportToSections(
	t *testing.T, fx *reportFixture, engine *reportRunEngine, datasetID, key string,
	depth string,
) (CreateReportResult, []model.ReportSection, []model.ReportCitation, []model.ReportClaim) {
	t.Helper()
	ctx := context.Background()
	request := reportCreateRequest(datasetID)
	request.ResearchDepth = depth
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, key, request)
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	fx.runSlice(t, created.Job.ID)
	sections := fx.reportSections(t, created.Revision.ID)
	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := fx.svc.Store.ListReportClaims(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = engine
	return *created, sections, citations, claims
}

// A54: locators are navigation, not evidence.
//
// The same report is generated twice over the same dataset and the same retrieval
// results; the second run additionally has a `ready` page-index projection. If
// locators leaked into the citation chain, the second run would differ in citation
// rows, in `evidence_status`, or in claim counts. It must not: only
// `location_hints_json` may change.
func TestReportLocatorsNeverEnterEvidence(t *testing.T) {
	fx, engine := newReportRunFixture(t, "a54")
	ctx := context.Background()
	datasetID := "link-policy-a54"

	baseline, baselineSections, baselineCitations, baselineClaims :=
		runReportToSections(t, fx, engine, datasetID, "req-a54-0001", "")

	seedReportLocatorArtifact(t, fx, datasetID, "artifact-a54-page-index", "制度目录：差旅与报销")

	withHints, hintedSections, hintedCitations, hintedClaims :=
		runReportToSections(t, fx, engine, datasetID, "req-a54-0002", "")

	// The projection must actually have been used, otherwise the rest of the test
	// proves nothing.
	hintedAny := 0
	for _, section := range hintedSections {
		if strings.TrimSpace(section.LocationHintsJSON) != "" {
			hintedAny++
		}
	}
	if hintedAny == 0 {
		t.Fatal("a ready projection must produce location hints on at least one section")
	}
	for _, section := range baselineSections {
		if strings.TrimSpace(section.LocationHintsJSON) != "" {
			t.Fatalf("the baseline run had no projection and must have no hints: %+v", section)
		}
	}

	// Shape: the locator payload has exactly the four navigation keys, and none of
	// them is a score, a quote or a chunk id.
	var hints []map[string]interface{}
	for _, section := range hintedSections {
		if strings.TrimSpace(section.LocationHintsJSON) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(section.LocationHintsJSON), &hints); err != nil {
			t.Fatalf("location_hints_json must be readable: %v", err)
		}
		break
	}
	if len(hints) == 0 {
		t.Fatal("expected at least one decoded hint")
	}
	allowed := map[string]bool{
		"kind": true, "artifact_id": true, "title": true, "source_document_ids": true,
	}
	for _, hint := range hints {
		for key := range hint {
			if !allowed[key] {
				t.Fatalf("locator carries %q; a locator may only navigate (keys: %v)", key, allowed)
			}
		}
		for _, forbidden := range []string{"score", "similarity", "excerpt", "chunk_id"} {
			if _, ok := hint[forbidden]; ok {
				t.Fatalf("locator must not carry %q: %v", forbidden, hint)
			}
		}
	}

	// The citation chain is byte-identical in *shape*: same number of rows, same
	// per-section distribution, same support statuses.
	if len(hintedCitations) != len(baselineCitations) {
		t.Fatalf("locators changed the citation count: %d with hints vs %d without",
			len(hintedCitations), len(baselineCitations))
	}
	perSection := func(rows []model.ReportCitation) map[int]int {
		counts := map[int]int{}
		for _, row := range rows {
			counts[row.SectionOrd]++
		}
		return counts
	}
	baselineCounts, hintedCounts := perSection(baselineCitations), perSection(hintedCitations)
	for ord, count := range baselineCounts {
		if hintedCounts[ord] != count {
			t.Fatalf("section %d citation count changed: %d → %d", ord, count, hintedCounts[ord])
		}
	}
	if len(hintedClaims) != len(baselineClaims) {
		t.Fatalf("locators changed the claim count: %d with hints vs %d without",
			len(hintedClaims), len(baselineClaims))
	}

	// `evidence_status` is derived from citations + claims + snapshots, so comparing
	// the read view is the end-to-end form of the same assertion.
	baselineView, err := fx.svc.ListReportSections(ctx, fx.owner.ID, fx.tenantID,
		baseline.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	hintedView, err := fx.svc.ListReportSections(ctx, fx.owner.ID, fx.tenantID,
		withHints.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(baselineView) != len(hintedView) {
		t.Fatalf("section count changed: %d vs %d", len(baselineView), len(hintedView))
	}
	for index := range baselineView {
		if baselineView[index].EvidenceStatus != hintedView[index].EvidenceStatus {
			t.Fatalf("evidence_status of section %d changed with hints: %q → %q",
				baselineView[index].Ord, baselineView[index].EvidenceStatus,
				hintedView[index].EvidenceStatus)
		}
		if baselineView[index].CitationCount != hintedView[index].CitationCount {
			t.Fatalf("citation_count of section %d changed with hints: %d → %d",
				baselineView[index].Ord, baselineView[index].CitationCount,
				hintedView[index].CitationCount)
		}
	}
}

// A55: the locator block is in the writer's prompt, is labelled navigation-only, and
// the prompt can never be read as permission to cite a locator.
func TestReportLocatorPromptIsLabelledNavigationOnly(t *testing.T) {
	hint := reportLocationHint{
		Kind: model.CompilationKindPageIndex, ArtifactID: "artifact-1",
		Title: "制度目录：差旅与报销", SourceDocumentIDs: []string{"doc-a"},
	}
	prompt := reportSectionPrompt(
		"本季度差旅报销是否符合差旅制度第三章？",
		model.ReportSection{SectionKey: "compliance", Title: "结论", Ord: 1, RequiresEvidence: true},
		"", 400, nil, []reportEvidenceItem{{
			ChunkID: "chunk-a", DocumentID: "doc-a", DocumentLabel: "制度汇编.md",
			DatasetID: "link-1", Content: "差旅住宿标准为每晚上限 600 元。",
		}}, []reportLocationHint{hint})

	// Search for the *blocks*, not for the words: the locator header itself says
	// "citation from # Evidence", so a bare search would match inside it.
	locatorAt := strings.Index(prompt, "\n\n# Locators")
	evidenceAt := strings.Index(prompt, "\n\n# Evidence")
	if locatorAt < 0 {
		t.Fatalf("a section with hints must carry a locator block:\n%s", prompt)
	}
	if evidenceAt < 0 || locatorAt > evidenceAt {
		t.Fatalf("the locator block must precede # Evidence (locator %d, evidence %d)",
			locatorAt, evidenceAt)
	}
	block := prompt[locatorAt:evidenceAt]
	if !strings.Contains(block, "navigation only") {
		t.Fatalf("the locator block must be labelled navigation-only:\n%s", block)
	}
	if !strings.Contains(block, "never cite") {
		t.Fatalf("the locator block must forbid citation:\n%s", block)
	}
	if !strings.Contains(block, "制度目录：差旅与报销") {
		t.Fatalf("the locator title must reach the prompt:\n%s", block)
	}
	// The hint text is the reader-facing form and must carry no citation marker: the
	// exporters print it verbatim, and a marker there would look like a footnote.
	text := reportLocationHintText(hint)
	if strings.Contains(text, "[") || strings.Contains(text, "]") {
		t.Fatalf("a locator's text must not look like a citation marker: %q", text)
	}
	if label := reportLocatorKindLabel(hint.Kind); label != "页面索引" {
		t.Fatalf("page_index must be labelled 页面索引, got %q", label)
	}
	if label := reportLocatorKindLabel("graph"); label != "" {
		t.Fatalf("an unknown locator kind must have no label, got %q", label)
	}

	// No hints → no block at all (a standard section's prompt must stay unchanged).
	plain := reportSectionPrompt(
		"问题", model.ReportSection{SectionKey: "compliance", Title: "结论", Ord: 1, RequiresEvidence: true},
		"", 400, nil, nil, nil)
	if strings.Contains(plain, "# Locators") {
		t.Fatalf("a section without hints must have no locator block:\n%s", plain)
	}
}

// A56: every way the planner can fail is a degradation, and the section still
// succeeds. This is the assertion that keeps "deep" from becoming "fragile".
func TestReportDeepResearchDegradesWithoutFailingSection(t *testing.T) {
	cases := []struct {
		name     string
		planning string
		// plannerReply is the first chat reply (the planner's); the writer's replies
		// come from the default queue afterwards.
		plannerReply string
		plannerErr   error
		shrinkBudget bool
	}{
		{
			name: "planner call fails", planning: reportResearchPlanningPlannerError,
			plannerErr: errors.New("provider refused the planner"),
		},
		{
			name: "planner answers prose", planning: reportResearchPlanningUnparseable,
			plannerReply: "I would suggest looking at the reimbursement rules.",
		},
		{
			// The engine reports a failed model route *as content*, so the planner looks
			// like it answered — but it answered with an error. That is a failed call, not
			// an unparseable list; recording it as `fallback_unparseable` would blame the
			// parser for a provider outage.
			name: "planner answers an error envelope", planning: reportResearchPlanningPlannerError,
			plannerReply: reportProviderErrorEnvelopeReply,
		},
		{
			name: "budget refuses the planner", planning: reportResearchPlanningBudget,
			shrinkBudget: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fx, engine := newReportRunFixture(t, "a56-"+strings.ReplaceAll(testCase.name, " ", "-"))
			ctx := context.Background()
			datasetID := "link-policy-a56-" + strings.ReplaceAll(testCase.name, " ", "-")

			request := reportCreateRequest(datasetID)
			request.ResearchDepth = model.ReportResearchDepthDeep
			created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID,
				"req-a56-"+strings.ReplaceAll(testCase.name, " ", "-"), request)
			if err != nil {
				t.Fatalf("create deep report: %v", err)
			}
			if created.Revision.ResearchDepth != model.ReportResearchDepthDeep {
				t.Fatalf("revision must freeze deep depth, got %q", created.Revision.ResearchDepth)
			}
			fx.runSlice(t, created.Job.ID)
			if testCase.shrinkBudget {
				// The planner's reserve is fixed at 600 tokens; the writer's is the
				// template's per-section allowance. A section budget below 600 with a
				// small per-section allowance is the real-world shape this fallback
				// exists for: the extra depth is unaffordable, the section is not.
				shrinkReportSectionAllowance(t, fx, *created, 100)
				fx.setEnvelope(t, created.Job.ID, 500)
			}
			if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
				created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
				t.Fatalf("confirm outline: %v", err)
			}
			// Every writer — including the ones whose sections precede the planner —
			// answers with a citation of the first evidence item, so "the section kept
			// its own retrieval" is observable as a citation row.
			engine.mu.Lock()
			engine.replies = []string{reportWriterCitationReply}
			if testCase.plannerErr != nil || testCase.plannerReply != "" {
				plannerErr, plannerReply := testCase.plannerErr, testCase.plannerReply
				// Target the planner only. Every other call — including the writers of
				// sections that precede it — falls through to the normal queue, so the
				// test cannot accidentally fail a section by failing a writer.
				engine.chatHook = func(prompt string) (string, error) {
					if !strings.Contains(prompt, reportPlannerPromptMarker) {
						return "", nil
					}
					if plannerErr != nil {
						return "", plannerErr
					}
					return plannerReply, nil
				}
			}
			engine.mu.Unlock()

			fx.runSlice(t, created.Job.ID)

			sections := fx.reportSections(t, created.Revision.ID)
			withEvidence := 0
			for _, section := range sections {
				if !section.RequiresEvidence {
					continue
				}
				withEvidence++
				if section.Status != model.ReportSectionSucceeded {
					t.Fatalf("a degraded planner must not fail the section: %s is %q (gap %q)",
						section.SectionKey, section.Status, section.GapReason)
				}
				plan := parseReportResearchPlan(section.ResearchPlanJSON)
				if plan == nil {
					t.Fatalf("section %s must disclose its plan even when degraded", section.SectionKey)
				}
				if plan.Depth != model.ReportResearchDepthDeep {
					t.Fatalf("plan depth must record deep, got %q", plan.Depth)
				}
				if plan.Planning != testCase.planning {
					t.Fatalf("plan state must be %q, got %q", testCase.planning, plan.Planning)
				}
				if label := reportResearchPlanningLabel(plan.Planning); label == "" {
					t.Fatalf("state %q must have a reader-facing label", plan.Planning)
				}
				// The writer ran on the section's own retrieval: degradation costs
				// extra queries, never the evidence the section already had.
				citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, citation := range citations {
					if citation.SectionOrd == section.Ord {
						found = true
					}
				}
				if !found {
					t.Fatalf("a degraded deep section must still cite its own retrieval")
				}
			}
			if withEvidence == 0 {
				t.Fatal("the compliance template must have at least one evidence section")
			}
		})
	}
}

// A57: the planner is billed as its own call. "How much did deep cost" must be
// answerable from the ledger, so the planner gets a distinct `call_seq` row rather
// than being folded into the writer's.
func TestReportDeepResearchPlannerIsBilledSeparately(t *testing.T) {
	fx, engine := newReportRunFixture(t, "a57")
	ctx := context.Background()
	datasetID := "link-policy-a57"

	request := reportCreateRequest(datasetID)
	request.ResearchDepth = model.ReportResearchDepthDeep
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-a57-0001", request)
	if err != nil {
		t.Fatal(err)
	}
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatal(err)
	}
	// The planner answers with two queries; every retrieval returns the mock's two
	// chunks, so one of them is a duplicate and one is new.
	engine.mu.Lock()
	engine.chatHook = func(prompt string) (string, error) {
		if !strings.Contains(prompt, reportPlannerPromptMarker) {
			return "", nil
		}
		return `["住宿标准","审批例外"]`, nil
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	var charges []model.ReportCharge
	if err := fx.gdb.Where("tenant_id = ? AND report_job_id = ?", fx.tenantID, created.Job.ID).
		Order("call_seq ASC").Find(&charges).Error; err != nil {
		t.Fatal(err)
	}
	if len(charges) < 2 {
		t.Fatalf("expected a planner row and a writer row, got %d charge rows", len(charges))
	}
	plannerRows := 0
	writerRows := 0
	for _, charge := range charges {
		switch charge.ReservedTokens {
		case reportResearchPlannerReserve:
			plannerRows++
		default:
			writerRows++
		}
	}
	if plannerRows == 0 {
		t.Fatalf("the planner must own a row reserved at %d tokens: %+v",
			reportResearchPlannerReserve, charges)
	}
	if writerRows == 0 {
		t.Fatalf("the writer must keep its own row: %+v", charges)
	}
	// Distinct `call_seq` per section: the ledger key embeds it, so a collision would
	// have been a unique-index failure rather than a silent merge — this asserts the
	// *intent* (separate identities) rather than the constraint.
	seen := map[string]struct{}{}
	for _, charge := range charges {
		if _, ok := seen[charge.LedgerKey]; ok {
			t.Fatalf("ledger key reused: %s", charge.LedgerKey)
		}
		seen[charge.LedgerKey] = struct{}{}
	}

	// The plan that reached the row records what the planner produced and what it
	// added over the section's own retrieval.
	sections := fx.reportSections(t, created.Revision.ID)
	checked := 0
	for _, section := range sections {
		plan := parseReportResearchPlan(section.ResearchPlanJSON)
		if plan == nil || plan.Planning != reportResearchPlanningPlanned {
			continue
		}
		checked++
		if plan.Depth != model.ReportResearchDepthDeep {
			t.Fatalf("planned section must record deep depth, got %q", plan.Depth)
		}
		if len(plan.Queries) != 2 {
			t.Fatalf("both planned queries must be recorded, got %d", len(plan.Queries))
		}
		for _, query := range plan.Queries {
			if query.Query == "" || query.Hits == 0 {
				t.Fatalf("a planned query must record its text and hit count: %+v", query)
			}
		}
		if plan.Merged < 0 || plan.Duplicates < 0 {
			// Both queries return the same two chunks as the section's own retrieval,
			// so the union adds nothing and everything is a duplicate. Asserting the
			// arithmetic (not a specific count) keeps the test about the accounting.
			t.Fatalf("merged/duplicates must be counted, got %d/%d", plan.Merged, plan.Duplicates)
		}
		if plan.Duplicates == 0 {
			t.Fatalf("repeating the same retrieval must register duplicates: %+v", plan)
		}
	}
	if checked == 0 {
		t.Fatal("deep depth must produce at least one planned section")
	}
}

// reportWriterCitationReply is a writer answer that cites the first evidence item of
// whatever section it is used for.
const reportWriterCitationReply = `{"answer":"适用制度为差旅制度第三章。[1]","usage":{"prompt_tokens":50,"completion_tokens":12}}`

// reportPlannerPromptMarker identifies the research planner among the model calls a
// slice makes: it is the planner prompt's own opening sentence, so a test can fail or
// answer exactly that call.
const reportPlannerPromptMarker = "You plan retrieval for one section"

// shrinkReportSectionAllowance lowers the per-section token allowance of the exact
// template version this report pinned — resolved through the same helper the worker
// uses, because the pinned row may be the platform seed rather than a tenant clone.
// The row is immutable through the API on purpose: the test simulates "this template
// was authored with a small allowance" instead of exercising the authoring surface.
func shrinkReportSectionAllowance(
	t *testing.T, fx *reportFixture, created CreateReportResult, allowance int,
) {
	t.Helper()
	row, _, err := fx.svc.reportPinnedTemplate(context.Background(), fx.tenantID, created.Revision)
	if err != nil {
		t.Fatalf("load pinned template: %v", err)
	}
	var limits map[string]interface{}
	if err := json.Unmarshal([]byte(row.LimitsJSON), &limits); err != nil {
		t.Fatalf("template limits are not JSON: %v", err)
	}
	limits["max_tokens_per_section"] = allowance
	encoded, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportTemplate{}).Where("id = ?", row.ID).
		Update("limits_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
}
