package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// ChatChunk deep-links to a source chunk referenced by a chat answer. It uses
// RAGFlow dataset/doc/chunk ids and enforces tenant ownership of the dataset.
func (h *Handler) ChatChunk(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	ck, err := h.Service.GetChatChunk(ctx, tenantID, c.Query("dataset"), c.Query("doc"), c.Query("chunk"), false)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ck)
}

// ChatDocChunks lists a chat-referenced document's chunks for the full-document viewer.
func (h *Handler) ChatDocChunks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	chunks, total, err := h.Service.ChatDocumentChunks(
		ctx, tenantID, c.Query("dataset"), c.Query("doc"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, chunks, total, page, pageSize)
}

// ChatDocPreview streams the original file of a chat-referenced document.
func (h *Handler) ChatDocPreview(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	data, ct, err := h.Service.ChatDocumentPreview(ctx, tenantID, c.Query("dataset"), c.Query("doc"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(200, ct, data)
}

// ChatImage streams a referenced chunk image.
func (h *Handler) ChatImage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, c.GetString(middleware.ContextUserID), "read", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	data, ct, err := h.Service.ChatImage(
		ctx, tenantID, c.Query("dataset"), c.Query("doc"), c.Query("chunk"), c.Query("image"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(200, ct, data)
}
