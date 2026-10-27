package repository

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertQuotaBucket lazily creates the (tenant_id, dimension, dimension_key,
// period_start) bucket row. It is idempotent: concurrent first writes converge
// on a single row via INSERT ... ON CONFLICT DO NOTHING. Limits <= 0 mean
// unlimited for that dimension; callers set the configured budget on create.
func (s *store) UpsertQuotaBucket(ctx context.Context, q *model.QuotaBucket) error {
	if q.ID == "" {
		q.ID = id.New()
	}
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "dimension_key"},
			{Name: "period_start"},
		},
		UpdateAll: true,
	}).Create(q).Error
}

// GetQuotaBucket returns the budget row for a bucket dimension/period.
func (s *store) GetQuotaBucket(ctx context.Context, tenantID, dimension, dimensionKey, periodStart string) (*model.QuotaBucket, error) {
	var q model.QuotaBucket
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ?",
			tenantID, dimension, dimensionKey, periodStart).
		First(&q).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// AddQuotaBucketReservation inserts the per-request reservation ledger row for
// a bucket. A retry carrying the same request_id is a no-op so reservations
// never double-count. It reports whether the row was actually inserted.
func (s *store) AddQuotaBucketReservation(ctx context.Context, r *model.QuotaBucketReservation) (bool, error) {
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(r)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// GetQuotaBucketReservation returns the bucket reservation ledger row by
// request_id.
func (s *store) GetQuotaBucketReservation(ctx context.Context, requestID string) (*model.QuotaBucketReservation, error) {
	var r model.QuotaBucketReservation
	err := s.WithContext(ctx).Where("request_id = ?", requestID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ReserveQuotaBucketTokens atomically adds an estimated token reservation
// and/or one logical request to the bucket's in-flight balances only when both
// configured limits would remain satisfied. The row-level guard is evaluated
// under the row's write lock via the guarded UPDATE, so concurrent
// reservations cannot overshoot. The returned values are the post-reservation
// token and request remainders (-1 when the corresponding budget is
// unlimited). When unable to satisfy the limits the applied reservation row is
// left pending-but-not-applied so the caller can roll it back exactly.
func (s *store) ReserveQuotaBucketTokens(ctx context.Context, tenantID, dimension, dimensionKey, periodStart, requestID string, est int64, reserveRequest bool) (bool, int64, int64, error) {
	updates := map[string]any{}
	if est > 0 {
		updates["pending"] = gorm.Expr("pending + ?", est)
	}
	if reserveRequest {
		updates["pending_requests"] = gorm.Expr("pending_requests + ?", 1)
	}
	if len(updates) == 0 {
		return true, -1, -1, nil
	}
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.QuotaBucket{}).
			Where(
				"tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ? AND (token_limit <= 0 OR tokens_used + pending + ? <= token_limit) AND (request_limit <= 0 OR requests_used + pending_requests + ? <= request_limit)",
				tenantID, dimension, dimensionKey, periodStart, est, boolToInt(reserveRequest),
			).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errQuotaExceeded
		}
		return tx.Model(&model.QuotaBucketReservation{}).
			Where("request_id = ?", requestID).
			Update("applied", true).Error
	})
	applied := true
	if err != nil {
		if errors.Is(err, errQuotaExceeded) {
			applied = false
		} else {
			return false, 0, 0, err
		}
	}
	q, err := s.GetQuotaBucket(ctx, tenantID, dimension, dimensionKey, periodStart)
	if err != nil {
		return false, 0, 0, err
	}
	if q == nil {
		return false, 0, 0, gorm.ErrRecordNotFound
	}
	return applied, quotaRemaining(q.TokenLimit, q.TokensUsed, q.Pending), quotaRemaining(q.RequestLimit, q.RequestsUsed, q.PendingRequests), nil
}

// FinalizeQuotaBucketReservation commits a successful meter against the
// bucket: marks the reservation finalized and moves the estimate out of pending
// while adding the actual consumed tokens to tokens_used. Idempotent per
// request_id.
func (s *store) FinalizeQuotaBucketReservation(ctx context.Context, requestID string, actual int64) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.QuotaBucketReservation
		if err := tx.Where("request_id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if r.Status != model.QuotaReservationPending || !r.Applied {
			return nil
		}
		if err := tx.Model(&model.QuotaBucketReservation{}).
			Where("request_id = ?", requestID).
			Updates(map[string]any{"status": model.QuotaReservationFinalized, "actual_tokens": actual}).Error; err != nil {
			return err
		}
		return tx.Model(&model.QuotaBucket{}).
			Where("tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ?",
				r.TenantID, r.Dimension, r.DimensionKey, r.PeriodStart).
			Updates(map[string]any{
				"tokens_used":      gorm.Expr("tokens_used + ?", actual),
				"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", r.Estimated, r.Estimated),
				"requests_used":    gorm.Expr("requests_used + ?", 1),
				"pending_requests": gorm.Expr("CASE WHEN pending_requests - 1 < 0 THEN 0 ELSE pending_requests - 1 END"),
			}).Error
	})
}

// ReleaseQuotaBucketReservation aborts a bucket reservation without consuming
// budget (provider error, closed connection): it releases pending estimates
// and marks the reservation released. Idempotent per request_id.
func (s *store) ReleaseQuotaBucketReservation(ctx context.Context, requestID string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.QuotaBucketReservation
		if err := tx.Where("request_id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if r.Status != model.QuotaReservationPending {
			return nil
		}
		if err := tx.Model(&model.QuotaBucketReservation{}).
			Where("request_id = ?", requestID).
			Update("status", model.QuotaReservationReleased).Error; err != nil {
			return err
		}
		if !r.Applied {
			return nil
		}
		return tx.Model(&model.QuotaBucket{}).
			Where("tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ?",
				r.TenantID, r.Dimension, r.DimensionKey, r.PeriodStart).
			Updates(map[string]any{
				"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", r.Estimated, r.Estimated),
				"pending_requests": gorm.Expr("CASE WHEN pending_requests - 1 < 0 THEN 0 ELSE pending_requests - 1 END"),
			}).Error
	})
}

// ReapStaleQuotaBucketReservations releases pending bucket reservations older
// than the cutoff so a crashed process cannot leave a stuck pending balance
// permanently blocking a near-exhausted bucket.
func (s *store) ReapStaleQuotaBucketReservations(ctx context.Context, tenantID, dimension, dimensionKey, periodStart string, before time.Time) (int64, error) {
	var released int64
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stale []model.QuotaBucketReservation
		if err := tx.Where("tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ? AND status = ? AND created_at < ?",
			tenantID, dimension, dimensionKey, periodStart, model.QuotaReservationPending, before).Find(&stale).Error; err != nil {
			return err
		}
		for _, r := range stale {
			if err := tx.Model(&model.QuotaBucket{}).
				Where("tenant_id = ? AND dimension = ? AND dimension_key = ? AND period_start = ?",
					r.TenantID, r.Dimension, r.DimensionKey, r.PeriodStart).
				Updates(map[string]any{
					"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", r.Estimated, r.Estimated),
					"pending_requests": gorm.Expr("CASE WHEN pending_requests - 1 < 0 THEN 0 ELSE pending_requests - 1 END"),
				}).Error; err != nil {
				return err
			}
			released += r.Estimated
		}
		return tx.Model(&model.QuotaBucketReservation{}).Where("request_id IN ?", collectBucketIDs(stale)).
			Updates(map[string]any{"status": model.QuotaReservationReleased}).Error
	})
	return released, err
}

func collectBucketIDs(rows []model.QuotaBucketReservation) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.RequestID)
	}
	return ids
}
