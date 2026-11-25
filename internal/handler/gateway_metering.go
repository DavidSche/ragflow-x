package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// GatewayMeteringHealth reports the served requests whose metering never landed
// (doc/304 §4 E147): the counts by state plus the oldest unfinished intents.
//
// It lives next to `/usage` because "did our books get written" is a usage
// question, and it is guarded by the same `read usage` capability. `scope=all`
// widens it across tenants and needs `governance.read tenant`, exactly like
// `/usage/details`: the two scopes are two answers, and a caller that may read one
// tenant must not read the rest by guessing a query parameter.
func (h *Handler) GatewayMeteringHealth(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, userID, "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	scopeAll := c.Query("scope") == "all" && h.Service.Authorize(ctx, userID, "governance.read", "tenant") == nil
	health, err := h.Service.GatewayMeteringHealth(ctx, tenantID, scopeAll)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, health)
}
