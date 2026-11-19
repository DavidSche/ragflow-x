package handler

// The archiving wire contract (doc/297 §2). The service layer already proves the
// rules; this file proves what actually crosses the HTTP boundary, because three
// things can only be wrong here:
//
//   - the two new verbs are routed and carry the right status code (200, not 202: the
//     report already existed, nothing is being accepted);
//   - `?status=` on the list reaches the service instead of being silently ignored, and
//     an unknown value comes back as 400/44105 rather than an empty page;
//   - the 202 body for a deferred run carries `concurrency`, since that object is the
//     only place the UI can learn it was queued behind other work.

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestReportArchiveEndpointsChangeLifecycle(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "archive-wire", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-archive-wire")
	reportID, _ := handlerPublishReport(t, svc, fx, datasetID, "key-archive-wire-0001")

	// ① The daily list shows it before the archive.
	_, envelope := fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20", "", nil)
	if total := listTotal(t, envelope); total != 1 {
		t.Fatalf("daily list total=%d, want 1", total)
	}

	// ② Archive answers 200 with the detail view, so the caller renders the report it
	// just put away instead of re-reading it.
	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/archive", "",
		map[string]interface{}{"reason": "季度归档"})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: %d (%s)", rec.Code, rec.Body.String())
	}
	detail, _ := envelope["data"].(map[string]interface{})
	reportRow, _ := detail["report"].(map[string]interface{})
	if reportRow["status"] != model.ReportStatusArchived {
		t.Fatalf("archive response status=%v", reportRow["status"])
	}

	// ③ The three list views, over HTTP: hidden by default, found under `archived`,
	// visible under `all`.
	if _, envelope := fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20", "", nil); listTotal(t, envelope) != 0 {
		t.Fatalf("daily list must hide an archived report: %v", envelope)
	}
	_, envelope = fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20&status=archived", "", nil)
	if listTotal(t, envelope) != 1 {
		t.Fatalf("archived view total=%d, want 1", listTotal(t, envelope))
	}
	if _, envelope := fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20&status=all", "", nil); listTotal(t, envelope) != 1 {
		t.Fatalf("all view total=%d, want 1", listTotal(t, envelope))
	}

	// ④ An unknown view is a request-shape error, not an empty page: `44105` keeps the
	// rule that "no rows" always means "no rows".
	rec, envelope = fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20&status=deleted", "", nil)
	if rec.Code != http.StatusBadRequest || envelopeCode(t, envelope) != 44105 {
		t.Fatalf("bad status filter: %d code=%d", rec.Code, envelopeCode(t, envelope))
	}

	// ⑤ Archiving twice is a transition error (409/44104), not a second event.
	rec, envelope = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/archive", "", nil)
	if rec.Code != http.StatusConflict || envelopeCode(t, envelope) != 44104 {
		t.Fatalf("archive twice: %d code=%d", rec.Code, envelopeCode(t, envelope))
	}

	// ⑥ Unarchive takes no body and returns the detail view; then the report is back.
	rec, envelope = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/unarchive", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unarchive: %d (%s)", rec.Code, rec.Body.String())
	}
	detail, _ = envelope["data"].(map[string]interface{})
	reportRow, _ = detail["report"].(map[string]interface{})
	if reportRow["status"] != model.ReportStatusActive {
		t.Fatalf("unarchive response status=%v", reportRow["status"])
	}
	if _, envelope := fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20", "", nil); listTotal(t, envelope) != 1 {
		t.Fatalf("unarchived report must be back in the daily list")
	}
}

func TestReportArchiveEndpointsAreOwnerOnly(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "archive-owner", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "policy-archive-owner")
	reportID, _ := handlerPublishReport(t, svc, owner, datasetID, "key-archive-owner-0001")

	// A colleague in the same workspace: the answer is 44109, the same word every
	// other object-level refusal uses, so the button's existence is not leaked.
	colleague, err := svc.CreateUser(context.Background(), owner.tenantID, "", service.CreateUserRequest{
		Username: "report-archive-colleague-wire", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("colleague: %v", err)
	}
	peer := newReportHarnessFor(t, svc, engine, owner.tenantID, colleague.ID)
	rec, envelope := peer.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/archive", "", nil)
	if rec.Code != http.StatusNotFound || envelopeCode(t, envelope) != 44109 {
		t.Fatalf("colleague archive: %d code=%d", rec.Code, envelopeCode(t, envelope))
	}

	// An unknown id answers exactly the same, so the route is not an existence oracle.
	rec, envelope = owner.do(t, http.MethodPost, "/api/v1/reports/no-such-report/archive", "", nil)
	if rec.Code != http.StatusNotFound || envelopeCode(t, envelope) != 44109 {
		t.Fatalf("unknown report archive: %d code=%d", rec.Code, envelopeCode(t, envelope))
	}
}

func TestReportCreateResponseExplainsADeferral(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "defer-wire", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-defer-wire")

	// Fill the workspace over HTTP, through the same endpoint a client uses.
	var last map[string]interface{}
	for index := 0; index <= service.ReportMaxConcurrentRuns; index++ {
		rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports",
			"key-defer-wire-"+string(rune('a'+index)), reportCreateBody(datasetID))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("create %d: %d (%s)", index, rec.Code, rec.Body.String())
		}
		last = envelope
	}

	// The over-limit answer is a 202 like any other — accepted, not refused — and the
	// only difference is the snapshot that says why nothing started yet.
	data, _ := last["data"].(map[string]interface{})
	job, _ := data["job"].(map[string]interface{})
	if job["status"] != model.ReportJobQueued {
		t.Fatalf("deferred job status=%v, want queued", job["status"])
	}
	if pointer, _ := job["current_runtime_job_id"].(string); pointer != "" {
		t.Fatalf("deferred job must have no runtime pointer, got %q", pointer)
	}
	snapshot, ok := data["concurrency"].(map[string]interface{})
	if !ok {
		t.Fatalf("deferred create must carry concurrency, got %v", data)
	}
	if limit, _ := snapshot["limit"].(float64); int(limit) != service.ReportMaxConcurrentRuns {
		t.Fatalf("concurrency.limit=%v", snapshot["limit"])
	}
	if active, _ := snapshot["active"].(float64); int(active) < service.ReportMaxConcurrentRuns {
		t.Fatalf("concurrency.active=%v", snapshot["active"])
	}

	// And an admitted report carries no snapshot: the field means "this is waiting",
	// so a zero-valued object would be worse than its absence.
	other, err := svc.CreateTenant(context.Background(), "report-defer-other-wire")
	if err != nil {
		t.Fatalf("second tenant: %v", err)
	}
	owner, err := svc.CreateUser(context.Background(), other.ID, "", service.CreateUserRequest{
		Username: "report-defer-other-wire", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("second owner: %v", err)
	}
	otherDataset := seedReportHandlerDataset(t, svc, engine, other.ID, "policy-defer-other-wire")
	otherFx := newReportHarnessFor(t, svc, engine, other.ID, owner.ID)
	if _, envelope := otherFx.do(t, http.MethodPost, "/api/v1/reports",
		"key-defer-other-wire-0001", reportCreateBody(otherDataset)); hasKey(envelope, "concurrency") {
		t.Fatalf("admitted create must omit concurrency: %v", envelope)
	}
}

// listTotal reads `data.total` out of a page envelope.
func listTotal(t *testing.T, envelope map[string]interface{}) int {
	t.Helper()
	page, ok := envelope["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("not a page envelope: %v", envelope)
	}
	total, ok := page["total"].(float64)
	if !ok {
		t.Fatalf("page has no total: %v", page)
	}
	return int(total)
}

// hasKey answers "does this envelope's data carry the field", including a null one:
// `omitempty` on a pointer drops the key entirely, and that is what a client discriminates on.
func hasKey(envelope map[string]interface{}, key string) bool {
	data, ok := envelope["data"].(map[string]interface{})
	if !ok {
		return false
	}
	_, present := data[key]
	return present
}
