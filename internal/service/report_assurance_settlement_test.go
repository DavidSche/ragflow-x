package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// settlementFixture is a tenant with an assurance allowance and a set of runs whose
// settled tokens can be dialled per test.
type settlementFixture struct {
	svc      *Service
	store    repository.Store
	ctx      context.Context
	tenantID string
	period   string
}

func newSettlementFixture(t *testing.T, tokenLimit int64, tokensUsed, pending int64) *settlementFixture {
	t.Helper()
	fx := newReportFixture(t, "assurance-settlement")
	svc, ctx := fx.svc, context.Background()
	store := svc.Store
	period := "2026-03"
	if tokenLimit > 0 {
		if err := store.EnsureQuotaBucketTokenLimit(
			ctx, fx.tenantID, model.QuotaBucketDimensionAssurance, fx.tenantID, period, tokenLimit); err != nil {
			t.Fatal(err)
		}
		if tokensUsed != 0 || pending != 0 {
			if err := store.UpsertQuotaBucket(ctx, &model.QuotaBucket{
				TenantID: fx.tenantID, Dimension: model.QuotaBucketDimensionAssurance,
				DimensionKey: fx.tenantID, PeriodStart: period, TokenLimit: tokenLimit,
				TokensUsed: tokensUsed, Pending: pending,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return &settlementFixture{svc: svc, store: store, ctx: ctx, tenantID: fx.tenantID, period: period}
}

// addRun writes one terminal run with the given settled tokens.
func (f *settlementFixture) addRun(t *testing.T, id string, settled int64, status string) {
	t.Helper()
	run := &model.ReportAssuranceRun{
		ID: id, TenantID: f.tenantID, ReportID: "report-" + id, RevisionID: "revision-" + id,
		Status: status, Mode: model.ReportAssuranceModeCitationAudit,
		RequestedPolicy: model.ReportAssurancePolicyAdvisory,
		QuotaPeriod:     f.period, QuotaSettledTokens: settled,
		IdempotencyKey: "assurance:" + id, CreatedAt: time.Now().UTC(),
	}
	if err := f.store.CreateAssuranceRun(f.ctx, run); err != nil {
		t.Fatalf("create run %s: %v", id, err)
	}
}

// TestAssuranceReconciliationBalancesAgainstPending — the bucket's `tokens_used` includes
// in-flight reservations, so the comparison must subtract `pending`; otherwise a review
// that is simply still running looks like an accounting error.
func TestAssuranceReconciliationBalancesAgainstPending(t *testing.T) {
	f := newSettlementFixture(t, 1_000_000, 5_000, 1_000)
	f.addRun(t, "run-a", 3_000, model.ReportAssuranceStatusSucceeded)
	f.addRun(t, "run-b", 1_000, model.ReportAssuranceStatusDegraded)

	result, err := f.svc.ReconcileAssuranceQuota(f.ctx, f.tenantID, f.period)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !result.Measurable() {
		t.Fatalf("both ledgers have data, so the drift must be measurable: %+v", result)
	}
	if result.Committed != 4_000 {
		t.Fatalf("committed must be tokens_used - pending = 4000, got %d", result.Committed)
	}
	if result.SettledTokens != 4_000 || result.SettledRuns != 2 {
		t.Fatalf("unexpected run ledger: settled=%d runs=%d", result.SettledTokens, result.SettledRuns)
	}
	if result.Drift == nil || *result.Drift != 0 {
		t.Fatalf("the two ledgers agree, so the drift must be a measured 0: %+v", result.Drift)
	}
	if result.OverCeiling() {
		t.Fatal("0 drift is not over the 1% ceiling")
	}
}

// TestAssuranceReconciliationFlagsDriftAboveCeiling — 1% is the ceiling, and the boundary
// itself is not over.
func TestAssuranceReconciliationFlagsDriftAboveCeiling(t *testing.T) {
	// settled 100k, committed 101k → |100k-101k|/100k = 1.0% exactly: at the line, not over.
	atLine := newSettlementFixture(t, 1_000_000, 101_000, 0)
	atLine.addRun(t, "run-line", 100_000, model.ReportAssuranceStatusSucceeded)
	result, err := atLine.svc.ReconcileAssuranceQuota(atLine.ctx, atLine.tenantID, atLine.period)
	if err != nil {
		t.Fatal(err)
	}
	if result.Drift == nil || *result.Drift != 0.01 {
		t.Fatalf("expected a 1%% drift, got %v", result.Drift)
	}
	if result.OverCeiling() {
		t.Fatal("exactly at the ceiling is not over it")
	}

	// settled 100k, committed 102k → 2%, over.
	over := newSettlementFixture(t, 1_000_000, 102_000, 0)
	over.addRun(t, "run-over", 100_000, model.ReportAssuranceStatusSucceeded)
	result, err = over.svc.ReconcileAssuranceQuota(over.ctx, over.tenantID, over.period)
	if err != nil {
		t.Fatal(err)
	}
	if result.Drift == nil || *result.Drift != 0.02 {
		t.Fatalf("expected a 2%% drift, got %v", result.Drift)
	}
	if !result.OverCeiling() {
		t.Fatal("2% must be over the 1% ceiling")
	}
}

// TestAssuranceReconciliationRefusesToInventADrift is the same discipline the release gate
// uses: no denominator means `nil`, and `nil` is neither healthy nor over the line.
func TestAssuranceReconciliationRefusesToInventADrift(t *testing.T) {
	cases := []struct {
		name    string
		limit   int64
		runsPer int64
		settled int64
	}{
		// `report.assurance_monthly_tokens = 0` ⇒ no bucket ⇒ nothing to reconcile. The
		// tenant still spends tokens, and the cost report still shows them (§6.5).
		{name: "unlimited allowance builds no bucket", limit: 0, runsPer: 1, settled: 9_000},
		// A bucket exists but no run has settled anything yet: 0/0.
		{name: "no settled run", limit: 1_000_000, runsPer: 0, settled: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettlementFixture(t, tc.limit, 0, 0)
			if tc.runsPer > 0 {
				f.addRun(t, "run-only", tc.settled, model.ReportAssuranceStatusSucceeded)
			}
			result, err := f.svc.ReconcileAssuranceQuota(f.ctx, f.tenantID, f.period)
			if err != nil {
				t.Fatal(err)
			}
			if result.Measurable() {
				t.Fatalf("no denominator must yield a nil drift, not 0: %+v", result)
			}
			if result.OverCeiling() {
				t.Fatal("unmeasurable is not over the ceiling")
			}
		})
	}
}

// TestAssuranceReconciliationIgnoresUnfinishedRuns — a running run is still settling, so
// counting it would make two reads a minute apart disagree.
func TestAssuranceReconciliationIgnoresUnfinishedRuns(t *testing.T) {
	f := newSettlementFixture(t, 1_000_000, 5_000, 0)
	f.addRun(t, "run-done", 5_000, model.ReportAssuranceStatusSucceeded)
	f.addRun(t, "run-running", 40_000, model.ReportAssuranceStatusRunning)
	f.addRun(t, "run-queued", 40_000, model.ReportAssuranceStatusQueued)

	result, err := f.svc.ReconcileAssuranceQuota(f.ctx, f.tenantID, f.period)
	if err != nil {
		t.Fatal(err)
	}
	if result.SettledTokens != 5_000 || result.Drift == nil || *result.Drift != 0 {
		t.Fatalf("only terminal runs may enter the run ledger: %+v", result)
	}
}

// TestAssuranceReconciliationSweepPublishesOnlyMeasurableTenants — the sweep walks the
// buckets (not a tenant list), and it must leave the gauge alone when it has nothing to
// compare rather than writing a healthy-looking zero.
func TestAssuranceReconciliationSweepPublishesOnlyMeasurableTenants(t *testing.T) {
	f := newSettlementFixture(t, 1_000_000, 10_000, 0)
	f.addRun(t, "run-measured", 10_000, model.ReportAssuranceStatusSucceeded)

	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if err := f.svc.reconcileAssuranceSettlement(f.ctx, now); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// A second tenant whose bucket exists but whose runs settled nothing: it must be
	// skipped, and the sweep must not fail because of it.
	if err := f.store.EnsureQuotaBucketTokenLimit(
		f.ctx, "t2", model.QuotaBucketDimensionAssurance, "t2", f.period, 500_000); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.reconcileAssuranceSettlement(f.ctx, now); err != nil {
		t.Fatalf("sweep with an unmeasurable tenant: %v", err)
	}
}

// TestAssuranceSettlementReconcileIsThrottled — the sweep runs every minute; the book
// comparison does not need to.
func TestAssuranceSettlementReconcileIsThrottled(t *testing.T) {
	f := newSettlementFixture(t, 1_000_000, 10_000, 0)
	f.addRun(t, "run-throttle", 10_000, model.ReportAssuranceStatusSucceeded)

	assuranceSettlementMu.Lock()
	assuranceSettlementLastAt = time.Time{}
	assuranceSettlementMu.Unlock()

	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	f.svc.reconcileAssuranceSettlementThrottled(f.ctx, now)
	assuranceSettlementMu.Lock()
	first := assuranceSettlementLastAt
	assuranceSettlementMu.Unlock()
	if !first.Equal(now) {
		t.Fatalf("the first sweep must reconcile immediately, stamp=%v", first)
	}

	f.svc.reconcileAssuranceSettlementThrottled(f.ctx, now.Add(time.Minute))
	assuranceSettlementMu.Lock()
	second := assuranceSettlementLastAt
	assuranceSettlementMu.Unlock()
	if !second.Equal(first) {
		t.Fatal("a sweep one minute later must be throttled, not reconciled again")
	}

	f.svc.reconcileAssuranceSettlementThrottled(f.ctx, now.Add(assuranceSettlementReconcileInterval))
	assuranceSettlementMu.Lock()
	third := assuranceSettlementLastAt
	assuranceSettlementMu.Unlock()
	if third.Equal(first) {
		t.Fatal("a sweep past the interval must reconcile again")
	}

	assuranceSettlementMu.Lock()
	assuranceSettlementLastAt = time.Time{}
	assuranceSettlementMu.Unlock()
}
