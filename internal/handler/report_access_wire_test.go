package handler

// The two new report wires (doc/296 §2.1–§2.2): the share write and the budget
// extension. Both are addressed on the report path, both answer with a *view* rather
// than 204, and both refuse a malformed body with the surface's own 44105 instead of
// inventing a code.
//
// The point of testing them here rather than only in the service is the wire shape:
// the panel renders `policy` + `grantees`, and the budget answer distinguishes
// "applied" from "pending_approval" — a client that cannot tell those apart would
// either claim a limit was raised when it is waiting for a human, or re-request the
// same extension in a loop.

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestReportAccessWriteReturnsTheShareView(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "access-wire", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-access-wire")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-access-wire-001", reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	reportID := reportIDOf(t, envelope)

	colleague, err := svc.CreateUser(context.Background(), fx.tenantID, "", service.CreateUserRequest{
		Username: "report-access-wire-colleague", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create colleague: %v", err)
	}

	rec, envelope = fx.do(t, http.MethodPut, "/api/v1/reports/"+reportID+"/access", "", map[string]interface{}{
		"policy": "selected_users", "user_ids": []string{colleague.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("share: %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := envelope["data"].(map[string]interface{})
	if data["policy"] != model.ReportAccessSelectedUsers {
		t.Fatalf("the answer must be the view, got %v", data)
	}
	if isOwner, _ := data["is_owner"].(bool); !isOwner {
		t.Fatalf("the owner must see is_owner=true: %v", data)
	}
	grantees, _ := data["grantees"].([]interface{})
	if len(grantees) != 1 {
		t.Fatalf("the view must list the grantee: %v", data)
	}
	grantee, _ := grantees[0].(map[string]interface{})
	if grantee["id"] != colleague.ID || grantee["kind"] != model.ReportGrantUser || grantee["name"] == "" {
		t.Fatalf("grantee must carry kind/id/name: %v", grantee)
	}

	// An unknown policy is the surface's "the request is wrong" code, not a new one.
	rec, envelope = fx.do(t, http.MethodPut, "/api/v1/reports/"+reportID+"/access", "", map[string]interface{}{
		"policy": "everyone",
	})
	if rec.Code != http.StatusBadRequest || envelopeCode(t, envelope) != 44105 {
		t.Fatalf("unknown policy must be 400/44105, got %d (%s)", rec.Code, rec.Body.String())
	}
	// A grantee list on a policy that does not take one is refused rather than
	// silently dropped: the panel would otherwise show grantees the policy ignores.
	rec, envelope = fx.do(t, http.MethodPut, "/api/v1/reports/"+reportID+"/access", "", map[string]interface{}{
		"policy": "owner_only", "user_ids": []string{colleague.ID},
	})
	if rec.Code != http.StatusBadRequest || envelopeCode(t, envelope) != 44105 {
		t.Fatalf("owner_only with grantees must be 400/44105, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestReportBudgetExtensionDistinguishesAppliedFromPending(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "budget-wire", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-budget-wire")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-budget-wire-001", reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	reportID := reportIDOf(t, envelope)

	// Zero is not an increment, and the answer is the surface's validation code.
	rec, envelope = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/budget-extension", "",
		map[string]interface{}{"tokens": 0})
	if rec.Code != http.StatusBadRequest || envelopeCode(t, envelope) != 44105 {
		t.Fatalf("zero increment must be 400/44105, got %d (%s)", rec.Code, rec.Body.String())
	}

	// With the approval workflow off, the increment applies immediately and the answer
	// says so — the status word is the contract, not a decoration.
	rec, envelope = fx.do(t, http.MethodPost, "/api/v1/reports/"+reportID+"/budget-extension", "",
		map[string]interface{}{"tokens": 100, "reason": "补齐第三章"})
	if rec.Code != http.StatusOK {
		t.Fatalf("extend: %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := envelope["data"].(map[string]interface{})
	if data["status"] != service.ReportBudgetExtensionApplied {
		t.Fatalf("an ungoverned workspace must answer applied, got %v", data)
	}
	if added, _ := data["added_tokens"].(float64); added != 100 {
		t.Fatalf("the answer must name the increment: %v", data)
	}
	if after, _ := data["budget_tokens"].(float64); after <= 100 {
		t.Fatalf("the answer must name the envelope after the increment: %v", data)
	}
}

// reportIDOf reads the created report id out of a create envelope.
func reportIDOf(t *testing.T, envelope map[string]interface{}) string {
	t.Helper()
	data, _ := envelope["data"].(map[string]interface{})
	report, _ := data["report"].(map[string]interface{})
	reportID, _ := report["id"].(string)
	if reportID == "" {
		t.Fatalf("response carries no report id: %v", envelope)
	}
	return reportID
}
