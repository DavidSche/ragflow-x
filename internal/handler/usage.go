package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// UsageReport returns a usage and estimated-cost report, optionally scoped to
// all tenants for platform admins and filtered by a date range.
func (h *Handler) UsageReport(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	// A delegated platform auditor may read cross-workspace usage with
	// tenant.usage.read alone; governance.read keeps working and stays first so
	// existing audit attribution does not churn (doc/248 / R-26).
	usageRef, usageErr := h.Service.AuthorizeAny(c.Request.Context(), userID, service.GovernanceReadRefs("usage")...)
	scopeAll := c.Query("scope") == "all" && usageErr == nil
	permittedBy := "governance.read:tenant"
	if usageErr == nil {
		permittedBy = usageRef.String()
	}
	if err := h.Service.Authorize(c.Request.Context(), userID, "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	if scopeAll {
		if err := h.RequireAudit(c.Request.Context(), &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "usage.governance.read", Resource: "usage", Scope: "ALL_AUTHORIZED", TargetTenantID: tenantID, AuthorizationPermission: permittedBy, IP: c.ClientIP(), TraceID: c.GetString("request_id")}); err != nil {
			response.Err(c, err)
			return
		}
	}
	rows, err := h.Service.UsageReport(c.Request.Context(), tenantID, scopeAll, c.Query("from"), c.Query("to"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if requestedTenantID := c.Query("tenant_id"); requestedTenantID != "" {
		filtered := rows[:0]
		for _, row := range rows {
			if row.TenantID == requestedTenantID {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	response.OK(c, rows)
}

// ExportUsageReport returns the usage report as CSV.
func (h *Handler) ExportUsageReport(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	usageRef, usageErr := h.Service.AuthorizeAny(ctx, userID, service.GovernanceReadRefs("usage")...)
	scopeAll := c.Query("scope") == "all" && usageErr == nil
	permittedBy := "governance.read:tenant"
	if usageErr == nil {
		permittedBy = usageRef.String()
	}
	if err := h.Service.Authorize(ctx, userID, "read", "usage-export"); err != nil {
		response.Err(c, err)
		return
	}
	data, err := h.Service.ExportUsageCSV(ctx, tenantID, scopeAll, c.Query("from"), c.Query("to"))
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Data(200, "text/csv; charset=utf-8", data)
	entry := &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "usage.export", Resource: "usage", DetailJSON: boolStr(scopeAll), IP: c.ClientIP(), TraceID: c.GetString("request_id")}
	if scopeAll {
		entry.Scope = "ALL_AUTHORIZED"
		entry.AuthorizationPermission = permittedBy
	}
	if err := h.RequireAudit(ctx, entry); err != nil {
		response.Err(c, err)
		return
	}
}
