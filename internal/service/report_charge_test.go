package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Token ledger (doc/288 §8.6 / T2c).
//
// Two properties are worth more than the rest and are asserted everywhere below:
// a token is *reserved before* the call and *settled after* it, so a budget can
// never be overspent by concurrent sections; and the job's summary columns always
// agree with the ledger rows, because they are recomputed from them instead of
// incremented.

// httperrStatusOf returns the HTTP status of a typed error, or 0.
func httperrStatusOf(err error) (int, string) {
	var typed *httperr.Error
	if errors.As(err, &typed) {
		return typed.Status, typed.Message
	}
	return 0, ""
}

func (fx *reportFixture) chargeRow(t *testing.T, ledgerKey string) *model.ReportCharge {
	t.Helper()
	var charge model.ReportCharge
	if err := fx.gdb.Where("ledger_key = ?", ledgerKey).First(&charge).Error; err != nil {
		t.Fatalf("load charge %s: %v", ledgerKey, err)
	}
	return &charge
}

// auditRows returns the audit entries of one action, newest first.
func (fx *reportFixture) auditRows(t *testing.T, action string) []model.AuditLog {
	t.Helper()
	var rows []model.AuditLog
	if err := fx.gdb.Where("action = ? AND tenant_id = ?", action, fx.tenantID).
		Order("at DESC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// setEnvelope replaces the budget the job pre-deducted, which is how a test makes
// the gate reachable without generating thousands of tokens.
func (fx *reportFixture) setEnvelope(t *testing.T, jobID string, budget int64) {
	t.Helper()
	if err := fx.gdb.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", fx.tenantID, jobID).
		Update("reserved_tokens", budget).Error; err != nil {
		t.Fatal(err)
	}
}

func chargeCall(jobID string, reserve int64) ReportChargeCall {
	return ReportChargeCall{
		JobID: jobID, SectionID: "sec-compliance",
		AttemptSeq: 1, SliceSeq: 1, SectionAttempt: 1,
		ReservedTokens: reserve, ModelID: "text-embedding-3-small",
	}
}

func TestReserveReportChargeAllocatesSequencesUnderOneKey(t *testing.T) {
	fx := newReportFixture(t, "charge-alloc")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-alloc", "req-charge-alloc-0001")
	now := time.Now().UTC()

	first, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now)
	if err != nil {
		t.Fatal(err)
	}
	if first.CallSeq != 1 || second.CallSeq != 2 {
		t.Fatalf("call_seq must be allocated in the insert transaction, got %d/%d",
			first.CallSeq, second.CallSeq)
	}
	// The six-segment key is built from business identity only. Recomputing it here
	// (rather than trusting the writer) is the point: a segment reorder would break
	// replay protection silently.
	want := model.ReportLedgerKey(created.Job.ID, 1, 1, "sec-compliance", 1, 2)
	if second.LedgerKey != want {
		t.Fatalf("ledger key must be report:<job>:<attempt>:<slice>:<section>:<section_attempt>:<call_seq>, got %q want %q",
			second.LedgerKey, want)
	}
	if first.State != model.ReportChargeReserved {
		t.Fatalf("a fresh charge must be reserved, got %q", first.State)
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Held != 800 || sums.Actual != 0 || sums.Charged != 0 {
		t.Fatalf("two reservations must pin 800 tokens and cost nothing yet, got %+v", sums)
	}
	job := fx.reportJob(t, created.Job.ID)
	usage, err := fx.svc.ReportChargeUsageOf(ctx, fx.tenantID, job)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Budget != job.ReservedTokens || usage.Available != job.ReservedTokens-800 {
		t.Fatalf("available must be budget minus held, got %+v (budget %d)", usage, job.ReservedTokens)
	}
}

func TestReserveReportChargeRefusesBeyondEnvelope(t *testing.T) {
	fx := newReportFixture(t, "charge-budget")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-budget", "req-charge-budget-0001")
	fx.setEnvelope(t, created.Job.ID, 500)
	now := time.Now().UTC()

	if _, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now); err != nil {
		t.Fatal(err)
	}
	_, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now)
	if err == nil {
		t.Fatal("the second reservation must be refused: only 100 tokens are left")
	}
	if code := reportErrorCode(err); code != codeReportBudgetExhausted {
		t.Fatalf("budget refusal must be 44111, got %d (%v)", code, err)
	}
	if code, _ := httperrStatusOf(err); code != 409 {
		t.Fatalf("44111 must be a 409 conflict, got %d", code)
	}
	// A refused reservation must not be written down: "no" has no side effects.
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Held != 400 {
		t.Fatalf("a refused reservation must not pin budget, got %+v", sums)
	}
}

func TestReserveReportChargeFailsClosedWithoutEnvelope(t *testing.T) {
	fx := newReportFixture(t, "charge-noenv")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-noenv", "req-charge-noenv-0001")
	fx.setEnvelope(t, created.Job.ID, 0)

	_, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 100), time.Now().UTC())
	if err == nil {
		t.Fatal("a job without an envelope must not be chargeable")
	}
	if code := reportErrorCode(err); code != codeReportLedgerInconsistent {
		t.Fatalf("a missing envelope is a bookkeeping bug (44112), got %d (%v)", code, err)
	}
}

func TestReserveReportChargeStopsAtSoftLimit(t *testing.T) {
	fx := newReportFixture(t, "charge-soft")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-soft", "req-charge-soft-0001")
	fx.setEnvelope(t, created.Job.ID, 1000)
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 950), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 950, now); err != nil {
		t.Fatal(err)
	}
	// 950 of 1000 is exactly the 95% mark: no new section may start, but the
	// already-paid work stays usable.
	_, err = fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 10), now)
	if err == nil {
		t.Fatal("the soft limit must stop new sections")
	}
	if code := reportErrorCode(err); code != codeReportBudgetExhausted {
		t.Fatalf("soft limit must answer 44111, got %d (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "soft limit") {
		t.Fatalf("the refusal must say which limit was hit, got %v", err)
	}
}

func TestSettleReportChargeWritesTheLedgerThrough(t *testing.T) {
	fx := newReportFixture(t, "charge-settle")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-settle", "req-charge-settle-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 300, now)
	if err != nil || !applied {
		t.Fatalf("settle: applied=%v err=%v", applied, err)
	}
	row := fx.chargeRow(t, charge.LedgerKey)
	if row.State != model.ReportChargeSettled || row.ActualTokens != 300 ||
		row.ChargedTokens != 300 || row.SettledAt == nil {
		t.Fatalf("a settled row must carry actual, charged and a settlement clock: %+v", row)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.ActualTokens != 300 || job.ChargedTokens != 300 {
		t.Fatalf("the job columns are a cache of the ledger, got %+v", job)
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Held != 0 {
		t.Fatalf("settling must release the pin, got held=%d", sums.Held)
	}
	if sums.Reserved != 400 {
		t.Fatalf("the row keeps the reservation it was given, got %+v", sums)
	}
}

func TestSettleReportChargeRejectsDoubleSettle(t *testing.T) {
	fx := newReportFixture(t, "charge-double")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-double", "req-charge-double-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 200), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 200, now); err != nil {
		t.Fatal(err)
	}
	_, err = fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 200, now)
	if err == nil {
		t.Fatal("settling the same key twice would double-charge; it must be refused")
	}
	if code := reportErrorCode(err); code != codeReportLedgerInconsistent {
		t.Fatalf("a double settle is 44112, got %d (%v)", code, err)
	}
}

func TestSettleReportChargeAdoptsOrphanedReservation(t *testing.T) {
	fx := newReportFixture(t, "charge-adopt")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-adopt", "req-charge-adopt-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 500), now)
	if err != nil {
		t.Fatal(err)
	}
	// The sweep already wrote the row down as unverifiable; then the call returns
	// with a real number. Settling from `orphan` keeps `pending_reconcile_tokens`
	// from claiming a cost we can now prove, without ever charging twice.
	if err := fx.gdb.Model(&model.ReportCharge{}).
		Where("id = ?", charge.ID).
		Updates(map[string]interface{}{
			"state":             model.ReportChargeOrphan,
			"reconcile_reason":  model.ReportReconcileStaleReserved,
			"reconcile_outcome": model.ReportReconcileOutcomeUnverified,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.Store.RefreshReportChargeTotals(ctx, fx.tenantID, created.Job.ID, now); err != nil {
		t.Fatal(err)
	}
	if job := fx.reportJob(t, created.Job.ID); job.PendingReconcileTokens != 500 {
		t.Fatalf("an unverified orphan must be disclosed as pending, got %d", job.PendingReconcileTokens)
	}

	applied, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 420, now)
	if err != nil || !applied {
		t.Fatalf("settle from orphan: applied=%v err=%v", applied, err)
	}
	row := fx.chargeRow(t, charge.LedgerKey)
	if row.State != model.ReportChargeSettled || row.ChargedTokens != 420 ||
		row.ReconcileOutcome != model.ReportReconcileOutcomeSettled {
		t.Fatalf("an adopted orphan must settle cleanly: %+v", row)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.PendingReconcileTokens != 0 || job.ChargedTokens != 420 {
		t.Fatalf("adoption must move the cost out of pending, got %+v", job)
	}
}

func TestReleaseReportChargeReturnsTheReservation(t *testing.T) {
	fx := newReportFixture(t, "charge-release")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-release", "req-charge-release-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 400), now)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := fx.svc.ReleaseReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, now)
	if err != nil || !applied {
		t.Fatalf("release: applied=%v err=%v", applied, err)
	}
	row := fx.chargeRow(t, charge.LedgerKey)
	if row.State != model.ReportChargeReleased || row.ReleasedTokens != 400 {
		t.Fatalf("a release must give back exactly what was reserved: %+v", row)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.ActualTokens != 0 || job.ChargedTokens != 0 {
		t.Fatalf("a released call is not a cost, got %+v", job)
	}
	sums, err := fx.svc.Store.SumReportCharges(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums.Held != 0 {
		t.Fatalf("a released reservation must stop pinning budget, got %+v", sums)
	}
}

func TestSweepStaleReportChargesOrphansOnlyLostCalls(t *testing.T) {
	fx := newReportFixture(t, "charge-sweep")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-sweep", "req-charge-sweep-0001")
	now := time.Now().UTC()

	stale, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 300), now)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 200), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportCharge{}).Where("id = ?", stale.ID).
		Update("created_at", now.Add(-reportStaleChargeAfter-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	sweep, err := fx.svc.SweepStaleReportCharges(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Orphaned != 1 {
		t.Fatalf("exactly the lost call must be orphaned, got %+v", sweep)
	}
	orphan := fx.chargeRow(t, stale.LedgerKey)
	if orphan.State != model.ReportChargeOrphan ||
		orphan.ReconcileReason != model.ReportReconcileStaleReserved ||
		orphan.ReconcileOutcome != model.ReportReconcileOutcomeUnverified {
		t.Fatalf("an orphan must be written down as unverifiable, got %+v", orphan)
	}
	if live := fx.chargeRow(t, fresh.LedgerKey); live.State != model.ReportChargeReserved {
		t.Fatalf("a live reservation must be left alone, got %+v", live)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.PendingReconcileTokens != 300 {
		t.Fatalf("the orphan must be disclosed as pending, got %d", job.PendingReconcileTokens)
	}
	// The sweep is idempotent: the row is no longer `reserved`, so a second pass
	// finds nothing to write down.
	again, err := fx.svc.SweepStaleReportCharges(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if again.Orphaned != 0 {
		t.Fatalf("a second sweep must not re-orphan the same row, got %+v", again)
	}
}

func TestReleaseReportChargeAdoptsOrphanedReservation(t *testing.T) {
	fx := newReportFixture(t, "charge-release-orphan")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-release-orphan", "req-charge-rel-orphan-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 250), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportCharge{}).Where("id = ?", charge.ID).
		Update("created_at", now.Add(-reportStaleChargeAfter-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.SweepStaleReportCharges(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// The provider then answers "rejected before billing": returning the
	// reservation now is the only way to close the exposure honestly.
	applied, err := fx.svc.ReleaseReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, now)
	if err != nil || !applied {
		t.Fatalf("release from orphan: applied=%v err=%v", applied, err)
	}
	row := fx.chargeRow(t, charge.LedgerKey)
	if row.State != model.ReportChargeReleased || row.ReleasedTokens != 250 ||
		row.ReconcileOutcome != model.ReportReconcileOutcomeReleased {
		t.Fatalf("an orphan released after the fact must close cleanly: %+v", row)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.PendingReconcileTokens != 0 || job.ChargedTokens != 0 {
		t.Fatalf("a released orphan is neither cost nor pending, got %+v", job)
	}
}

func TestReportChargeReconcileLeavesAuditTrail(t *testing.T) {
	fx := newReportFixture(t, "charge-audit")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-charge-audit", "req-charge-audit-0001")
	now := time.Now().UTC()

	charge, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 300), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportCharge{}).Where("id = ?", charge.ID).
		Update("created_at", now.Add(-reportStaleChargeAfter-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.SweepStaleReportCharges(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, charge.LedgerKey, 280, now); err != nil {
		t.Fatal(err)
	}
	orphaned := fx.auditRows(t, "report.charge.orphaned")
	if len(orphaned) != 1 {
		t.Fatalf("writing a reservation down must be audited once, got %d", len(orphaned))
	}
	if !strings.Contains(orphaned[0].DetailJSON, charge.LedgerKey) ||
		!strings.Contains(orphaned[0].DetailJSON, "stale_reserved") {
		t.Fatalf("the audit row must name the ledger key and the reason, got %s", orphaned[0].DetailJSON)
	}
	// The actor is the report owner: the write-down happens in the worker, but the
	// cost belongs to a person.
	if orphaned[0].UserID == "" || orphaned[0].Resource != "report" ||
		orphaned[0].ResourceID != created.Report.ID {
		t.Fatalf("audit row must be attributed to the report owner, got %+v", orphaned[0])
	}
	reconciled := fx.auditRows(t, "report.charge.reconciled")
	if len(reconciled) != 1 {
		t.Fatalf("adopting an orphan must be audited once, got %d", len(reconciled))
	}
	if !strings.Contains(reconciled[0].DetailJSON, "280") {
		t.Fatalf("the reconciled audit must carry the adopted amount, got %s", reconciled[0].DetailJSON)
	}
	// Ordinary settles are not duplicated into the audit chain: the ledger row with
	// its own clock is the record (deviation registered in doc/293 §12.5).
	if rows := fx.auditRows(t, "report.charge.settled"); len(rows) != 0 {
		t.Fatalf("a plain settle must not be mirrored into audit, got %d", len(rows))
	}
}

func TestReportBudgetReservationDerivesFromThePinnedTemplate(t *testing.T) {
	limits := ReportTemplateLimits{MaxTokensPerSection: 1600, MaxSections: 10}
	cases := []struct {
		name     string
		limits   ReportTemplateLimits
		sections int
		want     int64
	}{
		{"five declared sections", limits, 5, 5*1600 + reportOutlineTokenReserve},
		{"no sections", limits, 0, 0},
		{"no per-section cap", ReportTemplateLimits{}, 5, 0},
	}
	for _, test := range cases {
		if got := reportBudgetReservation(test.limits, test.sections); got != test.want {
			t.Fatalf("%s: got %d want %d", test.name, got, test.want)
		}
	}
	// The declared section count wins over the template ceiling: a template that
	// allows ten sections but writes five must not deduct ten.
	if got := reportBudgetReservation(limits, 5); got >= reportBudgetReservation(limits, 10) {
		t.Fatalf("budget must scale with the sections actually written, got %d", got)
	}
}

func TestReportChargeUsageArithmetic(t *testing.T) {
	job := &model.ReportJob{ReservedTokens: 1000}
	usage := reportChargeUsageFrom(job, repository.ReportChargeSums{
		Actual: 400, Held: 100, Charged: 400, Orphan: 50})
	if usage.Available != 500 {
		t.Fatalf("available = budget − actual − held, got %d", usage.Available)
	}
	if usage.Pending != 50 {
		t.Fatalf("pending must be disclosed separately, got %d", usage.Pending)
	}
	if usage.SoftLimited {
		t.Fatal("400 of 1000 must not be soft limited")
	}
	// Spend past the envelope never yields a negative "available": the gate answers
	// with a gap, not with a credit balance.
	over := reportChargeUsageFrom(job, repository.ReportChargeSums{Actual: 1200, Charged: 1200})
	if over.Available != 0 || !over.SoftLimited {
		t.Fatalf("overspend must clamp to zero and go soft, got %+v", over)
	}
}
