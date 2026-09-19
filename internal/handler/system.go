package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// Dashboard returns the role-scoped operational summary.
func (h *Handler) Dashboard(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "dashboard"); err != nil {
		response.Err(c, err)
		return
	}
	out, err := h.Service.Dashboard(c.Request.Context(), userID, tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// SystemHealth returns engine and dependency health.
func (h *Handler) SystemHealth(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "system-health"); err != nil {
		response.Err(c, err)
		return
	}
	out, err := h.Service.SystemHealth(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// Ready returns 200 when dependencies are healthy, 503 otherwise (K8s readyz).
func (h *Handler) Ready(c *gin.Context) {
	if _, err := h.Service.SystemHealth(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"status": "not_ready", "error": "dependency unavailable"})
		return
	}
	c.JSON(200, gin.H{"status": "ready"})
}
