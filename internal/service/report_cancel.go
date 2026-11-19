package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report cancellation (doc/288 §7.10 / §8.4).
//
// Cancel is an *intent*, not a state: the request writes `cancel_requested` and
// `cancel_requested_at`, and the terminal `cancelled` is derived by finalize. That
// split is what lets a cancel racing a finished slice publish the finished work
// instead of discarding it — the case §7.10 explicitly allows.
//
// Four convergence points consume the flag (they belong to the slice that owns the
// job, so they land with the generate worker):
//
//  1. before a slice starts writing,
//  2. between sections,
//  3. before assembly/publish,
//  4. before the terminal write.
//
// A job with **no live slice** has none of those points, so this request asks for
// the terminal write itself, through the same `BeginReportFinalization` the worker
// calls. Nothing here writes a terminal state: the outbox consumer and the
// reconcile sweep remain the only two triggers, and finalize stays the only writer.

// ReportCancelResult is the POST /reports/:id/cancel answer. `Status` is the status
// *after* the request, which is normally `cancel_requested`: the caller learns
// whether the run will stop without being told a terminal state that has not been
// written yet.
type ReportCancelResult struct {
	ReportID          string    `json:"report_id"`
	JobID             string    `json:"job_id"`
	Status            string    `json:"status"`
	CancelRequestedAt time.Time `json:"cancel_requested_at"`
	// AlreadyRequested marks a repeated cancel. It is `false` for a terminal job,
	// which is refused with 44104 instead: "already finished" must not read as
	// "your cancel worked".
	AlreadyRequested bool `json:"already_requested"`
}

// CancelReport asks the current job of one report to stop.
//
// Ownership is `owner_only` (44109 for anyone else), because a cancel is an
// operation on the caller's own deliverable, not a governance action. It is *not*
// `authorizeReportRead`: sharing widened the read side (doc/296 §2.1) and must not
// have widened this one, and a shared reader who could stop the owner's run would
// have been handed a write by a read permission.
func (s *Service) CancelReport(ctx context.Context, actorID, tenantID, reportID string) (*ReportCancelResult, error) {
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.assertReportOwner(ctx, actorID, report); err != nil {
		return nil, err
	}
	job, err := s.Store.GetLatestReportJob(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		// A live report without a job cannot exist (creation writes both, the purge
		// removes both). Answering "not found" keeps the shape of the other reads.
		return nil, errReportNotFound()
	}
	now := time.Now().UTC()
	switch {
	case job.FinalizedAt != nil || isReportJobTerminal(job.Status):
		return nil, errReportInvalidTransition("report job has already finished")
	case job.Status == model.ReportJobCancelRequested:
		// Repeated cancel: the flag is already set, so report the intent that exists
		// instead of pretending a second state change happened.
		result := &ReportCancelResult{
			ReportID: reportID, JobID: job.ID, Status: job.Status,
			CancelRequestedAt: reportTimeOr(job.CancelRequestedAt, now), AlreadyRequested: true,
		}
		if err := s.convergeCancelRequestedJob(ctx, tenantID, job, now); err != nil {
			return nil, err
		}
		return result, nil
	}
	applied, err := s.Store.RequestReportJobCancel(ctx, tenantID, job.ID, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		// The status moved between the read and the write (a concurrent finalize, or
		// a second cancel). Re-read once and answer from the new state instead of
		// claiming success for a write that did not land.
		current, err := s.Store.GetReportJob(ctx, tenantID, job.ID)
		if err != nil {
			return nil, err
		}
		if current == nil || current.FinalizedAt != nil || isReportJobTerminal(current.Status) {
			return nil, errReportInvalidTransition("report job has already finished")
		}
		return &ReportCancelResult{
			ReportID: reportID, JobID: current.ID, Status: current.Status,
			CancelRequestedAt: reportTimeOr(current.CancelRequestedAt, now), AlreadyRequested: true,
		}, nil
	}
	job.Status = model.ReportJobCancelRequested
	job.CancelRequestedAt = &now
	if err := s.convergeCancelRequestedJob(ctx, tenantID, job, now); err != nil {
		return nil, err
	}
	detail, err := json.Marshal(map[string]interface{}{
		"job_id": job.ID, "status": model.ReportJobCancelRequested,
	})
	if err != nil {
		return nil, err
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.job.cancel_requested",
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); err != nil {
		// Same rule as creation: a report whose state change cannot be audited must
		// not silently change state. The flag is already written, so the honest
		// answer is a failure the caller can retry — converge is idempotent.
		return nil, err
	}
	return &ReportCancelResult{
		ReportID: reportID, JobID: job.ID, Status: model.ReportJobCancelRequested,
		CancelRequestedAt: now,
	}, nil
}

// convergeCancelRequestedJob asks for the terminal write when no slice can do it.
//
// "No slice can do it" is precise: the job either never got a slice (cancelled while
// `queued` or `awaiting_outline`) or its slice has already settled. A live slice is
// left alone — its own checkpoint reads the flag, and forcing `finalizing` under a
// running slice is exactly the window A34 closes.
func (s *Service) convergeCancelRequestedJob(
	ctx context.Context, tenantID string, job *model.ReportJob, now time.Time,
) error {
	if job == nil || job.Status != model.ReportJobCancelRequested || job.FinalizedAt != nil {
		return nil
	}
	if job.CurrentRuntimeJobID != "" {
		runtimeJob, err := s.Store.GetJob(ctx, tenantID, job.CurrentRuntimeJobID)
		if err != nil {
			return err
		}
		if runtimeJob != nil && !reportRuntimeSettled(runtimeJob.Status) {
			return nil
		}
	}
	_, err := s.BeginReportFinalization(
		ctx, tenantID, job.ID, job.AttemptSeq, job.SliceSeq, job.CurrentRuntimeJobID)
	return err
}

// isReportJobTerminal reports whether a business status can no longer move. The
// four values are the ones finalize writes (§8.4); `finalizing` is not terminal —
// its terminal write has not happened yet, which is the whole point of the state.
func isReportJobTerminal(status string) bool {
	switch status {
	case model.ReportJobSucceeded, model.ReportJobPartial,
		model.ReportJobFailed, model.ReportJobCancelled:
		return true
	}
	return false
}

// reportTimeOr dereferences an optional clock, falling back to a known instant so
// the response never carries a zero time that serialises as year 1.
func reportTimeOr(value *time.Time, fallback time.Time) time.Time {
	if value != nil {
		return *value
	}
	return fallback
}
