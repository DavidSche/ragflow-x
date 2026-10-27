package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// ListAuditAnchors returns immutable audit-chain tail snapshots.
func (h *Handler) ListAuditAnchors(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "audit-anchor"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	scopeAll := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.read", "tenant") == nil
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
