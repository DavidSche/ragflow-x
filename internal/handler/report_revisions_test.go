package handler

// Report version history at the HTTP boundary (doc/288 §7.10 / A32 / A41).
//
// Before this route, a superseded revision was reachable only by guessing its
// number: the detail page resolves `current_revision_id`, so the API could serve a
// historical body that the product had no way to ask for. These tests pin the two
// things a client needs — the list exists, and it names the *live* revision rather
// than "the newest row" — plus the isolation rule that keeps a colleague's history
// indistinguishable from a nonexistent one.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestListReportRevisionsMarksTheLiveRevision(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "history", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-history")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-history-0001",
		reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := envelope["data"].(map[string]interface{})
	reportRow, _ := data["report"].(map[string]interface{})
	reportID, _ := reportRow["id"].(string)
	revisionRow, _ := data["revision"].(map[string]interface{})
	firstRevisionID, _ := revisionRow["id"].(string)

	// A second revision row that is *not* the current one: regeneration writes the
	// attempt, and only the publish step moves `current_revision_id`. That is the
	// difference the list must show, so the fixture states it directly instead of
	// driving a whole second run.
	raw, ok := svc.Store.(repository.RawDBProvider)
	if !ok {
		t.Fatal("handler tests need the gorm-backed store")
	}
	if _, err := raw.RawDB().Exec(
		`INSERT INTO rgx_report_revision
		 (id, report_id, tenant_id, revision, outline_confirmation, template_key,
		  template_version, status, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"rev-history-2", reportID, fx.tenantID, 2, "required",
		revisionRow["template_key"].(string), 1, model.ReportRevisionGenerating,
		fx.userID, time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed second revision: %v", err)
	}

	rec, envelope = fx.do(t, http.MethodGet, "/api/v1/reports/"+reportID+"/revisions", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ = envelope["data"].(map[string]interface{})
	rows, _ := data["revisions"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("history must list both revisions, got %v", data["revisions"])
	}
	newest, _ := rows[0].(map[string]interface{})
	oldest, _ := rows[1].(map[string]interface{})
	if newest["revision"].(float64) != 2 {
		t.Fatalf("history is newest-first, got %v", newest["revision"])
	}
	if newest["status"] != model.ReportRevisionGenerating {
		t.Fatalf("the list must report each row's own status, got %v", newest["status"])
	}
	// The generating attempt is newest, but it is not what the report shows.
	if newest["is_current"] != false {
		t.Fatalf("an unfinished attempt must not be marked current: %v", newest)
	}
	if oldest["is_current"] != true || oldest["id"] != firstRevisionID {
		t.Fatalf("the published base must be marked current: %v", oldest)
	}
	// No bodies in the list: history is a browser, the body is a separate read.
	if _, present := oldest["content_md"]; present {
		t.Fatalf("the history list must not ship bodies: %v", oldest)
	}

	// And the number from the list is directly readable.
	detail, envelope := fx.do(t, http.MethodGet,
		"/api/v1/reports/"+reportID+"/revisions/2", "", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("reading a listed revision must work, got %d (%s)", detail.Code, detail.Body.String())
	}
	detailData, _ := envelope["data"].(map[string]interface{})
	if body, _ := detailData["revision"].(map[string]interface{}); body["revision"].(float64) != 2 {
		t.Fatalf("revision read returned the wrong row: %v", detailData["revision"])
	}
}

func TestReportRevisionHistoryIsNotAColleagueOracle(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "history-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-history-iso")

	rec, envelope := owner.do(t, http.MethodPost, "/api/v1/reports", "key-history-iso-0001",
		reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := envelope["data"].(map[string]interface{})
	reportRow, _ := data["report"].(map[string]interface{})
	reportID, _ := reportRow["id"].(string)

	// A real second user in the same tenant: the isolation rule is about the report's
	// row-level access policy, not about the request carrying an unknown id.
	colleagueUser, err := svc.CreateUser(context.Background(), owner.tenantID,
		model.RoleTenantAdmin, service.CreateUserRequest{
			Username: "history-colleague", Password: "secret123",
			Role: model.RoleOperator,
		})
	if err != nil {
		t.Fatalf("create colleague: %v", err)
	}
	colleague := newReportHarnessFor(t, svc, engine, owner.tenantID, colleagueUser.ID)

	rec, envelope = colleague.do(t, http.MethodGet,
		"/api/v1/reports/"+reportID+"/revisions", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a colleague's history must answer not-found, got %d (%s)", rec.Code, rec.Body.String())
	}
	if code := envelopeCode(t, envelope); code != 44109 {
		t.Fatalf("isolation is 44109 (not 403), got %d", code)
	}

	// A nonexistent report answers exactly the same, so the id stays unprobeable.
	missing, missingEnvelope := colleague.do(t, http.MethodGet,
		"/api/v1/reports/rep-does-not-exist/revisions", "", nil)
	if missing.Code != rec.Code || envelopeCode(t, missingEnvelope) != 44109 {
		t.Fatalf("a foreign id and a missing id must be indistinguishable, got %d/%v vs %d/%v",
			rec.Code, envelope["code"], missing.Code, missingEnvelope["code"])
	}

	// Sanity: the owner's own read still works, so the isolation above is the
	// colleague's gate and not a broken route.
	if ok, _ := owner.do(t, http.MethodGet, "/api/v1/reports/"+reportID+"/revisions", "", nil); ok.Code != http.StatusOK {
		t.Fatalf("owner history must be 200, got %d", ok.Code)
	}
}
