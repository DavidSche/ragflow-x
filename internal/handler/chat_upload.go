package handler

import (
	"io"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// UploadChatFile accepts a multipart chat attachment and uploads it to the
// engine as a temporary blob, returning its metadata for attaching to messages.
func (h *Handler) UploadChatFile(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Fail(c, 400, 40000, "file is required")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		response.Fail(c, 400, 40000, "empty file")
		return
	}
	if header.Size > 20*1024*1024 {
		response.Fail(c, 400, 40000, "file too large (max 20MB)")
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		response.Err(c, err)
		return
	}
	uploaded, err := h.Service.UploadChatFile(ctx, header.Filename, data)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "chat.upload", Resource: "chat",
		ResourceID: uploaded.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, uploaded)
}
