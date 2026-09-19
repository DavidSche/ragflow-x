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

// errQuotaExceeded is an internal sentinel distinguishing a rejected
// reservation (row guard did not match) from a real store failure.
var errQuotaExceeded = errors.New("quota exceeded")

// UpsertQuotaLimit lazily creates the (key_id, period_start) budget row. It is
// idempotent: concurrent first requests converge on a single row via
// INSERT ... ON CONFLICT DO NOTHING.
func (s *store) UpsertQuotaLimit(ctx context.Context, q *model.QuotaLimit) error {
	if q.ID == "" {
		q.ID = id.New()
	}
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key_id"}, {Name: "period_start"}},
		DoNothing: true,
	}).Create(q).Error
}

// GetQuotaLimit returns the budget row for a key/period.
func (s *store) GetQuotaLimit(ctx context.Context, keyID, periodStart string) (*model.QuotaLimit, error) {
	var q model.QuotaLimit
	err := s.WithContext(ctx).Where("key_id = ? AND period_start = ?", keyID, periodStart).First(&q).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// AddQuotaReservation inserts the per-request reservation ledger row. It
// reports whether the row was actually inserted; a retry carrying the same
// request_id is a no-op so reservations never double-count.
func (s *store) AddQuotaReservation(ctx context.Context, r *model.QuotaReservation) (bool, error) {
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(r)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ReserveQuota atomically adds an estimated token reservation and/or one
// logical request to the key's in-flight balances only when both configured
// limits would remain satisfied. The row-level guard is evaluated under the
// row's write lock, so concurrent reservations cannot overshoot. The returned
// values are the post-reservation token and request remainders (-1 when the
// corresponding budget is unlimited).
func (s *store) ReserveQuotaTokens(ctx context.Context, keyID, periodStart, requestID string, est int64, reserveRequest bool) (bool, int64, int64, error) {
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{}
		if est > 0 {
			updates["pending"] = gorm.Expr("pending + ?", est)
		}
		if reserveRequest {
			updates["pending_requests"] = gorm.Expr("pending_requests + ?", 1)
		}
		if len(updates) == 0 {
			return nil
		}
		res := tx.Model(&model.QuotaLimit{}).
			Where(
				"key_id = ? AND period_start = ? AND (token_limit <= 0 OR tokens_used + pending + ? <= token_limit) AND (request_limit <= 0 OR requests_used + pending_requests + ? <= request_limit)",
				keyID, periodStart, est, boolToInt(reserveRequest),
			).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errQuotaExceeded
		}
		if err := tx.Model(&model.QuotaReservation{}).
			Where("request_id = ?", requestID).
			Update("applied", true).Error; err != nil {
			return err
		}
		return nil
	})
	applied := true
	if err != nil {
		if errors.Is(err, errQuotaExceeded) {
			applied = false
		} else {
			return false, 0, 0, err
		}
	}
	q, err := s.GetQuotaLimit(ctx, keyID, periodStart)
	if err != nil {
		return false, 0, 0, err
	}
	if q == nil {
		return false, 0, 0, gorm.ErrRecordNotFound
	}
	return applied, quotaRemaining(q.TokenLimit, q.TokensUsed, q.Pending), quotaRemaining(q.RequestLimit, q.RequestsUsed, q.PendingRequests), nil
}

func quotaRemaining(limit, used, pending int64) int64 {
	if limit <= 0 {
		return -1
	}
	remaining := limit - used - pending
	if remaining < 0 {
		return 0
	}
	return remaining
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// GetQuotaReservation returns the reservation ledger row by request_id.
func (s *store) GetQuotaReservation(ctx context.Context, requestID string) (*model.QuotaReservation, error) {
	var r model.QuotaReservation
	err := s.WithContext(ctx).Where("request_id = ?", requestID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FinalizeQuotaReservation commits a successful meter: it marks the
// reservation finalized and moves est out of pending while adding the actual
// consumed tokens to tokens_used — exactly once for a given request_id.
func (s *store) FinalizeQuotaReservation(ctx context.Context, requestID string, actual int64) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.QuotaReservation
		if err := tx.Where("request_id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if r.Status != model.QuotaReservationPending {
			return nil
		}
		if !r.Applied {
			return nil
		}
		if err := tx.Model(&model.QuotaReservation{}).
			Where("request_id = ?", requestID).
			Updates(map[string]any{"status": model.QuotaReservationFinalized, "actual_tokens": actual}).Error; err != nil {
			return err
		}
		return tx.Model(&model.QuotaLimit{}).
			Where("key_id = ? AND period_start = ?", r.KeyID, r.PeriodStart).
			Updates(map[string]any{
				"tokens_used":      gorm.Expr("tokens_used + ?", actual),
				"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", r.Estimated, r.Estimated),
				"requests_used":    gorm.Expr("requests_used + ?", 1),
				"pending_requests": gorm.Expr("CASE WHEN pending_requests - 1 < 0 THEN 0 ELSE pending_requests - 1 END"),
			}).Error
	})
}

// ReleaseQuotaReservation aborts a reservation without consuming budget (e.g.
// provider error, closed connection): it releases the estimated pending tokens
// and marks the reservation released. Idempotent per request_id.
func (s *store) ReleaseQuotaReservation(ctx context.Context, requestID string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.QuotaReservation
		if err := tx.Where("request_id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if r.Status != model.QuotaReservationPending {
			return nil
		}
		if err := tx.Model(&model.QuotaReservation{}).
			Where("request_id = ?", requestID).
			Update("status", model.QuotaReservationReleased).Error; err != nil {
			return err
		}
		if r.Applied {
			if err := tx.Model(&model.QuotaLimit{}).
				Where("key_id = ? AND period_start = ?", r.KeyID, r.PeriodStart).
				Updates(map[string]any{
					"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", r.Estimated, r.Estimated),
					"pending_requests": gorm.Expr("CASE WHEN pending_requests - 1 < 0 THEN 0 ELSE pending_requests - 1 END"),
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ReapStaleQuotaReservations releases pending reservations older than the
// cutoff (crashes never put their reservation back, so they must age out or a
// stuck pending balance would permanently block a near-exhausted key). It
// returns the number of estimated tokens released.
func (s *store) ReapStaleQuotaReservations(ctx context.Context, keyID, periodStart string, before time.Time) (int64, error) {
	var released int64
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stale []model.QuotaReservation
		if err := tx.Where("key_id = ? AND period_start = ? AND status = ? AND created_at < ?",
			keyID, periodStart, model.QuotaReservationPending, before).Find(&stale).Error; err != nil {
			return err
		}
		if len(stale) == 0 {
			return nil
		}
		ids := make([]string, 0, len(stale))
		releasedRequests := int64(0)
		for i := range stale {
			ids = append(ids, stale[i].RequestID)
			if stale[i].Applied {
				released += stale[i].Estimated
				releasedRequests++
			}
		}
		if err := tx.Model(&model.QuotaReservation{}).
			Where("request_id IN ?", ids).
			Update("status", model.QuotaReservationReleased).Error; err != nil {
			return err
		}
		return tx.Model(&model.QuotaLimit{}).
			Where("key_id = ? AND period_start = ?", keyID, periodStart).
			Updates(map[string]any{
				"pending":          gorm.Expr("CASE WHEN pending - ? < 0 THEN 0 ELSE pending - ? END", released, released),
				"pending_requests": gorm.Expr("CASE WHEN pending_requests - ? < 0 THEN 0 ELSE pending_requests - ? END", releasedRequests, releasedRequests),
			}).Error
	})
	return released, err
}
