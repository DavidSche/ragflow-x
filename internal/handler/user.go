package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type createUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID string `json:"tenant_id"`
}

// ListUsers lists users. Platform admins can request scope=all for every tenant.
func (h *Handler) ListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	page, pageSize := pageParams(c)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "user"); err != nil {
		response.Err(c, err)
		return
	}
	filter := repository.UserFilter{
		TenantID:    c.Query("tenant_id"),
		Username:    c.Query("username"),
		Status:      c.Query("status"),
		Role:        c.Query("role"),
		CreatedFrom: c.Query("created_from"),
		CreatedTo:   c.Query("created_to"),
	}
	if c.Query("scope") == "all" && h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "governance.read", "tenant") == nil {
		tenantID := filter.TenantID
		if tenantID != "" {
			if err := h.Service.EnsureTenantActive(ctx, tenantID); err != nil {
				response.Err(c, err)
				return
			}
		}
		items, total, err := h.Service.ListAllUsers(ctx, page, pageSize, filter)
		if err != nil {
			response.Err(c, err)
			return
		}
		response.OKPage(c, items, total, page, pageSize)
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	items, total, err := h.Service.ListUsers(ctx, tenantID, page, pageSize, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// ListRoles returns the assignable roles.
func (h *Handler) ListRoles(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "role"); err != nil {
		response.Err(c, err)
		return
	}
	filter := repository.RoleFilter{
		Name:  c.Query("name"),
		Scope: c.Query("scope"),
	}
	items, err := h.Service.ListRoles(c.Request.Context(), c.GetString(middleware.ContextRole), filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// CreateUser creates a tenant user. Requires manage permission on user.
// Platform admins may choose the target tenant; otherwise the actor's own
// tenant is used.
func (h *Handler) CreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid user payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actorRole := c.GetString(middleware.ContextRole)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "user"); err != nil {
		response.Err(c, err)
		return
	}
	if req.TenantID != "" {
		if h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "governance.manage", "tenant") != nil {
			response.Fail(c, 400, 40000, "only platform admins can choose a tenant")
			return
		}
		if err := h.Service.EnsureTenant(ctx, req.TenantID); err != nil {
			response.Err(c, err)
			return
		}
		tenantID = req.TenantID
	}
	u, err := h.Service.CreateUser(ctx, tenantID, actorRole, service.CreateUserRequest{
		Username: req.Username, Password: req.Password, Email: req.Email, Role: req.Role,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "user.create", Resource: "user", ResourceID: u.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, u)
}

type assignRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// AssignRole updates a user's role (cross-tenant for platform admins).
func (h *Handler) AssignRole(c *gin.Context) {
	var req assignRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid role payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actorRole := c.GetString(middleware.ContextRole)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "user"); err != nil {
		response.Err(c, err)
		return
	}
	targetID := c.Param("id")
	if targetID == userID {
		response.Fail(c, 409, 40906, "cannot change your own role")
		return
	}
	if err := h.Service.AssignRole(ctx, tenantID, actorRole, targetID, req.Role); err != nil {
		response.Err(c, err)
		return
	}
	if targetID != userID {
		h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "user.role", Resource: "user", ResourceID: targetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	}
	response.OK(c, gin.H{"id": targetID, "role": req.Role})
}

type userStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// SetUserStatus enables or disables a user.
func (h *Handler) SetUserStatus(c *gin.Context) {
	var req userStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid status payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actorRole := c.GetString(middleware.ContextRole)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "user"); err != nil {
		response.Err(c, err)
		return
	}
	targetID := c.Param("id")
	if targetID == userID {
		response.Fail(c, 409, 40907, "cannot change your own status")
		return
	}
	if err := h.Service.SetUserStatus(ctx, tenantID, actorRole, targetID, req.Status); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "user.status", Resource: "user", ResourceID: targetID, DetailJSON: req.Status, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": targetID, "status": req.Status})
}

// UpdateUserProfile edits a user's username/email/role and optionally resets
// the password.
func (h *Handler) UpdateUserProfile(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid user update payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actorRole := c.GetString(middleware.ContextRole)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "user"); err != nil {
		response.Err(c, err)
		return
	}
	targetID := c.Param("id")
	if targetID == userID && req.Role != "" {
		response.Fail(c, 409, 40906, "cannot change your own role")
		return
	}
	if err := h.Service.UpdateUserProfile(ctx, tenantID, actorRole, targetID, req.Username, req.Email, req.Role, req.Password); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "user.update", Resource: "user", ResourceID: targetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": targetID})
}

// DeleteUser removes a user. Platform admin users cannot be deleted.
func (h *Handler) DeleteUser(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	actorRole := c.GetString(middleware.ContextRole)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "user"); err != nil {
		response.Err(c, err)
		return
	}
	targetID := c.Param("id")
	if targetID == userID {
		response.Fail(c, 409, 40908, "cannot delete yourself")
		return
	}
	if err := h.Service.DeleteUser(ctx, tenantID, actorRole, targetID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "user.delete", Resource: "user", ResourceID: targetID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": targetID})
}
