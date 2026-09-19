package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type createRoleRequest struct {
	Name        string `json:"name" binding:"required"`
	Scope       string `json:"scope" binding:"required"`
	Description string `json:"description"`
	ParentID    string `json:"parent_id"`
}

// GetPermissionCatalog returns the set of (resource, action) pairs a role may
// be granted, driving the role permission management UI.
func (h *Handler) GetPermissionCatalog(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "role"); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, db.PermissionCatalog())
}

// CreateRole creates an RBAC role (platform/tenant/project scope).
func (h *Handler) CreateRole(c *gin.Context) {
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid role payload")
		return
	}
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	actorRole := c.GetString(middleware.ContextRole)
	r, err := h.Service.CreateRole(ctx, actorID, actorRole, service.CreateRoleRequest{
		Name: req.Name, Scope: req.Scope, Description: req.Description, ParentID: req.ParentID,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: c.GetString(middleware.ContextTenantID), UserID: actorID, Action: "role.create", Resource: "role", ResourceID: r.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, r)
}

// DeleteRole deletes a non-built-in role.
func (h *Handler) DeleteRole(c *gin.Context) {
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	roleID := c.Param("id")
	if err := h.Service.DeleteRole(ctx, actorID, c.GetString(middleware.ContextRole), roleID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: c.GetString(middleware.ContextTenantID), UserID: actorID, Action: "role.delete", Resource: "role", ResourceID: roleID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": roleID})
}

// GetRolePermissions returns a role's policy rules.
func (h *Handler) GetRolePermissions(c *gin.Context) {
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	roleID := c.Param("id")
	perms, err := h.Service.GetRolePermissions(ctx, actorID, c.GetString(middleware.ContextRole), roleID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, perms)
}

type setRolePermissionsRequest struct {
	Permissions []struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
		Effect   string `json:"effect"`
	} `json:"permissions"`
}

// SetRolePermissions replaces a role's policy rules.
func (h *Handler) SetRolePermissions(c *gin.Context) {
	var req setRolePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid permissions payload")
		return
	}
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	roleID := c.Param("id")
	specs := make([]service.PermissionSpec, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		specs = append(specs, service.PermissionSpec{Action: p.Action, Resource: p.Resource, Effect: p.Effect})
	}
	if err := h.Service.SetRolePermissions(ctx, actorID, c.GetString(middleware.ContextRole), roleID, specs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: c.GetString(middleware.ContextTenantID), UserID: actorID, Action: "role.permissions", Resource: "role", ResourceID: roleID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": roleID, "permissions": len(req.Permissions)})
}

// UpdateRole edits a role's name/description/scope/parent.
func (h *Handler) UpdateRole(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		Description string `json:"description"`
		ParentID    string `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid role payload")
		return
	}
	ctx := c.Request.Context()
	actorID := c.GetString(middleware.ContextUserID)
	actorRole := c.GetString(middleware.ContextRole)
	roleID := c.Param("id")
	r, err := h.Service.UpdateRole(ctx, actorID, actorRole, roleID, service.UpdateRoleRequest{
		Name: req.Name, Scope: req.Scope, Description: req.Description, ParentID: req.ParentID,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: c.GetString(middleware.ContextTenantID), UserID: actorID, Action: "role.update", Resource: "role", ResourceID: roleID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, r)
}
