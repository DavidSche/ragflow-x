package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AlertFilter narrows the operational alert list.
type AlertFilter struct {
	TenantID string
	Type     string
	Severity string
	Status   string
	Search   string
}

// AlertReview carries the actor and requested lifecycle state.
type AlertReview struct {
	Status string
	Actor  string
}

// AlertDeliveryFilter narrows persisted delivery lifecycle rows.
type AlertDeliveryFilter struct {
	TenantID string
	Channel  string
	Status   string
}

// AlertRepo persists and reviews dispatchable alert events.
type AlertRepo interface {
	CreateAlertEvent(ctx context.Context, event *model.AlertEvent) error
	GetAlertEvent(ctx context.Context, tenantID, eventID string, scopeAll bool) (*model.AlertEvent, error)
	ListAlertEvents(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter AlertFilter) ([]model.AlertEvent, int64, error)
	UpdateAlertEventStatus(ctx context.Context, tenantID, eventID string, scopeAll bool, review AlertReview) (bool, error)
	UpsertAlertDelivery(ctx context.Context, delivery *model.AlertDelivery) error
	GetAlertDelivery(ctx context.Context, tenantID string, scopeAll bool, alertEventID, channel string) (*model.AlertDelivery, error)
	ListAlertDeliveries(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter AlertDeliveryFilter) ([]model.AlertDelivery, int64, error)
	ListCompensableAlertDeliveries(ctx context.Context, before time.Time, limit int) ([]model.AlertDelivery, error)
	ClaimAlertDeliveryForManualRetry(ctx context.Context, tenantID string, scopeAll bool, alertEventID, channel, owner string, leaseExpiresAt time.Time) (*model.AlertDelivery, error)
	ClaimAlertDeliveryLease(ctx context.Context, alertEventID, channel, owner string, leaseExpiresAt, dueBefore time.Time) (*model.AlertDelivery, error)
	UpdateAlertDeliveryLease(ctx context.Context, alertEventID, channel, owner string, generation, attemptsDelta int64, delivery *model.AlertDelivery) (bool, error)
}

func (s *store) GetAlertEvent(ctx context.Context, tenantID, eventID string, scopeAll bool) (*model.AlertEvent, error) {
	query := s.WithContext(ctx).Where("id = ?", eventID)
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	}
	var event model.AlertEvent
	if err := query.First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

func (s *store) CreateAlertEvent(ctx context.Context, event *model.AlertEvent) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoNothing: true,
	}).Create(event).Error
}

func (s *store) alertScope(ctx context.Context, tenantID string, scopeAll bool, filter AlertFilter) *gorm.DB {
	query := s.WithContext(ctx).Model(&model.AlertEvent{})
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	} else if filter.TenantID != "" {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Severity != "" {
		query = query.Where("severity = ?", filter.Severity)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Search != "" {
		pattern := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(title) LIKE ? OR LOWER(detail) LIKE ?", pattern, pattern)
	}
	return query
}

func (s *store) ListAlertEvents(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter AlertFilter) ([]model.AlertEvent, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	query := s.alertScope(ctx, tenantID, scopeAll, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []model.AlertEvent
	if err := query.
		Order("occurred_at DESC, id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&events).Error; err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func (s *store) UpdateAlertEventStatus(ctx context.Context, tenantID, eventID string, scopeAll bool, review AlertReview) (bool, error) {
	query := s.WithContext(ctx).Model(&model.AlertEvent{}).Where("id = ?", eventID)
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	}
	now := time.Now().UTC()
	result := query.Updates(map[string]interface{}{
		"status":     review.Status,
		"acked_by":   review.Actor,
		"acked_at":   now,
		"updated_at": now,
	})
	return result.RowsAffected > 0, result.Error
}

func (s *store) UpsertAlertDelivery(ctx context.Context, delivery *model.AlertDelivery) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "alert_event_id"}, {Name: "channel"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"tenant_id":        delivery.TenantID,
			"status":           delivery.Status,
			"attempts":         gorm.Expr("rgx_alert_delivery.attempts + ?", delivery.Attempts),
			"last_error":       delivery.LastError,
			"last_attempt_at":  delivery.LastAttemptAt,
			"next_retry_at":    delivery.NextRetryAt,
			"lease_owner":      delivery.LeaseOwner,
			"lease_generation": delivery.LeaseGeneration,
			"lease_expires_at": delivery.LeaseExpiresAt,
			"delivered_at":     delivery.DeliveredAt,
			"updated_at":       delivery.UpdatedAt,
		}),
	}).Create(delivery).Error
}

func (s *store) ListAlertDeliveries(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter AlertDeliveryFilter) ([]model.AlertDelivery, int64, error) {
	query := s.WithContext(ctx).Model(&model.AlertDelivery{})
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	} else if filter.TenantID != "" {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.Channel != "" {
		query = query.Where("channel = ?", filter.Channel)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var deliveries []model.AlertDelivery
	offset, limit := paginate(page, pageSize)
	if err := query.
		Order("last_attempt_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&deliveries).Error; err != nil {
		return nil, 0, err
	}
	return deliveries, total, nil
}

func (s *store) GetAlertDelivery(ctx context.Context, tenantID string, scopeAll bool, alertEventID, channel string) (*model.AlertDelivery, error) {
	query := s.WithContext(ctx).
		Where("alert_event_id = ?", alertEventID).
		Where("channel = ?", channel)
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	}
	var delivery model.AlertDelivery
	if err := query.First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &delivery, nil
}

func (s *store) ListCompensableAlertDeliveries(ctx context.Context, before time.Time, limit int) ([]model.AlertDelivery, error) {
	if limit < 1 {
		limit = 20
	}
	var deliveries []model.AlertDelivery
	err := s.WithContext(ctx).
		Where(
			"(status = ? AND next_retry_at <= ?) OR (status = ? AND last_attempt_at <= ?)",
			model.AlertDeliveryStatusFailed, before,
			model.AlertDeliveryStatusPending, before.Add(-time.Minute),
		).
		Order("COALESCE(next_retry_at, last_attempt_at) ASC").
		Limit(limit).
		Find(&deliveries).Error
	return deliveries, err
}

func (s *store) ClaimAlertDeliveryForManualRetry(
	ctx context.Context, tenantID string, scopeAll bool, alertEventID, channel, owner string, leaseExpiresAt time.Time,
) (*model.AlertDelivery, error) {
	if owner == "" {
		return nil, errors.New("alert delivery lease owner is required")
	}
	now := time.Now().UTC()
	if !leaseExpiresAt.After(now) {
		return nil, errors.New("alert delivery lease expiry must be in the future")
	}
	query := s.WithContext(ctx).Model(&model.AlertDelivery{}).
		Where("alert_event_id = ? AND channel = ?", alertEventID, channel).
		Where("status IN ?", []string{
			model.AlertDeliveryStatusPending,
			model.AlertDeliveryStatusFailed,
			model.AlertDeliveryStatusAbandoned,
		}).
		Where("lease_expires_at IS NULL OR lease_expires_at <= ?", now)
	if !scopeAll {
		query = query.Where("tenant_id = ?", tenantID)
	}
	result := query.Updates(map[string]interface{}{
		"lease_owner":      owner,
		"lease_generation": gorm.Expr("COALESCE(lease_generation, 0) + 1"),
		"lease_expires_at": leaseExpiresAt,
		"updated_at":       now,
	})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	delivery, err := s.GetAlertDelivery(ctx, tenantID, scopeAll, alertEventID, channel)
	if err != nil || delivery == nil {
		return nil, err
	}
	return delivery, nil
}

func (s *store) ClaimAlertDeliveryLease(ctx context.Context, alertEventID, channel, owner string, leaseExpiresAt, dueBefore time.Time) (*model.AlertDelivery, error) {
	if owner == "" {
		return nil, errors.New("alert delivery lease owner is required")
	}
	now := time.Now().UTC()
	if !leaseExpiresAt.After(now) {
		return nil, errors.New("alert delivery lease expiry must be in the future")
	}
	result := s.WithContext(ctx).Model(&model.AlertDelivery{}).
		Where("alert_event_id = ? AND channel = ?", alertEventID, channel).
		Where("status IN ?", []string{model.AlertDeliveryStatusPending, model.AlertDeliveryStatusFailed}).
		Where(
			"(status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?) OR (status = ? AND last_attempt_at <= ?)",
			model.AlertDeliveryStatusFailed, dueBefore,
			model.AlertDeliveryStatusPending, dueBefore.Add(-time.Minute),
		).
		Where("lease_expires_at IS NULL OR lease_expires_at <= ?", now).
		Updates(map[string]interface{}{
			"lease_owner":      owner,
			"lease_generation": gorm.Expr("COALESCE(lease_generation, 0) + 1"),
			"lease_expires_at": leaseExpiresAt,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	var delivery model.AlertDelivery
	if err := s.WithContext(ctx).
		Where("alert_event_id = ? AND channel = ?", alertEventID, channel).
		First(&delivery).Error; err != nil {
		return nil, err
	}
	return &delivery, nil
}

func (s *store) UpdateAlertDeliveryLease(
	ctx context.Context, alertEventID, channel, owner string, generation, attemptsDelta int64,
	delivery *model.AlertDelivery,
) (bool, error) {
	if owner == "" {
		return false, errors.New("alert delivery lease owner is required")
	}
	now := time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.AlertDelivery{}).
		Where("alert_event_id = ? AND channel = ?", alertEventID, channel).
		Where("lease_owner = ? AND lease_generation = ?", owner, generation).
		Where("lease_expires_at > ?", now).
		Updates(map[string]interface{}{
			"status":           delivery.Status,
			"attempts":         gorm.Expr("COALESCE(attempts, 0) + ?", attemptsDelta),
			"last_error":       delivery.LastError,
			"last_attempt_at":  delivery.LastAttemptAt,
			"next_retry_at":    delivery.NextRetryAt,
			"delivered_at":     delivery.DeliveredAt,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
