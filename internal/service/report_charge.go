package service

// Token ledger for the report answer (doc/288 §8.6 / T2c).
//
// The rule the whole file exists for: a token is reserved *before* the provider is
// called and settled *after* it answers. Without the reservation, two sections
// running at the same time both see the same "budget left" and together overspend
// it; without the settle, a released reservation hides a real cost. The ledger row
// is therefore the unit of accounting, and the job's summary columns are a cache
// derived from it (`repository.RefreshReportChargeTotals`).
//
// Three deliberate decisions:
//
//   - `call_seq` is allocated inside the insert transaction (`MAX(call_seq)+1`),
//     never from a clock and never from a counter cached in a worker. A replayed
//     slice that re-issues a call must mint a *new* sequence, because the provider
//     really did charge twice; what the six-segment key prevents is the same
//     submission being written down twice, not a legitimate retry being free.
//   - the budget envelope (`reserved_tokens` on the job) is frozen at creation from
//     the pinned template, so raising `max_tokens_per_section` later cannot widen a
//     report that is already running.
//   - a reservation that never comes back is not guessed at. It becomes `orphan`
//     with `reconcile_outcome = unverified` and lands in
//     `pending_reconcile_tokens`, which is the only honest answer when the provider
//     may or may not have billed the call.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	// reportOutlineTokenReserve is the envelope set aside for the outline pass
	// (one model call that produces every section heading). It is a fixed reserve
	// rather than a second template field: the outline's cost scales with the
	// section count, which the per-section budget already covers.
	reportOutlineTokenReserve = 1200
	// reportBudgetSoftRatio stops *new* sections at 95% of the envelope. The last
	// 5% exists so an in-flight section can still be finished and settled instead
	// of being killed with its cost unaccounted (doc/288 §8.6 step 4).
	reportBudgetSoftRatio = 0.95
	// reportStaleChargeAfter is the age at which a reservation is treated as a
	// lost call. It must be comfortably longer than the runtime job timeout (3
	// minutes): a reservation that outlives its slice can no longer be settled by
	// that slice, because the slice is already gone.
	reportStaleChargeAfter = 10 * time.Minute
	reportChargeSweepBatch = 100
)

// ReportChargeCall is one billable provider call, identified by business identity
// only. `ReservedTokens` is the maximum this call may consume; anything beyond it
// is an overage and is recorded rather than refused (§8.6).
type ReportChargeCall struct {
	JobID          string
	SectionID      string
	AttemptSeq     int
	SliceSeq       int
	SectionAttempt int
	ReservedTokens int64
	ModelID        string
	RuntimeJobID   string
}

// ReportChargeUsage is the six quantities of §8.6 plus the two derived values the
// budget gate needs. It is what the frontend shows and what the gate reads, so the
// two can never disagree about "how much is left".
type ReportChargeUsage struct {
	// Budget is the envelope frozen at creation.
	Budget int64 `json:"budget_tokens"`
	// Held is budget pinned by calls that are in flight right now.
	Held int64 `json:"held_tokens"`
	// Actual is what the provider reported, settled or not.
	Actual int64 `json:"actual_tokens"`
	// Charged is what is billable: settled rows only.
	Charged int64 `json:"charged_tokens"`
	// Pending is the unverifiable residue (`orphan` + `unverified`).
	Pending int64 `json:"pending_reconcile_tokens"`
	// Available is what a new call may still reserve.
	Available int64 `json:"available_tokens"`
	// SoftLimited is true once spend passed the 95% mark: in-flight work may
	// finish, but no new section starts.
	SoftLimited bool `json:"soft_limited"`
	// BudgetPolicy is the frozen policy of this job: `enforce` or `observe`.
	BudgetPolicy string `json:"budget_policy"`
	// Observing is true when the envelope is a measurement, not a limit. The UI must
	// say so next to the numbers, otherwise "1.2× budget" reads as a violation.
	Observing bool `json:"observing"`
}

// ReportChargeSweep reports one reconcile pass over the ledger.
type ReportChargeSweep struct {
	Orphaned  int `json:"orphaned"`
	Refreshed int `json:"refreshed"`
}

// reportBudgetReservation is the envelope a job pre-deducts at creation:
// every declared section at its per-section cap, plus the outline reserve.
//
// `sections` is the number of sections the *revision* will write, not the
// template's ceiling: a template that allows ten sections but declares five must
// not deduct ten.
func reportBudgetReservation(limits ReportTemplateLimits, sections int) int64 {
	if sections <= 0 || limits.MaxTokensPerSection <= 0 {
		return 0
	}
	return int64(sections)*int64(limits.MaxTokensPerSection) + reportOutlineTokenReserve
}

// ReserveReportCharge pins budget for one call before it is placed.
//
// The gate and the insert share one transaction, in this order: lock the job row
// (`.TouchReportJob`), read the ledger *under that lock*, decide, insert. Checking
// the envelope outside the lock would let two sections that run concurrently both
// see "400 left" and both reserve 400 — the exact overspend the reservation exists
// to prevent. Nothing here can be reordered without reopening that hole.
//
// A refusal is 44111 and is a *gap* (`budget_exhausted`), not a failure: the
// sections already written stay published (doc/288 §7.10).
func (s *Service) ReserveReportCharge(
	ctx context.Context, tenantID string, call ReportChargeCall, now time.Time,
) (*model.ReportCharge, error) {
	if call.ReservedTokens <= 0 {
		return nil, errReportLedgerInconsistent("a reservation must be positive")
	}
	var charge *model.ReportCharge
	err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		job, err := tx.GetReportJob(ctx, tenantID, call.JobID)
		if err != nil {
			return err
		}
		if job == nil {
			return errReportInvalidTransition("report job vanished before it could be charged")
		}
		if _, err := tx.TouchReportJob(ctx, tenantID, call.JobID, now); err != nil {
			return err
		}
		sums, err := tx.SumReportCharges(ctx, tenantID, call.JobID)
		if err != nil {
			return err
		}
		if err := reportChargeGate(reportChargeUsageFrom(job, sums), call.ReservedTokens, job.ID); err != nil {
			return err
		}
		charge, err = tx.ReserveReportCharge(ctx, repository.ReportChargeReserve{
			TenantID: tenantID, JobID: call.JobID, SectionID: call.SectionID,
			AttemptSeq: call.AttemptSeq, SliceSeq: call.SliceSeq,
			SectionAttempt: call.SectionAttempt, ReservedTokens: call.ReservedTokens,
			ModelID: call.ModelID, RuntimeJobID: call.RuntimeJobID, Now: now,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return charge, nil
}

// SettleReportCharge converts one reservation into a cost.
//
// It settles from `reserved` first. If that loses, the row was already written down
// by the sweep as `orphan`: the call came back after we stopped waiting, and the
// honest move is to settle it from there (`pending_reconcile_tokens` shrinks,
// `charged_tokens` grows) instead of leaving a permanent "unknown". Adopting an
// orphan is a reconciliation, so it is audited (A31b) — a number that changed after
// the fact must be traceable.
func (s *Service) SettleReportCharge(
	ctx context.Context, tenantID, jobID, ledgerKey string, actualTokens int64, now time.Time,
) (bool, error) {
	if actualTokens < 0 {
		return false, errReportLedgerInconsistent("actual token count cannot be negative")
	}
	settle := func(from string) (bool, error) {
		return s.Store.TransitionReportCharge(ctx, repository.ReportChargeTransition{
			TenantID: tenantID, LedgerKey: ledgerKey, From: from,
			Set: map[string]interface{}{
				"state":             model.ReportChargeSettled,
				"actual_tokens":     actualTokens,
				"charged_tokens":    actualTokens,
				"reconcile_outcome": model.ReportReconcileOutcomeSettled,
				"settled_at":        now,
			},
		})
	}
	applied, err := settle(model.ReportChargeReserved)
	if err != nil {
		return false, err
	}
	adopted := false
	if !applied {
		applied, err = settle(model.ReportChargeOrphan)
		if err != nil {
			return false, err
		}
		adopted = applied
	}
	if !applied {
		// Neither reserved nor orphan: another actor already released or settled
		// this key. That is a double settle, and silence would hide it.
		return false, errReportLedgerInconsistent("charge " + ledgerKey + " is not reservable")
	}
	if adopted {
		if err := s.auditChargeReconcile(ctx, tenantID, jobID, ledgerKey,
			"report.charge.reconciled", map[string]interface{}{
				"outcome":   model.ReportReconcileOutcomeSettled,
				"llm_usage": map[string]interface{}{"actual": actualTokens, "charged": actualTokens},
			}, now); err != nil {
			return false, err
		}
	}
	if _, err := s.Store.RefreshReportChargeTotals(ctx, tenantID, jobID, now); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseReportCharge returns a reservation the provider never consumed (a call
// that failed before it was billable). The row keeps the reservation it was given,
// so `released_tokens` on the row and the job's unspent-envelope figure stay two
// separate, non-additive facts.
//
// `orphan → released` is allowed for the same reason `orphan → settled` is: a call
// the sweep had to write down as unverifiable can still be proven unbillable later
// (the provider answered "rejected before billing"). Refusing that would leave the
// exposure open forever.
func (s *Service) ReleaseReportCharge(
	ctx context.Context, tenantID, jobID, ledgerKey string, now time.Time,
) (bool, error) {
	release := func(from string) (bool, error) {
		return s.Store.TransitionReportCharge(ctx, repository.ReportChargeTransition{
			TenantID: tenantID, LedgerKey: ledgerKey, From: from,
			Set: map[string]interface{}{
				"state":             model.ReportChargeReleased,
				"released_tokens":   gorm.Expr("reserved_tokens"),
				"reconcile_outcome": model.ReportReconcileOutcomeReleased,
				"settled_at":        now,
			},
		})
	}
	applied, err := release(model.ReportChargeReserved)
	if err != nil {
		return false, err
	}
	if !applied {
		applied, err = release(model.ReportChargeOrphan)
		if err != nil {
			return false, err
		}
		if applied {
			if err := s.auditChargeReconcile(ctx, tenantID, jobID, ledgerKey,
				"report.charge.reconciled", map[string]interface{}{
					"outcome": model.ReportReconcileOutcomeReleased,
				}, now); err != nil {
				return false, err
			}
		}
	}
	if !applied {
		return false, nil
	}
	if _, err := s.Store.RefreshReportChargeTotals(ctx, tenantID, jobID, now); err != nil {
		return false, err
	}
	return true, nil
}

// auditChargeReconcile records a ledger transition that changed a number after the
// fact (doc/288 §8.6 / A31b). Actor is the report owner: the transition happens in
// the worker, but the report — and therefore the cost — is theirs.
//
// Amounts are nested under `llm_usage` instead of keys like `actual_tokens`
// because the audit writer redacts any key containing "token" (a secret filter, and
// the right default): `{"actual_tokens": 280}` would be stored as `"[REDACTED]"`.
//
// Ordinary `reserved → settled` and `reserved → released` are *not* audited: they
// are already written into the append-only ledger with their own timestamps, and
// duplicating every model call into the audit chain would double-write the same
// fact on the hot path. What must be traceable is the reconcile: a value a human
// had to decide about, or a write-down done by a sweep rather than by the code that
// made the call. This deviation is registered in doc/293 §12.5.
func (s *Service) auditChargeReconcile(
	ctx context.Context, tenantID, jobID, ledgerKey, action string,
	detail map[string]interface{}, now time.Time,
) error {
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return errReportLedgerInconsistent("report job " + jobID + " vanished during charge reconcile")
	}
	actor := ""
	if report, err := s.Store.GetReport(ctx, tenantID, job.ReportID); err != nil {
		return err
	} else if report != nil {
		actor = report.OwnerUserID
	}
	detail["report_job_id"] = jobID
	detail["ledger_key"] = ledgerKey
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actor, Action: action,
		Resource: "report", ResourceID: job.ReportID, Result: "SUCCESS",
		DetailJSON: string(payload), At: now,
	})
}

// ReportChargeUsageOf reads the six quantities of one job without gating anything.
// The read side uses it to show the user what a report has cost so far.
func (s *Service) ReportChargeUsageOf(
	ctx context.Context, tenantID string, job *model.ReportJob,
) (ReportChargeUsage, error) {
	sums, err := s.Store.SumReportCharges(ctx, tenantID, job.ID)
	if err != nil {
		return ReportChargeUsage{}, err
	}
	return reportChargeUsageFrom(job, sums), nil
}

// reportChargeUsageFrom is the single place where the gate's arithmetic lives, so a
// change of definition cannot make the endpoint and the guard disagree.
func reportChargeUsageFrom(job *model.ReportJob, sums repository.ReportChargeSums) ReportChargeUsage {
	available := job.ReservedTokens - sums.Actual - sums.Held
	if available < 0 {
		available = 0
	}
	soft := job.ReservedTokens > 0 &&
		float64(sums.Actual) >= float64(job.ReservedTokens)*reportBudgetSoftRatio
	policy := strings.TrimSpace(job.ReportBudgetPolicy)
	if policy == "" {
		// A row written before the column existed is `enforce`: that is what it was
		// actually billed under, and re-reading the tenant switch here would let an
		// old job change rules.
		policy = model.ReportBudgetEnforce
	}
	return ReportChargeUsage{
		Budget: job.ReservedTokens, Held: sums.Held, Actual: sums.Actual,
		Charged: sums.Charged, Pending: sums.Orphan,
		Available: available, SoftLimited: soft,
		BudgetPolicy: policy, Observing: policy == model.ReportBudgetObserve,
	}
}

// reportChargeGate is the gate in front of every call (§8.6 steps 2–4):
//
//  1. a job with no envelope cannot be charged at all — that is a bookkeeping bug,
//     not a user-facing limit, so it is 44112 and fails closed;
//  2. [`observe` only] stop here: the envelope is being measured, so the call is
//     recorded and never refused (doc/296 §2.2);
//  3. `budget − actual − held` must cover the call being reserved;
//  4. once spend passes the soft ratio, no *new* section starts.
//
// Pending (`orphan`, unverified) is disclosed but not subtracted: it may still turn
// into an actual settle, and subtracting it twice would strand a budget that was
// never really consumed.
func reportChargeGate(usage ReportChargeUsage, reserve int64, jobID string) error {
	if usage.Budget <= 0 {
		return errReportLedgerInconsistent("report job " + jobID + " has no budget envelope")
	}
	// `observe` keeps every other step intact — the reservation is still written, the
	// usage is still summed, the overage is still audited — and only drops the two
	// refusals. It is a measurement mode, not an unmetered mode.
	if usage.Observing {
		return nil
	}
	if reserve > 0 && usage.Available < reserve {
		return errReportBudgetExhausted(fmt.Sprintf(
			"report budget exhausted: %d available, %d required", usage.Available, reserve))
	}
	if usage.SoftLimited {
		return errReportBudgetExhausted(fmt.Sprintf(
			"report budget soft limit reached: %d of %d tokens spent", usage.Actual, usage.Budget))
	}
	return nil
}

// SweepStaleReportCharges writes down reservations whose call never came back
// (crash, provider timeout, killed pod) and refreshes the affected jobs.
//
// This is reconcile class 4 of §7.9 and it is deliberately not a terminal-write
// path: an orphan row never changes a report's status. It only stops a dead call's
// reservation from pinning budget forever, and it keeps the amount unknowable
// rather than inventing a number.
func (s *Service) SweepStaleReportCharges(ctx context.Context, limit int) (ReportChargeSweep, error) {
	now := time.Now().UTC()
	if limit <= 0 {
		limit = reportChargeSweepBatch
	}
	candidates, err := s.Store.ListStaleReservedReportCharges(
		ctx, now.Add(-reportStaleChargeAfter), limit)
	if err != nil {
		return ReportChargeSweep{}, err
	}
	var sweep ReportChargeSweep
	type jobRef struct{ tenantID, jobID string }
	touched := make(map[jobRef]bool, len(candidates))
	var failures []error
	for _, candidate := range candidates {
		ok, err := s.Store.OrphanReportCharge(ctx, candidate, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("charge %s: %w", candidate.LedgerKey, err))
			continue
		}
		if !ok {
			// Settled or released between the list and the CAS: the live writer
			// won, which is exactly what the CAS is for.
			continue
		}
		sweep.Orphaned++
		touched[jobRef{candidate.TenantID, candidate.JobID}] = true
		if err := s.auditChargeReconcile(ctx, candidate.TenantID, candidate.JobID,
			candidate.LedgerKey, "report.charge.orphaned", map[string]interface{}{
				"reason":    model.ReportReconcileStaleReserved,
				"outcome":   model.ReportReconcileOutcomeUnverified,
				"llm_usage": map[string]interface{}{"reserved": candidate.ReservedTokens},
			}, now); err != nil {
			failures = append(failures, fmt.Errorf("charge %s audit: %w", candidate.LedgerKey, err))
		}
	}
	for ref := range touched {
		if _, err := s.Store.RefreshReportChargeTotals(ctx, ref.tenantID, ref.jobID, now); err != nil {
			failures = append(failures, fmt.Errorf("report job %s totals: %w", ref.jobID, err))
			continue
		}
		sweep.Refreshed++
	}
	if sweep.Orphaned > 0 {
		// Loud on purpose: an orphan means we lost a call, and the scheduler that
		// lost it should be visible in the log even though nothing user-facing
		// breaks.
		logger.Warn("report charge reconcile orphaned stale reservations",
			"orphaned", sweep.Orphaned, "jobs", sweep.Refreshed)
	}
	if len(failures) > 0 {
		return sweep, failures[0]
	}
	return sweep, nil
}
