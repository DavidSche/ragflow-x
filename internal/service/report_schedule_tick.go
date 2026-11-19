package service

// The scheduler tick (doc/295 §2.4 / R20).
//
// One job kind, one tick: claim the due plans (`ClaimDueReportSchedules`, which
// advances `next_run_at` first), then for each claimed plan try to mint the next
// revision of its report. The tick is deliberately dumb about *how* a report is
// written — it calls the same `RegenerateReport` a human clicks — because a second
// generation path would drift from the first one immediately.
//
// Every outcome is one of six closed-set words on the schedule row. They exist so
// "why is there no new version today" is answerable without reading job rows:
//
//	created                 the run was armed
//	skipped_busy            a job is already running (the plan's turn is spent)
//	skipped_no_base         there is no published revision to update
//	skipped_template_missing the pinned template row is gone
//	skipped_disabled        the plan was disabled between claim and trigger
//	failed                  anything else; `last_error` carries the reason

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// reportScheduleTickWorker is the recurring job that drives every plan. It is
// registered unconditionally: a workspace without schedules costs one empty query
// per minute, which is cheaper than a registration that depends on configuration.
type reportScheduleTickWorker struct {
	svc *Service
}

func (w *reportScheduleTickWorker) Kind() string { return model.JobKindReportScheduleTick }

// reportScheduleTickInterval is how often the tick looks for due plans. A plan's
// resolution is a minute (the UI offers 00:00–23:59), so a minute is the finest
// interval that is still cheap: one indexed query per minute.
const reportScheduleTickInterval = time.Minute

const reportScheduleTickKeyPrefix = "report-schedule-tick:"

func (w *reportScheduleTickWorker) Run(ctx context.Context, job *model.Job) error {
	summary, err := w.svc.RunReportScheduleTick(ctx)
	if err != nil {
		logger.Warn("report schedule tick failed", "job_id", job.ID, "error", err)
	}
	// Re-arm before returning, even after a failure: a tick that stopped
	// rescheduling itself would silently freeze every plan, and the plans would
	// look "armed" in the UI while nothing ever ran.
	if _, scheduleErr := w.svc.ScheduleReportScheduleTick(ctx, job.ID); scheduleErr != nil {
		logger.Warn("report schedule tick reschedule failed", "job_id", job.ID, "error", scheduleErr)
		if err == nil {
			return scheduleErr
		}
	}
	_ = summary
	return err
}

// ScheduleReportScheduleTick arms the next tick. It is a platform-wide job (owned by
// the system tenant) and never keeps two active ticks: the due scan is global, so
// two ticks would only contend for the same row locks.
func (s *Service) ScheduleReportScheduleTick(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindReportScheduleTick, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	now := time.Now().UTC()
	key := reportScheduleTickKeyPrefix + strconv.FormatInt(now.Unix(), 10)
	return s.Runner.Enqueue(ctx, model.JobKindReportScheduleTick, key, SystemTenantID, "",
		now.Add(reportScheduleTickInterval), 0)
}

// reportScheduleTickSummary is the tick's outcome, logged and returned to tests.
type reportScheduleTickSummary struct {
	Claimed int `json:"claimed"`
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// RunReportScheduleTick claims and triggers. It is exported so the reconcile/test
// surfaces can drive one tick deterministically instead of waiting for the worker.
func (s *Service) RunReportScheduleTick(ctx context.Context) (*reportScheduleTickSummary, error) {
	now := time.Now().UTC()
	claimed, err := s.Store.ClaimDueReportSchedules(
		ctx, now, reportScheduleTickLimit, reportScheduleAdvance)
	if err != nil {
		return nil, err
	}
	summary := &reportScheduleTickSummary{Claimed: len(claimed)}
	for index := range claimed {
		outcome, revision, runErr := s.triggerScheduledReport(ctx, &claimed[index], now)
		finish := reportScheduleFinishOf(&claimed[index], outcome, revision, runErr, now)
		if _, err := s.Store.FinishReportScheduleRun(ctx, finish); err != nil {
			// The plan has already been advanced; a failed write-back costs accuracy
			// in the UI, not a duplicate run.
			logger.Warn("report schedule outcome not recorded",
				"schedule_id", claimed[index].ID, "status", outcome, "error", err)
		}
		switch outcome {
		case model.ReportScheduleStatusCreated:
			summary.Created++
		case model.ReportScheduleStatusFailed:
			summary.Failed++
		default:
			summary.Skipped++
		}
		if runErr != nil {
			logger.Warn("report schedule run failed",
				"schedule_id", claimed[index].ID, "report_id", claimed[index].ReportID, "error", runErr)
		}
	}
	return summary, nil
}

// triggerScheduledReport runs one plan's turn. The report id is the plan's own, and
// the owner is the principal: a schedule spends the owner's budget, never the
// system's, and the resulting revision is stamped `trigger=schedule`.
func (s *Service) triggerScheduledReport(
	ctx context.Context, schedule *model.ReportSchedule, now time.Time,
) (string, int64, error) {
	report, err := s.Store.GetReport(ctx, schedule.TenantID, schedule.ReportID)
	if err != nil {
		return model.ReportScheduleStatusFailed, 0, err
	}
	if report == nil {
		// The report is gone (deleted, or purged past its recovery window). The plan
		// is an orphan: disable it instead of retrying every occurrence forever.
		if _, err := s.Store.DeleteReportSchedule(ctx, schedule.TenantID, schedule.ReportID); err != nil {
			return model.ReportScheduleStatusFailed, 0, err
		}
		return model.ReportScheduleStatusSkippedNoBase, 0, nil
	}
	if !schedule.Enabled {
		return model.ReportScheduleStatusSkippedDisabled, 0, nil
	}
	if report.Status == model.ReportStatusArchived {
		// Archiving refuses to coexist with an armed plan (44104), so this is the
		// defence for the two ways the pair can still exist: a plan armed in the same
		// breath as the archive, or a row edited by hand. An archived report must not
		// produce content, and a plan that cannot run must not keep claiming turns —
		// the plan is switched off here and `last_status` says `skipped_disabled`,
		// which is true of the row after this write.
		if err := s.disableReportSchedule(ctx, schedule.ReportID, schedule.TenantID, now); err != nil {
			return model.ReportScheduleStatusFailed, 0, err
		}
		return model.ReportScheduleStatusSkippedDisabled, 0, nil
	}
	// `reuse_static` inherits the base revision's non-evidence sections only. The
	// set is computed here, from the base's delivered rows, for the same reason a
	// manual rerun's set is validated against them: naming a section the base never
	// wrote would be an error, and naming an evidence section would inherit a
	// conclusion that the corpus may no longer support.
	reuseSections, err := s.scheduledReuseSections(ctx, schedule)
	if err != nil {
		return model.ReportScheduleStatusFailed, 0, err
	}
	due := now
	if schedule.LastRunAt != nil {
		due = *schedule.LastRunAt
	}
	key := reportScheduleIdempotencyKey(schedule, due, now)
	result, err := s.RegenerateReport(ctx, report.OwnerUserID, schedule.TenantID,
		report.ID, key, RegenerateReportRequest{ReuseSections: reuseSections})
	if err != nil {
		return reportScheduleOutcomeOf(err), 0, err
	}
	if result == nil || result.Revision == nil {
		return model.ReportScheduleStatusFailed, 0,
			errors.New("scheduled regeneration returned no revision")
	}
	revision := result.Revision
	if err := s.Store.MarkReportRevisionScheduled(ctx, schedule.TenantID, revision.ID,
		schedule.ID, now); err != nil {
		return model.ReportScheduleStatusFailed, revision.Revision, err
	}
	logger.Info("report schedule triggered",
		"schedule_id", schedule.ID, "report_id", report.ID, "revision", revision.Revision)
	return model.ReportScheduleStatusCreated, revision.Revision, nil
}

// scheduledReuseSections derives the `reuse_static` set: succeeded sections whose
// template never required evidence. `full` means "no reuse at all".
func (s *Service) scheduledReuseSections(
	ctx context.Context, schedule *model.ReportSchedule,
) ([]int, error) {
	if schedule.UpdateMode != model.ReportScheduleUpdateReuseStatic {
		return nil, nil
	}
	report, err := s.Store.GetReport(ctx, schedule.TenantID, schedule.ReportID)
	if err != nil || report == nil {
		return nil, err
	}
	base, err := s.reportCurrentRevision(ctx, schedule.TenantID, report)
	if err != nil || base == nil {
		return nil, err
	}
	sections, err := s.Store.ListReportSections(ctx, schedule.TenantID, base.ID)
	if err != nil {
		return nil, err
	}
	ords := make([]int, 0, len(sections))
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded || section.RequiresEvidence {
			continue
		}
		ords = append(ords, section.Ord)
	}
	return ords, nil
}

// reportScheduleIdempotencyKey is the run's identity: one occurrence of one plan.
// Using the *due* instant rather than "now" is what makes a retried enqueue replay
// the same run instead of minting a second revision for the same minute.
func reportScheduleIdempotencyKey(
	schedule *model.ReportSchedule, due, now time.Time,
) string {
	instant := due
	if instant.IsZero() {
		// A plan that never ran has no `last_run_at`; the claimed `next_run_at` has
		// already been advanced past it, so the closest stable identity is the
		// current minute. Two ticks in the same minute still collapse into one run.
		instant = now.Truncate(time.Minute)
	}
	return "schedule:" + schedule.ID + ":" + strconv.FormatInt(instant.Unix(), 10)
}

// reportScheduleOutcomeOf maps a regeneration error onto the closed set. The
// mapping is by code, not by message: a schedule that skipped because of a busy job
// must say `skipped_busy`, and matching prose would break on the first rewording.
func reportScheduleOutcomeOf(err error) string {
	var httpErr *httperr.Error
	if errors.As(err, &httpErr) {
		switch httpErr.Code {
		case codeReportActiveJobConflict:
			return model.ReportScheduleStatusSkippedBusy
		case codeReportInvalidTransition:
			// No published base revision: "there is nothing to update from".
			return model.ReportScheduleStatusSkippedNoBase
		case codeReportTemplateNotFound:
			return model.ReportScheduleStatusSkippedTemplate
		}
	}
	return model.ReportScheduleStatusFailed
}

// reportScheduleFinishOf assembles the write-back. A skip clears `last_error` (there
// was no error) while `failed` sets it, so a stale message never explains a skip.
func reportScheduleFinishOf(
	schedule *model.ReportSchedule, status string, revision int64, runErr error, now time.Time,
) repository.ReportScheduleFinish {
	finish := repository.ReportScheduleFinish{
		TenantID: schedule.TenantID, ReportID: schedule.ReportID, Now: now,
		Status: status, Revision: revision,
	}
	if runErr != nil && status == model.ReportScheduleStatusFailed {
		finish.Error = runErr.Error()
	}
	return finish
}

// disableReportSchedule switches a plan off without deleting it. It is used by the
// tick when it finds a plan that may never run again (doc/297 §2.2): the owner keeps
// the cadence they configured, and `enabled=false` is the honest state of a plan that
// cannot produce a revision.
func (s *Service) disableReportSchedule(ctx context.Context, reportID, tenantID string, now time.Time) error {
	row, err := s.Store.GetReportSchedule(ctx, tenantID, reportID)
	if err != nil {
		return err
	}
	if row == nil || !row.Enabled {
		return nil
	}
	row.Enabled = false
	row.NextRunAt = time.Time{}
	row.UpdatedAt = now
	return s.Store.UpdateReportSchedule(ctx, row)
}
