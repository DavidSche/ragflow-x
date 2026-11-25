package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Gateway billing reconciliation store (doc/301 §5.10 GW-3 / T21).
//
// The repository's job is to hand the service one day of four ledgers and get out of
// the way: the arithmetic lives in a pure function, so the only thing that can be
// wrong here is a query. Every method below is read-only except the run/finding
// writers, which are insert-only for the same reason the assurance tables are — a
// reconciliation result is evidence, and evidence that can be edited is not evidence.

// GatewayBillingReconcileRepo is the persistence surface of the daily check.
type GatewayBillingReconcileRepo interface {
	// CreateGatewayBillingReconcileRunIfAbsent is the idempotency guard: the unique
	// (tenant, day) index decides, so a replayed job cannot reconcile the same day
	// twice and double-count its findings.
	CreateGatewayBillingReconcileRunIfAbsent(ctx context.Context, run *model.GatewayBillingReconcileRun) (bool, error)
	FinishGatewayBillingReconcileRun(ctx context.Context, run *model.GatewayBillingReconcileRun) error
	GetGatewayBillingReconcileRunByDay(ctx context.Context, tenantID, day string) (*model.GatewayBillingReconcileRun, error)
	ListGatewayBillingReconcileRuns(ctx context.Context, tenantID string, limit int) ([]model.GatewayBillingReconcileRun, error)
	InsertGatewayBillingReconcileFindings(ctx context.Context, findings []model.GatewayBillingReconcileFinding) error
	ListGatewayBillingReconcileFindings(ctx context.Context, tenantID, runID, kind string, limit int) ([]model.GatewayBillingReconcileFinding, error)
	// GatewayBillingSnapshot loads one tenant-day of the four ledgers plus the two
	// standing conditions (leaked reservations, balance drift).
	GatewayBillingSnapshot(ctx context.Context, tenantID, day string, from, to, staleBefore time.Time) (*model.GatewayBillingSnapshot, error)
	// GatewayBillingReconcileTenants lists the tenants with gateway activity in the
	// window. The sweep walks tenants, never a hand-picked list: a tenant that hit the
	// gateway must be reconciled even if nobody registered it anywhere.
	GatewayBillingReconcileTenants(ctx context.Context, from, to time.Time) ([]string, error)
}

func (s *store) CreateGatewayBillingReconcileRunIfAbsent(ctx context.Context, run *model.GatewayBillingReconcileRun) (bool, error) {
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(run)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// FinishGatewayBillingReconcileRun writes the terminal counters. It refuses to
// rewrite a run that already finished: a second finishing statement would let the
// same evidence change after the fact.
func (s *store) FinishGatewayBillingReconcileRun(ctx context.Context, run *model.GatewayBillingReconcileRun) error {
	res := s.WithContext(ctx).Model(&model.GatewayBillingReconcileRun{}).
		Where("id = ? AND status = ?", run.ID, model.GatewayBillingReconcileRunning).
		Updates(map[string]interface{}{
			"status":                 run.Status,
			"checked":                run.Checked,
			"findings":               run.Findings,
			"missing_meter":          run.MissingMeter,
			"missing_cost":           run.MissingCost,
			"charged_after_failure":  run.ChargedAfterFailure,
			"duplicate_request":      run.DuplicateRequest,
			"stale_reservation":      run.StaleReservation,
			"quota_drift_buckets":    run.QuotaDriftBuckets,
			"usage_drift":            run.UsageDrift,
			"quota_expected_tokens":  run.QuotaExpectedTokens,
			"quota_actual_tokens":    run.QuotaActualTokens,
			"usage_metered_requests": run.UsageMeteredRequests,
			"usage_billed_requests":  run.UsageBilledRequests,
			"failure_reason":         run.FailureReason,
			"finished_at":            run.FinishedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("gateway billing reconcile run is not running: %w", gorm.ErrRecordNotFound)
	}
	return nil
}

func (s *store) GetGatewayBillingReconcileRunByDay(ctx context.Context, tenantID, day string) (*model.GatewayBillingReconcileRun, error) {
	var run model.GatewayBillingReconcileRun
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND day = ?", tenantID, day).
		First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) ListGatewayBillingReconcileRuns(ctx context.Context, tenantID string, limit int) ([]model.GatewayBillingReconcileRun, error) {
	if limit <= 0 || limit > 90 {
		limit = 14
	}
	var runs []model.GatewayBillingReconcileRun
	err := s.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("day DESC").
		Limit(limit).
		Find(&runs).Error
	return runs, err
}

func (s *store) InsertGatewayBillingReconcileFindings(ctx context.Context, findings []model.GatewayBillingReconcileFinding) error {
	if len(findings) == 0 {
		return nil
	}
	return s.WithContext(ctx).Create(&findings).Error
}

func (s *store) ListGatewayBillingReconcileFindings(ctx context.Context, tenantID, runID, kind string, limit int) ([]model.GatewayBillingReconcileFinding, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := s.WithContext(ctx).Model(&model.GatewayBillingReconcileFinding{}).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	var findings []model.GatewayBillingReconcileFinding
	err := q.Order("kind ASC, request_id ASC").Limit(limit).Find(&findings).Error
	return findings, err
}

// GatewayBillingSnapshot loads the four ledgers for one tenant-day.
//
// Day scoping is deliberately per-ledger rather than one global window: the metering
// and cost rows carry a `date` column written by the request, while the retry and
// reservation ledgers only carry `created_at`. Using the wrong clock would silently
// move rows across the day boundary and manufacture findings at midnight.
func (s *store) GatewayBillingSnapshot(ctx context.Context, tenantID, day string, from, to, staleBefore time.Time) (*model.GatewayBillingSnapshot, error) {
	snapshot := &model.GatewayBillingSnapshot{}

	var idempotency []model.GatewayIdempotency
	if err := s.WithContext(ctx).Model(&model.GatewayIdempotency{}).
		Where("tenant_id = ? AND created_at >= ? AND created_at < ?", tenantID, from, to).
		Select("request_id, status, metering_state").
		Find(&idempotency).Error; err != nil {
		return nil, err
	}
	for _, row := range idempotency {
		snapshot.Idempotency = append(snapshot.Idempotency, model.GatewayBillingLedgerRef{
			RequestID: row.RequestID, Status: row.Status, MeteringState: row.MeteringState,
		})
	}

	// A request id claimed by more than one retry row is the replay-double-billing
	// shape; the database cannot forbid it (the unique key includes the caller's key).
	var duplicates []struct {
		RequestID string
		Total     int64
	}
	if err := s.WithContext(ctx).Model(&model.GatewayIdempotency{}).
		Select("request_id, COUNT(*) AS total").
		Where("tenant_id = ? AND created_at >= ? AND created_at < ?", tenantID, from, to).
		Group("request_id").
		Having("COUNT(*) > 1").
		Scan(&duplicates).Error; err != nil {
		return nil, err
	}
	for _, row := range duplicates {
		snapshot.DuplicateRequests = append(snapshot.DuplicateRequests, row.RequestID)
	}

	var metered []string
	if err := s.WithContext(ctx).Model(&model.MeterRequest{}).
		Where("tenant_id = ? AND date = ?", tenantID, day).
		Pluck("request_id", &metered).Error; err != nil {
		return nil, err
	}
	snapshot.Metered = metered

	var charged []model.CostMetric
	if err := s.WithContext(ctx).Model(&model.CostMetric{}).
		Where("tenant_id = ? AND date = ?", tenantID, day).
		Select("request_id, key_id").
		Find(&charged).Error; err != nil {
		return nil, err
	}
	for _, row := range charged {
		snapshot.Charged = append(snapshot.Charged, model.GatewayBillingLedgerRef{
			RequestID: row.RequestID, KeyID: row.KeyID,
		})
	}

	// The daily aggregate is the third ledger GW-3 names. It keeps no request ids,
	// so the only thing comparable is the count it billed for the day.
	var usageRequests int64
	if err := s.WithContext(ctx).Model(&model.QuotaUsage{}).
		Where("tenant_id = ? AND date = ?", tenantID, day).
		Select("COALESCE(SUM(requests), 0)").
		Scan(&usageRequests).Error; err != nil {
		return nil, err
	}
	snapshot.UsageRequests = usageRequests

	var stale []model.QuotaReservation
	if err := s.WithContext(ctx).Model(&model.QuotaReservation{}).
		Where("tenant_id = ? AND status = ? AND created_at < ?", tenantID, model.QuotaReservationPending, staleBefore).
		Select("request_id, key_id, period_start, estimated, created_at").
		Find(&stale).Error; err != nil {
		return nil, err
	}
	for _, row := range stale {
		snapshot.StaleReservations = append(snapshot.StaleReservations, model.GatewayBillingReservationRef{
			RequestID: row.RequestID, KeyID: row.KeyID, PeriodStart: row.PeriodStart,
			Estimated: row.Estimated, CreatedAt: row.CreatedAt,
		})
	}

	// The balance comparison is per (key, period), not per day: a key's used balance
	// accumulates across the whole month, so a day-scoped sum would disagree by design
	// and report a finding every midnight.
	var balances []model.QuotaLimit
	if err := s.WithContext(ctx).Model(&model.QuotaLimit{}).
		Where("tenant_id = ? AND period_start >= ?", tenantID, from.AddDate(0, -1, 0).Format("2006-01")).
		Select("key_id, period_start, tokens_used").
		Find(&balances).Error; err != nil {
		return nil, err
	}
	for _, balance := range balances {
		var settled int64
		if err := s.WithContext(ctx).Model(&model.QuotaReservation{}).
			Where("tenant_id = ? AND key_id = ? AND period_start = ? AND status = ?",
				tenantID, balance.KeyID, balance.PeriodStart, model.QuotaReservationFinalized).
			Select("COALESCE(SUM(actual_tokens), 0)").
			Scan(&settled).Error; err != nil {
			return nil, err
		}
		snapshot.Buckets = append(snapshot.Buckets, model.GatewayBillingBucketDrift{
			KeyID: balance.KeyID, PeriodStart: balance.PeriodStart,
			ExpectedTokens: settled, ActualTokens: balance.TokensUsed,
		})
	}
	return snapshot, nil
}

func (s *store) GatewayBillingReconcileTenants(ctx context.Context, from, to time.Time) ([]string, error) {
	var tenants []string
	union := s.WithContext(ctx).Raw(
		`SELECT tenant_id FROM rgx_gateway_idempotency WHERE created_at >= ? AND created_at < ?
		 UNION SELECT tenant_id FROM rgx_meter_request WHERE created_at >= ? AND created_at < ?`,
		from, to, from, to)
	rows, err := union.Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		if tenantID != "" {
			tenants = append(tenants, tenantID)
		}
	}
	return tenants, rows.Err()
}
