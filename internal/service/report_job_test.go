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
	if job.Status != model.ReportJobQueued || job.Stage != model.ReportStageOutlining {
		t.Fatalf("job must start queued/outlining: %+v", job)
	}
	if job.CurrentRuntimeJobID != "" {
		// Creation deliberately does not enqueue: the dispatch step owns the first
		// runtime slice, and a pointer here would claim a job that does not exist.
		t.Fatalf("creation must not claim a runtime job, got %q", job.CurrentRuntimeJobID)
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
	reports, err := fx.svc.Store.ListReports(ctx, fx.tenantID, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("replay must not create a second report, got %d", len(reports))
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

func TestSoftDeleteReportKeepsTTLAndSecondDeleteIsNotFound(t *testing.T) {
	fx := newReportFixture(t, "delete")
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-delete-0001",
		reportCreateRequest("link-policy-delete"))
	if err != nil {
		t.Fatal(err)
	}
	frozenTTL := created.Report.ExpiresAt

	if err := fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, "季度归档"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	stored, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.DeletedAt == nil {
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
	err = fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, "重复删除")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied {
		t.Fatalf("second delete must answer 404/%d, got %v", codeReportAccessDenied, err)
	}
	after, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.CleanupHoldUntil.Equal(*stored.CleanupHoldUntil) {
		t.Fatalf("second delete must not extend the window: %v -> %v", stored.CleanupHoldUntil, after.CleanupHoldUntil)
	}
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
