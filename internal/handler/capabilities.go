package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

func (h *Handler) ListSystemCapabilities(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "system"); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, h.Service.ListRAGFlowCapabilities())
}

func (h *Handler) VerifySystemCapabilities(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "system"); err != nil {
		response.Err(c, err)
		return
	}
	report, err := h.Service.VerifyRAGFlowCapabilities(ctx)
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: userID,
		Action: "ragflow.capabilities.verify", Resource: "system", ResourceID: report.Provider,
		DetailJSON: report.RuntimeHealth, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, report)
}
