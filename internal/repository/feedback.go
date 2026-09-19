package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm/clause"
)

// FeedbackRepo persists per-turn chat feedback for quality improvement.
type FeedbackRepo interface {
	UpsertMessageFeedback(ctx context.Context, f *model.MessageFeedback) error
	ListMessageFeedback(ctx context.Context, tenantID, chatID string, page, pageSize int) ([]model.MessageFeedback, int64, error)
}

func (s *store) UpsertMessageFeedback(ctx context.Context, f *model.MessageFeedback) error {
	if f.ID == "" {
		f.ID = id.New()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	f.UpdatedAt = time.Now().UTC()
	assignments := map[string]interface{}{
		"rating": f.Rating, "comment": f.Comment, "updated_at": f.UpdatedAt,
	}
	if f.RequestID != "" {
		assignments["request_id"] = f.RequestID
	}
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_id"}, {Name: "session_id"}, {Name: "message_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(assignments),
	}).Create(f).Error
}

func (s *store) ListMessageFeedback(ctx context.Context, tenantID, chatID string, page, pageSize int) ([]model.MessageFeedback, int64, error) {
	var list []model.MessageFeedback
	var total int64
	q := s.WithContext(ctx).Model(&model.MessageFeedback{}).Where("tenant_id = ?", tenantID)
	if chatID != "" {
		q = q.Where("chat_id = ?", chatID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}
