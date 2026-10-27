package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type SessionMessage struct {
	ragflow.Message
	RequestID string `json:"request_id,omitempty"`
}

func answerTraceKey(question, answer string) string {
	return sha256Hex(question + "\x1f" + answer)
}

func (s *Service) traceSessionMessages(
	ctx context.Context, tenantID, assistantID, sessionID string, items []ragflow.Message,
) []SessionMessage {
	traced := make([]SessionMessage, len(items))
	for index, item := range items {
		traced[index] = SessionMessage{Message: item}
	}
	if sessionID == "" {
		return traced
	}

	runs, err := s.Store.ListAnswerRunSnapshots(ctx, tenantID, assistantID, sessionID)
	if err != nil {
		logger.Warn("answer snapshot trace lookup failed",
			"tenant_id", tenantID, "assistant_id", assistantID,
			"session_id", sessionID, "error", err)
		return traced
	}
	pending := make(map[string][]string)
	for _, run := range runs {
		key := answerTraceKey(run.Snapshot.UserMessage, run.Snapshot.Content)
		pending[key] = append(pending[key], run.Run.RequestID)
	}

	previousQuestion := ""
	for index := range traced {
		switch traced[index].Role {
		case "user":
			previousQuestion = traced[index].Content
		case "assistant":
			if previousQuestion == "" {
				continue
			}
			key := answerTraceKey(previousQuestion, traced[index].Content)
			queue := pending[key]
			if len(queue) == 0 {
				continue
			}
			traced[index].RequestID = queue[0]
			pending[key] = queue[1:]
		}
	}
	return traced
}
