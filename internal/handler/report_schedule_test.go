package handler

// R20 HTTP surface (doc/295 §2.4). The three routes are thin, and these assertions
// are about the contract a client codes against: 201 the first time the plan exists,
// 200 afterwards, a `{"schedule": null}` body when the report has no plan, and 204
// for a delete that is idempotent by design.

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestReportScheduleRoutesStatusContract(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "sched-http", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, owner.tenantID, "sched-http")

	created, envelope := owner.do(t, http.MethodPost, "/api/v1/reports", "req-sched-http-0001",
		reportCreateBody(datasetID))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create report: %d %v", created.Code, envelope)
	}
	data, _ := envelope["data"].(map[string]interface{})
	reportBody, _ := data["report"].(map[string]interface{})
	reportID, _ := reportBody["id"].(string)
	if reportID == "" {
		t.Fatalf("create response carries no report id: %v", envelope)
	}
	path := "/api/v1/reports/" + reportID + "/schedule"

	// No plan yet: 200 with a null payload, not a 404. "This report has no timer" is
	// an answer, and a card that renders errors for a normal state trains users to
	// ignore errors.
	empty, envelope := owner.do(t, http.MethodGet, path, "", nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty read: %d %v", empty.Code, envelope)
	}
	if data, ok := envelope["data"].(map[string]interface{}); !ok || data["schedule"] != nil {
		t.Fatalf("empty read must answer {\"schedule\":null}, got %v", envelope["data"])
	}

	body := map[string]interface{}{
		"cadence": "weekly", "hour": 9, "minute": 30, "weekday": 1,
		"tz_offset_min": 480, "update_mode": "reuse_static",
	}
	first, envelope := owner.do(t, http.MethodPut, path, "", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first arming must be 201, got %d %v", first.Code, envelope)
	}
	if envelope["message"] != "created" {
		t.Fatalf("201 envelope must say created, got %v", envelope["message"])
	}
	plan := envelope["data"].(map[string]interface{})
	if plan["cadence"] != "weekly" || plan["update_mode"] != "reuse_static" {
		t.Fatalf("arming must echo the stored plan, got %v", plan)
	}
	if _, ok := plan["last_status"]; !ok {
		t.Fatalf("the view must carry last_status even when it never ran: %v", plan)
	}

	body["hour"] = 10
	second, envelope := owner.do(t, http.MethodPut, path, "", body)
	if second.Code != http.StatusOK {
		t.Fatalf("editing must be 200, got %d %v", second.Code, envelope)
	}
	if hour, _ := envelope["data"].(map[string]interface{})["hour"].(float64); hour != 10 {
		t.Fatalf("edit must land, got %v", envelope["data"])
	}

	// Validation is 400/44105 — the report layer's own boundary error.
	bad, envelope := owner.do(t, http.MethodPut, path, "",
		map[string]interface{}{"cadence": "monthly"})
	if bad.Code != http.StatusBadRequest || envelopeCode(t, envelope) != 44105 {
		t.Fatalf("invalid cadence: %d %v", bad.Code, envelope)
	}

	// A colleague may read the report? No: 44109 for both read and write. The
	// handler must not turn that into a 403.
	colleague := newReportHarnessForHarness(t, svc, engine, owner)
	if rec, envelope := colleague.do(t, http.MethodGet, path, "", nil); rec.Code != http.StatusNotFound ||
		envelopeCode(t, envelope) != 44109 {
		t.Fatalf("foreign read must be 404/44109, got %d %v", rec.Code, envelope)
	}
	if rec, envelope := colleague.do(t, http.MethodPut, path, "", body); rec.Code != http.StatusNotFound ||
		envelopeCode(t, envelope) != 44109 {
		t.Fatalf("foreign write must be 404/44109, got %d %v", rec.Code, envelope)
	}

	// Delete is 204 and idempotent: the second call has nothing to remove and must
	// still succeed, because the caller's intent ("no timer") is now true.
	for attempt := 0; attempt < 2; attempt++ {
		rec, _ := owner.do(t, http.MethodDelete, path, "", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete attempt %d: %d", attempt+1, rec.Code)
		}
	}
	if rec, envelope := owner.do(t, http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("read after delete: %d %v", rec.Code, envelope)
	}
}

// newReportHarnessForHarness mints a second principal in the *same* workspace, which
// is the case the 44109 rule is hardest to get right for: the caller passes RBAC, so
// only the row-level owner check can refuse them.
func newReportHarnessForHarness(
	t *testing.T, svc *service.Service, engine *reportHandlerEngine, base *reportHarness,
) *reportHarness {
	t.Helper()
	user, err := svc.CreateUser(t.Context(), base.tenantID, "", service.CreateUserRequest{
		Username: "report-colleague-" + base.tenantID[:8], Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newReportHarnessFor(t, svc, engine, base.tenantID, user.ID)
}
