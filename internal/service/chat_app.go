package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/sseutil"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// ResolveChatTarget reads the optional chat_id/session_id extension fields from
// an OpenAI-compatible request body. An empty chatID means the request targets
// the classic model-route gateway path.
func (s *Service) ResolveChatTarget(rawReq []byte) (chatID, sessionID string) {
	var b struct {
		ChatID    string `json:"chat_id"`
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(rawReq, &b)
	return b.ChatID, b.SessionID
}

// AuthorizeChatTarget verifies that an API key may invoke the requested chat
// and, when supplied, session. Chat Shadow is the platform's tenant ownership
// record; RAGFlow remains the engine and must never be trusted to make this
// decision for callers holding a tenant-scoped API key.
func (s *Service) AuthorizeChatTarget(ctx context.Context, key *model.APIKey, chatID, sessionID string) error {
	chat, err := s.getOwnedChat(ctx, key.TenantID, chatID, false)
	if err != nil {
		return httperr.New(502, 50232, "chat authorization lookup failed")
	}
	if chat == nil || chat.Status != model.TenantStatusActive {
		return httperr.NotFound("chat not found")
	}
	if sessionID != "" {
		if _, err := s.RAGFlow.GetChatSession(ctx, chatID, sessionID); err != nil {
			return httperr.NotFound("chat session not found")
		}
	}
	return nil
}

// ChatAppCompletion forwards a non-streaming request to a RAGFlow chat
// assistant (retrieval-backed via the engine) and meters the usage into both
// the daily aggregate and the per-request cost detail.
func (s *Service) ChatAppCompletion(ctx context.Context, key *model.APIKey, chatID, sessionID string, rawReq []byte, requestID string) (*ChatCompletionResult, error) {
	if err := s.AuthorizeChatTarget(ctx, key, chatID, sessionID); err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, err
	}
	var req ragflow.CompletionRequest
	if err := json.Unmarshal(rawReq, &req); err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return nil, httperr.BadRequest(40060, "invalid request body")
	}
	req.ChatID = chatID
	req.SessionID = sessionID
	req.Stream = false
	startedAt := time.Now()
	question := lastUserQuestion(req.Messages)

	resp, err := s.RAGFlow.ChatCompletion(ctx, chatID, req)
	if err != nil {
		notify.Emit(ctx, notify.Event{
			Title: "ragflow chat completion failed", Severity: "error", TenantID: key.TenantID,
			Resource: "chat", Type: "provider_error", ResourceID: chatID, Detail: err.Error(),
		})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		return nil, httperr.New(502, 50230, "ragflow chat completion failed")
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	tIn, tOut := completionUsage(resp)
	modelName := chatID
	if resp.Model != "" {
		modelName = resp.Model
	}
	s.meterChat(ctx, key, chatID, modelName, sessionID, tIn, tOut, requestID)
	answer := ""
	if resp != nil && len(resp.Choices) > 0 {
		answer = resp.Choices[0].Message.Content
	}
	s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, answer, 0, tIn, tOut, int64(time.Since(startedAt).Milliseconds())))
	s.bumpChatCount(ctx, chatID)
	s.FinalizeGatewayQuota(ctx, key, requestID, tIn+tOut)
	return &ChatCompletionResult{Status: 200, Body: body}, nil
}

// StreamChatAppCompletion streams a request to a RAGFlow chat assistant,
// passing the engine's SSE through unchanged while best-effort metering token
// usage from any OpenAI-style data blocks it contains.
func (s *Service) StreamChatAppCompletion(ctx context.Context, key *model.APIKey, chatID, sessionID string, rawReq []byte, w io.Writer, requestID string) (int, string, error) {
	if err := s.AuthorizeChatTarget(ctx, key, chatID, sessionID); err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", err
	}
	var req ragflow.CompletionRequest
	if err := json.Unmarshal(rawReq, &req); err != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return 0, "", httperr.BadRequest(40060, "invalid request body")
	}
	req.ChatID = chatID
	req.SessionID = sessionID
	req.Stream = true
	startedAt := time.Now()
	question := lastUserQuestion(req.Messages)

	var tIn, tOut int64
	capture := &knowledgeOpsStreamCapture{w: w}
	if err := s.RAGFlow.StreamChatCompletion(ctx, chatID, req, capture); err != nil {
		notify.Emit(ctx, notify.Event{
			Title: "ragflow stream chat completion failed", Severity: "error", TenantID: key.TenantID,
			Resource: "chat", Type: "provider_error", ResourceID: chatID, Detail: err.Error(),
		})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, "", 0, capture.tokensIn, capture.tokensOut, int64(time.Since(startedAt).Milliseconds())))
		return 0, "text/event-stream", httperr.New(502, 50231, "ragflow stream chat completion failed")
	}
	tIn, tOut = capture.tokensIn, capture.tokensOut
	// Streams do not expose a model name in the passthrough; attribute to the chat.
	s.meterChat(ctx, key, chatID, chatID, sessionID, tIn, tOut, requestID)
	s.recordCitationReferences(ctx, key.TenantID, chatID, sessionID, requestID, capture.reference)
	s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, capture.answer.String(), int64(capture.citations), tIn, tOut, int64(time.Since(startedAt).Milliseconds())))
	s.bumpChatCount(ctx, chatID)
	s.FinalizeGatewayQuota(ctx, key, requestID, tIn+tOut)
	return 200, "text/event-stream", nil
}

// meterChat records a chat-app request into the daily aggregate and the
// per-request cost detail, scoped to the key's tenant/user.
func (s *Service) meterChat(ctx context.Context, key *model.APIKey, chatID, modelName, sessionID string, tIn, tOut int64, requestID string) {
	date := time.Now().UTC().Format("2006-01-02")
	if _, err := s.Store.RecordUsage(ctx, &model.QuotaUsage{
		TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
		Date: date, TokensIn: tIn, TokensOut: tOut, Requests: 1,
	}); err != nil {
		logger.Warn("metering aggregate write failed", "tenant_id", key.TenantID, "user_id", key.UserID, "request_id", requestID, "error", err)
	}
	if _, err := s.Store.RecordCostMetric(ctx, &model.CostMetric{
		TenantID: key.TenantID, UserID: key.UserID, KeyID: key.ID, RequestID: requestID,
		Date: date, ChatID: chatID, Model: modelName, Scenario: "chat", SessionID: sessionID,
		TokensIn: tIn, TokensOut: tOut,
	}); err != nil {
		logger.Warn("metering detail write failed", "tenant_id", key.TenantID, "user_id", key.UserID, "request_id", requestID, "error", err)
	}
}

func completionUsage(resp *ragflow.CompletionResponse) (int64, int64) {
	if resp == nil || resp.Usage == nil {
		return 0, 0
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
}

// knowledgeOpsStreamCapture passes SSE through unchanged while accumulating
// the response content, reference count and token usage for the quality ledger.
type knowledgeOpsStreamCapture struct {
	w         io.Writer
	answer    strings.Builder
	citations int
	reference []map[string]interface{}
	tokensIn  int64
	tokensOut int64
	sawError  bool
}

func (c *knowledgeOpsStreamCapture) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		c.observe(line)
	}
	return c.w.Write(p)
}

func (c *knowledgeOpsStreamCapture) observe(line string) {
	sseutil.TrackUsage(line, &c.tokensIn, &c.tokensOut)
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	var frame map[string]any
	if json.Unmarshal([]byte(payload), &frame) != nil {
		return
	}
	if frame["error"] != nil {
		c.sawError = true
		return
	}
	inner, ok := frame["data"].(map[string]any)
	if !ok {
		if choices, ok := frame["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok {
						c.answer.WriteString(content)
					}
				}
			}
		}
		return
	}
	if content, ok := inner["content"].(string); ok {
		c.answer.WriteString(content)
	}
	if answer, ok := inner["answer"].(string); ok {
		c.answer.WriteString(answer)
	}
	if choices, ok := inner["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if delta, ok := choice["delta"].(map[string]any); ok {
				if content, ok := delta["content"].(string); ok {
					c.answer.WriteString(content)
				}
			}
		}
	}
	if citations, ok := inner["reference"].([]any); ok {
		c.citations = len(citations)
		c.reference = appendCitationObjects(c.reference, citations)
	} else if references, ok := inner["references"].([]any); ok {
		c.citations = len(references)
		c.reference = appendCitationObjects(c.reference, references)
	} else if reference, ok := inner["reference"].(map[string]any); ok {
		if chunks, ok := reference["chunks"].([]any); ok {
			c.citations = len(chunks)
			c.reference = appendCitationObjects(c.reference, chunks)
		}
	}
}

func appendCitationObjects(current []map[string]interface{}, values []any) []map[string]interface{} {
	for _, value := range values {
		if citation, ok := value.(map[string]interface{}); ok {
			current = append(current, citation)
		}
	}
	return current
}

// bumpChatCount increments the shadow chat's message counter after a turn.
func (s *Service) bumpChatCount(ctx context.Context, chatID string) {
	_ = s.Store.IncChatMessageCount(ctx, chatID)
}
