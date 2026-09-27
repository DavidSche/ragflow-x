package service

import (
	"bytes"
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

	// Retrieval-layer metadata pushdown (doc/123 §8.1): the evidence rides
	// into the projection regardless of pushdown success.
	pushdown := s.applyChatPushdownForChat(ctx, key.TenantID, chatID, &req)

	resp, err := s.RAGFlow.ChatCompletion(ctx, chatID, req)
	if err != nil {
		notify.Emit(ctx, notify.Event{
			Title: "ragflow chat completion failed", Severity: "error", TenantID: key.TenantID,
			Resource: "chat", Type: "provider_error", ResourceID: chatID, Detail: err.Error(),
		})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		s.recordFailedAnswerDelivery(ctx, key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, "", err.Error())
		return nil, httperr.New(502, 50230, "ragflow chat completion failed")
	}
	for index := range resp.Choices {
		resp.Choices[index].Message.Content = visibleRAGFlowAnswer(resp.Choices[index].Message.Content)
	}
	resp.Answer = visibleRAGFlowAnswer(resp.Answer)
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
	s.recordCitationReferences(ctx, key.TenantID, chatID, sessionID, requestID, resp.Reference)
	s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, answer, citationCountFromMaps(resp.Reference), tIn, tOut, int64(time.Since(startedAt).Milliseconds())))
	s.recordAnswerDeliveryWithPushdown(ctx, key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, answer, citationsFromProviderReferences(resp.Reference), pushdown)
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

	pushdown := s.applyChatPushdownForChat(ctx, key.TenantID, chatID, &req)

	var tIn, tOut int64
	capture := &knowledgeOpsStreamCapture{w: w}
	if err := s.RAGFlow.StreamChatCompletion(ctx, chatID, req, capture); err != nil {
		_ = capture.Flush()
		notify.Emit(ctx, notify.Event{
			Title: "ragflow stream chat completion failed", Severity: "error", TenantID: key.TenantID,
			Resource: "chat", Type: "provider_error", ResourceID: chatID, Detail: err.Error(),
		})
		s.ReleaseGatewayQuota(ctx, key, requestID)
		s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, "", 0, capture.tokensIn, capture.tokensOut, int64(time.Since(startedAt).Milliseconds())))
		s.recordFailedAnswerDelivery(ctx, key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, capture.answer.String(), err.Error())
		return 0, "text/event-stream", httperr.New(502, 50231, "ragflow stream chat completion failed")
	}
	if err := capture.Flush(); err != nil {
		return 0, "text/event-stream", httperr.Internal(err.Error())
	}
	tIn, tOut = capture.tokensIn, capture.tokensOut
	// Streams do not expose a model name in the passthrough; attribute to the chat.
	s.meterChat(ctx, key, chatID, chatID, sessionID, tIn, tOut, requestID)
	s.recordCitationReferences(ctx, key.TenantID, chatID, sessionID, requestID, capture.reference)
	s.recordKnowledgeEvent(ctx, knowledgeEvent(key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, capture.answer.String(), int64(capture.citations), tIn, tOut, int64(time.Since(startedAt).Milliseconds())))
	s.recordAnswerDeliveryWithPushdown(ctx, key.TenantID, key.UserID, "chat", chatID, sessionID, requestID, question, capture.answer.String(), citationsFromProviderReferences(capture.reference), pushdown)
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
		EstimatedCost: (float64(tIn) + float64(tOut)) / 1000 * s.EstimatedCostPer1K,
		Estimated:     tIn+tOut == 0, EstimationPolicyVersion: "v1",
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

// knowledgeOpsStreamCapture suppresses provider thinking output while
// accumulating visible content, references and token usage for the quality ledger.
type knowledgeOpsStreamCapture struct {
	w         io.Writer
	answer    strings.Builder
	citations int
	reference []map[string]interface{}
	tokensIn  int64
	tokensOut int64
	sawError  bool
	thinking  bool
	pending   []byte
}

func (c *knowledgeOpsStreamCapture) Write(p []byte) (int, error) {
	c.pending = append(c.pending, p...)
	for {
		index := bytes.IndexByte(c.pending, '\n')
		if index < 0 {
			break
		}
		line := string(c.pending[:index])
		c.pending = c.pending[index+1:]
		if err := c.writeLine(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (c *knowledgeOpsStreamCapture) writeLine(line string) error {
	_, err := c.w.Write([]byte(c.sanitizeSSELine(line) + "\n"))
	return err
}

func (c *knowledgeOpsStreamCapture) Flush() error {
	if c.thinking {
		c.pending = nil
		return nil
	}
	if len(c.pending) == 0 {
		return nil
	}
	line := string(c.pending)
	c.pending = nil
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "data:") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload != "" && payload != "[DONE]" {
			var frame map[string]any
			if json.Unmarshal([]byte(payload), &frame) != nil {
				return nil
			}
		}
	}
	return c.writeLine(line)
}

func (c *knowledgeOpsStreamCapture) sanitizeSSELine(line string) string {
	sseutil.TrackUsage(line, &c.tokensIn, &c.tokensOut)
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return line
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return line
	}
	var frame map[string]any
	if json.Unmarshal([]byte(payload), &frame) != nil {
		return line
	}
	if frame["error"] != nil {
		c.sawError = true
		return line
	}
	event := frame
	if inner, ok := frame["data"].(map[string]any); ok {
		event = inner
	}
	if c.boolFlag(event, "start_to_think") || c.boolFlag(frame, "start_to_think") {
		c.thinking = true
	}
	if c.boolFlag(event, "end_to_think") || c.boolFlag(frame, "end_to_think") {
		c.thinking = false
	}
	changed := false
	if value, changedContent := c.observeText(event["content"]); changedContent {
		event["content"] = value
		changed = true
	}
	if value, changedAnswer := c.observeText(event["answer"]); changedAnswer {
		event["answer"] = value
		changed = true
	}
	if changedChoice := c.observeChoices(event["choices"]); changedChoice {
		changed = true
	}
	if citations, ok := event["reference"].([]any); ok {
		c.citations = len(citations)
		c.reference = appendCitationObjects(c.reference, citations)
	} else if references, ok := event["references"].([]any); ok {
		c.citations = len(references)
		c.reference = appendCitationObjects(c.reference, references)
	} else if reference, ok := event["reference"].(map[string]any); ok {
		if chunks, ok := reference["chunks"].([]any); ok {
			c.citations = len(chunks)
			c.reference = appendCitationObjects(c.reference, chunks)
		}
	}
	if !changed {
		return line
	}
	sanitized, err := json.Marshal(frame)
	if err != nil {
		return line
	}
	return trimmed[:len(trimmed)-len(payload)] + string(sanitized)
}

func (c *knowledgeOpsStreamCapture) boolFlag(event map[string]any, key string) bool {
	value, ok := event[key].(bool)
	return ok && value
}

func (c *knowledgeOpsStreamCapture) observeChoices(choices any) bool {
	values, ok := choices.([]any)
	if !ok || len(values) == 0 {
		return false
	}
	return c.observeChoice(values[0])
}

func (c *knowledgeOpsStreamCapture) observeChoice(choice any) bool {
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	delta, ok := choiceMap["delta"].(map[string]any)
	if !ok {
		delta, ok = choiceMap["message"].(map[string]any)
		if !ok {
			return false
		}
	}
	if value, changed := c.observeText(delta["content"]); changed {
		delta["content"] = value
		return true
	}
	return false
}

func (c *knowledgeOpsStreamCapture) observeText(value any) (any, bool) {
	text, ok := value.(string)
	if !ok {
		return value, false
	}
	if c.thinking {
		return "", true
	}
	visible := visibleRAGFlowAnswer(text)
	c.answer.WriteString(visible)
	if visible == text {
		return text, false
	}
	return visible, true
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
