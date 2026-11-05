package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// ChatReferences returns the citation evidence (with in-document locator
// positions) for a chat turn so the forward UI can render the citation drawer
// and highlight the cited span (P2 citation experience, report §13.5.1).
// The chat is resolved through the tenant-owned scope; a cross-tenant chat
// reference leaks nothing because execution is authorized per tenant.
func (h *Handler) ChatReferences(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	refs, err := h.Service.ListChatReferences(ctx, tenantID, c.Param("id"), c.Query("session_id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, refs)
}
