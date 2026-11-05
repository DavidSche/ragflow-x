package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func (s *store) CreateCitationReference(ctx context.Context, reference *model.CitationReference) error {
	return s.WithContext(ctx).Create(reference).Error
}

func (s *store) ListCitationReferences(ctx context.Context, tenantID, requestID string) ([]model.CitationReference, error) {
	rows := make([]model.CitationReference, 0)
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND request_id = ?", tenantID, requestID,
	).Order("created_at ASC").Find(&rows).Error
	return rows, err
}

// ListChatCitationReferences returns the citation evidence for a chat turn,
// optionally narrowed to a session. Entries are ordered by their reference
// index (answer-side [n] marker order) and then by retrieval time so the
// drawer renders the citations in the same order the user saw them.
func (s *store) ListChatCitationReferences(ctx context.Context, tenantID, chatID, sessionID string) ([]model.CitationReference, error) {
	rows := make([]model.CitationReference, 0)
	q := s.WithContext(ctx).Where("tenant_id = ? AND chat_id = ?", tenantID, chatID)
	if sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	err := q.Order("ref_index ASC, created_at ASC").Find(&rows).Error
	return rows, err
}
