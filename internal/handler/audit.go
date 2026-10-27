package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"strings"
)

// ListAudits returns a page of the tenant's audit logs.
func (h *Handler) ListAudits(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "audit"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "audit")
	if !ok {
		return
	}
	page, pageSize := pageParams(c)
	filter := repository.AuditFilter{
		UserID:          c.Query("user_id"),
		Action:          c.Query("action"),
		Resource:        c.Query("resource"),
		TargetTenant:    c.Query("target_tenant_id"),
		Result:          c.Query("result"),
		ApprovalID:      c.Query("approval_id"),
		ActingContextID: c.Query("acting_context_id"),
		CreatedFrom:     c.Query("created_from"),
		CreatedTo:       c.Query("created_to"),
	}
	items, total, err := h.Service.ListAuditsForScope(c.Request.Context(), scope, page, pageSize, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// ExportAudits streams the audit log as CSV.
func (h *Handler) ExportAudits(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "audit"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "audit")
	if !ok {
		return
	}
	rows, data, err := h.Service.ExportAuditsForScope(c.Request.Context(), scope)
	if err != nil {
		response.Err(c, err)
		return
	}
	entry := &model.AuditLog{
		TenantID: scope.ActorTenantID, UserID: c.GetString(middleware.ContextUserID),
		Action: "audit.exported", Resource: "audit", ResourceID: "csv",
		DetailJSON: fmt.Sprintf(`{"rows":%d,"limit":10000,"scope":%q,"target_tenant_id":%q}`,
			rows, scope.Kind, scope.TargetTenantID),
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	}
	entry.Scope = string(scope.Kind)
	entry.ActorTenantID = scope.ActorTenantID
	entry.TargetTenantID = scope.TargetTenantID
	entry.AuthorizationDecision = "ALLOW"
	entry.AuthorizationPermission = "read:audit"
	entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
	if err := h.RequireAudit(c.Request.Context(), entry); err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="audit.csv"`)
	c.Data(200, "text/csv; charset=utf-8", data)
}

// VerifyAuditChain checks the audit hash chain integrity.
func (h *Handler) VerifyAuditChain(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "audit"); err != nil {
		response.Err(c, err)
		return
	}
	valid, err := h.Service.VerifyAuditChain(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"valid": valid})
}

func (h *Handler) RepairAuditChain(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.manage", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	var req struct {
		TenantID string `json:"tenant_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.TenantID) == "" {
		response.Fail(c, 400, 40000, "tenant_id is required")
		return
	}
	_, err := h.Service.RepairAuditChain(c.Request.Context(), req.TenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: req.TenantID, UserID: c.GetString(middleware.ContextUserID),
		Action: "audit.repair", Resource: "audit", ResourceID: req.TenantID,
		DetailJSON: `{"mode":"reanchor"}`, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	}); err != nil {
		response.Err(c, err)
		return
	}
	valid, err := h.Service.VerifyAuditChain(c.Request.Context(), req.TenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"tenant_id": req.TenantID, "valid": valid})
}
