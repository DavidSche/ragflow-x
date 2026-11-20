package service

// Monthly assurance-allowance reconciliation (doc/301 §6.5, task T25).
//
// Two ledgers hold the same money:
//
//	bucket ledger   rgx_quota_bucket.tokens_used - rgx_quota_bucket.pending
//	run ledger      Σ rgx_report_assurance_run.quota_settled_tokens
//
// `pending` is subtracted because a reservation that is still in flight has been added to
// `tokens_used` but is not settled yet; comparing against `tokens_used` alone would show a
// drift exactly as large as the work currently running, which is a false alarm every time
// a review is in progress.
//
// The drift ceiling is §4.2's `budget_settlement_drift ≤ 1%`. Two things this file
// refuses to do:
//
//   - **Report a drift when there is nothing to compare.** No bucket (`token_limit = 0`
//     is "unlimited", which builds no bucket) or no settled runs both mean *no
//     denominator*; the result is `nil`, never 0.
//   - **Silently skip a tenant.** The sweep walks the buckets instead of a tenant list,
//     so a tenant whose allowance exists is a tenant that gets reconciled.

import (
	"context"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// AssuranceQuotaReconciliation is one tenant-period reconciliation result.
type AssuranceQuotaReconciliation struct {
	TenantID string `json:"tenant_id"`
	Period   string `json:"period"`

	// Bucket side.
	hasBucket  bool
	TokenLimit int64 `json:"token_limit"`
	TokensUsed int64 `json:"tokens_used"`
	Pending    int64 `json:"pending"`
	// Committed is `TokensUsed - Pending`: what the bucket believes was settled.
	Committed int64 `json:"committed"`

	// Run side.
	SettledTokens int64 `json:"settled_tokens"`
	SettledRuns   int64 `json:"settled_runs"`

	// Drift is nil when either side has no denominator.
	Drift       *float64 `json:"drift"`
	overCeiling bool
}

// OverCeiling reports whether this period's drift exceeds the §4.2 ceiling. It is false
// when the drift could not be measured — "not measured" is not "over the line", and it
// is not "within the line" either; the caller decides what to do with `Drift == nil`.
func (r *AssuranceQuotaReconciliation) OverCeiling() bool { return r.overCeiling }

// Measurable reports whether both ledgers had something to compare.
func (r *AssuranceQuotaReconciliation) Measurable() bool { return r.Drift != nil }

// ReconcileAssuranceQuota compares the two ledgers for one tenant and `YYYY-MM` period.
func (s *Service) ReconcileAssuranceQuota(ctx context.Context, tenantID, period string) (*AssuranceQuotaReconciliation, error) {
	result := &AssuranceQuotaReconciliation{TenantID: tenantID, Period: period}

	bucket, err := s.Store.GetQuotaBucket(ctx, tenantID, model.QuotaBucketDimensionAssurance, tenantID, period)
	if err != nil {
		return nil, err
	}
	if bucket != nil && bucket.TokenLimit > 0 {
		result.hasBucket = true
		result.TokenLimit = bucket.TokenLimit
		result.TokensUsed = bucket.TokensUsed
		result.Pending = bucket.Pending
		result.Committed = bucket.TokensUsed - bucket.Pending
	}

	settled, runs, err := s.Store.SumAssuranceSettledTokens(ctx, tenantID, period)
	if err != nil {
		return nil, err
	}
	result.SettledTokens = settled
	result.SettledRuns = runs

	if !result.hasBucket || result.SettledRuns == 0 {
		// Either the allowance is unlimited (no bucket is built at all) or no run has
		// settled anything. Both are "nothing to compare", and §6.5's own words are that
		// an unlimited tenant is *skipped* for this check but keeps its cost report.
		return result, nil
	}
	result.Drift = reportAssuranceDrift(result.SettledTokens, result.Committed)
	if result.Drift != nil && *result.Drift > releaseGateDriftCeiling {
		result.overCeiling = true
	}
	return result, nil
}

// assuranceSettlementReconcileInterval throttles the sweep's reconciliation.
//
// The assurance reconciler sweeps every minute because closing a dead run must be fast;
// reconciliation is a monthly-book question and re-reading every bucket per minute would
// be pure cost. One process holds one throttle: `architecture/02` runs a single
// `cmd/server`, and the sweep is guarded by `CountActiveJobsExcept`, so there is no
// second writer to coordinate with. The consequence is honest rather than precise: after a
// restart the first sweep reconciles immediately, and that is the right first move.
const assuranceSettlementReconcileInterval = 6 * time.Hour

var (
	assuranceSettlementMu     sync.Mutex
	assuranceSettlementLastAt time.Time
)

// reconcileAssuranceSettlement publishes the settlement drift of every tenant that has an
// assurance allowance in the current period.
func (s *Service) reconcileAssuranceSettlement(ctx context.Context, now time.Time) error {
	period := now.UTC().Format("2006-01")
	buckets, err := s.Store.ListAssuranceQuotaBuckets(ctx, model.QuotaBucketDimensionAssurance, period)
	if err != nil {
		return err
	}
	observer := obs.Get()
	for _, bucket := range buckets {
		result, err := s.ReconcileAssuranceQuota(ctx, bucket.TenantID, period)
		if err != nil || result == nil || result.Drift == nil {
			// A tenant whose drift cannot be measured keeps whatever the gauge said
			// last time rather than being reset to a healthy-looking zero.
			continue
		}
		if observer != nil {
			observer.SetAssuranceSettlementDrift(*result.Drift)
		}
	}
	return nil
}

// reconcileAssuranceSettlementThrottled is what the sweep calls.
func (s *Service) reconcileAssuranceSettlementThrottled(ctx context.Context, now time.Time) {
	assuranceSettlementMu.Lock()
	if !assuranceSettlementLastAt.IsZero() && now.Sub(assuranceSettlementLastAt) < assuranceSettlementReconcileInterval {
		assuranceSettlementMu.Unlock()
		return
	}
	assuranceSettlementLastAt = now
	assuranceSettlementMu.Unlock()

	if err := s.reconcileAssuranceSettlement(ctx, now); err != nil {
		logger.Warn("assurance allowance reconciliation failed", "error", err)
	}
}
