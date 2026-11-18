package service

// Unconfirmable usage is never zero (doc/288 §8.6 / A31b, 292 D7).
//
// A call that was placed but never answered has exactly one honest accounting: the
// provider may have billed it, so the amount is *unknown*, not 0 and not charged.
// Three assertions protect that:
//
//  1. a transport timeout writes the reservation down as `orphan` +
//     `provider_unconfirmed` + `unverified`, and the amount lands in
//     `pending_reconcile_tokens` while `charged_tokens` stays 0;
//  2. a receipt that arrives later adopts the orphan (`orphan → settled`), moves the
//     amount into `actual/charged`, recomputes the job's columns and is audited — a
//     number that changed after the fact must be traceable;
//  3. a receipt that never arrives leaves the row unverified forever: no sweep, no
//     clock and no re-read is allowed to quietly zero it or make it disappear. The
//     24h reconcile window of §8.6 does not exist in P0-B, so what must be proved is
//     that its absence does not turn "unknown" into "free".

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// chargeRowsOf reads the ledger of one job straight from the table: the point of
// A31b is what is actually stored, not what a service helper summarises.
func chargeRowsOf(t *testing.T, fx *reportFixture, jobID string) []model.ReportCharge {
	t.Helper()
	var rows []model.ReportCharge
	if err := fx.gdb.Where("tenant_id = ? AND report_job_id = ?", fx.tenantID, jobID).
		Order("call_seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// orphanedTimeoutFixture generates a report whose every section call times out, so
// the reservations can only end up orphaned.
func orphanedTimeoutFixture(t *testing.T, name string) (*reportFixture, CreateReportResult) {
	t.Helper()
	fx, engine := newReportRunFixture(t, name)
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-"+name, "req-"+name+"-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	// One entry repeats forever: every section call, including the in-slice retry,
	// hits a transport timeout. No usage ever comes back.
	engine.chatErrs = []error{&ragflow.Error{
		Type: ragflow.ErrorTypeTransport, Message: "connection reset by peer",
	}}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)
	// The slice ends in `finalizing`; the terminal settlement (which releases what
	// is still reserved and rewrites the job's columns from the ledger) happens in
	// the finalize step. Without it the job is not terminal and its summary columns
	// are still the creation-time zeros.
	// The runner settles its own `rgx_job` row once the worker body returns; this
	// fixture calls the worker directly, so the row is still `queued` and finalize's
	// "slice must have settled" gate would defer forever.
	if err := fx.gdb.Model(&model.Job{}).
		Where("id = ?", fx.reportJob(t, created.Job.ID).CurrentRuntimeJobID).
		Update("status", model.JobStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.FinalizedAt == nil {
		t.Fatalf("the terminal write must land in this fixture, job is still %s", job.Status)
	}
	return fx, created
}

func TestUnconfirmedUsageBecomesPendingNotZero(t *testing.T) {
	fx, created := orphanedTimeoutFixture(t, "a31b-orphan")
	ctx := context.Background()

	rows := chargeRowsOf(t, fx, created.Job.ID)
	if len(rows) == 0 {
		t.Fatal("a placed call must leave a ledger row even when nothing comes back")
	}
	var pending int64
	for _, row := range rows {
		if row.State != model.ReportChargeOrphan {
			t.Fatalf("an unanswered call must be written down as orphan, got %q", row.State)
		}
		if row.ReconcileReason != model.ReportReconcileProviderUnconfirmed {
			t.Fatalf("the reason must distinguish a lost call from a crashed one, got %q",
				row.ReconcileReason)
		}
		if row.ReconcileOutcome != model.ReportReconcileOutcomeUnverified {
			t.Fatalf("an unanswered call is unverified, got %q", row.ReconcileOutcome)
		}
		// The two quantities that must NOT move: the provider reported nothing, so
		// claiming an actual cost would be a guess in the other direction.
		if row.ChargedTokens != 0 || row.ActualTokens != 0 {
			t.Fatalf("unconfirmed usage must not be charged or counted as actual: %+v", row)
		}
		if row.ReservedTokens <= 0 {
			t.Fatalf("the reservation must stay readable: %+v", row)
		}
		pending += row.ReservedTokens
	}

	job, err := fx.svc.Store.GetReportJob(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.PendingReconcileTokens != pending {
		t.Fatalf("pending_reconcile_tokens must equal the orphaned reservations: %d vs %d",
			job.PendingReconcileTokens, pending)
	}
	if job.ChargedTokens != 0 {
		t.Fatalf("nothing was confirmed, so nothing may be charged: %d", job.ChargedTokens)
	}

	usage, err := fx.svc.ReportChargeUsageOf(ctx, fx.tenantID, job)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Pending != pending || usage.Charged != 0 {
		t.Fatalf("the visible usage must show the unverified amount: %+v", usage)
	}
}

func TestLateReceiptAdoptsTheOrphanAndIsAudited(t *testing.T) {
	fx, created := orphanedTimeoutFixture(t, "a31b-late")
	ctx := context.Background()
	now := time.Now().UTC()

	rows := chargeRowsOf(t, fx, created.Job.ID)
	if len(rows) < 2 {
		t.Fatalf("need at least two orphaned calls to keep one unverified, got %d", len(rows))
	}
	adopted := rows[0]
	settledTokens := adopted.ReservedTokens

	ok, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, adopted.LedgerKey, settledTokens, now)
	if err != nil {
		t.Fatalf("a late receipt must be accepted: %v", err)
	}
	if !ok {
		t.Fatal("adopting an orphan is a real transition, not a no-op")
	}

	var stored model.ReportCharge
	if err := fx.gdb.Where("id = ?", adopted.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.State != model.ReportChargeSettled ||
		stored.ReconcileOutcome != model.ReportReconcileOutcomeSettled {
		t.Fatalf("orphan must move to settled/settled, got %q/%q", stored.State, stored.ReconcileOutcome)
	}
	if stored.ActualTokens != settledTokens || stored.ChargedTokens != settledTokens {
		t.Fatalf("the receipt must fill actual and charged: %+v", stored)
	}

	job, err := fx.svc.Store.GetReportJob(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ChargedTokens != settledTokens {
		t.Fatalf("the job's charged cache must be recomputed from the ledger: %d vs %d",
			job.ChargedTokens, settledTokens)
	}
	var pending int64
	for _, row := range rows[1:] {
		pending += row.ReservedTokens
	}
	if job.PendingReconcileTokens != pending {
		t.Fatalf("only the adopted row leaves pending: %d vs %d", job.PendingReconcileTokens, pending)
	}

	// The adoption is audited (§8.6) — a number changed after the fact, so it must be
	// traceable. Finalize's own ledger-vs-cache drift check writes a
	// `report.charge.reconciled` row too, so the adoption is identified by its
	// `outcome`, not by the action name.
	type reconcileDetail struct {
		Outcome string `json:"outcome"`
		Usage   struct {
			Actual  int64 `json:"actual"`
			Charged int64 `json:"charged"`
		} `json:"llm_usage"`
	}
	var audits []model.AuditLog
	if err := fx.gdb.Where("tenant_id = ? AND action = ?", fx.tenantID, "report.charge.reconciled").
		Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	var adoptions []struct {
		audit  model.AuditLog
		detail reconcileDetail
	}
	for _, audit := range audits {
		var detail reconcileDetail
		if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
			t.Fatalf("audit detail must be readable: %v (%q)", err, audit.DetailJSON)
		}
		if detail.Outcome == model.ReportReconcileOutcomeSettled {
			adoptions = append(adoptions, struct {
				audit  model.AuditLog
				detail reconcileDetail
			}{audit, detail})
		}
	}
	if len(adoptions) != 1 {
		t.Fatalf("adopting one orphan is one audit row, got %d of %d reconciled rows",
			len(adoptions), len(audits))
	}
	if adoptions[0].detail.Usage.Charged != settledTokens {
		t.Fatalf("the audit must record what changed: %s", adoptions[0].audit.DetailJSON)
	}
	// The actor is the report owner: the transition happens in a worker, but the
	// cost is the user's.
	if adoptions[0].audit.UserID != fx.owner.ID {
		t.Fatalf("reconcile audit must be attributed to the report owner, got %q",
			adoptions[0].audit.UserID)
	}
}

func TestUnexpiredUnknownStaysUnverifiedAcrossSweeps(t *testing.T) {
	fx, created := orphanedTimeoutFixture(t, "a31b-stays")
	ctx := context.Background()

	rows := chargeRowsOf(t, fx, created.Job.ID)
	if len(rows) == 0 {
		t.Fatal("need orphaned rows")
	}
	var pending int64
	for _, row := range rows {
		pending += row.ReservedTokens
	}

	// Age the rows past both the 10-minute staleness line and any plausible 24h
	// reconcile window, then sweep repeatedly. None of it may touch an orphan: the
	// sweeper's job is to stop a dead reservation pinning budget, never to decide that
	// an unknown amount was zero.
	stale := time.Now().UTC().Add(-72 * time.Hour)
	if err := fx.gdb.Model(&model.ReportCharge{}).
		Where("tenant_id = ? AND report_job_id = ?", fx.tenantID, created.Job.ID).
		Update("created_at", stale).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sweep, err := fx.svc.SweepStaleReportCharges(ctx, 100)
		if err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
		if sweep.Orphaned != 0 {
			t.Fatalf("an already-orphaned row must not be re-orphaned by the sweep: %+v", sweep)
		}
	}
	after := chargeRowsOf(t, fx, created.Job.ID)
	if len(after) != len(rows) {
		t.Fatalf("a sweep must not create or drop ledger rows: %d → %d", len(rows), len(after))
	}
	for index, row := range after {
		if row.State != model.ReportChargeOrphan ||
			row.ReconcileReason != model.ReportReconcileProviderUnconfirmed ||
			row.ReconcileOutcome != model.ReportReconcileOutcomeUnverified {
			t.Fatalf("an unverified row must stay exactly unverified: %+v", row)
		}
		if row.ChargedTokens != 0 {
			t.Fatalf("a sweep must never charge an unknown amount: %+v", row)
		}
		if row.LedgerKey != rows[index].LedgerKey || row.ReservedTokens != rows[index].ReservedTokens {
			t.Fatalf("the ledger row must be immutable apart from its state: %+v vs %+v",
				row, rows[index])
		}
	}

	job, err := fx.svc.Store.GetReportJob(ctx, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.PendingReconcileTokens != pending {
		t.Fatalf("pending must survive the sweeps: %d vs %d", job.PendingReconcileTokens, pending)
	}
	if job.ChargedTokens != 0 {
		t.Fatalf("charged must stay 0 while nothing is confirmed: %d", job.ChargedTokens)
	}

	// The read side must keep reporting the backlog loudly: a report whose cost is
	// unknown until reconciliation cannot look free in the UI either.
	usage, err := fx.svc.ReportChargeUsageOf(ctx, fx.tenantID, job)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Pending != pending {
		t.Fatalf("the usage surface must keep disclosing pending: %+v", usage)
	}
}
