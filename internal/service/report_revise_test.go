package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report regeneration contract (doc/288 §7.10 / §11.4 POST /api/v1/reports/:id/revisions
// and GET /api/v1/reports/:id/revisions/:revision). These tests pin the
// cross-table facts that make "重新生成（同条件）" honest:
//
//   - one regenerate writes revision `current+1` + a fresh `report_generate`
//     job + one `job.queued` event in the same transaction, and leaves the
//     published base untouched;
//   - the new revision copies the base's template (pinned version), dataset
//     bindings and outline confirmation — nothing is re-read from the caller;
//   - the stored idempotency key lives in the `regenerate:` namespace, so a
//     creation key can never replay here and a regeneration key can never
//     collide with the key that minted the report;
//   - refused before the report ever published (44104), while any job is active
//     (44110), while soft-deleted (44109), and for anyone who is not the owner
//     (44109 — no existence oracle).

func regenerateReport(t *testing.T, fx *reportFixture, reportID, key string) (*CreateReportResult, error) {
	t.Helper()
	return fx.svc.RegenerateReport(context.Background(), fx.owner.ID, fx.tenantID, reportID, key)
}

func countRevisions(t *testing.T, fx *reportFixture, reportID string) int64 {
	t.Helper()
	var count int64
	if err := fx.gdb.Model(&model.ReportRevision{}).
		Where("report_id = ?", reportID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRegenerateReportMintsNextRevisionFromPublishedBase(t *testing.T) {
	fx := newReportFixture(t, "regen")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	baseReport, err := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)
	if err != nil || baseReport == nil {
		t.Fatalf("load report: %v", err)
	}

	result, err := regenerateReport(t, fx, baseReport.ID, "req-regen-0001")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if !result.Created {
		t.Fatal("first regeneration must report Created=true")
	}
	if result.Revision == nil || result.Job == nil {
		t.Fatalf("regeneration must return revision+job: %+v", result)
	}
	if result.Revision.Revision != base.Revision+1 {
		t.Fatalf("new revision number must be base+1: got %d, base %d",
			result.Revision.Revision, base.Revision)
	}
	if result.Revision.BasedOnRevision != base.Revision {
		t.Fatalf("new revision must name its published base: got %d want %d",
			result.Revision.BasedOnRevision, base.Revision)
	}
	// "同条件": template (pinned version), datasets and outline confirmation are
	// inherited verbatim, never re-read from the caller or the live template.
	if result.Revision.TemplateKey != base.TemplateKey || result.Revision.TemplateVersion != base.TemplateVersion {
		t.Fatalf("regenerated revision must pin the base template: %+v", result.Revision)
	}
	if result.Revision.DatasetsJSON != base.DatasetsJSON {
		t.Fatalf("regenerated revision must inherit the base dataset bindings")
	}
	if result.Revision.OutlineConfirmation != base.OutlineConfirmation {
		t.Fatalf("regenerated revision must inherit outline confirmation: %q",
			result.Revision.OutlineConfirmation)
	}
	if result.Revision.Status != model.ReportRevisionDraft {
		t.Fatalf("the new revision starts draft, got %q", result.Revision.Status)
	}
	if result.Revision.ReportID != baseReport.ID || result.Revision.TenantID != fx.tenantID {
		t.Fatalf("new revision must belong to the same report: %+v", result.Revision)
	}

	job := result.Job
	if job.JobKind != model.ReportJobKindGenerate || job.Status != model.ReportJobQueued ||
		job.Stage != model.ReportStageOutlining {
		t.Fatalf("regenerated job must be queued/outlining generate: %+v", job)
	}
	if job.ReportID != baseReport.ID || job.RevisionID != result.Revision.ID {
		t.Fatalf("job must bind the new revision: %+v", job)
	}
	if job.ProgressTotal != len(successSeeds()) {
		t.Fatalf("progress denominator must match the pinned template's sections, got %d",
			job.ProgressTotal)
	}
	if job.IdempotencyKey != "regenerate:"+fx.owner.ID+":req-regen-0001" {
		t.Fatalf("regeneration key must live in its own namespace, got %q", job.IdempotencyKey)
	}
	if job.ReservedTokens <= 0 {
		t.Fatalf("regenerated job must reserve a budget, got %d", job.ReservedTokens)
	}

	// Exactly one `job.queued` event naming the base, and one audit entry.
	events, err := fx.svc.Store.ListReportEvents(ctx, fx.tenantID, job.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != reportEventJobQueued {
		t.Fatalf("regeneration must write exactly one job.queued event: %+v", events)
	}
	if !strings.Contains(events[0].PayloadJSON, `"based_on_revision":`+strconv.FormatInt(base.Revision, 10)) {
		t.Fatalf("queued event must name the base revision: %s", events[0].PayloadJSON)
	}
	audit, _, err := fx.svc.Store.ListAudits(ctx, fx.tenantID, 1, 50, repository.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	foundRegen := false
	for _, entry := range audit {
		if entry.Action == "report.job.created" && entry.ResourceID == baseReport.ID &&
			strings.Contains(entry.DetailJSON, `"based_on_revision":`+strconv.FormatInt(base.Revision, 10)) {
			foundRegen = true
		}
	}
	if !foundRegen {
		t.Fatal("regeneration must write a report.job.created audit entry naming the base")
	}

	// The published base must be untouched — it stays readable and downloadable.
	baseAfter, err := fx.svc.Store.GetReportRevision(ctx, fx.tenantID, baseReport.ID, base.Revision)
	if err != nil || baseAfter == nil {
		t.Fatalf("base revision must survive regeneration: %v", err)
	}
	if baseAfter.Status != model.ReportRevisionPublished || baseAfter.PublishedAt == nil {
		t.Fatalf("base must stay published after regeneration: %+v", baseAfter)
	}
}

func TestRegenerateReportSameKeyReplaysNotDuplicates(t *testing.T) {
	fx := newReportFixture(t, "regen-replay")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)

	first, err := regenerateReport(t, fx, report.ID, "req-regen-replay-0001")
	if err != nil {
		t.Fatalf("first regenerate: %v", err)
	}
	second, err := regenerateReport(t, fx, report.ID, "req-regen-replay-0001")
	if err != nil {
		t.Fatalf("same-key replay must succeed, not 409: %v", err)
	}
	if second.Created {
		t.Fatal("replay must report Created=false")
	}
	if second.Revision.ID != first.Revision.ID || second.Job.ID != first.Job.ID {
		t.Fatalf("same key must answer the same aggregate: %q vs %q",
			first.Job.ID, second.Job.ID)
	}
	// A replay must not have minted a third revision.
	if got := countRevisions(t, fx, report.ID); got != 2 {
		t.Fatalf("publish(1) + regenerate(1) + replay(0) must leave 2 revisions, got %d", got)
	}
}

// publishRegeneration drives a freshly regenerated run to a published revision
// through the real finalize path, exactly like publishReport does for creation.
// The regenerated revision needs its sections seeded before finalize (the outline
// step of a real run would have done that).
func publishRegeneration(t *testing.T, fx *reportFixture, result *CreateReportResult) {
	t.Helper()
	fx.seedSections(t, result.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, result.Job.ID, runtimeID)
	ctx := context.Background()
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, result.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatalf("begin regeneration finalization: %v", err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, result.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatalf("finalize regeneration: %v", err)
	}
	revision := fx.reportRevision(t, result.Revision.ID)
	if revision.Status != model.ReportRevisionPublished {
		t.Fatalf("regenerated revision must publish: %s", revision.Status)
	}
}

func TestRegenerateReportDifferentKeyMintsNextRevision(t *testing.T) {
	fx := newReportFixture(t, "regen-next")
	base := publishReport(t, fx, successSeeds())

	// Regenerate is serialized while a run is active (44110), so the second
	// regeneration is only legal after the first regenerated run has published —
	// and then it bases on the NEW current revision.
	a, err := regenerateReport(t, fx, base.ReportID, "req-regen-a-0001")
	if err != nil {
		t.Fatalf("regenerate A: %v", err)
	}
	publishRegeneration(t, fx, a)

	b, err := regenerateReport(t, fx, base.ReportID, "req-regen-b-0001")
	if err != nil {
		t.Fatalf("second regeneration after the first published must succeed: %v", err)
	}
	if b.Revision.Revision != base.Revision+2 {
		t.Fatalf("second regenerate must mint revision base+2: got %d want %d",
			b.Revision.Revision, base.Revision+2)
	}
	if b.Revision.BasedOnRevision != a.Revision.Revision {
		t.Fatalf("second regenerate bases on the NEW current revision: got %d want %d",
			b.Revision.BasedOnRevision, a.Revision.Revision)
	}
}

func TestRegenerateReportRefusesUnpublishedBase(t *testing.T) {
	fx := newReportFixture(t, "regen-unpub")
	created := fx.createReport(t, fx.datasetLink, "req-regen-unpub-0001")

	_, err := regenerateReport(t, fx, created.Report.ID, "req-regen-unpub-0002")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportInvalidTransition || httpErr.Status != 409 {
		t.Fatalf("regenerating an unpublished report must refuse 409/%d, got %v",
			codeReportInvalidTransition, err)
	}
	// Nothing may be written by the refused run: still revision 1 only.
	if got := countRevisions(t, fx, created.Report.ID); got != 1 {
		t.Fatalf("refused regeneration must not mint a revision, got %d", got)
	}
}

func TestRegenerateReportRefusesActiveJob(t *testing.T) {
	fx := newReportFixture(t, "regen-active")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)

	// The first regeneration leaves a queued job holding the single active run.
	if _, err := regenerateReport(t, fx, report.ID, "req-regen-active-0001"); err != nil {
		t.Fatalf("first regenerate: %v", err)
	}
	_, err := regenerateReport(t, fx, report.ID, "req-regen-active-0002")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportActiveJobConflict || httpErr.Status != 409 {
		t.Fatalf("regeneration while a job is active must refuse 409/%d, got %v",
			codeReportActiveJobConflict, err)
	}
	if got := countRevisions(t, fx, report.ID); got != 2 {
		t.Fatalf("conflicted regeneration must not mint a third revision, got %d", got)
	}
}

func TestRegenerateReportRefusesSoftDeleted(t *testing.T) {
	fx := newReportFixture(t, "regen-deleted")
	base := publishReport(t, fx, successSeeds())

	now := time.Now().UTC()
	if err := fx.gdb.Model(&model.Report{}).Where("id = ?", base.ReportID).
		Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}

	_, err := regenerateReport(t, fx, base.ReportID, "req-regen-deleted-0001")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("regenerating a soft-deleted report must refuse 404/44109, got %v", err)
	}
}

func TestRegenerateReportRefusesForeignTenant(t *testing.T) {
	foreign := newReportFixture(t, "regen-foreign")
	ownerFx := newReportFixture(t, "regen-owner")
	ctx := context.Background()
	base := publishReport(t, ownerFx, successSeeds())

	// A foreign workspace's user must get the same answer as "no such report":
	// 404/44109, not 403 and not an existence oracle.
	_, err := foreign.svc.RegenerateReport(ctx, foreign.owner.ID, foreign.tenantID, base.ReportID, "req-regen-foreign-0001")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("cross-tenant regeneration must refuse 404/44109, got %v", err)
	}
}

func TestRegenerateReportRequiresIdempotencyKey(t *testing.T) {
	fx := newReportFixture(t, "regen-key")
	base := publishReport(t, fx, successSeeds())

	if _, err := fx.svc.RegenerateReport(context.Background(), fx.owner.ID, fx.tenantID, base.ReportID, "   "); err == nil {
		t.Fatal("missing regeneration key must be refused")
	}
}

func TestGetReportRevisionByNumberReadsOwnRevision(t *testing.T) {
	fx := newReportFixture(t, "rev-read")
	ctx := context.Background()
	base := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, base.ReportID)

	detail, err := fx.svc.GetReportRevisionByNumber(ctx, fx.owner.ID, fx.tenantID, report.ID, base.Revision)
	if err != nil {
		t.Fatalf("read own revision: %v", err)
	}
	if detail.Revision == nil || detail.Revision.ID != base.ID {
		t.Fatalf("must return the requested revision: %+v", detail.Revision)
	}
	if detail.Report == nil || detail.Report.ID != report.ID {
		t.Fatalf("must carry the report: %+v", detail.Report)
	}

	// A number that never existed is 404/44108 (the report exists — this is not
	// an existence oracle).
	_, err = fx.svc.GetReportRevisionByNumber(ctx, fx.owner.ID, fx.tenantID, report.ID, 99)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportRevisionNotFound || httpErr.Status != 404 {
		t.Fatalf("reading a missing revision must refuse 404/44108, got %v", err)
	}
}

func TestGetReportRevisionByNumberOwnerScoped(t *testing.T) {
	foreign := newReportFixture(t, "rev-foreign")
	ownerFx := newReportFixture(t, "rev-owner")
	ctx := context.Background()
	base := publishReport(t, ownerFx, successSeeds())

	_, err := foreign.svc.GetReportRevisionByNumber(ctx, foreign.owner.ID, foreign.tenantID, base.ReportID, base.Revision)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("cross-tenant revision read must refuse 404/44109, got %v", err)
	}
}
