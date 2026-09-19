package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type createTenantRequest struct {
	Name      string `json:"name" binding:"required"`
	Status    string `json:"status"`
	BrandName string `json:"brand_name"`
	BrandLogo string `json:"brand_logo"`
}

// ListTenants returns a paginated list of tenants.
func (h *Handler) ListTenants(c *gin.Context) {
	page, pageSize := pageParams(c)
	ctx := c.Request.Context()
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "tenant")
	if !ok {
		return
	}
	filter := repository.TenantFilter{
		Name:        c.Query("name"),
		Status:      c.Query("status"),
		CreatedFrom: c.Query("created_from"),
		CreatedTo:   c.Query("created_to"),
	}
	items, total, err := h.Service.ListTenantsForScope(ctx, scope, page, pageSize, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// CreateTenant creates a tenant and applies status/branding if provided.
func (h *Handler) CreateTenant(c *gin.Context) {
	ctx := c.Request.Context()
	var req createTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid tenant payload")
		return
	}
	actorID := c.GetString(middleware.ContextUserID)
	actorTenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, actorID, "manage", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	t, err := h.Service.CreateTenant(ctx, req.Name)
	if err != nil {
		response.Err(c, err)
		return
	}
	if req.Status != "" || req.BrandName != "" || req.BrandLogo != "" {
		if _, err := h.Service.UpdateTenant(ctx, actorID, actorTenantID, t.ID, service.UpdateTenantRequest{
			Status: req.Status, BrandName: req.BrandName, BrandLogo: req.BrandLogo,
		}); err != nil {
			response.Err(c, err)
			return
		}
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: t.ID, UserID: actorID, Action: "tenant.create", Resource: "tenant", ResourceID: t.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, t)
}

// UpdateTenant edits a tenant's name/status/branding.
func (h *Handler) UpdateTenant(c *gin.Context) {
	var req struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		BrandName string `json:"brand_name"`
		BrandLogo string `json:"brand_logo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid tenant payload")
		return
	}
	ctx := c.Request.Context()
	actorTenantID := c.GetString(middleware.ContextTenantID)
	actorID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, actorID, "manage", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID := c.Param("id")
	t, err := h.Service.UpdateTenant(ctx, actorID, actorTenantID, tenantID, service.UpdateTenantRequest{
		Name: req.Name, Status: req.Status, BrandName: req.BrandName, BrandLogo: req.BrandLogo,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actorID, Action: "tenant.update", Resource: "tenant", ResourceID: tenantID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, t)
}

// BatchUpdateTenantStatus enables or disables multiple tenants in one call.
func (h *Handler) BatchUpdateTenantStatus(c *gin.Context) {
	var req struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid batch tenant payload")
		return
	}
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, actorID, "manage", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	if len(req.IDs) == 0 {
		response.Fail(c, 400, 40000, "ids required")
		return
	}
	if err := h.Service.BatchUpdateTenantStatus(ctx, actorID, req.IDs, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: c.GetString(middleware.ContextTenantID), UserID: actorID, Action: "tenant.status.batch", Resource: "tenant", DetailJSON: req.Status + ":" + strings.Join(req.IDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"updated": len(req.IDs)})
}

// ExportTenants streams all tenants as CSV.
func (h *Handler) ExportTenants(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, userID, "read", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	data, err := h.Service.ExportTenants(ctx, c.GetString(middleware.ContextUserID), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="tenants.csv"`)
	c.Data(200, "text/csv; charset=utf-8", data)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "tenant.export", Resource: "tenant", ResourceID: tenantID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
}

// DeleteTenant deletes a tenant, optionally force-removing its children.
func (h *Handler) DeleteTenant(c *gin.Context) {
	ctx := c.Request.Context()
	actorTenantID := c.GetString(middleware.ContextTenantID)
	actorID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, actorID, "manage", "tenant"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID := c.Param("id")
	force := c.Query("force") == "true"
	if err := h.Service.DeleteTenant(ctx, c.GetString(middleware.ContextUserID), actorTenantID, tenantID, force); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actorID, Action: "tenant.delete", Resource: "tenant", ResourceID: tenantID, DetailJSON: boolStr(force), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": tenantID})
}

func boolStr(b bool) string {
	if b {
		return "force"
	}
	return ""
}
