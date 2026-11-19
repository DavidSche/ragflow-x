package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// Section reuse across revisions (doc/288 §7.4 / §9.8 / R14).
//
// The contract these tests pin:
//
//   - the plan is frozen on the revision row at creation time, so a retried slice
//     cannot inherit a different set than the caller asked for;
//   - reuse is a real copy — own section row, own citation rows, own claim rows
//     pointing at the *same* evidence snapshots — never a shared pointer;
//   - reuse is droppable. A target that is no longer pending, or a citation whose
//     snapshot expired, declines the inheritance instead of producing a report whose
//     appendix cannot be exported. `applied = 0` is a normal answer.

func TestNormalizeReportReuseSectionsValidatesAgainstTheBaseOutline(t *testing.T) {
	base := []model.ReportSection{
		{Ord: 1, Status: model.ReportSectionSucceeded},
		{Ord: 2, Status: model.ReportSectionSucceeded},
		{Ord: 3, Status: model.ReportSectionFailed},
	}
	cases := []struct {
		name string
		ords []int
		want []int
	}{
		{"nothing requested", nil, nil},
		{"kept in outline order and deduped", []int{2, 1, 2}, []int{1, 2}},
	}
	for _, tc := range cases {
		got, err := normalizeReportReuseSections(tc.ords, base)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, got)
		}
		for index := range tc.want {
			if got[index] != tc.want[index] {
				t.Fatalf("%s: want %v, got %v", tc.name, tc.want, got)
			}
		}
	}

	// Refusals, not silent no-ops: the caller asked for something specific, and
	// quietly rewriting that section anyway would look like reuse worked.
	refused := [][]int{
		{0},  // ords are 1-based
		{-1}, // negative
		{9},  // the base never had a ninth section
		{3},  // the base had one, but never delivered it
		make([]int, reportReuseMaxSections+1),
	}
	for _, ords := range refused {
		if _, err := normalizeReportReuseSections(ords, base); err == nil {
			t.Fatalf("ords %v must be refused", ords)
		} else if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Code != codeReportOutlineInvalid {
			t.Fatalf("ords %v: want 400/%d, got %v", ords, codeReportOutlineInvalid, err)
		}
	}
}

func TestReportReusePlanRoundTripsAndToleratesGarbage(t *testing.T) {
	encoded, err := reportReusePlanJSON([]int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != `{"sections":[1,3]}` {
		t.Fatalf("unexpected plan encoding: %s", encoded)
	}
	if plan := parseReportReusePlan(encoded); len(plan.Sections) != 2 || plan.Sections[1] != 3 {
		t.Fatalf("plan must round-trip: %+v", plan)
	}
	// No plan stores the empty string, so "not reused" reads back as absent rather
	// than as an empty list to be distinguished from a missing column.
	if encoded, err := reportReusePlanJSON(nil); err != nil || encoded != "" {
		t.Fatalf("an empty plan must store nothing: %q / %v", encoded, err)
	}
	// An unreadable plan degrades to "nothing to reuse": writing every section is
	// always a correct, if more expensive, answer.
	if plan := parseReportReusePlan("{not json"); len(plan.Sections) != 0 {
		t.Fatalf("garbage must degrade to an empty plan: %+v", plan)
	}
}

func TestRegenerateReportFreezesTheReusePlanOnTheRevision(t *testing.T) {
	fx := newReportFixture(t, "reuse-plan")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	baseReport, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || baseReport == nil {
		t.Fatalf("load report: %v", err)
	}

	// A plan that names a section the base never delivered is a bad request, and it
	// must not leave a half-created revision behind.
	if _, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, "req-reuse-plan-0001",
		RegenerateReportRequest{ReuseSections: []int{9}}); err == nil {
		t.Fatal("an unknown ord must be refused")
	} else if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Code != codeReportOutlineInvalid {
		t.Fatalf("want 400/%d, got %v", codeReportOutlineInvalid, err)
	}
	if got := countRevisions(t, fx, baseReport.ID); got != 1 {
		t.Fatalf("the refused request must not mint a revision: %d revisions", got)
	}

	result, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, "req-reuse-plan-0002",
		RegenerateReportRequest{ReuseSections: []int{2}})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	// Frozen on the revision row: a retried slice cannot inherit a different set
	// than the one the caller asked for.
	if result.Revision.ReusePlanJSON != `{"sections":[2]}` {
		t.Fatalf("the plan must be frozen at creation: %q", result.Revision.ReusePlanJSON)
	}
	if got := countRevisions(t, fx, baseReport.ID); got != 2 {
		t.Fatalf("want two revisions, got %d", got)
	}
}

// TestApplyReportSectionReuseCopiesBodyCitationsAndClaims is the happy path: the
// target becomes `succeeded` with the base's wording, carries its own citation and
// claim rows, and points back at the version it was written for.
func TestApplyReportSectionReuseCopiesBodyCitationsAndClaims(t *testing.T) {
	fx := newReportFixture(t, "reuse-apply")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	seedCitations(t, fx, base, successSeeds())
	seedClaims(t, fx, base)
	baseReport, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || baseReport == nil {
		t.Fatalf("load report: %v", err)
	}
	baseSections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, base.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, "req-reuse-apply-0001",
		RegenerateReportRequest{ReuseSections: []int{2}})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	revision := fx.reportRevision(t, result.Revision.ID)
	// The outline step creates every section as `pending`; that is what reuse fills
	// in, so the test creates the same rows the runner would.
	fx.seedSectionsWithStatus(t, revision.ID, successSeeds(), model.ReportSectionPending)
	sections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := fx.svc.applyReportSectionReuse(ctx, fx.tenantID, result.Job.ID, "writing",
		revision, sections, time.Now().UTC())
	if err != nil {
		t.Fatalf("apply reuse: %v", err)
	}
	if applied != 1 {
		t.Fatalf("one section was planned, %d applied", applied)
	}

	after, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var (
		reused  *model.ReportSection
		pending []model.ReportSection
	)
	for index := range after {
		if after[index].Ord == 2 {
			reused = &after[index]
			continue
		}
		pending = append(pending, after[index])
	}
	if reused == nil {
		t.Fatal("section 2 must exist")
	}
	var source model.ReportSection
	for _, section := range baseSections {
		if section.Ord == 2 {
			source = section
		}
	}
	if reused.Status != model.ReportSectionSucceeded {
		t.Fatalf("the reused section must be written, got %s", reused.Status)
	}
	if reused.RewriteReason != model.ReportRewriteReasonReuse {
		t.Fatalf("the reason is what tells a reader this was carried forward, got %q", reused.RewriteReason)
	}
	if reused.ContentMD != source.ContentMD {
		t.Fatalf("the body must be the base's: want %q, got %q", source.ContentMD, reused.ContentMD)
	}
	if reused.BasedOnSectionVersion != source.SectionVersion {
		t.Fatalf("reuse must name the version it came from: want %d, got %d",
			source.SectionVersion, reused.BasedOnSectionVersion)
	}
	if reused.CitationCount != source.CitationCount {
		t.Fatalf("citation count must match the copied rows: want %d, got %d",
			source.CitationCount, reused.CitationCount)
	}
	// Only the planned section is touched: reuse is a per-section decision, not a
	// switch that short-circuits the run.
	for _, section := range pending {
		if section.Status != model.ReportSectionPending {
			t.Fatalf("section %d was not planned but became %s", section.Ord, section.Status)
		}
	}

	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(citations) != source.CitationCount {
		t.Fatalf("want %d copied citations, got %d", source.CitationCount, len(citations))
	}
	baseCitations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]struct{}, len(baseCitations))
	for _, citation := range baseCitations {
		snapshots[citation.EvidenceSnapshotID] = struct{}{}
	}
	for _, citation := range citations {
		// A snapshot is an immutable observation of a source: two revisions citing
		// the same chunk must share it, or the appendix doubles.
		if _, ok := snapshots[citation.EvidenceSnapshotID]; !ok {
			t.Fatalf("copied citation must point at the base's snapshot: %q", citation.EvidenceSnapshotID)
		}
		if citation.SectionID != reused.ID || citation.SectionOrd != reused.Ord {
			t.Fatalf("copied citation must belong to the new section: %+v", citation)
		}
	}

	claims, err := fx.svc.Store.ListReportClaims(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != len(sourceClaimOrds(t, fx, ctx, base.ID)) {
		t.Fatalf("claims must be copied with the body: got %d", len(claims))
	}
	claimIDs := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		claimIDs[claim.ID] = struct{}{}
		if claim.SectionID != reused.ID {
			t.Fatalf("copied claim must belong to the new section: %+v", claim)
		}
	}
	for _, citation := range citations {
		if citation.ClaimID == "" {
			continue
		}
		// A citation pointing at the *base* revision's claim would name a row this
		// reader cannot open.
		if _, ok := claimIDs[citation.ClaimID]; !ok {
			t.Fatalf("citation claim_id must be remapped onto this revision: %q", citation.ClaimID)
		}
	}

	// The job's progress moved by exactly one section.
	job := fx.reportJob(t, result.Job.ID)
	if job.ProgressDone != 1 {
		t.Fatalf("reuse must advance progress like a write does, got %d", job.ProgressDone)
	}

	// The read surface hands a reader both facts in one response: which sections were
	// carried forward (R14) and the sentences they assert with their verdicts (R11).
	// Markers are re-derived from the stored text, because claim rows keep no marker
	// column — a reader must not get a claim list whose references are unrecoverable.
	views, err := fx.svc.ListReportSections(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, revision.Revision)
	if err != nil {
		t.Fatalf("list sections: %v", err)
	}
	var reusedView *ReportSectionView
	for index := range views {
		if views[index].Ord == 2 {
			reusedView = &views[index]
		}
	}
	if reusedView == nil {
		t.Fatal("the reused section must be readable")
	}
	if reusedView.RewriteReason != model.ReportRewriteReasonReuse {
		t.Fatalf("the reader must be told this body was carried over, got %q",
			reusedView.RewriteReason)
	}
	if len(reusedView.Claims) == 0 {
		t.Fatal("a section whose claims were copied must list them")
	}
	for _, claim := range reusedView.Claims {
		if claim.Text == "" || claim.Type == "" || claim.SupportStatus == "" {
			t.Fatalf("a claim view carries text, type and verdict: %+v", claim)
		}
	}
	markerSeen := false
	for _, claim := range reusedView.Claims {
		if len(claim.Markers) > 0 {
			markerSeen = true
		}
	}
	if !markerSeen {
		t.Fatalf("markers must be recovered from the claim text: %+v", reusedView.Claims)
	}
}

// TestApplyReportSectionReuseDeclinesWhenTheTargetIsNoLongerPending: a retried slice
// runs after the section was written, and filling it in again is not a reuse.
func TestApplyReportSectionReuseDeclinesWhenTheTargetIsNoLongerPending(t *testing.T) {
	fx := newReportFixture(t, "reuse-race")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	seedCitations(t, fx, base, successSeeds())
	baseReport, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || baseReport == nil {
		t.Fatalf("load report: %v", err)
	}
	result, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, "req-reuse-race-0001",
		RegenerateReportRequest{ReuseSections: []int{2}})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	revision := fx.reportRevision(t, result.Revision.ID)
	fx.seedSectionsWithStatus(t, revision.ID, successSeeds(), model.ReportSectionSucceeded)
	sections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := fx.svc.applyReportSectionReuse(ctx, fx.tenantID, result.Job.ID, "writing",
		revision, sections, time.Now().UTC())
	if err != nil {
		t.Fatalf("declining must not be an error: %v", err)
	}
	if applied != 0 {
		t.Fatalf("a written section cannot be reused again, applied %d", applied)
	}
}

// TestApplyReportSectionReuseDeclinesWhenTheEvidenceExpired is the case that matters:
// inheriting a body whose snapshots are gone produces a report whose appendix cannot
// be exported (44116). The copy is refused so the caller writes the section again.
func TestApplyReportSectionReuseDeclinesWhenTheEvidenceExpired(t *testing.T) {
	fx := newReportFixture(t, "reuse-expired")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	seedCitations(t, fx, base, successSeeds())
	baseReport, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || baseReport == nil {
		t.Fatalf("load report: %v", err)
	}
	result, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, baseReport.ID, "req-reuse-exp-0001",
		RegenerateReportRequest{ReuseSections: []int{2}})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	revision := fx.reportRevision(t, result.Revision.ID)
	fx.seedSectionsWithStatus(t, revision.ID, successSeeds(), model.ReportSectionPending)

	// Retention ran between the two revisions.
	if err := fx.gdb.Where("tenant_id = ?", fx.tenantID).
		Delete(&model.ReportEvidenceSnapshot{}).Error; err != nil {
		t.Fatal(err)
	}

	sections, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := fx.svc.applyReportSectionReuse(ctx, fx.tenantID, result.Job.ID, "writing",
		revision, sections, time.Now().UTC())
	if err != nil {
		t.Fatalf("declining must not be an error: %v", err)
	}
	if applied != 0 {
		t.Fatalf("a body whose snapshot expired must not be inherited, applied %d", applied)
	}
	after, err := fx.svc.Store.ListReportSections(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range after {
		if section.Status != model.ReportSectionPending {
			t.Fatalf("section %d must stay pending for a normal write, got %s", section.Ord, section.Status)
		}
	}
	if citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, revision.ID); err != nil {
		t.Fatal(err)
	} else if len(citations) != 0 {
		t.Fatalf("a declined reuse must not copy citations, got %d", len(citations))
	}
}

// seedClaims writes one claim per section ord 2, mirroring what `commitSection` does
// for a written body (§9.2).
func seedClaims(t *testing.T, fx *reportFixture, revision *model.ReportRevision) {
	t.Helper()
	sections, err := fx.svc.Store.ListReportSections(context.Background(), fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range sections {
		if section.Ord != 2 {
			continue
		}
		for ord := 1; ord <= 2; ord++ {
			claim := &model.ReportClaim{
				ID: id.New(), TenantID: fx.tenantID, RevisionID: revision.ID,
				SectionID: section.ID, SectionOrd: section.Ord, SectionVersion: section.SectionVersion,
				// With a marker, because a claim without one is a *different*
				// disclosure ("（未引用证据）") and the point here is that the
				// reference survives the copy and the read.
				Ord: ord, ClaimText: "结论句 " + section.SectionKey + "[1]",
				ClaimType: model.ReportClaimTypeFact, SupportStatus: model.ReportClaimSupportSupported,
			}
			if err := fx.gdb.Create(claim).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

func sourceClaimOrds(t *testing.T, fx *reportFixture, ctx context.Context, revisionID string) []int {
	t.Helper()
	claims, err := fx.svc.Store.ListReportClaims(ctx, fx.tenantID, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	ords := make([]int, 0, len(claims))
	for _, claim := range claims {
		ords = append(ords, claim.Ord)
	}
	return ords
}

// seedSectionsWithStatus writes the fixture's sections under a chosen status, which
// is how a test stands in for the outline step (`pending`) or for a finished run.
func (fx *reportFixture) seedSectionsWithStatus(
	t *testing.T, revisionID string, seeds []reportSectionSeed, status string,
) {
	t.Helper()
	for ord, seed := range seeds {
		row := &model.ReportSection{
			ID: id.New(), TenantID: fx.tenantID, RevisionID: revisionID, Ord: ord + 1,
			SectionKey: seed.Key, SectionVersion: 2, Title: seed.Key,
			RequiresEvidence: seed.RequiresEvi, Status: status,
			QualityStatus: model.ReportQualityNotChecked, EvidenceStatus: seed.EvidenceStatus,
			CitationCount: seed.CitationCount,
			ContentMD:     "新写：" + seed.Key, ContentHash: "new-hash-" + seed.Key,
		}
		if err := fx.gdb.Create(row).Error; err != nil {
			t.Fatalf("seed section %s: %v", seed.Key, err)
		}
	}
}

func TestReportRewriteReasonReuseIsADistinctWord(t *testing.T) {
	// The reader distinguishes "written for this version" from "carried forward", so
	// the marker must not collide with the other reasons.
	for _, reason := range []string{
		model.ReportRewriteReasonUserEdit, model.ReportRewriteReasonRetry,
		model.ReportRewriteReasonQualityFix, model.ReportRewriteReasonRegenerate,
	} {
		if strings.EqualFold(reason, model.ReportRewriteReasonReuse) {
			t.Fatalf("reuse must not share a word with %q", reason)
		}
	}
}
