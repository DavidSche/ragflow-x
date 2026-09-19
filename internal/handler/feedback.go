package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ChatFeedback records a user's like/dislike on a chat turn.
func (h *Handler) ChatFeedback(c *gin.Context) {
	var req struct {
		ChatID    string `json:"chat_id" binding:"required"`
		SessionID string `json:"session_id" binding:"required"`
		MessageID string `json:"message_id" binding:"required"`
		Rating    string `json:"rating" binding:"required"`
		Comment   string `json:"comment"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid feedback payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	f, err := h.Service.RecordMessageFeedback(ctx, tenantID, userID, service.FeedbackRequest{
		ChatID: req.ChatID, SessionID: req.SessionID, MessageID: req.MessageID,
		Rating: req.Rating, Comment: req.Comment,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "chat.feedback", Resource: "chat", ResourceID: req.ChatID,
		DetailJSON: req.Rating, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, f)
}
