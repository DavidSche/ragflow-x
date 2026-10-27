package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

type projectRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// ListProjects lists the tenant's projects.
func (h *Handler) ListProjects(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "project"); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListProjects(c.Request.Context(), tenantID, c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextRole))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// CreateProject creates a tenant project.
func (h *Handler) CreateProject(c *gin.Context) {
	var req projectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid project payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	p, err := h.Service.CreateProject(ctx, tenantID, req.Name, req.Description)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.create", Resource: "project", ResourceID: p.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, p)
}

// GetProject returns a single tenant project (visible to project members).
func (h *Handler) GetProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	projectID := c.Param("id")
	if err := h.Service.AuthorizeProject(ctx, actor, "read", "project", tenantID, projectID); err != nil {
		response.Err(c, err)
		return
	}
	p, err := h.Service.GetProject(ctx, tenantID, projectID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, p)
}

// UpdateProject updates a tenant project's name and description.
func (h *Handler) UpdateProject(c *gin.Context) {
	var req projectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid project payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	projectID := c.Param("id")
	p, err := h.Service.UpdateProject(ctx, tenantID, projectID, req.Name, req.Description)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.update", Resource: "project", ResourceID: projectID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, p)
}

// DeleteProject deletes a tenant project.
func (h *Handler) DeleteProject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	id := c.Param("id")
	if err := h.Service.DeleteProject(ctx, tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.delete", Resource: "project", ResourceID: id, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": id})
}

// ListProjectUsers lists project members.
func (h *Handler) ListProjectUsers(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	projectID := c.Param("id")
	if err := h.Service.AuthorizeProject(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "project", tenantID, projectID); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListProjectUsers(c.Request.Context(), tenantID, projectID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// AddProjectUser adds a user to a project.
func (h *Handler) AddProjectUser(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "user_id is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	projectID := c.Param("id")
	if err := h.Service.AddProjectUser(ctx, tenantID, projectID, req.UserID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.member.add", Resource: "project", ResourceID: projectID, DetailJSON: req.UserID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"project_id": projectID, "user_id": req.UserID})
}

// RemoveProjectUser removes a user from a project.
func (h *Handler) RemoveProjectUser(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	projectID := c.Param("id")
	memberID := c.Param("userId")
	if err := h.Service.RemoveProjectUser(ctx, tenantID, projectID, memberID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.member.remove", Resource: "project", ResourceID: projectID, DetailJSON: memberID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"project_id": projectID, "user_id": memberID})
}

type projectBindRequest struct {
	IDs []string `json:"ids"`
}

// GetProjectDatasets lists all tenant datasets with their binding to the
// project (drives the dataset binding management section).
func (h *Handler) GetProjectDatasets(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	projectID := c.Param("id")
	if err := h.Service.AuthorizeProject(ctx, actor, "read", "project", tenantID, projectID); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListProjectDatasets(ctx, tenantID, projectID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// BindProjectDatasets assigns the selected datasets to the project.
func (h *Handler) BindProjectDatasets(c *gin.Context) {
	var req projectBindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "ids is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	projectID := c.Param("id")
	if err := h.Service.BindProjectDatasets(ctx, tenantID, projectID, req.IDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.dataset.bind", Resource: "project", ResourceID: projectID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"project_id": projectID})
}

// GetProjectTeams lists all tenant teams with their binding to the project.
func (h *Handler) GetProjectTeams(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	projectID := c.Param("id")
	if err := h.Service.AuthorizeProject(ctx, actor, "read", "project", tenantID, projectID); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListProjectTeams(ctx, tenantID, projectID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// BindProjectTeams replaces the project's bound teams.
func (h *Handler) BindProjectTeams(c *gin.Context) {
	var req projectBindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "ids is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, actor, "manage", "project", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	projectID := c.Param("id")
	if err := h.Service.BindProjectTeams(ctx, tenantID, projectID, req.IDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "project.team.bind", Resource: "project", ResourceID: projectID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"project_id": projectID})
}
