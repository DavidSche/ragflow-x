package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type teamRequest struct {
	Name    string `json:"name" binding:"required"`
	OwnerID string `json:"owner_id"`
}

// ListTeams lists the tenant's teams the caller may see (admin: all; others:
// teams they own or belong to).
func (h *Handler) ListTeams(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	role := c.GetString(middleware.ContextRole)
	if err := h.Service.Authorize(c.Request.Context(), userID, "read", "team"); err != nil {
		response.Err(c, err)
		return
	}
	filter := repository.TeamFilter{
		Name:        c.Query("name"),
		CreatedFrom: c.Query("created_from"),
		CreatedTo:   c.Query("created_to"),
	}
	items, err := h.Service.ListTeams(c.Request.Context(), tenantID, userID, role, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// GetTeam returns a single team with tenant/owner names.
func (h *Handler) GetTeam(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.AuthorizeTeamRead(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	team, err := h.Service.GetTeamView(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, team)
}

// CreateTeam creates a tenant team; the owner (if any) becomes team_admin.
func (h *Handler) CreateTeam(c *gin.Context) {
	var req teamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid team payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.AuthorizeObject(ctx, userID, "manage", "team", tenantID, ""); err != nil {
		response.Err(c, err)
		return
	}
	t, err := h.Service.CreateTeam(ctx, tenantID, req.Name, req.OwnerID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "team.create", Resource: "team", ResourceID: t.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, t)
}

// UpdateTeam updates a tenant team (owner transfer reassigns team_admin).
func (h *Handler) UpdateTeam(c *gin.Context) {
	var req teamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid team payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	id := c.Param("id")
	if err := h.Service.AuthorizeTeamManage(ctx, userID, tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	t, err := h.Service.UpdateTeam(ctx, tenantID, id, req.Name, req.OwnerID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "team.update", Resource: "team", ResourceID: id, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, t)
}

// DeleteTeam deletes a tenant team.
func (h *Handler) DeleteTeam(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	id := c.Param("id")
	if err := h.Service.AuthorizeTeamManage(ctx, userID, tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.DeleteTeam(ctx, tenantID, id); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "team.delete", Resource: "team", ResourceID: id, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": id})
}

// ListTeamUsers lists the members of a team.
func (h *Handler) ListTeamUsers(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	teamID := c.Param("id")
	if err := h.Service.AuthorizeTeamRead(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, teamID); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListTeamUsers(c.Request.Context(), tenantID, teamID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// AddTeamUser binds a user to a team (owner or tenant admin only).
func (h *Handler) AddTeamUser(c *gin.Context) {
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
	teamID := c.Param("id")
	if err := h.Service.AuthorizeTeamManage(ctx, actor, tenantID, teamID); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.AddUserToTeam(ctx, tenantID, teamID, req.UserID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "team.member.add", Resource: "team", ResourceID: teamID, DetailJSON: req.UserID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"team_id": teamID, "user_id": req.UserID})
}

// RemoveTeamUser unbinds a user from a team (owner or tenant admin only).
func (h *Handler) RemoveTeamUser(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actor := c.GetString(middleware.ContextUserID)
	teamID := c.Param("id")
	if err := h.Service.AuthorizeTeamManage(ctx, actor, tenantID, teamID); err != nil {
		response.Err(c, err)
		return
	}
	memberID := c.Param("userId")
	if err := h.Service.RemoveUserFromTeam(ctx, tenantID, teamID, memberID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: actor, Action: "team.member.remove", Resource: "team", ResourceID: teamID, DetailJSON: memberID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"team_id": teamID, "user_id": memberID})
}

// ListTeamProjects returns the projects authorized to a tenant team.
func (h *Handler) ListTeamProjects(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	teamID := c.Param("id")
	if err := h.Service.AuthorizeTeamRead(ctx, c.GetString(middleware.ContextUserID), tenantID, teamID); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListTeamProjects(ctx, tenantID, teamID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// BindTeamProjects replaces the projects authorized to a tenant team.
func (h *Handler) BindTeamProjects(c *gin.Context) {
	var req struct {
		ProjectIDs []string `json:"project_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid team project binding payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	teamID := c.Param("id")
	if err := h.Service.AuthorizeTeamManage(ctx, userID, tenantID, teamID); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.SetTeamProjects(ctx, tenantID, teamID, req.ProjectIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "team.project.bind", Resource: "team", ResourceID: teamID, DetailJSON: strings.Join(req.ProjectIDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"team_id": teamID, "project_ids": req.ProjectIDs})
}
