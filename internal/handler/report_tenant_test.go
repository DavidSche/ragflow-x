package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// T19 — handler half of doc/288 §7.6 "隔离测试（P0-A，不得后移）". The repository
// half (internal/repository/report_tenant_test.go) proves every by-ID SQL is
// tenant-scoped. This file proves the HTTP surface answers the same — a foreign
// workspace (and a same-workspace non-owner) gets `404`/44109 on every report
// route, never 403, never a hint that the addressed id exists.
//
// Faces covered here, mirroring §7.6's four:
//   ② handler object-level validation (guess an id across cancel / outline
//      confirm / revision read / export / download / retry);
//   ③ the download endpoint is cross-workspace NotFound;
//   ④ the report list never leaks rows across workspaces.
//
// The owner-side sanities (cancel → 202, export on an unpublished revision →
// 409/44106) exist so a foreign `404` cannot be explained away as "the route is
// not live": the route demonstrably serves the owner, and the foreign answer is
// the access gate, not a broken handler.

func createReportIDRow(t *testing.T, envelope map[string]interface{}, key string) string {
	t.Helper()
	data, _ := envelope["data"].(map[string]interface{})
	row, _ := data[key].(map[string]interface{})
	idValue, _ := row["id"].(string)
	return idValue
}

// downloadRaw serves one request through the harness router without assuming an
// JSON envelope — the §7.6 ③ download face streams bytes, not a code body.
func (h *reportHarness) downloadRaw(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func TestReportHandlerObjectLevelIsolationAcrossWorkspaces(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "tenant-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-tenant-owner")

	_, created := owner.do(t, http.MethodPost, "/api/v1/reports", "key-tenant-iso-0001", reportCreateBody(datasetID))
	reportID := createReportIDRow(t, created, "report")
	jobID := createReportIDRow(t, created, "job")
	if reportID == "" || jobID == "" {
		t.Fatalf("create must return report+job: %v", created)
	}

	foreign := newReportHarness(t, svc, engine, "tenant-foreign", model.RoleOperator)

	// ② object-level guess-id: every business route on the owner's ids.
	assertForeignGone := func(t *testing.T, method, path string, body interface{}) {
		t.Helper()
		rec, envelope := foreign.do(t, method, path, "", body)
		if rec.Code != http.StatusNotFound || envelopeCode(t, envelope) != 44109 {
			t.Fatalf("%s %s must be 404/44109 for a foreign workspace, got %d %v",
				method, path, rec.Code, envelope)
		}
	}
	assertForeignGone(t, http.MethodGet, "/api/v1/reports/"+reportID, nil)
	assertForeignGone(t, http.MethodPost, "/api/v1/reports/"+reportID+"/cancel", nil)
	assertForeignGone(t, http.MethodPost, "/api/v1/reports/"+reportID+"/revisions/1/outline/confirm",
		map[string]interface{}{"sections": []map[string]string{{"section_key": "intro", "title": "概述"}}})
	assertForeignGone(t, http.MethodGet, "/api/v1/reports/"+reportID+"/revisions/1", nil)
	assertForeignGone(t, http.MethodPost, "/api/v1/reports/"+reportID+"/revisions/1/artifacts",
		map[string]interface{}{"format": "docx"})
	// ③ download: the artifact address lives *under* the report + revision, so a
	// foreign caller fails on the report, never learns the artifact exists.
	assertForeignGone(t, http.MethodGet, "/api/v1/reports/"+reportID+"/revisions/1/artifacts/any-artifact/download", nil)
	// job namespace: a foreign workspace cannot reach the owner's job either.
	assertForeignGone(t, http.MethodPost, "/api/v1/report-jobs/"+jobID+"/sections/1/retry", map[string]interface{}{})

	// ④ list: nothing of the owner's leaks into the foreign page.
	rec, body := foreign.do(t, http.MethodGet, "/api/v1/reports", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("foreign list must still be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := body["data"].(map[string]interface{})
	if total, _ := data["total"].(float64); total != 0 {
		t.Fatalf("foreign list must be empty, got total=%v", total)
	}

	// Owner sanities: the same routes do serve the owner.
	if rec, _ := owner.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/cancel", "", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("owner cancel must be 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	// Owner export on an unpublished revision reaches the business gate (409/
	// 44106), which is a different answer than the access 404 — proving the
	// foreign 404 was isolation, not a missing route.
	rec, body = owner.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/revisions/1/artifacts",
		"", map[string]interface{}{"format": "docx"})
	if rec.Code != http.StatusConflict || envelopeCode(t, body) != 44106 {
		t.Fatalf("owner export on unpublished revision must be 409/44106, got %d %v", rec.Code, body)
	}
}

// TestReportHandlerSameWorkspaceNonOwnerIsolation pins that P0-A writes only
// `owner_only`: a colleague in the same workspace is just as blind as a foreign
// tenant — owner-only reports are the P0-A publishing stance (doc/288 §13).
func TestReportHandlerSameWorkspaceNonOwnerIsolation(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "colleague-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-colleague")

	_, created := owner.do(t, http.MethodPost, "/api/v1/reports", "key-colleague-0001", reportCreateBody(datasetID))
	reportID := createReportIDRow(t, created, "report")

	colleagueUser, err := svc.CreateUser(nil, owner.tenantID, "", service.CreateUserRequest{
		Username: "report-colleague-2", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	colleague := &reportHarness{
		svc: svc, tenantID: owner.tenantID, userID: colleagueUser.ID,
		router: reportRouterFor(svc, owner.tenantID, colleagueUser.ID),
	}
	if rec, body := colleague.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil); rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("same-workspace non-owner read must be 404/44109, got %d %v", rec.Code, body)
	}
	if rec, body := colleague.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/cancel", "", nil); rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("same-workspace non-owner cancel must be 404/44109, got %d %v", rec.Code, body)
	}
	if rec, body := colleague.do(t, http.MethodGet, "/api/v1/reports/"+reportID+"/revisions/1/artifacts/any/download", "", nil); rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("same-workspace non-owner download must be 404/44109, got %d %v", rec.Code, body)
	}
}

// handlerPublishReport drives one report to a published revision through the real
// service pipeline (Dispatch → Claim/Complete the runtime slice → finalize) and
// exports one Markdown artifact, so the §7.6 ③ download face can assert an
// owner-fetch pays out real bytes while every other caller is 404/44109.
func handlerPublishReport(t *testing.T, svc *service.Service, owner *reportHarness, datasetID, key string) (reportID, artifactID string) {
	t.Helper()
	ctx := context.Background()
	created, err := svc.CreateReport(ctx, owner.userID, owner.tenantID, key, service.CreateReportRequest{
		Title:       "季度差旅合规检查",
		Question:    "本季度差旅报销是否符合差旅制度第三章？",
		TemplateKey: "report-compliance",
		Datasets: []service.CapabilityCheckDataset{{
			Role: "primary", DatasetID: datasetID,
		}},
	})
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	revID, jobID := created.Revision.ID, created.Job.ID

	// Seed the five sections the `report-compliance` template pins, mirroring the
	// service fixture's `successSeeds`: a finalize derives its outcome (and its
	// `MissingKeys`) from the pinned template, so a foreign-shaped seed would
	// fail as "structural gap" before the download test could run.
	seeds := []model.ReportSection{
		{SectionKey: "background", Title: "背景", Status: model.ReportSectionSucceeded,
			ContentMD: "## 背景\n说明", ContentHash: "handler-hash-background"},
		{SectionKey: "applicable_rules", Title: "适用规则", Status: model.ReportSectionSucceeded,
			RequiresEvidence: true, EvidenceStatus: model.ReportEvidenceSupported,
			CitationCount: 2, QualityStatus: model.ReportQualityPassed,
			ContentMD: "## 适用规则\n说明", ContentHash: "handler-hash-applicable_rules"},
		{SectionKey: "compliance", Title: "合规结论", Status: model.ReportSectionSucceeded,
			RequiresEvidence: true, EvidenceStatus: model.ReportEvidenceSupported,
			CitationCount: 3, QualityStatus: model.ReportQualityPassed,
			ContentMD: "## 合规结论\n说明", ContentHash: "handler-hash-compliance"},
		{SectionKey: "gaps", Title: "差距", Status: model.ReportSectionSucceeded,
			RequiresEvidence: true, EvidenceStatus: model.ReportEvidencePartiallySupported,
			CitationCount: 1, QualityStatus: model.ReportQualityPassed,
			ContentMD: "## 差距\n说明", ContentHash: "handler-hash-gaps"},
		{SectionKey: "remediation", Title: "整改", Status: model.ReportSectionSucceeded,
			RequiresEvidence: true, EvidenceStatus: model.ReportEvidenceSupported,
			CitationCount: 1, QualityStatus: model.ReportQualityPassed,
			ContentMD: "## 整改\n说明", ContentHash: "handler-hash-remediation"},
	}
	for ord, section := range seeds {
		section.TenantID, section.RevisionID = owner.tenantID, revID
		section.Ord, section.SectionVersion = ord, 1
		if err := svc.Store.CreateReportSection(ctx, &section); err != nil {
			t.Fatalf("seed section %s: %v", section.SectionKey, err)
		}
	}

	// Creation already armed slice 1; settle that slice instead of arming a second.
	armed, err := svc.Store.GetReportJob(ctx, owner.tenantID, jobID)
	if err != nil || armed == nil || armed.CurrentRuntimeJobID == "" {
		t.Fatalf("load armed job: %v %+v", err, armed)
	}
	runtimeID := armed.CurrentRuntimeJobID
	now := time.Now().UTC()
	claimed, err := svc.Store.ClaimJobs(ctx, 10, now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	owned := false
	for _, j := range claimed {
		if j.ID == runtimeID {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("settling runtime slice: expected %s among claimed, got %+v", runtimeID, claimed)
	}
	if err := svc.Store.CompleteJob(ctx, runtimeID, 1, ""); err != nil {
		t.Fatalf("complete runtime slice: %v", err)
	}
	if _, err := svc.BeginReportFinalization(ctx, owner.tenantID, jobID, 1, 1, runtimeID); err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	if outcome, err := svc.FinalizeReportJob(ctx, owner.tenantID, jobID, runtimeID, now); err != nil || outcome != service.ReportFinalizeApplied {
		t.Fatalf("finalize: outcome=%s err=%v", outcome, err)
	}
	export, err := svc.CreateReportExport(ctx, owner.userID, owner.tenantID, service.ReportExportRequest{
		ReportID: created.Report.ID, Revision: 1, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if export.Artifact == nil {
		t.Fatalf("export returned no artifact")
	}
	return created.Report.ID, export.Artifact.ID
}

func TestReportHandlerDownloadIsolationAcrossWorkspaces(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "dl-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-dl")
	reportID, artifactID := handlerPublishReport(t, svc, owner, datasetID, "key-dl-0001")
	downloadPath := filepath.ToSlash("/api/v1/reports/" + reportID + "/revisions/1/artifacts/" + artifactID + "/download")

	// ③ owner: the frozen bytes pay out.
	rec := owner.downloadRaw(t, http.MethodGet, downloadPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner download must stream 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(rec.Body.Bytes()) == 0 {
		t.Fatalf("owner download body must be non-empty")
	}
	if rec.Header().Get("Content-Type") == "" || rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("owner download must carry content-type + disposition, got %v", rec.Header())
	}

	// Same-workspace non-owner: owner_only → 404/44109.
	colleagueUser, err := svc.CreateUser(context.Background(), owner.tenantID, "", service.CreateUserRequest{
		Username: "dl-colleague", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	colleague := &reportHarness{
		svc: svc, tenantID: owner.tenantID, userID: colleagueUser.ID,
		router: reportRouterFor(svc, owner.tenantID, colleagueUser.ID),
	}
	if rec, body := colleague.do(t, http.MethodGet, downloadPath, "", nil); rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("same-workspace non-owner download must be 404/44109, got %d %v", rec.Code, body)
	}

	// Foreign workspace: same answer.
	foreign := newReportHarness(t, svc, engine, "dl-foreign", model.RoleOperator)
	if rec, body := foreign.do(t, http.MethodGet, downloadPath, "", nil); rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
		t.Fatalf("foreign download must be 404/44109, got %d %v", rec.Code, body)
	}
}
