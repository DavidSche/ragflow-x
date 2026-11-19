package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// Report creation contract (doc/288 §11.4 / §7.6 / §8.4). These tests exercise the
// real service against a real migrated schema, because the invariants they pin are
// cross-table facts, not unit behaviour:
//
//   - one POST writes report + revision 1 + job + exactly one `job.queued` event;
//   - the four clocks are frozen from the policy at creation time;
//   - `Idempotency-Key` is request identity, stored as `<user_id>:<client key>`;
//   - capability `blocked` refuses the run, and refusal leaves no rows behind;
//   - `outline_confirmation=auto` is refused while P0-A only implements `required`.

// reportTestEngine is the mock engine with one difference: datasets report a bound
// embedding model. The plain mock never does, which would make every run
// `blocked` and would test the gate instead of the creation path.
type reportTestEngine struct {
	ragflow.Mock
}

func (e *reportTestEngine) GetDatasetConfig(ctx context.Context, datasetID string) (*ragflow.DatasetConfig, error) {
	cfg, err := e.Mock.GetDatasetConfig(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	cfg.EmbeddingModel = "text-embedding-3-small"
	return cfg, nil
}

type reportFixture struct {
	svc      *Service
	engine   *reportTestEngine
	tenantID string
	owner    *model.User
	// datasetLink is the link-policy-* id created by newReportFixture; createReport
	// resolves it through the capability probe, so it must match the seeded name.
	datasetLink string
	// gdb is for assertions that must see a column the service hides (and for
	// simulating a row written by an older release).
	gdb *gorm.DB
}

// newReportFixture builds one workspace with an operator (holds `execute:chat`)
// and one retrievable dataset.
func newReportFixture(t *testing.T, name string) *reportFixture {
	t.Helper()
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: testSQLiteDSN(t, name+".db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	engine := &reportTestEngine{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), engine, jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	svc.RAGFlow = engine

	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "报告工作区 "+name)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "report-owner-" + name, Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureBuiltinReportTemplates(ctx, model.PlatformTenantID); err != nil {
		t.Fatal(err)
	}
	seedReportDataset(t, ctx, svc, engine, tenant.ID, "policy-"+name)
	setReportRetention(t, svc)
	return &reportFixture{svc: svc, engine: engine, tenantID: tenant.ID, owner: owner, datasetLink: "link-policy-" + name, gdb: gdb}
}

// seedReportDataset creates one engine dataset, links it to the tenant and marks
// its single document as parsed with chunks, which is what "retrievable" means to
// the capability probe.
func seedReportDataset(t *testing.T, ctx context.Context, svc *Service, engine *reportTestEngine, tenantID, name string) string {
	t.Helper()
	dataset, err := engine.Mock.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := engine.Mock.CreateDocument(ctx, dataset.ID, &ragflow.DocumentUpload{
		Name: "制度汇编.md", Content: []byte("# 差旅制度\n\n差旅标准见第三章。"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Mock.SetDocumentExecutionState(ctx, dataset.ID, doc.ID, "parsed", 3, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	link := &model.DatasetLink{
		ID: "link-" + name, TenantID: tenantID, RAGFlowDatasetID: dataset.ID,
		Name: name, DocumentCount: 1,
	}
	if err := svc.Store.CreateDatasetLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	return link.ID
}

func setReportRetention(t *testing.T, svc *Service) {
	t.Helper()
	svc.SetRetentionPolicy(config.Retention{
		Enabled: true, AuditDays: 180, UsageDays: 90, FeedbackDays: 180, JobDays: 30,
		ReportDays: 90, ReportEvidenceDays: 90, ReportArtifactDays: 30,
		ReportEventDays: 7, ReportCleanupHoldDays: 7,
	})
}

func reportCreateRequest(datasetID string) CreateReportRequest {
	return CreateReportRequest{
		Title: "季度差旅合规检查", Question: "本季度差旅报销是否符合差旅制度第三章？",
		TemplateKey: reportComplianceTemplateKey,
		Datasets:    []CapabilityCheckDataset{{Role: scenarioDatasetRolePrimary, DatasetID: datasetID}},
	}
}

func TestCreateReportWritesAggregateAndFreezesClocks(t *testing.T) {
	fx := newReportFixture(t, "create")
	ctx := context.Background()
	datasetID := "link-policy-create"

	before := time.Now().UTC()
	result, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-0001-abcdef", reportCreateRequest(datasetID))
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	if !result.Created {
		t.Fatal("first creation must report Created=true")
	}
	report, revision, job := result.Report, result.Revision, result.Job
	if report.TenantID != fx.tenantID || report.OwnerUserID != fx.owner.ID {
		t.Fatalf("report identity wrong: %+v", report)
	}
	if report.AccessPolicy != model.ReportAccessOwnerOnly || report.Status != model.ReportStatusActive {
		t.Fatalf("P0-A defaults wrong: policy=%q status=%q", report.AccessPolicy, report.Status)
	}
	if report.CurrentRevisionID != revision.ID {
		t.Fatalf("current_revision_id must point at revision 1: %q vs %q", report.CurrentRevisionID, revision.ID)
	}
	if revision.Revision != 1 || revision.Status != model.ReportRevisionDraft {
		t.Fatalf("revision must be v1 draft: %+v", revision)
	}
	if revision.TemplateKey != reportComplianceTemplateKey || revision.TemplateVersion != 1 {
		t.Fatalf("revision must pin the template version: %+v", revision)
	}
	if revision.OutlineConfirmation != model.ReportOutlineRequired {
		t.Fatalf("P0-A must write outline_confirmation=required, got %q", revision.OutlineConfirmation)
	}
	if job.Status != model.ReportJobRunning || job.Stage != model.ReportStageOutlining {
		t.Fatalf("creation must arm the run (running/outlining): %+v", job)
	}
	if job.CurrentRuntimeJobID == "" {
		// A created report whose first slice was never dispatched is a job that
		// stays `queued` forever with no error attached — the exact silent dead end
		// doc/288 §8.2 forbids.
		t.Fatal("creation must claim the first runtime job")
	}
	if job.StartedAt == nil {
		t.Fatal("an armed job must carry its start clock")
	}
	if job.IdempotencyKey != fx.owner.ID+":req-0001-abcdef" {
		t.Fatalf("idempotency key must be namespaced by user, got %q", job.IdempotencyKey)
	}

	// Clocks: report 90 days, event 7 days, both relative to creation.
	wantReport := reportExpiry(before, 90, reportDefaultTTLDays)
	if delta := report.ExpiresAt.Sub(wantReport); delta < -time.Minute || delta > time.Minute {
		t.Fatalf("report clock off by %v (got %v want ~%v)", delta, report.ExpiresAt, wantReport)
	}
	events, err := fx.svc.Store.ListReportEvents(ctx, fx.tenantID, job.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("creation must write exactly one event, got %d", len(events))
	}
	if events[0].Type != reportEventJobQueued || events[0].Seq != 1 {
		t.Fatalf("first event must be job.queued seq=1: %+v", events[0])
	}
	wantEvent := reportExpiry(before, 7, reportDefaultEventDays)
	if delta := events[0].ExpiresAt.Sub(wantEvent); delta < -time.Minute || delta > time.Minute {
		t.Fatalf("event clock off by %v", delta)
	}

	// The audit trail is part of the deliverable: a compliance report must be
	// findable in the audit log from the first second.
	audit, _, err := fx.svc.Store.ListAudits(ctx, fx.tenantID, 1, 50, repository.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if entry.Action == "report.job.created" && entry.ResourceID == report.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("creation must write a report.job.created audit entry")
	}
}

func TestCreateReportIdempotencyKeyReplaysSameAggregate(t *testing.T) {
	fx := newReportFixture(t, "replay")
	ctx := context.Background()
	req := reportCreateRequest("link-policy-replay")

	first, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-replay-0001", req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-replay-0001", req)
	if err != nil {
		t.Fatalf("replay must succeed, not 409: %v", err)
	}
	if second.Created {
		t.Fatal("replay must report Created=false")
	}
	if second.Report.ID != first.Report.ID || second.Job.ID != first.Job.ID {
		t.Fatalf("replay returned a different aggregate: %q/%q vs %q/%q",
			second.Report.ID, second.Job.ID, first.Report.ID, first.Job.ID)
	}
	// The replayed aggregate describes *this job's* run. Answering with the report's
	// newest revision would silently hand a later regenerate's revision to a caller
	// replaying the original request.
	if second.Revision == nil || second.Revision.ID != first.Revision.ID {
		t.Fatalf("replay must return the job's own revision %q, got %+v",
			first.Revision.ID, second.Revision)
	}
	reports, err := fx.svc.Store.ListReports(ctx, fx.tenantID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("replay must not create a second report, got %d", len(reports))
	}
}

// A38: the key is the only dedup key. An identical question is not.
//
// "Same question = same report" is the tempting shortcut, and it is wrong twice:
// a user legitimately wants a fresh run of the same question after the data moved,
// and it would let one user's key-respecting retry collide with another request
// that merely happens to ask the same thing. So two different keys must produce two
// independent reports even when the question hash, template and datasets match
// byte for byte.
func TestCreateReportDifferentKeysWithTheSameQuestionMakeTwoReports(t *testing.T) {
	fx := newReportFixture(t, "same-question")
	ctx := context.Background()
	req := reportCreateRequest("link-policy-same-question")

	first, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-same-question-0001", req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-same-question-0002", req)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if !second.Created {
		t.Fatal("a distinct key must be a real creation, not a replay")
	}
	if second.Report.ID == first.Report.ID || second.Job.ID == first.Job.ID {
		t.Fatalf("distinct keys must not share an aggregate: %q/%q vs %q/%q",
			first.Report.ID, first.Job.ID, second.Report.ID, second.Job.ID)
	}
	reports, err := fx.svc.Store.ListReports(ctx, fx.tenantID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("the same question under two keys must leave two reports, got %d", len(reports))
	}
	// Each report must own the revision its own job produced, so a later regenerate
	// on one of them can never be answered as the other's.
	if first.Revision == nil || second.Revision == nil {
		t.Fatalf("each creation must return its revision: %+v / %+v", first.Revision, second.Revision)
	}
	if first.Revision.ID == second.Revision.ID {
		t.Fatalf("two reports must not share a revision, both got %q", first.Revision.ID)
	}
	if second.Revision.ReportID != second.Report.ID {
		t.Fatalf("revision %q does not belong to report %q", second.Revision.ID, second.Report.ID)
	}
}

func TestCreateReportRequiresUsableIdempotencyKey(t *testing.T) {
	fx := newReportFixture(t, "key")
	ctx := context.Background()
	req := reportCreateRequest("link-policy-key")

	for _, key := range []string{"", "   ", "short", "has space here", "with/slash/segments"} {
		_, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, key, req)
		if err == nil {
			t.Fatalf("key %q must be refused", key)
		}
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) {
			t.Fatalf("key %q: want *httperr.Error, got %T", key, err)
		}
		if httpErr.Code != codeReportIdempotencyKeyRequired || httpErr.Status != 400 {
			t.Fatalf("key %q: want 400/%d, got %d/%d",
				key, codeReportIdempotencyKeyRequired, httpErr.Status, httpErr.Code)
		}
	}
	reports, err := fx.svc.Store.ListReports(ctx, fx.tenantID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatalf("a refused key must leave no report behind, got %d", len(reports))
	}
}

func TestCreateReportUnknownTemplateIsTemplateNotFound(t *testing.T) {
	fx := newReportFixture(t, "template")
	ctx := context.Background()
	req := reportCreateRequest("link-policy-template")
	req.TemplateKey = "report-does-not-exist"

	_, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-template-0001", req)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportTemplateNotFound || httpErr.Status != 404 {
		t.Fatalf("want 404/%d, got %v", codeReportTemplateNotFound, err)
	}
}

// A10 has two halves and both must refuse: a dataset with no embedding model, and
// a dataset whose documents are still being parsed. Either one starts a billed job
// that can only produce empty sections.
func TestCreateReportRefusesBlockedCapabilityWithoutWriting(t *testing.T) {
	fx := newReportFixture(t, "blocked")
	ctx := context.Background()

	// Case 1: no embedding model bound. The plain mock never binds one.
	plain := ragflow.NewMock()
	unbound, err := plain.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "unbound"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-unbound", TenantID: fx.tenantID, RAGFlowDatasetID: unbound.ID, Name: "unbound",
	}); err != nil {
		t.Fatal(err)
	}

	// Case 2: embedding bound, document still pending parse.
	pending, err := fx.engine.Mock.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := fx.engine.Mock.CreateDocument(ctx, pending.ID, &ragflow.DocumentUpload{
		Name: "未解析.md", Content: []byte("# 待解析"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.engine.Mock.SetDocumentExecutionState(ctx, pending.ID, doc.ID, "pending", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-pending", TenantID: fx.tenantID, RAGFlowDatasetID: pending.ID,
		Name: "pending", DocumentCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	for _, datasetID := range []string{"link-unbound", "link-pending"} {
		svc := fx.svc
		if datasetID == "link-unbound" {
			svc.RAGFlow = plain
		} else {
			svc.RAGFlow = fx.engine
		}
		_, err := svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-blocked-"+datasetID,
			reportCreateRequest(datasetID))
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Code != codeReportCapabilityBlocked || httpErr.Status != 400 {
			t.Fatalf("%s: want 400/%d, got %v", datasetID, codeReportCapabilityBlocked, err)
		}
	}
	reports, err := fx.svc.Store.ListReports(ctx, fx.tenantID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatalf("a blocked creation must not persist a report, got %d", len(reports))
	}
}

func TestCreateReportRefusesAutoOutlineWhileP0A(t *testing.T) {
	fx := newReportFixture(t, "outline")
	ctx := context.Background()
	req := reportCreateRequest("link-policy-outline")
	req.OutlineConfirmation = model.ReportOutlineAuto

	_, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-outline-0001", req)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportInvalidTransition || httpErr.Status != 409 {
		t.Fatalf("want 409/%d, got %v", codeReportInvalidTransition, err)
	}
}

// A70: `outline_confirmation=auto` needs two independent consents, and a refusal is
// never a silent downgrade.
//
//  1. asking for `auto` against a template that says `required` is a 409 — the server
//     will not decide for the owner, and it will not decide against the template;
//  2. a workspace version may grant the permission, but only by naming who may use it
//     (`auto_confirm_allowed_roles`), because permission without a role list hands it
//     to everyone;
//  3. when the workspace policy is `approval`, the same request is refused *and*
//     audited — an operator who switched governance on needs to see owners hitting it;
//  4. with template + policy both consenting, the revision freezes `auto` on the row.
func TestReportAutoOutlineNeedsBothTemplateAndWorkspaceConsent(t *testing.T) {
	fx := newReportFixture(t, "outline")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	autoRequest := func() CreateReportRequest {
		req := reportCreateRequest("link-policy-outline")
		req.OutlineConfirmation = model.ReportOutlineAuto
		return req
	}

	// The platform baseline says `required`.
	_, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-outline-0001", autoRequest())
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportInvalidTransition || httpErr.Status != 409 {
		t.Fatalf("want 409/%d, got %v", codeReportInvalidTransition, err)
	}

	// A workspace version may permit it — but not without naming the roles.
	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "合规检查（自动）")
	payload.OutlineConfirmation = model.ReportOutlineAuto
	if _, err := fx.svc.PublishReportTemplate(ctx, admin.ID, fx.tenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	}); err == nil {
		t.Fatal("auto without auto_confirm_allowed_roles must be rejected")
	}
	payload.AutoConfirmAllowedRoles = []string{model.RoleTenantAdmin}
	if _, err := fx.svc.PublishReportTemplate(ctx, admin.ID, fx.tenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	}); err != nil {
		t.Fatalf("publish an auto template: %v", err)
	}

	// Workspace policy `approval` is a governance stop: refused, audited, not downgraded.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		AutoConfirmPolicy: model.ReportAutoConfirmApproval,
	}); err != nil {
		t.Fatalf("set auto-confirm policy: %v", err)
	}
	_, err = fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-outline-0002", autoRequest())
	if !errors.As(err, &httpErr) || httpErr.Status != 409 {
		t.Fatalf("a governed workspace must refuse auto, got %v", err)
	}
	refusals := fx.auditRows(t, "report.outline.auto_refused")
	if len(refusals) != 1 {
		t.Fatalf("the refusal must be audited once, got %d", len(refusals))
	}
	if refusals[0].Result != "DENIED" {
		t.Fatalf("a refused governance question is a DENIED row, got %q", refusals[0].Result)
	}

	// Both consents present: the revision freezes `auto`, so a later policy flip cannot
	// reinterpret a report that is already running.
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		AutoConfirmPolicy: model.ReportAutoConfirmSelf,
	}); err != nil {
		t.Fatalf("reset auto-confirm policy: %v", err)
	}
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-outline-0003", autoRequest())
	if err != nil {
		t.Fatalf("an owner asking for a permission the template grants must succeed: %v", err)
	}
	if created.Revision.OutlineConfirmation != model.ReportOutlineAuto {
		t.Fatalf("the revision must freeze the chosen mode, got %q", created.Revision.OutlineConfirmation)
	}
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		AutoConfirmPolicy: model.ReportAutoConfirmApproval,
	}); err != nil {
		t.Fatalf("flip auto-confirm policy: %v", err)
	}
	if row := fx.reportRevision(t, created.Revision.ID); row.OutlineConfirmation != model.ReportOutlineAuto {
		t.Fatalf("a policy flip must not rewrite a frozen revision, got %q", row.OutlineConfirmation)
	}
}

func TestSoftDeleteReportKeepsTTLAndSecondDeleteIsNotFound(t *testing.T) {
	fx := newReportFixture(t, "delete")
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-delete-0001",
		reportCreateRequest("link-policy-delete"))
	if err != nil {
		t.Fatal(err)
	}
	frozenTTL := created.Report.ExpiresAt

	if err := fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, "季度归档", false); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// The row is read straight from the table: a soft-deleted report is no longer
	// reachable through Store.GetReport (that is the whole point of the delete), but
	// the recovery window and the frozen TTL are facts on the row itself.
	stored := loadReportRow(t, fx, created.Report.ID)
	if stored.DeletedAt == nil {
		t.Fatal("soft delete must stamp deleted_at")
	}
	if stored.CleanupHoldUntil == nil {
		t.Fatal("soft delete must open a recovery window")
	}
	if delta := stored.CleanupHoldUntil.Sub(*stored.DeletedAt) - 7*24*time.Hour; delta < -time.Minute || delta > time.Minute {
		t.Fatalf("recovery window must be the configured 7 days, got %v", stored.CleanupHoldUntil.Sub(*stored.DeletedAt))
	}
	if !stored.ExpiresAt.Equal(frozenTTL) {
		// The recovery window is a grace period on a deleted row, never a TTL
		// extension: extending expires_at silently would resurrect the report.
		t.Fatalf("soft delete must not move expires_at: %v -> %v", frozenTTL, stored.ExpiresAt)
	}

	// A second delete is a no-op, not a fresh 7-day window.
	err = fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, "重复删除", false)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied {
		t.Fatalf("second delete must answer 404/%d, got %v", codeReportAccessDenied, err)
	}
	after := loadReportRow(t, fx, created.Report.ID)
	if !after.CleanupHoldUntil.Equal(*stored.CleanupHoldUntil) {
		t.Fatalf("second delete must not extend the window: %v -> %v", stored.CleanupHoldUntil, after.CleanupHoldUntil)
	}
}

// loadReportRow reads a report row including soft-deleted ones. The read surface
// deliberately hides a deleted report, so a test that asserts the recovery window
// has to bypass it.
func loadReportRow(t *testing.T, fx *reportFixture, reportID string) model.Report {
	t.Helper()
	var report model.Report
	if err := fx.gdb.Where("tenant_id = ? AND id = ?", fx.tenantID, reportID).
		First(&report).Error; err != nil {
		t.Fatalf("load report row %s: %v", reportID, err)
	}
	return report
}

func TestSoftDeleteReportIsTenantScoped(t *testing.T) {
	fx := newReportFixture(t, "scope")
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-scope-0001",
		reportCreateRequest("link-policy-scope"))
	if err != nil {
		t.Fatal(err)
	}
	// Another workspace must not be able to reach the report at all: the id is
	// real, but the tenant in the query is not the owner's.
	err = fx.svc.Store.SoftDeleteReport(ctx, "other-tenant", created.Report.ID,
		time.Now().UTC(), time.Now().UTC())
	if err == nil {
		t.Fatal("cross-tenant soft delete must fail")
	}
	stored, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeletedAt != nil {
		t.Fatal("cross-tenant delete must not touch the report")
	}
}
