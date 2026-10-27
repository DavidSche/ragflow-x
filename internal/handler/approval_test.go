package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestApprovalAcceptedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/approvals", nil)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	approval := &model.Approval{
		ID: "approval-1", RequestNo: "APR-001", Status: model.ApprovalStatusPendingApproval,
		CurrentStep: 1, ExpiresAt: now,
	}

	approvalAccepted(ctx, approval)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if got := recorder.Header().Get("X-Approval-Request-Id"); got != approval.RequestNo {
		t.Fatalf("request id header = %q, want %q", got, approval.RequestNo)
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			ApprovalID  string `json:"approval_id"`
			RequestNo   string `json:"request_no"`
			Status      string `json:"status"`
			CurrentStep int    `json:"current_step"`
			ExpireAt    string `json:"expire_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 {
		t.Fatalf("code = %d, want 0", body.Code)
	}
	if body.Data.ApprovalID != approval.ID || body.Data.RequestNo != approval.RequestNo {
		t.Fatalf("unexpected approval identity: %+v", body.Data)
	}
	if body.Data.Status != approval.Status || body.Data.CurrentStep != 1 {
		t.Fatalf("unexpected approval state: %+v", body.Data)
	}
	if body.Data.ExpireAt != now.Format(time.RFC3339Nano) && body.Data.ExpireAt != now.Format(time.RFC3339) {
		t.Fatalf("expire_at = %q, want %q", body.Data.ExpireAt, now.Format(time.RFC3339))
	}
}
