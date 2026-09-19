package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// KnowledgeOpsRepo reads and dispositions the durable knowledge quality ledger.
type KnowledgeOpsRepo interface {
	UpsertKnowledgeOpsEvent(ctx context.Context, event *model.KnowledgeOpsEvent) error
	KnowledgeOpsSummary(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) (*model.KnowledgeOpsSummary, error)
	KnowledgeOpsFeedbackSummary(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) (positive, negative int64, err error)
	TopKnowledgeQueries(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string, limit int) ([]model.KnowledgeOpsQueryRow, error)
	ListKnowledgeOpsEvents(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter KnowledgeOpsFilter) ([]model.KnowledgeOpsEvent, int64, error)
	GetKnowledgeOpsEvent(ctx context.Context, tenantID, eventID string, scopeAll bool) (*model.KnowledgeOpsEvent, error)
	AttachMessageFeedbackToKnowledgeOpsEvent(ctx context.Context, tenantID, requestID string, feedback *model.MessageFeedback) (bool, error)
	ReviewKnowledgeOpsEvent(ctx context.Context, tenantID, eventID string, scopeAll bool, review KnowledgeOpsReview) (bool, error)
}

// KnowledgeOpsFilter controls the badcase list query.
type KnowledgeOpsFilter struct {
	AppType      string
	AppID        string
	Status       string
	ReviewStatus string
	Search       string
	DateFrom     string
	DateTo       string
}

// KnowledgeOpsReview carries a badcase disposition.
type KnowledgeOpsReview struct {
	Status string
	Note   string
	Actor  string
}

func (s *store) UpsertKnowledgeOpsEvent(ctx context.Context, event *model.KnowledgeOpsEvent) error {
	if event.RequestID == "" {
		return errors.New("request_id is required for knowledge ops")
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.ReviewStatus == "" {
		event.ReviewStatus = model.KnowledgeOpsReviewOpen
	}
	event.UpdatedAt = time.Now().UTC()
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "request_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"answer_excerpt":  event.AnswerExcerpt,
			"status":          event.Status,
			"citations_count": event.CitationsCount,
			"duration_ms":     event.DurationMs,
			"tokens_in":       event.TokensIn,
			"tokens_out":      event.TokensOut,
			"updated_at":      event.UpdatedAt,
		}),
	}).Create(event).Error
}

func knowledgeOpsDateRange(dateFrom, dateTo string) (time.Time, time.Time, bool) {
	from, ok := parseFilterDate(dateFrom)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	to := from.AddDate(0, 0, 1)
	if parsedTo, ok := parseFilterDate(dateTo); ok {
		if _, parseErr := time.Parse(time.RFC3339, dateTo); parseErr == nil {
			to = parsedTo
		} else {
			to = parsedTo.AddDate(0, 0, 1)
		}
	}
	return from, to, true
}

func (s *store) KnowledgeOpsSummary(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) (*model.KnowledgeOpsSummary, error) {
	var row struct {
		Total      int64   `gorm:"column:total_turns"`
		Users      int64   `gorm:"column:active_users"`
		Sessions   int64   `gorm:"column:active_sessions"`
		Completed  int64   `gorm:"column:completed"`
		NoAnswer   int64   `gorm:"column:no_answer"`
		Failed     int64   `gorm:"column:failed"`
		Citations  int64   `gorm:"column:with_citations"`
		TokensIn   int64   `gorm:"column:tokens_in"`
		TokensOut  int64   `gorm:"column:tokens_out"`
		Latency    float64 `gorm:"column:avg_latency_ms"`
		Resolution float64 `gorm:"column:avg_resolution_duration_ms"`
	}
	q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).Select(`
		COUNT(*) AS total_turns,
		COUNT(DISTINCT user_id) AS active_users,
		COUNT(DISTINCT CASE WHEN session_id <> '' THEN session_id END) AS active_sessions,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS completed,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS no_answer,
		SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed,
		SUM(CASE WHEN citations_count > 0 THEN 1 ELSE 0 END) AS with_citations,
		COALESCE(SUM(tokens_in), 0) AS tokens_in,
		COALESCE(SUM(tokens_out), 0) AS tokens_out
		,COALESCE(AVG(duration_ms), 0) AS avg_latency_ms
		,COALESCE(AVG(resolution_duration_ms), 0) AS avg_resolution_duration_ms
	`, model.KnowledgeOpsCompleted, model.KnowledgeOpsNoAnswer, model.KnowledgeOpsFailed)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if from, to, ok := knowledgeOpsDateRange(dateFrom, dateTo); ok {
		q = q.Where("created_at >= ? AND created_at < ?", from, to)
	}
	if err := q.Scan(&row).Error; err != nil {
		return nil, err
	}

	positive, negative, err := s.KnowledgeOpsFeedbackSummary(ctx, tenantID, scopeAll, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	summary := &model.KnowledgeOpsSummary{
		TotalTurns: row.Total, ActiveUsers: row.Users, ActiveSessions: row.Sessions,
		Completed: row.Completed, NoAnswer: row.NoAnswer, Failed: row.Failed,
		WithCitations: row.Citations, TokensIn: row.TokensIn, TokensOut: row.TokensOut,
		AvgLatencyMs:       row.Latency,
		AvgResolutionHours: row.Resolution / 3600000,
		Positive:           positive, Negative: negative,
	}
	if row.Total > 0 {
		summary.CitationRate = float64(row.Citations) / float64(row.Total)
		summary.NoAnswerRate = float64(row.NoAnswer) / float64(row.Total)
		summary.FailureRate = float64(row.Failed) / float64(row.Total)
	}
	if rated := positive + negative; rated > 0 {
		summary.SatisfactionRate = float64(positive) / float64(rated)
	}
	return summary, nil
}

func (s *store) KnowledgeOpsFeedbackSummary(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) (int64, int64, error) {
	type feedbackCounts struct {
		Positive int64 `gorm:"column:positive"`
		Negative int64 `gorm:"column:negative"`
	}
	var row feedbackCounts
	q := s.WithContext(ctx).Model(&model.MessageFeedback{}).Select(`
		SUM(CASE WHEN rating = ? THEN 1 ELSE 0 END) AS positive,
		SUM(CASE WHEN rating = ? THEN 1 ELSE 0 END) AS negative
	`, model.FeedbackPositive, model.FeedbackNegative)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if from, to, ok := knowledgeOpsDateRange(dateFrom, dateTo); ok {
		q = q.Where("created_at >= ? AND created_at < ?", from, to)
	}
	if err := q.Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.Positive, row.Negative, nil
}

func (s *store) TopKnowledgeQueries(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string, limit int) ([]model.KnowledgeOpsQueryRow, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	var rows []model.KnowledgeOpsQueryRow
	q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).
		Select(`
			question,
			MAX(question_hash) AS question_hash,
			COUNT(*) AS requests,
			MAX(created_at) AS last_asked_at,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS no_answer_count,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed_count,
			SUM(CASE WHEN citations_count = 0 THEN 1 ELSE 0 END) AS citation_missing_count
			,COALESCE(AVG(duration_ms), 0) AS avg_latency_ms
		`, model.KnowledgeOpsNoAnswer, model.KnowledgeOpsFailed).
		Group("question").
		Order("requests DESC, last_asked_at DESC").
		Limit(limit)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if from, to, ok := knowledgeOpsDateRange(dateFrom, dateTo); ok {
		q = q.Where("created_at >= ? AND created_at < ?", from, to)
	}
	err := q.Scan(&rows).Error
	return rows, err
}

func (s *store) ListKnowledgeOpsEvents(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter KnowledgeOpsFilter) ([]model.KnowledgeOpsEvent, int64, error) {
	var list []model.KnowledgeOpsEvent
	var total int64
	q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.AppType != "" {
		q = q.Where("app_type = ?", filter.AppType)
	}
	if filter.AppID != "" {
		q = q.Where("app_id = ?", filter.AppID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.ReviewStatus != "" {
		q = q.Where("review_status = ?", filter.ReviewStatus)
	}
	if filter.Search != "" {
		pattern := likePattern(filter.Search)
		q = q.Where("question LIKE ? ESCAPE '\\'", pattern)
	}
	if from, to, ok := knowledgeOpsDateRange(filter.DateFrom, filter.DateTo); ok {
		q = q.Where("created_at >= ? AND created_at < ?", from, to)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) GetKnowledgeOpsEvent(ctx context.Context, tenantID, eventID string, scopeAll bool) (*model.KnowledgeOpsEvent, error) {
	var event model.KnowledgeOpsEvent
	q := s.WithContext(ctx)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	} else if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Where("id = ?", eventID).First(&event).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &event, err
}

func (s *store) AttachMessageFeedbackToKnowledgeOpsEvent(ctx context.Context, tenantID, requestID string, feedback *model.MessageFeedback) (bool, error) {
	if requestID == "" || feedback == nil {
		return false, nil
	}
	feedbackAt := time.Now().UTC()
	res := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).
		Where("tenant_id = ? AND request_id = ? AND app_type = ? AND app_id = ? AND (session_id = ? OR session_id = '')",
			tenantID, requestID, "chat", feedback.ChatID, feedback.SessionID).
		Updates(map[string]interface{}{
			"feedback_id":      feedback.ID,
			"feedback_rating":  feedback.Rating,
			"feedback_comment": feedback.Comment,
			"feedback_at":      feedbackAt,
			"updated_at":       feedbackAt,
		})
	return res.RowsAffected > 0, res.Error
}

func (s *store) ReviewKnowledgeOpsEvent(ctx context.Context, tenantID, eventID string, scopeAll bool, review KnowledgeOpsReview) (bool, error) {
	now := time.Now().UTC()
	lookupQ := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).Where("id = ?", eventID)
	if !scopeAll {
		lookupQ = lookupQ.Where("tenant_id = ?", tenantID)
	}
	var event model.KnowledgeOpsEvent
	if err := lookupQ.Select("created_at").First(&event).Error; err != nil {
		return false, err
	}
	q := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).Where("id = ?", eventID)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	resolvedAt := now
	resolutionDurationMs := int64(0)
	if review.Status == model.KnowledgeOpsReviewResolved {
		resolutionDurationMs = now.Sub(event.CreatedAt).Milliseconds()
	}
	res := q.Updates(map[string]interface{}{
		"review_status":          review.Status,
		"review_note":            review.Note,
		"reviewed_by":            review.Actor,
		"reviewed_at":            now,
		"resolved_at":            resolvedAt,
		"resolution_duration_ms": resolutionDurationMs,
		"updated_at":             now,
	})
	return res.RowsAffected > 0, res.Error
}
