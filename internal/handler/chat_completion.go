package handler

import (
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
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
		// A14: the attachment is validated inside the service, before the first byte.
		// Committing the 200 here would turn `400/44105` into an error frame inside a
		// stream that the client already considers started.
		stream := newDeferredStreamWriter(c.Writer)
		_, _, err := h.Service.StreamChatAppCompletion(ctx, userKey, chatID, sessionID, body, stream, requestID)
		if err != nil {
			// The client leaving is not a gateway failure. When the request
			// context is already dead nothing can be written back and nothing
			// is owed: logging it as an error (and auditing it) made a closed
			// tab indistinguishable from a broken upstream, which is how six
			// truncated answers of 2026-11-27 were filed before the cause was
			// found.
			if c.Request.Context().Err() != nil {
				logger.Info("chat stream abandoned by client", "tenant_id", tenantID, "user_id", userID, "request_id", requestID)
				return
			}
			if !stream.Committed() {
				// Nothing was written: this was a refused request, not a broken
				// stream, and the comment said so while the code logged it as a
				// failure anyway. Live run of 2026-11-28 filed a documented
				// 400/44105 (`report_context` typo, probe R25 case ②) as an ERROR
				// line plus a `gateway.stream_error` row, an action
				// doc/architecture/233 defines as "failed after 200 was
				// committed". A 4xx is the caller's; a 5xx is still ours.
				if internal, code := streamRefusalSeverity(err); internal {
					logger.Error("workbench chat stream refused by an internal failure", "tenant_id", tenantID, "user_id", userID, "request_id", requestID, "code", code, "error", err)
				} else {
					logger.Info("workbench chat stream refused before the first byte", "tenant_id", tenantID, "user_id", userID, "request_id", requestID, "code", code)
				}
				response.Err(c, err)
				return
			}
			logger.Error("workbench chat stream failed", "tenant_id", tenantID, "user_id", userID, "request_id", requestID, "error", err)
			auditCtx, auditCancel := service.StreamBookkeepingContext(ctx)
			defer auditCancel()
			h.RecordAudit(auditCtx, &model.AuditLog{
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
