package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// CreateMeteringIntentIfAbsent writes the write-ahead intent for one request and
// reports whether it was new. ON CONFLICT DO NOTHING on the unique `request_id`
// makes the prepare step idempotent: a retried request with the same id resolves
// to the row that already exists instead of creating a second intent.
func (s *store) CreateMeteringIntentIfAbsent(ctx context.Context, m *model.MeteringIntent) (bool, error) {
	if m == nil || m.TenantID == "" || m.RequestID == "" {
		// Tenant and request id are the key and the attribution; the user is not,
		// because a search app may be owned by no one and its consumption still
		// belongs to the tenant.
		return false, errors.New("metering intent requires tenant and request id")
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(m)
	return res.RowsAffected > 0, res.Error
}

// GetMeteringIntent returns the intent for a request id, or nil when the request
// never reached the metering point. Nil and "row with state=none" are the same
// answer, and the service resolves that into the `none` vocabulary value.
func (s *store) GetMeteringIntent(ctx context.Context, requestID string) (*model.MeteringIntent, error) {
	if requestID == "" {
		return nil, nil
	}
	var intent model.MeteringIntent
	err := s.WithContext(ctx).Where("request_id = ?", requestID).First(&intent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &intent, nil
}

// SettleMeteringIntent records the outcome of one attempt batch. `attempts` is
// added, not assigned: the column's job is to distinguish "one transient error"
// from "retried every five minutes for a day", and overwriting it would erase
// exactly that.
func (s *store) SettleMeteringIntent(ctx context.Context, id, state, lastError string, attempts int) error {
	now := time.Now().UTC()
	updates := map[string]interface{}{
		"state": state, "last_error": lastError, "updated_at": now,
		"attempts": gorm.Expr("attempts + ?", attempts),
	}
	if state == model.MeteringRecorded {
		updates["recorded_at"] = now
	}
	return s.WithContext(ctx).Model(&model.MeteringIntent{}).Where("id = ?", id).Updates(updates).Error
}

// ListMeteringIntentsByState returns intents overdue for a recovery pass: the
// state says they are unfinished, and `created_at` is older than the grace
// window so an in-flight request is never replayed underneath itself.
func (s *store) ListMeteringIntentsByState(ctx context.Context, states []string, olderThan time.Time, limit int) ([]model.MeteringIntent, error) {
	var list []model.MeteringIntent
	if len(states) == 0 {
		return list, nil
	}
	if limit <= 0 {
		limit = 100
	}
	err := s.WithContext(ctx).Model(&model.MeteringIntent{}).
		Where("state IN ?", states).
		Where("created_at <= ?", olderThan).
		Order("created_at ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

// CountMeteringIntentsByState is the coverage denominator: "how many intents are
// still unfinished" has to be answerable without paginating a list.
func (s *store) CountMeteringIntentsByState(ctx context.Context, states []string) (int64, error) {
	var n int64
	if len(states) == 0 {
		return 0, nil
	}
	err := s.WithContext(ctx).Model(&model.MeteringIntent{}).Where("state IN ?", states).Count(&n).Error
	return n, err
}

// CountMeteringIntentsForTenant returns how many intents sit in each of the given
// states for one tenant (or all tenants when scopeAll is set). It is the reader for
// the metering gap (doc/304 §4 E147): "how much is owed" has to be answerable from
// a screen, not only from a metric that a dashboard may not be scraping.
func (s *store) CountMeteringIntentsForTenant(ctx context.Context, tenantID string, scopeAll bool, states []string) (map[string]int64, error) {
	out := make(map[string]int64, len(states))
	if len(states) == 0 {
		return out, nil
	}
	type row struct {
		State string
		N     int64
	}
	var rows []row
	q := s.WithContext(ctx).Model(&model.MeteringIntent{}).
		Select("state, count(*) as n").
		Where("state IN ?", states)
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.Group("state").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, st := range states {
		out[st] = 0
	}
	for _, r := range rows {
		out[r.State] = r.N
	}
	return out, nil
}

// ListUnsettledMeteringIntents returns the oldest unfinished intents — the
// worklist behind the counts, so a reader sees *which* requests are owed and why.
func (s *store) ListUnsettledMeteringIntents(ctx context.Context, tenantID string, scopeAll bool, limit int) ([]model.MeteringIntent, error) {
	var list []model.MeteringIntent
	if limit <= 0 {
		limit = 20
	}
	q := s.WithContext(ctx).Model(&model.MeteringIntent{}).
		Where("state IN ?", []string{model.MeteringPending, model.MeteringFailed})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Order("created_at ASC").Limit(limit).Find(&list).Error
	return list, err
}

// CountUnsettledMeteringIntentsByTenant returns, per tenant, how many intents are
// still `pending` or `failed`. The sweep publishes one gauge series per tenant from
// it, so a repaired tenant can disappear instead of leaving a stale value behind.
func (s *store) CountUnsettledMeteringIntentsByTenant(ctx context.Context) (map[string]int64, error) {
	type row struct {
		TenantID string
		N        int64
	}
	var rows []row
	err := s.WithContext(ctx).Model(&model.MeteringIntent{}).
		Select("tenant_id, count(*) as n").
		Where("state IN ?", []string{model.MeteringPending, model.MeteringFailed}).
		Group("tenant_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.TenantID] = r.N
	}
	return out, nil
}
