package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// executeIMQuestion is the channel-neutral question pipeline shared by the
// WeCom/Feishu/DingTalk adapters (doc/125 §2.3). Channel files only supply
// identity resolution and the reply transport; authorization, command
// parsing, assistant routing, gateway execution and citation summarization
// stay here so business logic is never duplicated per channel (doc/107 §3.2.5).
//
// The returned string is the outbound reply text; the caller owns delivery.
func (s *Service) executeIMQuestion(
	ctx context.Context, defaultChatID string, target *WeComMessageTarget,
	question, requestID string, resolveIdentity func(ctx context.Context) (*model.User, error),
) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", httperr.BadRequest(40060, "question is required")
	}
	user, err := resolveIdentity(ctx)
	if err != nil {
		return "", err
	}
	target.UserID = user.ID
	target.TenantID = user.TenantID
	if err := s.Authorize(ctx, user.ID, "execute", "chat"); err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(question), "feedback") {
		command := parseIMCommand(question)
		if command == nil {
			return "", httperr.BadRequest(40090, "invalid feedback command")
		}
		command.UserID, command.ChatID = user.ID, target.ChatID
		if err := s.RecordWeComFeedback(ctx, command); err != nil {
			return "", err
		}
		return "反馈已记录，感谢您的意见。", nil
	}
	chatID, sessionID, questionText := imRoutingInput(question, defaultChatID)
	if chatID == "" {
		return "", httperr.BadRequest(40060, "assistant is required")
	}
	if strings.TrimSpace(questionText) == "" {
		return "", httperr.BadRequest(40060, "question is required")
	}
	target.ChatID = chatID
	target.SessionID = sessionID
	request := ragflow.CompletionRequest{Messages: []ragflow.Message{{Role: "user", Content: strings.TrimSpace(questionText)}}}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	key := &model.APIKey{TenantID: user.TenantID, UserID: user.ID}
	if err := s.AuthorizeChatTarget(ctx, key, chatID, sessionID); err != nil {
		return "", err
	}
	result, err := s.ChatAppCompletion(ctx, key, chatID, sessionID, rawRequest, requestID)
	if err != nil {
		return "", err
	}
	if result.Status >= 300 {
		return "", httperr.New(502, 50230, "ragflow chat completion failed")
	}
	var completion map[string]any
	if err := json.Unmarshal(result.Body, &completion); err != nil {
		return "", httperr.New(502, 50222, "read provider response failed")
	}
	answer, citations := wecomCompletionSummary(completion)
	if target.SessionID == "" {
		target.SessionID = stringFromAny(completion["session_id"])
	}
	target.MessageID = stringFromAny(completion["id"])
	s.recordCitationReferences(ctx, user.TenantID, chatID, target.SessionID, requestID, wecomCitations(completion))
	return answer + "\n\nTrace: " + requestID +
		"\nSession: " + target.SessionID +
		"\nMessage: " + target.MessageID +
		"\nCitations: " + itoa(citations), nil
}

// httpTimeoutClient returns the shared outbound HTTP client used by the IM
// adapters (single client, per-request contexts carry deadlines).
func (s *Service) httpTimeoutClient() *http.Client {
	if s.httpClient == nil {
		return &http.Client{Timeout: 10 * time.Second}
	}
	return s.httpClient
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
