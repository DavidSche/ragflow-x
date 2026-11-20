package repository

// The monthly assurance-allowance reconciliation behind doc/301 §6.5 and T25.
//
// §6.5 says the allowance lives in `rgx_quota_bucket` (dimension `assurance`) while the
// per-call cost lives on the evaluation rows and rolls up into
// `rgx_report_assurance_run.quota_settled_tokens`. Two ledgers, one truth: the drift
// between them is the only evidence that "reserve per call, settle after the call" worked.
//
// Therefore this file answers exactly one question per tenant and period: what did the
// runs *say* they spent? It returns the count of contributing runs too, because a sum of
// nothing is 0 and 0 looks like a perfectly balanced ledger.

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// SumAssuranceSettledTokens returns (settled tokens, contributing runs) for one tenant
// and quota period.
//
// Only terminal runs count: a `running` run has settled some calls but its own total is
// still moving, and including it would make the reconciliation disagree with itself
// between two reads a minute apart.
func (s *store) SumAssuranceSettledTokens(ctx context.Context, tenantID, period string) (int64, int64, error) {
	var row struct {
		Settled int64 `gorm:"column:settled"`
		Runs    int64 `gorm:"column:runs"`
	}
	err := s.WithContext(ctx).Model(&model.ReportAssuranceRun{}).
		Select("COALESCE(SUM(quota_settled_tokens), 0) AS settled, COUNT(*) AS runs").
		Where("tenant_id = ? AND quota_period = ?", tenantID, period).
		Where("status IN ?", []string{
			model.ReportAssuranceStatusSucceeded,
			model.ReportAssuranceStatusDegraded,
			model.ReportAssuranceStatusFailed,
		}).
		Scan(&row).Error
	if err != nil {
		return 0, 0, err
	}
	return row.Settled, row.Runs, nil
}

// ListAssuranceQuotaBuckets returns every assurance-dimension bucket of one period.
//
// The reconciliation sweeps tenants rather than being told about them: a tenant whose
// allowance silently stops being reconciled is exactly the tenant whose numbers are
// wrong, and nobody would notice because there would be no alert.
func (s *store) ListAssuranceQuotaBuckets(ctx context.Context, dimension, period string) ([]model.QuotaBucket, error) {
	var buckets []model.QuotaBucket
	err := s.WithContext(ctx).Model(&model.QuotaBucket{}).
		Where("dimension = ? AND period_start = ?", dimension, period).
		Order("tenant_id ASC").
		Find(&buckets).Error
	return buckets, err
}
