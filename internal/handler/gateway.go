package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// GatewayChat is the OpenAI-compatible gateway entry point. It authenticates a
// tenant API key and forwards the request either to a RAGFlow chat assistant
// (retrieval-backed; selected by the optional chat_id/session_id extension) or
// to the classic model-route provider. Streaming is passed through as SSE with
// metering and audit.
func (h *Handler) GatewayChat(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("request_id")
	raw := c.GetHeader("Authorization")
	if len(raw) > 7 && raw[:7] == "Bearer " {
		raw = raw[7:]
	}
	key, err := h.Service.ResolveAPIKey(ctx, raw)
	if err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.Authorize(ctx, key.UserID, "execute", "chat"); err != nil {
		response.Err(c, err)
		return
	}
	if err := h.Service.AuthorizeAPIKeyIP(key, c.ClientIP()); err != nil {
		h.RecordAudit(ctx, &model.AuditLog{
			TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.ip_denied", Resource: "chat",
			DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: requestID,
		})
		response.Err(c, err)
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid request body")
		return
	}

	idempotencyState, ierr := h.Service.BeginGatewayIdempotency(
		ctx, key, c.GetHeader("Idempotency-Key"), c.Request.Method,
		c.Request.URL.RequestURI(), body, requestID,
	)
	if ierr != nil {
		response.Err(c, ierr)
		return
	}
	if idempotencyState.Replayed {
		gatewayIdempotencyReplay(c, idempotencyState)
		return
	}

	// QoS A1: quota preauthorization runs before any provider call. When the
	// key carries a monthly token quota, an over-budget request is rejected
	// with 429 (never forwarded) and the remaining budget is advertised on the
	// response header so clients can pace themselves.
	quota, qerr := h.Service.PreauthorizeGatewayQuota(ctx, key, body, requestID)
	setGatewayQuotaHeaders(c, quota)
	if qerr != nil {
		if he, ok := qerr.(*httperr.Error); ok && he.Status == http.StatusTooManyRequests {
			h.RecordAudit(ctx, &model.AuditLog{
				TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.quota_exceeded", Resource: "chat",
				DetailJSON: qerr.Error(), IP: c.ClientIP(), TraceID: requestID,
			})
		}
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, qerr.Error())
		response.Err(c, qerr)
		return
	}

	var meta struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &meta)

	chatID, sessionID := h.Service.ResolveChatTarget(body)
	if chatID != "" {
		if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeRoutes, "chat"); err != nil {
			h.Service.ReleaseGatewayQuota(ctx, key, requestID)
			h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
			response.Err(c, err)
			return
		}
		if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeChatApps, chatID); err != nil {
			h.Service.ReleaseGatewayQuota(ctx, key, requestID)
			h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
			response.Err(c, err)
			return
		}
		if err := h.Service.AuthorizeChatTarget(ctx, key, chatID, sessionID); err != nil {
			h.Service.ReleaseGatewayQuota(ctx, key, requestID)
			h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
			response.Err(c, err)
			return
		}
		h.gatewayChatApp(c, ctx, key, chatID, sessionID, body, meta.Stream, requestID, idempotencyState)
		return
	}
	modelName := gatewayModelName(body)
	if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeModels, modelName); err != nil {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeRoutes, "chat"); err != nil {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	h.gatewayModelRoute(c, ctx, key, body, meta.Stream, requestID, idempotencyState)
}

// GatewaySearchApp exposes a controlled Search App execution path for API
// keys. It applies the same production controls as /v1/chat/completions.
func (h *Handler) GatewaySearchApp(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("request_id")
	key, err := h.resolveGatewayKey(c, requestID)
	if err != nil {
		return
	}
	if err := h.authorizeGatewayPath(c, ctx, key, "search-app", "execute", requestID); err != nil {
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid request body")
		return
	}
	idempotencyState, ierr := h.Service.BeginGatewayIdempotency(
		ctx, key, c.GetHeader("Idempotency-Key"), c.Request.Method,
		c.Request.URL.RequestURI(), body, requestID,
	)
	if ierr != nil {
		response.Err(c, ierr)
		return
	}
	if idempotencyState.Replayed {
		gatewayIdempotencyReplay(c, idempotencyState)
		return
	}
	quota, qerr := h.Service.PreauthorizeGatewayQuota(ctx, key, body, requestID)
	setGatewayQuotaHeaders(c, quota)
	if qerr != nil {
		h.gatewayQuotaFailed(c, ctx, key, "search-app", requestID, idempotencyState, qerr)
		return
	}
	var input struct {
		Question string `json:"question"`
		Stream   bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &input); err != nil || strings.TrimSpace(input.Question) == "" {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, "question is required")
		response.Fail(c, 400, 40000, "question is required")
		return
	}
	searchAppID := c.Param("id")
	if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeRoutes, "search-app"); err != nil {
		h.gatewayScopeFailed(c, ctx, key, "search-app", searchAppID, requestID, idempotencyState, err)
		return
	}
	if err := h.Service.AuthorizeGatewaySearchAppTarget(ctx, key, searchAppID); err != nil {
		h.gatewayScopeFailed(c, ctx, key, "search-app", searchAppID, requestID, idempotencyState, err)
		return
	}
	if input.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.WriteHeader(200)
		var captured bytes.Buffer
		if err := h.Service.StreamSearchAppCompletion(ctx, key.TenantID, searchAppID, input.Question, requestID, false, io.MultiWriter(c.Writer, &captured)); err != nil {
			h.gatewayStreamFailed(c, key, "search-app", searchAppID, requestID, idempotencyState, err)
			return
		}
		h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, captured.String())
		h.Service.FinalizeGatewayQuota(ctx, key, requestID, h.Service.EstimateGatewayTokens(body)+int64(captured.Len()/2))
		h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.search_app.stream", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: requestID})
		return
	}
	result, err := h.Service.SearchAppCompletion(ctx, key.TenantID, searchAppID, input.Question, requestID, false)
	if err != nil {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, "search-app:"+requestID)
	h.Service.FinalizeGatewayQuota(ctx, key, requestID, h.Service.EstimateGatewayTokens(body)+int64(len([]rune(result.Answer))/2))
	h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.search_app", Resource: "search-app", ResourceID: searchAppID, IP: c.ClientIP(), TraceID: requestID})
	response.OK(c, result)
}

// GatewayAgent exposes a controlled Agent completion path for API keys.
func (h *Handler) GatewayAgent(c *gin.Context) {
	ctx := c.Request.Context()
	requestID := c.GetString("request_id")
	key, err := h.resolveGatewayKey(c, requestID)
	if err != nil {
		return
	}
	if err := h.authorizeGatewayPath(c, ctx, key, "agent", "execute", requestID); err != nil {
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid request body")
		return
	}
	idempotencyState, ierr := h.Service.BeginGatewayIdempotency(
		ctx, key, c.GetHeader("Idempotency-Key"), c.Request.Method,
		c.Request.URL.RequestURI(), body, requestID,
	)
	if ierr != nil {
		response.Err(c, ierr)
		return
	}
	if idempotencyState.Replayed {
		gatewayIdempotencyReplay(c, idempotencyState)
		return
	}
	quota, qerr := h.Service.PreauthorizeGatewayQuota(ctx, key, body, requestID)
	setGatewayQuotaHeaders(c, quota)
	if qerr != nil {
		h.gatewayQuotaFailed(c, ctx, key, "agent", requestID, idempotencyState, qerr)
		return
	}
	var input struct {
		SessionID string                   `json:"session_id"`
		Stream    bool                     `json:"stream"`
		Messages  []ragflow.Message        `json:"messages"`
		Files     []map[string]interface{} `json:"files"`
	}
	if err := json.Unmarshal(body, &input); err != nil || len(input.Messages) == 0 {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, "messages are required")
		response.Fail(c, 400, 40000, "messages are required")
		return
	}
	agentID := c.Param("id")
	if err := h.Service.AuthorizeAPIKeyScope(key, service.APIKeyScopeRoutes, "agent"); err != nil {
		h.gatewayScopeFailed(c, ctx, key, "agent", agentID, requestID, idempotencyState, err)
		return
	}
	if err := h.Service.AuthorizeGatewayAgentTarget(ctx, key, agentID); err != nil {
		h.gatewayScopeFailed(c, ctx, key, "agent", agentID, requestID, idempotencyState, err)
		return
	}
	if input.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Writer.WriteHeader(200)
		var captured bytes.Buffer
		if err := h.Service.StreamAgentChatCompletion(ctx, key.TenantID, agentID, key.UserID, input.Messages, input.Files, requestID, input.SessionID, io.MultiWriter(c.Writer, &captured)); err != nil {
			h.gatewayStreamFailed(c, key, "agent", agentID, requestID, idempotencyState, err)
			return
		}
		h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, captured.String())
		h.Service.FinalizeGatewayQuota(ctx, key, requestID, h.Service.EstimateGatewayTokens(body)+int64(captured.Len()/2))
		h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.agent.stream", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: requestID})
		return
	}
	resp, err := h.Service.AgentChatCompletion(ctx, key.TenantID, agentID, key.UserID, input.Messages, input.Files, requestID, input.SessionID)
	if err != nil {
		h.Service.ReleaseGatewayQuota(ctx, key, requestID)
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, "agent:"+requestID)
	tokens := int64(len([]rune(resp.Choices[0].Message.Content)) / 2)
	if resp.Usage != nil {
		tokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}
	h.Service.FinalizeGatewayQuota(ctx, key, requestID, h.Service.EstimateGatewayTokens(body)+tokens)
	h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.agent", Resource: "agent", ResourceID: agentID, IP: c.ClientIP(), TraceID: requestID})
	response.OK(c, resp)
}

func (h *Handler) resolveGatewayKey(c *gin.Context, requestID string) (*model.APIKey, error) {
	raw := c.GetHeader("Authorization")
	if len(raw) > 7 && raw[:7] == "Bearer " {
		raw = raw[7:]
	}
	key, err := h.Service.ResolveAPIKey(c.Request.Context(), raw)
	if err != nil {
		response.Err(c, err)
		return nil, err
	}
	if err := h.Service.AuthorizeAPIKeyIP(key, c.ClientIP()); err != nil {
		h.RecordAudit(c.Request.Context(), &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.ip_denied", Resource: "gateway", DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: requestID})
		response.Err(c, err)
		return nil, err
	}
	return key, nil
}

// setGatewayQuotaHeaders exposes the same post-reservation budgets on every
// gateway entrypoint so API consumers can pace Chat, Search App and Agent
// traffic with one contract.
func setGatewayQuotaHeaders(c *gin.Context, quota service.QuotaResult) {
	if !quota.Enabled {
		return
	}
	if quota.TokenEnabled {
		c.Writer.Header().Set("X-Quota-Remaining", strconv.FormatInt(quota.Remaining, 10))
	}
	if quota.RequestEnabled {
		c.Writer.Header().Set("X-Quota-Requests-Remaining", strconv.FormatInt(quota.RemainingRequests, 10))
	}
}

func (h *Handler) authorizeGatewayPath(c *gin.Context, ctx context.Context, key *model.APIKey, resource, action, requestID string) error {
	if err := h.Service.Authorize(ctx, key.UserID, action, resource); err != nil {
		h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.denied", Resource: resource, DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: requestID})
		response.Err(c, err)
		return err
	}
	return nil
}

func (h *Handler) gatewayQuotaFailed(c *gin.Context, ctx context.Context, key *model.APIKey, resource, requestID string, state *service.GatewayIdempotencyState, qerr error) {
	if he, ok := qerr.(*httperr.Error); ok && he.Status == http.StatusTooManyRequests {
		h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.quota_exceeded", Resource: resource, DetailJSON: qerr.Error(), IP: c.ClientIP(), TraceID: requestID})
	}
	h.Service.FailGatewayIdempotency(ctx, state, requestID, qerr.Error())
	response.Err(c, qerr)
}

func (h *Handler) gatewayScopeFailed(c *gin.Context, ctx context.Context, key *model.APIKey, resource, resourceID, requestID string, state *service.GatewayIdempotencyState, err error) {
	h.Service.ReleaseGatewayQuota(ctx, key, requestID)
	h.Service.FailGatewayIdempotency(ctx, state, requestID, err.Error())
	h.RecordAudit(ctx, &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.scope_denied", Resource: resource, ResourceID: resourceID, DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: requestID})
	response.Err(c, err)
}

func (h *Handler) gatewayStreamFailed(c *gin.Context, key *model.APIKey, resource, resourceID, requestID string, state *service.GatewayIdempotencyState, err error) {
	logger.Error("gateway stream failed", "tenant_id", key.TenantID, "user_id", key.UserID, "key_id", key.ID, "request_id", requestID, "error", err)
	h.RecordAudit(c.Request.Context(), &model.AuditLog{TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.stream_error", Resource: resource, ResourceID: resourceID, DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: requestID})
	gatewayStreamError(c.Writer, err)
	h.Service.FailGatewayIdempotency(context.Background(), state, requestID, err.Error())
}

// gatewayChatApp routes to a RAGFlow chat assistant (retrieval-backed).
func (h *Handler) gatewayChatApp(c *gin.Context, ctx context.Context, key *model.APIKey, chatID, sessionID string, body []byte, stream bool, requestID string, idempotencyState *service.GatewayIdempotencyState) {
	if stream {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(200)
		var captured bytes.Buffer
		_, _, err := h.Service.StreamChatAppCompletion(ctx, key, chatID, sessionID, body, io.MultiWriter(c.Writer, &captured), requestID)
		if err != nil {
			logger.Error("gateway chat app stream failed", "tenant_id", key.TenantID, "user_id", key.UserID, "key_id", key.ID, "request_id", c.GetString("request_id"), "error", err)
			h.RecordAudit(ctx, &model.AuditLog{
				TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.stream_error", Resource: "chat", ResourceID: chatID,
				DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
			})
			gatewayStreamError(c.Writer, err)
			h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
			return
		}
		h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, captured.String())
		return
	}
	res, err := h.Service.ChatAppCompletion(ctx, key, chatID, sessionID, body, requestID)
	if err != nil {
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.chat", Resource: "chat", ResourceID: chatID,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, string(res.Body))
	c.Data(res.Status, "application/json", res.Body)
}

// gatewayModelRoute keeps the existing model-route forwarding path.
func (h *Handler) gatewayModelRoute(c *gin.Context, ctx context.Context, key *model.APIKey, body []byte, stream bool, requestID string, idempotencyState *service.GatewayIdempotencyState) {
	if stream {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(200)
		var captured bytes.Buffer
		_, _, err := h.Service.WriteStreamCompletion(ctx, key, body, io.MultiWriter(c.Writer, &captured), requestID)
		if err != nil {
			logger.Error("gateway stream failed", "tenant_id", key.TenantID, "user_id", key.UserID, "key_id", key.ID, "request_id", c.GetString("request_id"), "error", err)
			modelName := gatewayModelName(body)
			h.RecordAudit(ctx, &model.AuditLog{
				TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.stream_error", Resource: "chat", ResourceID: modelName,
				DetailJSON: err.Error(), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
			})
			gatewayStreamError(c.Writer, err)
			h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
			return
		}
		h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, captured.String())
		return
	}
	res, err := h.Service.ChatCompletion(ctx, key, body, requestID)
	if err != nil {
		h.Service.FailGatewayIdempotency(ctx, idempotencyState, requestID, err.Error())
		response.Err(c, err)
		return
	}
	modelName := gatewayModelName(body)
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: key.TenantID, UserID: key.UserID, Action: "gateway.chat", Resource: "chat", ResourceID: modelName,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	h.Service.CompleteGatewayIdempotency(ctx, idempotencyState, requestID, string(res.Body))
	c.Data(res.Status, "application/json", res.Body)
}

func gatewayIdempotencyReplay(c *gin.Context, state *service.GatewayIdempotencyState) {
	record := state.Record
	if record == nil {
		response.Err(c, httperr.New(502, 50240, "idempotency replay unavailable"))
		return
	}
	if record.Status == model.IdempotencyInProgress {
		response.Err(c, httperr.New(409, 40981, "IDEMPOTENCY_IN_PROGRESS"))
		return
	}
	if record.Status == model.IdempotencyExpired {
		response.Err(c, httperr.New(409, 40982, "idempotency record expired"))
		return
	}
	if record.Status == model.IdempotencyFailed || record.ResponseRef == "" {
		response.Err(c, httperr.New(502, 50241, "idempotent request previously failed"))
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/v1/chat/completions") && strings.Contains(record.ResponseRef, "\ndata:") {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(200)
		_, _ = io.WriteString(c.Writer, record.ResponseRef)
		return
	}
	c.Data(200, "application/json", []byte(record.ResponseRef))
}

// gatewayModelName extracts the request's model alias for audit correlation.
func gatewayModelName(body []byte) string {
	var b struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &b)
	return b.Model
}

// gatewayStreamError writes a terminal SSE error frame after streaming has
// already committed to a 200 text/event-stream response. It follows the
// OpenAI-compatible convention `data: {"error": {...}}` so standard clients
// and the workbench can recognize it as an error instead of content.
func gatewayStreamError(w io.Writer, err error) {
	code := 0
	message := "gateway request failed"
	if he, ok := err.(*httperr.Error); ok {
		code = he.Code
		message = he.Message
	}
	frame, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "gateway_error",
			"code":    code,
		},
	})
	_, _ = io.WriteString(w, "data: "+string(frame)+"\n\n")
}
