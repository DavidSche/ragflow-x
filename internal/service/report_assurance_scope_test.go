package service

// Six over-permission cases for the assurance surface (doc/301 §7.5).
//
// The assurance routes are the first report surface that reads *other people's*
// summarised judgments, so the interesting failures are not "viewer can call it" —
// the router already answers that. They are the four shapes below, each of which
// looks correct from one side:
//
//  1. a valid run id from another workspace ("the id exists, so the row must be mine");
//  2. a valid finding id from another workspace;
//  3. a same-tenant colleague who *can* read the report but is not its owner spending
//     the tenant's allowance by triggering a run;
//  4. the same colleague adjudicating a finding, which would let a reader close the
//     problem they are reading about;
//  5. the workbench exposing uncited candidate *content* to a reader who has no
//     dataset permission (the count is fine; the text is corpus access);
//  6. the same for an administrator who holds `read:report` but no dataset grant —
//     proving the masking is not role-based but owner-based (doc/301 §9 反证候选面板).
//
// Cases 5 and 6 assert the same absence from two different principals on purpose:
// "we mask it for them but not for us" is exactly the bug a single-principal test
// cannot see. Case 0 pins the other half of the contract: the *owner* does see the
// frozen excerpts, because they are chunks the owner's own retrieval read while
// writing this revision — a panel that hides them from the author while showing them
// to nobody would report a contrast the author cannot inspect.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// assuranceScopeFixture is one workspace with a published revision, a finished run,
// a finding and a frozen candidate — all written directly, because these tests are
// about answering questions over existing rows, not about producing them.
type assuranceScopeFixture struct {
	*reportFixture
	reportID   string
	revisionID string
	runID      string
	findingID  string
}

func newAssuranceScopeFixture(t *testing.T, name string) *assuranceScopeFixture {
	t.Helper()
	fx := newReportFixture(t, name)
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-scope-"+name, reportCreateRequest(fx.datasetLink))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	now := time.Now().UTC()
	// `CreateReport` already minted revision 1 (in `drafting`). Publishing it rather
	// than inserting a second row keeps the fixture honest: the unique index on
	// (report_id, revision) is the same one production relies on, and a fixture that
	// bypassed it would be testing a shape that cannot exist.
	revision, err := fx.svc.Store.GetReportRevision(ctx, fx.tenantID, created.Report.ID, 1)
	if err != nil || revision == nil {
		t.Fatalf("load created revision: %v", err)
	}
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("id = ?", revision.ID).
		Updates(map[string]interface{}{"status": model.ReportRevisionPublished, "published_at": &now}).Error; err != nil {
		t.Fatalf("publish revision: %v", err)
	}
	run := &model.ReportAssuranceRun{
		ID: "run-scope-" + name, TenantID: fx.tenantID, ReportID: created.Report.ID,
		RevisionID: revision.ID, Revision: 1, Status: model.ReportAssuranceStatusSucceeded,
		Mode: model.ReportAssuranceModeFull, RequestedPolicy: model.ReportAssurancePolicyAdvisory,
		ReleaseEligibility: model.ReportAssuranceEligibilityTier0,
		TriggeredBy:        fx.owner.ID, CreatedAt: now,
	}
	if err := fx.gdb.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	finding := &model.ReportReviewFinding{
		ID: "finding-scope-" + name, TenantID: fx.tenantID, ReportID: created.Report.ID,
		RevisionID: revision.ID, RunID: &run.ID, SectionOrd: 1,
		Source: "semantic", Category: model.ReportFindingQuoteUnverifiable,
		Severity: model.ReportSeverityHigh, Status: model.ReportFindingStatusOpen,
		Title: "引文无法定位", CreatedAt: now, UpdatedAt: now,
	}
	if err := fx.gdb.Create(finding).Error; err != nil {
		t.Fatalf("seed finding: %v", err)
	}
	candidate := &model.ReportConflictCandidate{
		ID: "cand-scope-" + name, TenantID: fx.tenantID, ReportID: created.Report.ID,
		RevisionID: revision.ID, SectionOrd: 1, SectionID: "sec-1",
		ChunkID: "chunk-scope-1", Content: "差旅标准为每天 800 元。", ContentHash: "hash-scope-1",
		PoolRank: 2, CreatedAt: now,
	}
	if err := fx.gdb.Create(candidate).Error; err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	return &assuranceScopeFixture{
		reportFixture: fx, reportID: created.Report.ID, revisionID: revision.ID,
		runID: run.ID, findingID: finding.ID,
	}
}

// colleague mints a second principal in the same workspace holding a role that can
// read *and* execute reports, so neither answer below can be blamed on a missing
// permission: the refusal must come from the ownership rule.
func (fx *assuranceScopeFixture) colleague(t *testing.T, role string) *model.User {
	t.Helper()
	user, err := fx.svc.CreateUser(context.Background(), fx.tenantID, "", CreateUserRequest{
		Username: "assurance-colleague-" + role + "-" + fx.reportID[len(fx.reportID)-6:],
		Password: "secret123", Role: role,
	})
	if err != nil {
		t.Fatalf("create colleague: %v", err)
	}
	return user
}

// otherWorkspace builds a principal in a different tenant, which is what makes a
// guessed id cross-tenant rather than merely unfamiliar.
func (fx *assuranceScopeFixture) otherWorkspace(t *testing.T) (*model.User, string) {
	t.Helper()
	ctx := context.Background()
	tenant, err := fx.svc.CreateTenant(ctx, "assurance-other-"+fx.reportID)
	if err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	user, err := fx.svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "assurance-other-admin-" + tenant.ID[len(tenant.ID)-6:],
		Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	return user, tenant.ID
}

func assuranceErrorCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	he, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("expected a classified httperr, got %T: %v", err, err)
	}
	return he.Code
}

// TestAssuranceRunIdIsNotAddressableAcrossWorkspaces (case 1): a real, valid run id
// from another workspace answers the report layer's not-found, never 200 and never
// 403 — a 403 would confirm the run exists.
//
// The call carries the *caller's* tenant, which is the only shape the HTTP layer
// ever produces (`tenantID` comes from the authenticated principal, never from the
// path). Passing the foreign tenant would instead trip the RBAC gate first and
// answer 403 for the wrong reason.
func TestAssuranceRunIdIsNotAddressableAcrossWorkspaces(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "tenant-run")
	user, otherTenant := fx.otherWorkspace(t)
	ctx := context.Background()

	_, err := fx.svc.GetReportAssuranceRun(ctx, user.ID, otherTenant, fx.reportID, 1, fx.runID)
	if code := assuranceErrorCode(t, err); code != 44109 {
		t.Fatalf("cross-tenant run read: code = %d, err = %v (want 44109)", code, err)
	}
	// Listing is the same answer: the report itself is not addressable.
	if _, err := fx.svc.ListReportAssuranceRuns(ctx, user.ID, otherTenant, fx.reportID, 1, 10); err == nil {
		t.Fatal("cross-tenant run list must fail")
	}
	// And the real owner can still read it, so the assertion above is not passing for
	// a trivially broken fixture.
	if _, err := fx.svc.GetReportAssuranceRun(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1, fx.runID); err != nil {
		t.Fatalf("owner run read must work: %v", err)
	}
}

// TestAssuranceFindingIsNotAdjudicableAcrossWorkspaces (case 2).
func TestAssuranceFindingIsNotAdjudicableAcrossWorkspaces(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "tenant-finding")
	user, otherTenant := fx.otherWorkspace(t)
	ctx := context.Background()

	_, err := fx.svc.UpdateReviewFinding(ctx, user.ID, otherTenant, fx.reportID, 1,
		fx.findingID, model.ReportFindingStatusAcknowledged, "跨租户尝试", "")
	if code := assuranceErrorCode(t, err); code != 44109 {
		t.Fatalf("cross-tenant finding adjudication: code = %d, err = %v", code, err)
	}
	// Reading the run through the owning tenant is what the foreign caller must not
	// be able to do; the assertion above says the refusal came before any row read.
	// The row must be untouched: a rejected adjudication that still wrote is worse
	// than a leak, because the audit trail would show a status nobody chose.
	stored, err := fx.svc.Store.GetReviewFinding(ctx, fx.tenantID, fx.findingID)
	if err != nil || stored == nil {
		t.Fatalf("reload finding: %v", err)
	}
	if stored.Status != model.ReportFindingStatusOpen {
		t.Fatalf("rejected adjudication changed status to %q", stored.Status)
	}
}

// TestAssuranceTriggerIsOwnerOnlyWithinWorkspace (case 3): a colleague holding
// `execute:report` still cannot spend the tenant's allowance on someone else's report.
func TestAssuranceTriggerIsOwnerOnlyWithinWorkspace(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "owner-trigger")
	colleague := fx.colleague(t, model.RoleOperator)
	ctx := context.Background()
	enableAssuranceForScopeTest(t, fx.svc)

	_, err := fx.svc.TriggerReportAssurance(ctx, colleague.ID, fx.tenantID, fx.reportID, 1,
		"scope-trigger-0001", ReportAssuranceTriggerInput{Mode: model.ReportAssuranceModeSemantic})
	if code := assuranceErrorCode(t, err); code != 44109 {
		t.Fatalf("non-owner trigger: code = %d, err = %v (want 44109)", code, err)
	}
}

// TestAssuranceFindingAdjudicationIsOwnerOnlyWithinWorkspace (case 4): the reader who
// is reading the problem must not be the one who closes it.
func TestAssuranceFindingAdjudicationIsOwnerOnlyWithinWorkspace(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "owner-finding")
	colleague := fx.colleague(t, model.RoleOperator)
	ctx := context.Background()

	_, err := fx.svc.UpdateReviewFinding(ctx, colleague.ID, fx.tenantID, fx.reportID, 1,
		fx.findingID, model.ReportFindingStatusDismissed, "不是问题", "")
	if code := assuranceErrorCode(t, err); code != 44109 {
		t.Fatalf("non-owner adjudication: code = %d, err = %v (want 44109)", code, err)
	}
	stored, err := fx.svc.Store.GetReviewFinding(ctx, fx.tenantID, fx.findingID)
	if err != nil || stored == nil {
		t.Fatalf("reload finding: %v", err)
	}
	if stored.Status != model.ReportFindingStatusOpen {
		t.Fatalf("non-owner adjudication changed status to %q", stored.Status)
	}
	// The owner can, which keeps this test from passing on a broken transition table.
	if _, err := fx.svc.UpdateReviewFinding(ctx, fx.owner.ID, fx.tenantID, fx.reportID, 1,
		fx.findingID, model.ReportFindingStatusAcknowledged, "已知，待下一版修复", ""); err != nil {
		t.Fatalf("owner adjudication must work: %v", err)
	}
}

// TestReviewProjectionCarriesCandidateTextOnlyForTheOwner (cases 5, 6 and the owner
// case).
//
// The workbench reports `candidate_count` and `scan_scope` to every reader, and the
// frozen candidate excerpts to exactly one: the report's owner (doc/301 §9
// 反证候选面板 — "owner 可见正文；分享成员只见『另有 N 条未引用候选参与对照』").
//
// Three principals, because each answer has a different cause and a single one of
// them would look like masking:
//
//  1. the owner reads the report and gets the excerpts, with a document label
//     resolved from this revision's own citation rows;
//  2. a same-tenant administrator who was never granted the report cannot read it at
//     all (44109) — the share policy answers before the projection is built, so no
//     candidate field can leak through a path the owner never exercised;
//  3. the same administrator *after* the owner shares the report with the workspace
//     can read it, and still gets the count without the text. If the text appeared
//     for (3), its presence for (1) would have been a coincidence rather than a rule.
//
// In every case the projection must stay free of `chunk_id`, `document_id` and any
// score: a candidate that carries an identifier or a number will be sorted next to a
// citation by some renderer, and a candidate is not evidence (§5.3.1).
func TestReviewProjectionCarriesCandidateTextOnlyForTheOwner(t *testing.T) {
	fx := newAssuranceScopeFixture(t, "candidate-text")
	ctx := context.Background()
	admin := fx.colleague(t, model.RoleTenantAdmin)

	ownerView := readReviewAs(t, fx.svc, "owner", fx.owner.ID, fx.tenantID, fx.reportID)
	if !ownerView.CandidatesVisible {
		t.Fatal("owner: candidates_visible must be true")
	}
	if len(ownerView.Candidates) != 1 {
		t.Fatalf("owner: candidates = %d, want 1", len(ownerView.Candidates))
	}
	candidate := ownerView.Candidates[0]
	if !strings.Contains(candidate.Content, "差旅标准为每天 800 元") {
		t.Fatalf("owner: candidate content missing: %q", candidate.Content)
	}
	if candidate.SectionOrd != 1 || candidate.PoolRank != 2 {
		t.Fatalf("owner: candidate must keep its section and pool rank: %+v", candidate)
	}
	// `document_label` is resolved from the citation rows of the same revision, so a
	// seeded candidate whose document is not cited anywhere legitimately has none.
	if candidate.DocumentLabel != "" {
		t.Fatalf("owner: a document cited nowhere must carry no label: %q", candidate.DocumentLabel)
	}
	assertNoCandidateIdentifiers(t, "owner", ownerView)

	_, err := fx.svc.ReportReview(ctx, admin.ID, fx.tenantID, fx.reportID, 1, "", "", "")
	if code := assuranceErrorCode(t, err); code != 44109 {
		t.Fatalf("admin without a share: code = %d, err = %v (want 44109)", code, err)
	}

	if _, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, fx.reportID, SetReportAccessRequest{
		Policy: model.ReportAccessTenantMembers,
	}); err != nil {
		t.Fatalf("share report with the workspace: %v", err)
	}
	sharedView := readReviewAs(t, fx.svc, "shared-admin", admin.ID, fx.tenantID, fx.reportID)
	if sharedView.CandidatesVisible {
		t.Fatal("shared-admin: candidates_visible must be false")
	}
	if len(sharedView.Candidates) != 0 {
		t.Fatalf("shared-admin: candidates must be empty, got %d", len(sharedView.Candidates))
	}
	if sharedView.CandidateCount != 1 {
		t.Fatalf("shared-admin: candidate_count = %d, want 1 (the count is disclosed)",
			sharedView.CandidateCount)
	}
	if sharedView.ScanScope == "" {
		t.Fatal("shared-admin: scan_scope must be disclosed even without candidate text")
	}
	assertNoCandidateIdentifiers(t, "shared-admin", sharedView)
	raw, err := json.Marshal(sharedView)
	if err != nil {
		t.Fatalf("shared-admin: marshal view: %v", err)
	}
	if strings.Contains(string(raw), "差旅标准为每天 800 元") {
		t.Fatalf("shared-admin: review projection leaked candidate content: %s", raw)
	}
}

// readReviewAs reads the workbench as one principal.
func readReviewAs(
	t *testing.T, svc *Service, name, actorID, tenantID, reportID string,
) *ReportReviewView {
	t.Helper()
	view, err := svc.ReportReview(context.Background(), actorID, tenantID, reportID, 1, "", "", "")
	if err != nil {
		t.Fatalf("%s: review read failed: %v", name, err)
	}
	return view
}

// assertNoCandidateIdentifiers pins the "a candidate is not evidence" shape even for
// the principal who may read the text.
func assertNoCandidateIdentifiers(t *testing.T, name string, view *ReportReviewView) {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("%s: marshal view: %v", name, err)
	}
	for _, forbidden := range []string{
		"chunk-scope-1", // the chunk id
		`"document_id"`,
		`"document_title"`,
		`"candidate_content"`,
		`"retrieval_score"`,
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("%s: review projection carries %q: %s", name, forbidden, raw)
		}
	}
}

// enableAssuranceForScopeTest turns the three settings the trigger path needs on,
// explicitly rather than relying on defaults: a defaulted-on gate would make the
// owner-only assertion untestable the day the default changes.
func enableAssuranceForScopeTest(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatalf("current settings revision: %v", err)
	}
	expected := int64(0)
	if current != nil {
		expected = current.Revision
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: expected,
		Changes: map[string]interface{}{
			"report.assurance_enabled": true,
			"report.assurance_gate":    model.ReportAssurancePolicyAdvisory,
		},
		Note:      "assurance owner-only scope test",
		Confirmed: true,
	}); err != nil {
		t.Fatalf("enable assurance: %v", err)
	}
}
