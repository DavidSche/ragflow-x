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
