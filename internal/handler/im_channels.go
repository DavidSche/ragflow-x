package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// writeCallbackError maps a verification failure to its httperr status
// (doc/125 §1.5): disabled channel → 404, incomplete configuration → 503,
// signature/token/replay failures → 401. Non-typed errors stay 401 so the
// callback never leaks internals to the platform.
func writeCallbackError(c *gin.Context, err error) {
	var he *httperr.Error
	if errors.As(err, &he) && he.Status != http.StatusUnauthorized {
		c.String(he.Status, "invalid callback")
		return
	}
	c.String(http.StatusUnauthorized, "invalid callback")
}

// FeishuCallback answers the url_verification handshake synchronously and
// acknowledges message events with 200 before executing the question in the
// background (the LLM completion can exceed the platform callback timeout).
// The security contract mirrors the WeCom adapter: signature/token checks,
// replay window, dedupe and fail-closed identity mapping (doc/125 §2.4).
func (h *Handler) FeishuCallback(c *gin.Context) {
	cfg := h.Service.CurrentFeishuConfig()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.String(http.StatusBadRequest, "invalid callback")
		return
	}

	if challenge, verifyErr := h.Service.DecryptFeishuURLVerification(cfg, body); verifyErr == nil {
		c.Data(http.StatusOK, "application/json", []byte(challenge))
		return
	} else if isVerificationBody(body) {
		writeCallbackError(c, verifyErr)
		return
	}

	event, err := h.Service.DecryptFeishuEvent(cfg, body)
	if err != nil {
		writeCallbackError(c, err)
		return
	}
	ack, err := json.Marshal(map[string]int{"code": 0})
	if err != nil {
		c.String(http.StatusInternalServerError, "callback ack failed")
		return
	}
	c.Data(http.StatusOK, "application/json", ack)

	requestID := c.GetString("request_id")
	remoteIP := c.ClientIP()
	target := &service.WeComMessageTarget{ChatID: cfg.DefaultChatID}
	subject := event.Sender.SenderID.OpenID
	if subject == "" {
		subject = event.Sender.SenderID.UserID
	}
	if subject == "" {
		subject = event.Sender.SenderID.UnionID
	}
	callbackContext, prepareErr := h.Service.PrepareFeishuCallback(c.Request.Context(), cfg, event)
	if prepareErr != nil {
		logger.Warn("feishu callback prepare failed", "request_id", requestID, "error", prepareErr)
		return
	}
	target.UserID = callbackContext.User.ID
	target.TenantID = callbackContext.User.TenantID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := h.Service.ExecuteFeishuQuestion(ctx, cfg, target, event.Message.Content, requestID, subject); err != nil {
			logger.Warn("feishu chat execution failed",
				"tenant_id", target.TenantID, "user_id", target.UserID,
				"request_id", requestID, "error", err)
			_ = h.Service.SendFeishuText(ctx, cfg, subject, "抱歉，本次处理失败，请稍后重试。")
			_ = h.Service.FailWeComCallback(ctx, callbackContext, err.Error())
			h.recordIMAudit(ctx, requestID, remoteIP, target, "im.feishu.error", "im-feishu", err.Error())
			return
		}
		_ = h.Service.CompleteWeComCallback(ctx, callbackContext, requestID)
		h.recordIMAudit(ctx, requestID, remoteIP, target, "im.feishu.question", "im-feishu", "")
	}()
}

// DingTalkCallback verifies the enterprise robot signature and answers text
// messages on the caller's sessionWebhook. Ack + background execution keeps
// the handler inside the platform callback timeout (doc/125 §2.4).
func (h *Handler) DingTalkCallback(c *gin.Context) {
	cfg := h.Service.CurrentDingTalkConfig()
	timestamp := c.Query("timestamp")
	sign := c.Query("sign")
	if timestamp == "" {
		timestamp = c.GetHeader("timestamp")
		sign = c.GetHeader("sign")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.String(http.StatusBadRequest, "invalid callback")
		return
	}
	callback, err := h.Service.DecryptDingTalkCallback(cfg, timestamp, sign, body)
	if err != nil {
		writeCallbackError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", []byte(`{"code":0}`))

	requestID := c.GetString("request_id")
	remoteIP := c.ClientIP()
	target := &service.WeComMessageTarget{ChatID: cfg.DefaultChatID}
	callbackContext, prepareErr := h.Service.PrepareDingTalkCallback(c.Request.Context(), cfg, timestamp, callback)
	if prepareErr != nil {
		logger.Warn("dingtalk callback prepare failed", "request_id", requestID, "error", prepareErr)
		return
	}
	target.UserID = callbackContext.User.ID
	target.TenantID = callbackContext.User.TenantID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := h.Service.ExecuteDingTalkQuestion(ctx, cfg, target, callback.Text.Content, requestID, callback.SenderStaffID, callback.SessionWebhook); err != nil {
			logger.Warn("dingtalk chat execution failed",
				"tenant_id", target.TenantID, "user_id", target.UserID,
				"request_id", requestID, "error", err)
			_ = h.Service.SendDingTalkMarkdown(ctx, cfg, callback.SessionWebhook, "RAGFlow-X", "抱歉，本次处理失败，请稍后重试。")
			_ = h.Service.FailWeComCallback(ctx, callbackContext, err.Error())
			h.recordIMAudit(ctx, requestID, remoteIP, target, "im.dingtalk.error", "im-dingtalk", err.Error())
			return
		}
		_ = h.Service.CompleteWeComCallback(ctx, callbackContext, requestID)
		h.recordIMAudit(ctx, requestID, remoteIP, target, "im.dingtalk.question", "im-dingtalk", "")
	}()
}

func (h *Handler) recordIMAudit(ctx context.Context, requestID, remoteIP string, target *service.WeComMessageTarget, action, resource, detail string) {
	if err := h.Service.RecordAudit(ctx, &model.AuditLog{
		TenantID: target.TenantID, UserID: target.UserID, Action: action,
		Resource: resource, ResourceID: target.ChatID, DetailJSON: detail,
		IP: remoteIP, TraceID: requestID,
	}); err != nil {
		logger.Warn("im audit write failed", "request_id", requestID, "error", err)
	}
}

// isVerificationBody distinguishes the handshake request from an event so a
// failed handshake is reported as 401 instead of being treated as an event.
func isVerificationBody(body []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Type == "url_verification"
}
