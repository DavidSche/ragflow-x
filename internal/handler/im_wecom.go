package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// WeComCallback verifies the encrypted platform callback. GET handles WeCom's
// URL verification; POST acknowledges quickly and executes the question in the
// background because LLM completion can exceed the platform callback timeout.
func (h *Handler) WeComCallback(c *gin.Context) {
	cfg := h.Service.CurrentWeComConfig()
	signature := c.Query("msg_signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	if c.Request.Method == http.MethodGet {
		plaintext, err := h.Service.DecryptWeComEcho(
			cfg, timestamp, nonce, signature, c.Query("echostr"),
		)
		if err != nil {
			c.String(http.StatusUnauthorized, "invalid callback")
			return
		}
		c.String(http.StatusOK, plaintext)
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.String(http.StatusBadRequest, "invalid callback")
		return
	}
	callbackContext, err := h.Service.PrepareWeComCallback(
		c.Request.Context(),
		cfg, timestamp, nonce, signature, string(body),
	)
	if err != nil {
		c.String(http.StatusUnauthorized, "invalid callback")
		return
	}
	ack, err := h.Service.WeComResponseXML(cfg, timestamp, nonce, "已收到请求，正在处理")
	if err != nil {
		c.String(http.StatusInternalServerError, "callback ack failed")
		return
	}
	c.Data(http.StatusOK, "application/xml", []byte(ack))

	requestID := c.GetString("request_id")
	remoteIP := c.ClientIP()
	callback := callbackContext.Callback
	target := &service.WeComMessageTarget{
		UserID: callbackContext.User.ID, TenantID: callbackContext.User.TenantID,
		ChatID: cfg.DefaultChatID,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if callback.MsgType != "text" || callback.Content == "" {
			h.recordWeComAudit(ctx, requestID, remoteIP, target, "im.wecom.event", "")
			_ = h.Service.CompleteWeComCallback(ctx, callbackContext, "event")
			return
		}
		if err := h.Service.ExecuteWeComQuestion(ctx, target, callback.Content, requestID, callback.FromUser); err != nil {
			logger.Warn("wecom chat execution failed",
				"tenant_id", target.TenantID, "user_id", target.UserID,
				"request_id", requestID, "error", err)
			_ = h.Service.SendWeComText(ctx, callback.FromUser, "抱歉，本次处理失败，请稍后重试。")
			_ = h.Service.FailWeComCallback(ctx, callbackContext, err.Error())
			h.recordWeComAudit(ctx, requestID, remoteIP, target, "im.wecom.error", err.Error())
			return
		}
		_ = h.Service.CompleteWeComCallback(ctx, callbackContext, requestID)
		h.recordWeComAudit(ctx, requestID, remoteIP, target, "im.wecom.question", "")
	}()
}

func (h *Handler) recordWeComAudit(ctx context.Context, requestID, remoteIP string, target *service.WeComMessageTarget, action, detail string) {
	if err := h.Service.RecordAudit(ctx, &model.AuditLog{
		TenantID: target.TenantID, UserID: target.UserID, Action: action,
		Resource: "im-wecom", ResourceID: target.ChatID, DetailJSON: detail,
		IP: remoteIP, TraceID: requestID,
	}); err != nil {
		logger.Warn("wecom audit write failed", "request_id", requestID, "error", err)
	}
}
