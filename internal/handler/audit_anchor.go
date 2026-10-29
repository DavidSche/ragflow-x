package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ListAuditAnchors returns immutable audit-chain tail snapshots.
func (h *Handler) ListAuditAnchors(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "audit-anchor"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	// Audit-chain anchors are audit data: a delegated auditor holding only
	// tenant.audit.read may list them across workspaces (doc/248 / R-26).
	scopeAll := h.Service.CanAny(c.Request.Context(), c.GetString(middleware.ContextUserID), service.GovernanceReadRefs("audit")...)
	items, total, err := h.Service.ListAuditAnchors(
		c.Request.Context(),
		c.GetString(middleware.ContextTenantID),
		scopeAll,
		page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}
