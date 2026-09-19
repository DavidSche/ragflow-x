package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// FeedbackRequest carries a user's rating on a chat turn.
type FeedbackRequest struct {
	ChatID    string
	SessionID string
	MessageID string
	Rating    string
	Comment   string
	RequestID string
}

// RecordMessageFeedback records a user's like/dislike on a chat turn, scoped to
// the caller's tenant (chat ownership is verified). Re-submitting the same
// rating updates the comment, so a user can change their mind.
func (s *Service) RecordMessageFeedback(ctx context.Context, tenantID, userID string, req FeedbackRequest) (*model.MessageFeedback, error) {
	if req.ChatID == "" || req.SessionID == "" || req.MessageID == "" {
		return nil, httperr.BadRequest(40090, "chat_id, session_id and message_id are required")
	}
	if req.Rating != model.FeedbackPositive && req.Rating != model.FeedbackNegative {
		return nil, httperr.BadRequest(40091, "rating must be positive or negative")
	}
	cs, err := s.getOwnedChat(ctx, tenantID, req.ChatID, false)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	f := &model.MessageFeedback{
		TenantID: tenantID, UserID: userID, ChatID: req.ChatID,
		SessionID: req.SessionID, MessageID: req.MessageID, RequestID: req.RequestID,
		Rating: req.Rating, Comment: req.Comment,
	}
	if err := s.Store.UpsertMessageFeedback(ctx, f); err != nil {
		return nil, err
	}
	if _, err := s.Store.AttachMessageFeedbackToKnowledgeOpsEvent(ctx, tenantID, req.RequestID, f); err != nil {
		return nil, err
	}
	return f, nil
}
