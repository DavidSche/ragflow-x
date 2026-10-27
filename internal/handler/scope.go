package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// resolveGovernanceScope turns raw query parameters into a server-authorized
// scope for governed resource reads.
func (h *Handler) resolveGovernanceScope(c *gin.Context, resource string) (service.TenantScope, bool) {
	if cached, ok := c.Get(middleware.ContextTenantScope); ok {
		if scope, valid := cached.(service.TenantScope); valid {
			return scope, true
		}
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	scope, err := h.Service.ResolveTenantScope(c.Request.Context(), userID, tenantID, c.Query("scope"), c.Query("tenant_id"))
	if err != nil {
		response.Err(c, err)
		return service.TenantScope{}, false
	}
	if scope.ScopeAll() || scope.Kind == service.TenantScopeSpecific {
		entry := &model.AuditLog{
			TenantID:   tenantID,
			UserID:     userID,
			Action:     resource + ".governance.read",
			Resource:   resource,
			ResourceID: c.Query("tenant_id"),
			DetailJSON: fmt.Sprintf("%q", scope.Kind),
			IP:         c.ClientIP(),
			TraceID:    c.GetString("request_id"),
		}
		entry.Scope = string(scope.Kind)
		entry.TargetTenantID = scope.TargetTenantID
		entry.AuthorizationPermission = "governance.read:tenant"
		entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
		if err := h.RequireAudit(c.Request.Context(), entry); err != nil {
			response.Err(c, err)
			return service.TenantScope{}, false
		}
	}
	return scope, true
}

func (h *Handler) requireSensitiveGovernanceRead(c *gin.Context, scope service.TenantScope) bool {
	if scope.Kind == service.TenantScopeCurrent {
		return true
	}
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.read_sensitive", "tenant"); err != nil {
		response.Err(c, err)
		return false
	}
	return true
}

// resolveGovernanceWriteScope turns raw write parameters into an exact target
// tenant. Cross-tenant platform operations must select a workspace; scope=all
// is deliberately never writable.
func (h *Handler) resolveGovernanceWriteScope(c *gin.Context, resource string) (service.TenantScope, bool) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	scope, err := h.Service.ResolveTenantScope(c.Request.Context(), userID, tenantID, c.Query("scope"), c.Query("tenant_id"))
	if err != nil {
		response.Err(c, err)
		return service.TenantScope{}, false
	}
	if scope.ScopeAll() {
		response.Fail(c, 400, 40000, "cross-tenant writes require one selected workspace")
		return service.TenantScope{}, false
	}
	targetTenantID := scope.CurrentTenantID()
	entry := &model.AuditLog{
		TenantID:   tenantID,
		UserID:     userID,
		Action:     resource + ".governance.write",
		Resource:   resource,
		ResourceID: targetTenantID,
		DetailJSON: fmt.Sprintf("%q", scope.Kind),
		IP:         c.ClientIP(),
		TraceID:    c.GetString("request_id"),
	}
	entry.Scope = string(scope.Kind)
	entry.TargetTenantID = targetTenantID
	entry.AuthorizationPermission = "governance.manage:tenant"
	entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
	if err := h.RequireAudit(c.Request.Context(), entry); err != nil {
		response.Err(c, err)
		return service.TenantScope{}, false
	}
	return scope, true
}
