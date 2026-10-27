package handler

import (
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// UserChatCompletion is the JWT-authenticated chat endpoint used by the
// workbench. Unlike the gateway (/v1/chat/completions), it authenticates with
// the logged-in user's session and resolves a chat scoped to their tenant, so
// end users never need to paste an API key.
func (h *Handler) UserChatCompletion(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("request_id")
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid request body")
		return
	}
	chatID, sessionID := h.Service.ResolveChatTarget(body)
	chatID, err = h.Service.ResolveChat(ctx, tenantID, chatID)
	if err != nil {
		response.Err(c, err)
		return
	}
	// Metering context: no API key (login-based), attributed to the user's tenant.
	userKey := &model.APIKey{TenantID: tenantID, UserID: userID}

	var meta struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &meta)
	if meta.Stream {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(200)
		_, _, err := h.Service.StreamChatAppCompletion(ctx, userKey, chatID, sessionID, body, c.Writer, requestID)
		if err != nil {
			logger.Error("workbench chat stream failed", "tenant_id", tenantID, "user_id", userID, "request_id", requestID, "error", err)
			h.RecordAudit(ctx, &model.AuditLog{
				TenantID: tenantID, UserID: userID, Action: "gateway.stream_error", Resource: "chat", ResourceID: chatID,
				DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
			})
			gatewayStreamError(c.Writer, err)
		}
		return
	}
	res, err := h.Service.ChatAppCompletion(ctx, userKey, chatID, sessionID, body, requestID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "gateway.chat", Resource: "chat", ResourceID: chatID,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	c.Data(res.Status, "application/json", res.Body)
}
