package handler

// A15 + A47 at the HTTP boundary. The service tests prove the rules; these prove the
// *route* answers the shape a client can use, and that the two "quiet" failure modes
// stay quiet in the right way:
//
//   - `view` is refused when it holds a value the service does not know, and the
//     platform's own `scope` vocabulary (current|specific|all) is *not* reused here;
//   - the trend is 200 with `quality_gate_pass_rate: null` on a workspace where nothing
//     was judged (a `0` here is what made the live dashboard self-contradictory);
//   - restore is 200 with the report body, not `{"restored":true}`, and a second restore
//     is 409/44104 rather than a success that changed nothing;
//   - an absent `force` is not an error, and a present-but-invalid one is 44105.

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestReportMetricsTrendAnswersNullRateAndView(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "trend", model.RoleOperator)

	rec, envelope := fx.do(t, http.MethodGet, "/api/v1/report-metrics/trend?days=7&bucket=day", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trend must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := envelope["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("trend must carry a data object: %v", envelope)
	}
	if data["view"] != "own" || data["bucket"] != "day" {
		t.Fatalf("trend must echo its view and bucket: %v", data)
	}
	totals, _ := data["totals"].(map[string]interface{})
	if totals == nil {
		t.Fatalf("trend must carry totals: %v", data)
	}
	// An empty workspace has no denominator, so the rate is JSON null and not 0.
	rate, present := totals["quality_gate_pass_rate"]
	if !present || rate != nil {
		t.Fatalf("an unjudged window must answer null, got %v (present=%v)", rate, present)
	}
	if buckets, ok := data["buckets"].([]interface{}); !ok || len(buckets) != 0 {
		t.Fatalf("an empty window must carry an empty bucket list, not omit it: %v", data["buckets"])
	}

	// A present-but-invalid value is refused; an absent one takes the default.
	for _, query := range []string{"days=abc", "days=0", "days=999", "bucket=month", "view=all"} {
		bad, body := fx.do(t, http.MethodGet, "/api/v1/report-metrics/trend?"+query, "", nil)
		if bad.Code != http.StatusBadRequest || envelopeCode(t, body) != 44105 {
			t.Fatalf("%s must be 400/44105, got %d %v", query, bad.Code, body)
		}
	}
}

func TestReportMetricsTrendRequiresManageForTheWorkspaceView(t *testing.T) {
	svc, engine := newReportService(t)
	operator := newReportHarness(t, svc, engine, "trend-scope", model.RoleOperator)

	rec, body := operator.do(t, http.MethodGet, "/api/v1/report-metrics/trend?view=tenant", "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an operator may not read the workspace trend, got %d (%s)", rec.Code, rec.Body.String())
	}
	if body["code"] == nil {
		t.Fatalf("the refusal must carry a code: %v", body)
	}
}

func TestRestoreReportRouteReopensAndThenRefuses(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "restore", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-restore")
	_, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-restore", reportCreateBody(datasetID))
	data, _ := envelope["data"].(map[string]interface{})
	created, _ := data["report"].(map[string]interface{})
	reportID, _ := created["id"].(string)

	rec, body := fx.do(t, http.MethodDelete, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if deleted, _ := body["data"].(map[string]interface{}); deleted["recoverable"] != true {
		t.Fatalf("a plain delete must say the window is open: %v", body)
	}

	rec, body = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/restore", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore inside the window must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	detail, _ := body["data"].(map[string]interface{})
	restored, _ := detail["report"].(map[string]interface{})
	if restored["id"] != reportID {
		t.Fatalf("restore must answer the report detail: %v", body)
	}
	if restored["cleanup_hold_until"] != nil {
		t.Fatalf("a restored report must not carry a recovery window: %v", restored)
	}

	// Visible again: the read surface that answered 404 a moment ago answers 200.
	rec, _ = fx.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a restored report must be readable, got %d (%s)", rec.Code, rec.Body.String())
	}

	// A second restore is a state refusal, not a missing route.
	rec, body = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/restore", "", nil)
	if rec.Code != http.StatusConflict || envelopeCode(t, body) != 44104 {
		t.Fatalf("second restore must be 409/44104, got %d %v", rec.Code, body)
	}
}

func TestReportDeleteForceFlagIsValidatedAndGated(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "force", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-force")
	_, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-force", reportCreateBody(datasetID))
	data, _ := envelope["data"].(map[string]interface{})
	created, _ := data["report"].(map[string]interface{})
	reportID, _ := created["id"].(string)

	// A malformed flag is a payload error, and the report must survive it.
	rec, body := fx.do(t, http.MethodDelete, "/api/v1/reports/"+reportID+"?force=maybe", "", nil)
	if rec.Code != http.StatusBadRequest || envelopeCode(t, body) != 44105 {
		t.Fatalf("force=maybe must be 400/44105, got %d %v", rec.Code, body)
	}
	if live, _ := fx.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil); live.Code != http.StatusOK {
		t.Fatalf("a rejected force delete must leave the report live: %d", live.Code)
	}

	// An operator may delete their own report but not skip its recovery window.
	rec, _ = fx.do(t, http.MethodDelete, "/api/v1/reports/"+reportID+"?force=true", "", nil)
	if rec.Code == http.StatusOK {
		t.Fatal("force delete must require manage:report")
	}
	if live, _ := fx.do(t, http.MethodGet, "/api/v1/reports/"+reportID, "", nil); live.Code != http.StatusOK {
		t.Fatalf("a refused force delete must leave the report live: %d", live.Code)
	}
}

// The trend reads stored verdicts, so the endpoint must reflect a revision's
// `metrics_json` end to end — including the `not_checked` count that has no rate.
func TestReportMetricsTrendReportsStoredVerdicts(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "trend-stored", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-trend-stored")
	_, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-report-trend", reportCreateBody(datasetID))
	data, _ := envelope["data"].(map[string]interface{})
	created, _ := data["report"].(map[string]interface{})
	revision, _ := data["revision"].(map[string]interface{})
	reportID, _ := created["id"].(string)
	revisionID, _ := revision["id"].(string)

	// Write the verdict the runner would have written (finalize's own tests own that
	// path) and then check the read answers it. Raw SQL keeps the seeding explicit:
	// this test is about what the endpoint *reads*, not about how a revision is written.
	raw, ok := svc.Store.(repository.RawDBProvider)
	if !ok {
		t.Fatal("handler tests need the gorm-backed store")
	}
	if _, err := raw.RawDB().Exec(
		`UPDATE rgx_report_revision SET status = ?, completeness = ?, metrics_json = ? WHERE id = ?`,
		model.ReportRevisionPublished, model.ReportCompletenessFull,
		`{"structure_completeness":1,"citation_coverage":0.5,"quality_judged":0,"quality_not_checked":2,"quality_flag":"ok"}`,
		revisionID,
	); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}
	_ = reportID

	rec, body := fx.do(t, http.MethodGet, "/api/v1/report-metrics/trend?days=7", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trend must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	payload, _ := body["data"].(map[string]interface{})
	totals, _ := payload["totals"].(map[string]interface{})
	if totals["published"] != float64(1) || totals["quality_not_checked"] != float64(2) {
		t.Fatalf("trend must report the stored counts: %v", totals)
	}
	if totals["quality_gate_pass_rate"] != nil {
		t.Fatalf("nothing was judged, so the rate must be null: %v", totals["quality_gate_pass_rate"])
	}
	if totals["citation_coverage"] != 0.5 {
		t.Fatalf("trend must report the stored coverage: %v", totals["citation_coverage"])
	}
}
