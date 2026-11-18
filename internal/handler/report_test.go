package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// The report routes are the first place a caller-supplied id reaches the report
// family, so these tests pin the HTTP half of the isolation contract from
// doc/288 §7.6: the tenant comes from the session, a foreign report is
// `404`/44109 (never 403, never a hint that it exists), and a create without an
// `Idempotency-Key` is refused rather than quietly minting one.

// reportHandlerEngine is the mock engine with one difference: datasets report a
// bound embedding model. The plain mock never does, which would make the
// creation-time capability recheck answer 44102 instead of exercising the route.
type reportHandlerEngine struct {
	ragflow.Mock
}

func (e *reportHandlerEngine) GetDatasetConfig(ctx context.Context, datasetID string) (*ragflow.DatasetConfig, error) {
	cfg, err := e.Mock.GetDatasetConfig(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	cfg.EmbeddingModel = "text-embedding-3-small"
	return cfg, nil
}

type reportHarness struct {
	svc      *service.Service
	router   *gin.Engine
	tenantID string
	userID   string
}

// newReportHarness builds one workspace with its own router. Two harnesses over
// the same *service.Service are how the cross-workspace test gets a second
// session without a second database.
func newReportHarness(t *testing.T, svc *service.Service, engine *reportHandlerEngine, name, role string) *reportHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "report-handler-"+name)
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "report-" + name, Password: "secret123", Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newReportHarnessFor(t, svc, engine, tenant.ID, user.ID)
}

// newReportHarnessFor builds a harness for an *existing* principal. The A19 role
// matrix needs this: a platform administrator is minted in the system tenant by
// BootstrapAdmin, so the tenant cannot be created by the harness itself.
func newReportHarnessFor(
	t *testing.T, svc *service.Service, engine *reportHandlerEngine, tenantID, userID string,
) *reportHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenantID)
		c.Set(middleware.ContextUserID, userID)
		c.Next()
	})
	RegisterReportRoutes(group)
	return &reportHarness{svc: svc, router: router, tenantID: tenantID, userID: userID}
}

// newReportService builds the shared service these harnesses talk to.
func newReportService(t *testing.T) (*service.Service, *reportHandlerEngine) {
	t.Helper()
	engine := &reportHandlerEngine{Mock: *ragflow.NewMock()}
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), engine, nil, "test-encryption-key")
	t.Cleanup(func() {
		if svc.Store != nil {
			_ = svc.Store.Close()
		}
	})
	svc.RAGFlow = engine
	if _, err := svc.EnsureBuiltinReportTemplates(context.Background(), model.PlatformTenantID); err != nil {
		t.Fatalf("seed report templates: %v", err)
	}
	svc.SetRetentionPolicy(config.Retention{
		Enabled: true, AuditDays: 180, UsageDays: 90, FeedbackDays: 180, JobDays: 30,
		ReportDays: 90, ReportEvidenceDays: 90, ReportArtifactDays: 30,
		ReportEventDays: 7, ReportCleanupHoldDays: 7,
	})
	return svc, engine
}

// seedReportHandlerDataset creates one engine dataset with one parsed document
// and links it to the tenant. Retrievable is what the capability probe checks.
func seedReportHandlerDataset(t *testing.T, svc *service.Service, engine *reportHandlerEngine, tenantID, name string) string {
	t.Helper()
	ctx := context.Background()
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

// reportCreateBody is the smallest request the creation path accepts.
func reportCreateBody(datasetID string) map[string]interface{} {
	return map[string]interface{}{
		"title": "季度差旅合规检查", "question": "本季度差旅报销是否符合差旅制度第三章？",
		"template_key": "report-compliance",
		"datasets":     []map[string]string{{"role": "primary", "dataset_id": datasetID}},
	}
}

func (h *reportHarness) do(t *testing.T, method, path, idempotencyKey string, body interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = raw
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	var envelope map[string]interface{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("%s %s: response is not JSON: %v (%s)", method, path, err, rec.Body.String())
		}
	}
	return rec, envelope
}

func envelopeCode(t *testing.T, envelope map[string]interface{}) int {
	t.Helper()
	raw, ok := envelope["code"].(float64)
	if !ok {
		t.Fatalf("response has no numeric code: %v", envelope)
	}
	return int(raw)
}

func TestCreateReportRequiresIdempotencyKey(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "idem", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-idem")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "", reportCreateBody(datasetID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key must be 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, envelope); code != 44114 {
		t.Fatalf("missing Idempotency-Key must answer 44114, got %d", code)
	}
}

func TestCreateReportIsAcceptedAndReplaysOneReport(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "create", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-create")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-0001", reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		// 202 is the honest status: the aggregate exists, the content does not.
		t.Fatalf("create must be 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, envelope); code != 0 {
		t.Fatalf("create must answer code 0, got %d", code)
	}
	data, _ := envelope["data"].(map[string]interface{})
	if created, _ := data["created"].(bool); !created {
		t.Fatalf("first create must be created=true: %v", data)
	}
	createdReport, _ := data["report"].(map[string]interface{})
	reportID, _ := createdReport["id"].(string)
	if reportID == "" {
		t.Fatalf("create response carries no report id: %v", data)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/reports/"+reportID {
		t.Fatalf("Location must point at the report, got %q", loc)
	}

	// Same key, same answer: the second POST is the same *request*, so the
	// idempotency record decides, not the content (doc/288 §7.7).
	rec2, envelope2 := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-0001", reportCreateBody(datasetID))
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("replay must stay 202, got %d (%s)", rec2.Code, rec2.Body.String())
	}
	data2, _ := envelope2["data"].(map[string]interface{})
	if created, _ := data2["created"].(bool); created {
		t.Fatalf("replay must be created=false: %v", data2)
	}
	replayed, _ := data2["report"].(map[string]interface{})
	if got, _ := replayed["id"].(string); got != reportID {
		t.Fatalf("replay must return the same report: %q != %q", got, reportID)
	}
}

func TestReportDetailIsOwnerScopedAcrossWorkspaces(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-owner")
	_, envelope := owner.do(t, http.MethodPost, "/api/v1/reports", "key-report-0002", reportCreateBody(datasetID))
	data, _ := envelope["data"].(map[string]interface{})
	createdReport, _ := data["report"].(map[string]interface{})
	reportID, _ := createdReport["id"].(string)
	if reportID == "" {
		t.Fatalf("create response carries no report id: %v", data)
	}

	// The owner reads it: read:report + the row's owner_only policy.
	if rec, body := owner.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner read must be 200, got %d (%s)", rec.Code, rec.Body.String())
	} else if code := envelopeCode(t, body); code != 0 {
		t.Fatalf("owner read must answer code 0, got %d", code)
	}

	// A member of the same workspace is a colleague, not a co-owner: P0-A only
	// writes owner_only, so the answer is the same one a nonexistent id gets.
	colleagueUser, err := svc.CreateUser(context.Background(), owner.tenantID, "", service.CreateUserRequest{
		Username: "report-colleague", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	colleague := &reportHarness{
		svc: svc, tenantID: owner.tenantID, userID: colleagueUser.ID,
		router: reportRouterFor(svc, owner.tenantID, colleagueUser.ID),
	}
	rec, body := colleague.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("same-workspace non-owner must get 404/44109, got %d %v", rec.Code, body)
	}

	// Another workspace must not be able to tell this id from a random one.
	other := newReportHarness(t, svc, engine, "other", model.RoleOperator)
	rec, body = other.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("cross-workspace read must get 404/44109, got %d %v", rec.Code, body)
	}

	// Same for the list: nothing of the owner's leaks into another caller's page.
	rec, body = other.do(t, http.MethodGet, "/api/v1/reports", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-workspace list must still be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	page, _ := body["data"].(map[string]interface{})
	if total, _ := page["total"].(float64); total != 0 {
		t.Fatalf("cross-workspace list must be empty, got total=%v", total)
	}
}

// reportRouterFor builds a router with an explicit session, for the "same
// workspace, different user" case that the harness naturally cannot express.
func reportRouterFor(svc *service.Service, tenantID, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenantID)
		c.Set(middleware.ContextUserID, userID)
		c.Next()
	})
	RegisterReportRoutes(group)
	return router
}

func TestDeleteReportSoftDeletesAndSecondDeleteIs404(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "delete", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-delete")
	_, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-0003", reportCreateBody(datasetID))
	data, _ := envelope["data"].(map[string]interface{})
	createdReport, _ := data["report"].(map[string]interface{})
	reportID, _ := createdReport["id"].(string)

	// The body is optional: a plain DELETE has nothing to say.
	rec, _ := fx.do(t, http.MethodDelete, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// A deleted report is withdrawn from every read surface, not just from the
	// list: reading it, exporting it and deleting it again must all answer the
	// same "no such report" (44109/404) while the recovery window keeps the row.
	// Leaving the read surface open served a deleted report's body and download
	// links until the janitor purged it (found live on 4.151, R16).
	rec, body := fx.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("reading a deleted report must be 404/44109, got %d %v", rec.Code, body)
	}
	listing, listBody := fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=50", "", nil)
	if listing.Code != http.StatusOK {
		t.Fatalf("list must be 200, got %d (%s)", listing.Code, listing.Body.String())
	}
	if page, _ := listBody["data"].(map[string]interface{}); page != nil {
		if items, _ := page["items"].([]interface{}); len(items) > 0 {
			for _, item := range items {
				if entry, _ := item.(map[string]interface{}); entry["id"] == reportID {
					t.Fatalf("a deleted report must not be listed: %v", entry)
				}
			}
		}
	}

	rec, body = fx.do(t, http.MethodDelete, "/api/v1/reports/"+reportID, "", map[string]string{"reason": "重复删除"})
	if rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("second delete must be 404/44109, got %d %v", rec.Code, body)
	}
}

func TestListReportTemplatesReturnsSeededCatalog(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "templates", model.RoleBusinessUser)

	rec, body := fx.do(t, http.MethodGet, "/api/v1/report-templates", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog read must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	list, ok := body["data"].([]interface{})
	if !ok || len(list) == 0 {
		t.Fatalf("catalog must be a non-empty list: %v", body["data"])
	}
	seen := map[string]bool{}
	for _, item := range list {
		entry, _ := item.(map[string]interface{})
		if key, _ := entry["template_key"].(string); key != "" {
			seen[key] = true
		}
		if _, ok := entry["payload"]; !ok {
			t.Fatalf("catalog entry must carry its payload: %v", entry)
		}
	}
	// report-compliance is the built-in baseline (report_template_seed.go:30).
	if !seen["report-compliance"] {
		t.Fatalf("catalog is missing the built-in baseline, got %v", seen)
	}
}

// TestCancelReportAnswersAccepted pins the cancel contract: 202 with the intent
// status, a Location that points at the report, and no terminal claim. A 200 here
// would promise a state finalize has not written yet (doc/288 §8.4).
func TestCancelReportAnswersAccepted(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "cancel", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-cancel")

	_, created := fx.do(t, http.MethodPost, "/api/v1/reports", "key-cancel-report", reportCreateBody(datasetID))
	data, _ := created["data"].(map[string]interface{})
	report, _ := data["report"].(map[string]interface{})
	reportID, _ := report["id"].(string)
	if reportID == "" {
		t.Fatalf("create must return the report: %v", created)
	}

	rec, body := fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/cancel", "", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel must be 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, body); code != 0 {
		t.Fatalf("cancel must answer code 0, got %d", code)
	}
	cancelled, _ := body["data"].(map[string]interface{})
	if status, _ := cancelled["status"].(string); status != "cancel_requested" {
		t.Fatalf("cancel must report the recorded intent, got %v", cancelled)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, reportID) {
		t.Fatalf("Location must point at the report, got %q", loc)
	}

	// The terminal state is not written by the request: the armed slice's next
	// checkpoint derives it (a job cancelled before any slice existed has the
	// request hand the terminal write to the outbox — pinned by the service test
	// TestCancelReportConvergesWithoutSlice).
	job, err := svc.Store.GetLatestReportJob(context.Background(), fx.tenantID, reportID)
	if err != nil || job == nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Status != model.ReportJobCancelRequested {
		t.Fatalf("the request must record the intent, got %s", job.Status)
	}
	if job.FinalizedAt != nil {
		t.Fatal("the request must not claim a terminal state finalize has not written")
	}
}

// TestConfirmReportOutlineAnswersAccepted pins the confirmation contract:
// 202 with the new attempt/slice, a Location that points at the report, and a
// 44105 for a revision path that is not a number. A 200 here would claim content
// that does not exist yet — confirmation only *arms* the next slice (doc/288 §8.4).
func TestConfirmReportOutlineAnswersAccepted(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "outline", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-outline")
	ctx := context.Background()

	_, created := fx.do(t, http.MethodPost, "/api/v1/reports", "key-outline-0001", reportCreateBody(datasetID))
	data, _ := created["data"].(map[string]interface{})
	report, _ := data["report"].(map[string]interface{})
	reportID, _ := report["id"].(string)
	if reportID == "" {
		t.Fatalf("create must return the report: %v", created)
	}

	// The outline step is the worker's job; here it is reproduced so the handler
	// is exercised against a job that really is `awaiting_outline`.
	job, err := svc.Store.GetLatestReportJob(ctx, fx.tenantID, reportID)
	if err != nil || job == nil {
		t.Fatalf("load job: %v", err)
	}
	// Creation arms the first slice already; the outline step runs against it.
	if job.CurrentRuntimeJobID == "" {
		t.Fatal("creation must arm the first runtime slice")
	}
	now := time.Now().UTC()
	applied, err := svc.Store.BeginReportOutline(ctx, repository.ReportOutlineStart{
		TenantID: fx.tenantID, JobID: job.ID, RevisionID: job.RevisionID,
		RuntimeJobID: job.CurrentRuntimeJobID, AttemptSeq: job.AttemptSeq, SliceSeq: job.SliceSeq,
		Now: now, OutlineJSON: `{"sections":[{"section_key":"background"}]}`,
		SnapshotJSON: `{"model":{"model_id":"mock-chat-model"}}`, SnapshotHash: "snapshot-hash-outline",
		EngineVersion: "1.0.0",
		Sections: []model.ReportSection{{
			ID: "rsec-outline-1", TenantID: fx.tenantID, RevisionID: job.RevisionID,
			Ord: 1, SectionKey: "background", SectionVersion: 1, Title: "背景",
			Query: "差旅制度的覆盖范围是什么？", Status: model.ReportSectionPending,
			QualityStatus: model.ReportQualityNotChecked, EvidenceStatus: model.ReportEvidenceNotRequired,
		}},
	})
	if err != nil || !applied {
		t.Fatalf("park the job in awaiting_outline: applied=%v err=%v status=%s", applied, err, job.Status)
	}

	// The path segment is the revision *number*, not its id.
	rev, err := svc.Store.GetReportRevision(ctx, fx.tenantID, reportID, 1)
	if err != nil || rev == nil {
		t.Fatalf("load revision: %v", err)
	}
	base := "/api/v1/reports/" + reportID + "/revisions/"
	// The revision is a path segment, so a non-numeric one is a request error
	// (44105), not a "job not awaiting" conflict (44104).
	rec, body := fx.do(t, http.MethodPost, base+"abc/outline/confirm", "",
		map[string]interface{}{"sections": []map[string]string{{"section_key": "background"}}})
	if rec.Code != http.StatusBadRequest || envelopeCode(t, body) != 44105 {
		t.Fatalf("a bad revision path must be 400/44105, got %d/%d", rec.Code, envelopeCode(t, body))
	}

	confirmPath := base + strconv.FormatInt(rev.Revision, 10) + "/outline/confirm"
	rec, body = fx.do(t, http.MethodPost, confirmPath, "",
		map[string]interface{}{"sections": []map[string]string{
			{"section_key": "background", "title": "差旅制度总览"},
		}})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("confirm must be 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, body); code != 0 {
		t.Fatalf("confirm must answer code 0, got %d", code)
	}
	confirmed, _ := body["data"].(map[string]interface{})
	if attempt, _ := confirmed["attempt_seq"].(float64); int(attempt) != 2 {
		t.Fatalf("confirmation must arm attempt 2, got %v", confirmed["attempt_seq"])
	}
	if slice, _ := confirmed["slice_seq"].(float64); int(slice) != 1 {
		t.Fatalf("confirmation must arm slice 1, got %v", confirmed["slice_seq"])
	}
	if edited, _ := confirmed["edited_sections"].(float64); int(edited) != 1 {
		t.Fatalf("a renamed section is exactly one edit: %v", confirmed)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/reports/"+reportID {
		t.Fatalf("Location must point at the report, got %q", loc)
	}

	sections, err := svc.Store.ListReportSections(ctx, fx.tenantID, job.RevisionID)
	if err != nil || len(sections) != 1 {
		t.Fatalf("list sections: %v (%d)", err, len(sections))
	}
	if sections[0].Title != "差旅制度总览" {
		t.Fatalf("the edited title must be stored, got %q", sections[0].Title)
	}

	// The second confirmation finds a job that already moved on: 409/44104.
	rec, body = fx.do(t, http.MethodPost, confirmPath, "",
		map[string]interface{}{"sections": []map[string]string{{"section_key": "background"}}})
	if rec.Code != http.StatusConflict || envelopeCode(t, body) != 44104 {
		t.Fatalf("a repeated confirmation must be 409/44104, got %d/%d", rec.Code, envelopeCode(t, body))
	}
}

// TestPutReportRetentionRejectsAMalformedBody pins the code, not just the status.
//
// A body that does not bind is a `400` "the request itself is wrong", so the answer
// is `44105` (this surface's invalid-parameter code) — not `44104`, which the registry
// maps to `409` "the current state forbids this". The two are easy to swap in a
// handler, and only a pinned assertion catches it.
func TestPutReportRetentionRejectsAMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/report-retention",
		strings.NewReader(`{"report_days":"ninety"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	// The bind fails before any service call, so the handler can be exercised without
	// a Service — which is exactly the property this test relies on.
	putReportRetention(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed retention body status = %d, want 400", recorder.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if code := envelopeCode(t, body); code != 44105 {
		t.Fatalf("malformed retention body code = %d, want 44105", code)
	}
}
