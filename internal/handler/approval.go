package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type approvalActionRequest struct {
	Comment string `json:"comment" binding:"required"`
}

type approvalBatchDecisionRequest struct {
	IDs     []string `json:"ids"`
	Comment string   `json:"comment"`
}

type approvalDelegationRequest struct {
	DelegateID string `json:"delegate_id"`
	ObjectType string `json:"object_type"`
	Action     string `json:"action"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
}

func parseApprovalTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func (h *Handler) ListApprovals(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	tenantID := c.GetString(middleware.ContextTenantID)
	req := service.ApprovalListRequest{
		TenantID:    c.Query("tenant_id"),
		Status:      c.Query("status"),
		ObjectType:  c.Query("object_type"),
		Action:      c.Query("action"),
		PolicyID:    c.Query("policy_id"),
		RequesterID: c.Query("requester_id"),
		ApproverID:  c.Query("approver_id"),
		Search:      c.Query("q"),
		From:        parseApprovalTime(c.Query("from")),
		To:          parseApprovalTime(c.Query("to")),
		SortBy:      c.Query("sort_by"),
		Order:       c.Query("order"),
	}
	req.Page, req.PageSize = pageParams(c)
	items, total, err := h.Service.ListApprovals(ctx, userID, tenantID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, req.Page, req.PageSize)
}

func (h *Handler) CreateApproval(c *gin.Context) {
	var req service.ApprovalSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid approval payload")
		return
	}
	if c.GetHeader("Idempotency-Key") != "" {
		req.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	approval, err := h.Service.SubmitApproval(c.Request.Context(), c.GetString(middleware.ContextUserID), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	if approval == nil {
		c.JSON(http.StatusOK, response.Body{
			Code:    0,
			Message: "no matching policy",
			Data:    gin.H{"created": false},
			TraceID: c.GetString("request_id"),
		})
		return
	}
	approvalAccepted(c, approval)
}

func (h *Handler) GetApproval(c *gin.Context) {
	detail, err := h.Service.GetApproval(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, detail)
}

func (h *Handler) ExportApprovals(c *gin.Context) {
	req := service.ApprovalListRequest{
		TenantID:    c.Query("tenant_id"),
		Status:      c.Query("status"),
		ObjectType:  c.Query("object_type"),
		Action:      c.Query("action"),
		PolicyID:    c.Query("policy_id"),
		RequesterID: c.Query("requester_id"),
		ApproverID:  c.Query("approver_id"),
		Search:      c.Query("q"),
		From:        parseApprovalTime(c.Query("from")),
		To:          parseApprovalTime(c.Query("to")),
		SortBy:      c.Query("sort_by"),
		Order:       c.Query("order"),
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", "attachment; filename=\"approvals-"+time.Now().UTC().Format("20060102-150405")+".csv\"")
	rows, err := h.Service.ExportApprovals(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), req, c.Writer,
	)
	if err != nil {
		if rows == 0 {
			response.Err(c, err)
		}
		return
	}
}

func (h *Handler) ApproveApproval(c *gin.Context) {
	h.decision(c, "approve")
}

func (h *Handler) RejectApproval(c *gin.Context) {
	h.decision(c, "reject")
}

func (h *Handler) CancelApproval(c *gin.Context) {
	var req approvalActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "comment is required")
		return
	}
	approval, err := h.Service.CancelApproval(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"), req.Comment,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, approval)
}

func (h *Handler) RetryApproval(c *gin.Context) {
	var req approvalActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "comment is required")
		return
	}
	approval, err := h.Service.RetryApprovalExecution(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"), req.Comment,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, approval)
}

func (h *Handler) BatchApproveApprovals(c *gin.Context) {
	h.batchDecision(c, "approve")
}

func (h *Handler) BatchRejectApprovals(c *gin.Context) {
	h.batchDecision(c, "reject")
}

func (h *Handler) batchDecision(c *gin.Context, decision string) {
	var req approvalBatchDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "ids and comment are required")
		return
	}
	results, err := h.Service.BatchDecideApprovals(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), decision, service.ApprovalBatchDecisionRequest(req),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, results)
}

func (h *Handler) ListApprovalDelegations(c *gin.Context) {
	items, err := h.Service.ListApprovalDelegations(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

func (h *Handler) CreateApprovalDelegation(c *gin.Context) {
	var req approvalDelegationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid approval delegation payload")
		return
	}
	delegation, err := h.Service.CreateApprovalDelegation(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), service.ApprovalDelegationRequest{
			DelegateID: req.DelegateID, ObjectType: req.ObjectType, Action: req.Action,
			StartsAt: parseApprovalTime(req.StartsAt), EndsAt: parseApprovalTime(req.EndsAt),
		},
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, delegation)
}

func (h *Handler) DeleteApprovalDelegation(c *gin.Context) {
	deleted, err := h.Service.DeleteApprovalDelegation(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	if !deleted {
		response.Fail(c, 404, 40400, "approval delegation not found")
		return
	}
	c.JSON(http.StatusOK, response.Body{Code: 0, Message: "", Data: gin.H{"deleted": true}, TraceID: c.GetString("request_id")})
}

func (h *Handler) ListApprovalPolicies(c *gin.Context) {
	items, err := h.Service.ListApprovalPolicies(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

func (h *Handler) SaveApprovalPolicy(c *gin.Context) {
	var req service.ApprovalPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid approval policy payload")
		return
	}
	if c.Request.Method == http.MethodPut {
		req.ID = c.Param("id")
	}
	policy, err := h.Service.SaveApprovalPolicy(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, policy)
}

func (h *Handler) decision(c *gin.Context, decision string) {
	var req approvalActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "comment is required")
		return
	}
	approval, err := h.Service.DecideApproval(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"), decision, req.Comment,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, approval)
}

func (h *Handler) approvalHold(c *gin.Context, objectType, action, objectID string, payload map[string]any) bool {
	approval, hold, err := h.Service.ApprovalGate(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		objectType, action, objectID, payload, c.GetHeader("Idempotency-Key"),
	)
	if err != nil {
		response.Err(c, err)
		return true
	}
	if !hold {
		return false
	}
	approvalAccepted(c, approval)
	return true
}

func (h *Handler) approvalHoldForTenant(c *gin.Context, targetTenantID string, crossTenant bool, objectType, action, objectID string, payload map[string]any) bool {
	if !crossTenant {
		return h.approvalHold(c, objectType, action, objectID, payload)
	}
	approval, hold, err := h.Service.ApprovalGateForTenant(
		c.Request.Context(), c.GetString(middleware.ContextUserID), targetTenantID,
		objectType, action, objectID, payload, c.GetHeader("Idempotency-Key"),
	)
	if err != nil {
		response.Err(c, err)
		return true
	}
	if !hold {
		response.Fail(c, 400, 40000, "cross-tenant writes require an approval policy")
		return true
	}
	approvalAccepted(c, approval)
	return true
}

func approvalAccepted(c *gin.Context, approval *model.Approval) {
	c.Header("X-Approval-Request-Id", approval.RequestNo)
	c.JSON(202, response.Body{
		Code:    0,
		Message: "approval submitted",
		Data: gin.H{
			"approval_id":   approval.ID,
			"request_no":    approval.RequestNo,
			"object_type":   approval.ObjectType,
			"action":        approval.Action,
			"status":        approval.Status,
			"current_step":  approval.CurrentStep,
			"expire_at":     approval.ExpiresAt,
			"approval_path": "/approvals/" + approval.ID + "/show",
		},
		TraceID: c.GetString("request_id"),
	})
}

func payloadFromRequest(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, httperr.Internal("failed to encode approval payload")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, httperr.Internal("failed to decode approval payload")
	}
	return payload, nil
}
