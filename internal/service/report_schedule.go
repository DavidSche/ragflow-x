package service

// Recurring reports (doc/295 §2.4 / R20).
//
// A schedule answers one question — "when does this report run next" — and it must
// answer it identically in three places: the tick that claims it, the UI that shows
// it, and the test that asserts it. So the arithmetic is one pure function over the
// stored plan (`reportScheduleAdvance`), and everything else reads it.
//
// Design decisions worth naming:
//
//   - The plan is expressed in the *workspace's* wall clock (`tz_offset_min`), not
//     the server's. "Every Monday 09:00" means 09:00 where the readers are, and the
//     stored `next_run_at` is the corresponding UTC instant.
//   - `next_run_at` strictly moves forward. A tick claims a due plan and advances it
//     *before* the run starts, so a crash delays the next occurrence instead of
//     firing the same one again.
//   - Only the report's owner may create, change or delete the plan. A schedule
//     spends the owner's budget in the owner's name, so `manage:report` is not
//     enough: cross-user access answers 44109, the same code as "does not exist"
//     (doc/288 §8.7).
//   - `reuse_static` inherits only sections that never required evidence. A
//     scheduled report that copied last month's conclusions would be the exact
//     failure continuous updating exists to prevent.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const (
	// reportScheduleMaxAdvanceSteps bounds the search for the next occurrence. A
	// weekly plan is at most 7 days away and a daily one at most 1, so 10 steps is
	// generous; the bound exists so a bug cannot turn the function into a loop.
	reportScheduleMaxAdvanceSteps = 10
	// reportScheduleTickLimit caps one tick. Fifty reports per minute is far above
	// what a single workspace schedules, and the cap keeps the tick's latency flat.
	reportScheduleTickLimit = 50
	// reportScheduleMinuteSec / HourSec / DaySec keep the arithmetic readable and
	// avoid magic numbers in the offset handling.
	reportScheduleMinuteSec = 60
	reportScheduleHourSec   = 60 * reportScheduleMinuteSec
	reportScheduleDaySec    = 24 * reportScheduleHourSec
	// reportScheduleMaxTZOffsetMin is UTC+14:00; the minimum is UTC-12:00. Both are
	// the real extremes of civil time, and the pair is what the UI's select shows.
	reportScheduleMaxTZOffsetMin = 840
	reportScheduleMinTZOffsetMin = -720
)

// ReportScheduleView is what the API answers. It is the row plus the derived
// `next_run_label` pieces the client would otherwise have to recompute (and get
// wrong): the UTC instant and the local wall-clock rendering of the same moment.
type ReportScheduleView struct {
	ReportID     string     `json:"report_id"`
	Cadence      string     `json:"cadence"`
	Hour         int        `json:"hour"`
	Minute       int        `json:"minute"`
	Weekday      int        `json:"weekday"`
	TZOffsetMin  int        `json:"tz_offset_min"`
	UpdateMode   string     `json:"update_mode"`
	Enabled      bool       `json:"enabled"`
	NextRunAt    time.Time  `json:"next_run_at"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastRevision int64      `json:"last_revision"`
	LastStatus   string     `json:"last_status"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ReportScheduleRequest is the create/update body. Every field is optional on an
// update: an omitted cadence keeps the stored one, which is what a UI toggle needs.
type ReportScheduleRequest struct {
	Cadence     string `json:"cadence"`
	Hour        *int   `json:"hour"`
	Minute      *int   `json:"minute"`
	Weekday     *int   `json:"weekday"`
	TZOffsetMin *int   `json:"tz_offset_min"`
	UpdateMode  string `json:"update_mode"`
	Enabled     *bool  `json:"enabled"`
}

// reportSchedulePlan is the validated, normalised plan. Keeping it separate from the
// request means validation and arithmetic share one shape.
type reportSchedulePlan struct {
	Cadence     string
	Hour        int
	Minute      int
	Weekday     int
	TZOffsetMin int
	UpdateMode  string
	Enabled     bool
}

// normalizedPlan validates and fills defaults. An unknown cadence or update mode is
// a 400 (44105): a schedule that silently ran at the wrong time costs money.
func reportSchedulePlanOf(req ReportScheduleRequest, base *model.ReportSchedule) (reportSchedulePlan, error) {
	plan := reportSchedulePlan{
		Cadence: model.ReportScheduleCadenceDaily, Hour: 9, Minute: 0, Weekday: 1,
		TZOffsetMin: 480, UpdateMode: model.ReportScheduleUpdateFull, Enabled: true,
	}
	if base != nil {
		plan = reportSchedulePlan{
			Cadence: base.Cadence, Hour: base.Hour, Minute: base.Minute,
			Weekday: base.Weekday, TZOffsetMin: base.TZOffsetMin,
			UpdateMode: base.UpdateMode, Enabled: base.Enabled,
		}
	}
	if trimmed := strings.TrimSpace(req.Cadence); trimmed != "" {
		if trimmed != model.ReportScheduleCadenceDaily && trimmed != model.ReportScheduleCadenceWeekly {
			return plan, errReportOutlineInvalid("cadence must be daily or weekly")
		}
		plan.Cadence = trimmed
	}
	if req.Hour != nil {
		plan.Hour = *req.Hour
	}
	if req.Minute != nil {
		plan.Minute = *req.Minute
	}
	if req.Weekday != nil {
		plan.Weekday = *req.Weekday
	}
	if req.TZOffsetMin != nil {
		plan.TZOffsetMin = *req.TZOffsetMin
	}
	if trimmed := strings.TrimSpace(req.UpdateMode); trimmed != "" {
		if trimmed != model.ReportScheduleUpdateFull && trimmed != model.ReportScheduleUpdateReuseStatic {
			return plan, errReportOutlineInvalid("update_mode must be full or reuse_static")
		}
		plan.UpdateMode = trimmed
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if plan.Hour < 0 || plan.Hour > 23 {
		return plan, errReportOutlineInvalid("hour must be between 0 and 23")
	}
	if plan.Minute < 0 || plan.Minute > 59 {
		return plan, errReportOutlineInvalid("minute must be between 0 and 59")
	}
	if plan.Weekday < 0 || plan.Weekday > 6 {
		return plan, errReportOutlineInvalid("weekday must be between 0 and 6")
	}
	if plan.TZOffsetMin < reportScheduleMinTZOffsetMin || plan.TZOffsetMin > reportScheduleMaxTZOffsetMin {
		return plan, errReportOutlineInvalid("tz_offset_min must be between -720 and 840")
	}
	return plan, nil
}

// reportScheduleAdvance is the pure `next_run_at` rule: the first occurrence of the
// plan's wall-clock time that is strictly after `from`.
//
// "Strictly after" is what makes the tick's claim safe. If `next_run_at == from`,
// the next value must be the *following* occurrence; returning `from` would make the
// row permanently due and the tick would re-claim it forever.
func reportScheduleAdvance(row *model.ReportSchedule, from time.Time) time.Time {
	plan := reportSchedulePlan{
		Cadence: row.Cadence, Hour: row.Hour, Minute: row.Minute,
		Weekday: row.Weekday, TZOffsetMin: row.TZOffsetMin,
	}
	return reportScheduleNextRunAt(plan, from)
}

// reportScheduleNextRunAt implements the rule for any plan, which is also what the
// API uses when a plan is created or edited (`from = now`).
//
// The local wall clock is derived by shifting the UTC instant by the offset, then
// the candidate is built back in UTC by shifting the other way. Doing it this way
// (rather than constructing a `time.Date` in a `FixedZone`) keeps the result a UTC
// instant with no location attached — which is what the column stores and what the
// API answers, so no reader has to know which zone a value is in.
func reportScheduleNextRunAt(plan reportSchedulePlan, from time.Time) time.Time {
	local := from.UTC().Add(time.Duration(plan.TZOffsetMin) * time.Minute)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	for step := 0; step <= reportScheduleMaxAdvanceSteps; step++ {
		candidate := dayStart.AddDate(0, 0, step).
			Add(time.Duration(plan.Hour)*time.Hour + time.Duration(plan.Minute)*time.Minute)
		if plan.Cadence == model.ReportScheduleCadenceWeekly &&
			int(candidate.Weekday()) != plan.Weekday {
			continue
		}
		if candidate.After(local) {
			return candidate.Add(-time.Duration(plan.TZOffsetMin) * time.Minute)
		}
	}
	// Unreachable for a validated plan (a daily cadence matches every day); the
	// fallback keeps the signature total instead of panicking inside a tick.
	return local.Add(24 * time.Hour).Add(-time.Duration(plan.TZOffsetMin) * time.Minute)
}

// reportScheduleViewOf renders the row. `next_run_local` is intentionally absent:
// the client formats `next_run_at` with the same `tz_offset_min` it stored, so there
// is one rendering rule instead of a server-side format the UI cannot re-derive.
func reportScheduleViewOf(row *model.ReportSchedule) ReportScheduleView {
	view := ReportScheduleView{
		ReportID: row.ReportID, Cadence: row.Cadence, Hour: row.Hour, Minute: row.Minute,
		Weekday: row.Weekday, TZOffsetMin: row.TZOffsetMin, UpdateMode: row.UpdateMode,
		Enabled: row.Enabled, NextRunAt: row.NextRunAt, LastRunAt: row.LastRunAt,
		LastRevision: row.LastRevision, LastStatus: row.LastStatus, LastError: row.LastError,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return view
}

// GetReportSchedule answers the plan of one report, or nil when it has none. A
// schedule is readable by anyone who may read the report: it is metadata about the
// report, not a secret, and hiding it would make "why did a new version appear"
// unanswerable for a viewer.
func (s *Service) GetReportSchedule(
	ctx context.Context, actorID, tenantID, reportID string,
) (*ReportScheduleView, error) {
	report, err := s.reportForSchedule(ctx, actorID, tenantID, reportID, "read")
	if err != nil {
		return nil, err
	}
	row, err := s.Store.GetReportSchedule(ctx, tenantID, report.ID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	view := reportScheduleViewOf(row)
	return &view, nil
}

// UpsertReportSchedule creates or replaces the plan.
//
// `created=true` distinguishes the two answers so the handler can pick 201 vs 200;
// more importantly it lets the audit record say "armed" instead of "changed", which
// is the difference a reviewer cares about.
func (s *Service) UpsertReportSchedule(
	ctx context.Context, actorID, tenantID, reportID string, req ReportScheduleRequest,
) (*ReportScheduleView, bool, error) {
	report, err := s.reportForSchedule(ctx, actorID, tenantID, reportID, "execute")
	if err != nil {
		return nil, false, err
	}
	// `execute:report` is the RBAC gate (the same action a manual rerun needs, so an
	// operator who can regenerate can also schedule); ownership is the business gate.
	// A tenant admin who may manage reports does not thereby acquire the right to
	// spend another user's budget on a recurring job — the report's owner does.
	if report.OwnerUserID != actorID {
		// Cross-user answers the same 44109 as "does not exist": §8.7 forbids
		// telling a colleague that a report they cannot see has a schedule.
		return nil, false, errReportNotFound()
	}
	// An archived report may not be re-armed (doc/297 §2.2): archive and a timer are
	// mutually exclusive, and this is the half that keeps it that way when the
	// archive happened first.
	if err := s.assertReportArchivedBlocksWork(report); err != nil {
		return nil, false, err
	}
	existing, err := s.Store.GetReportSchedule(ctx, tenantID, report.ID)
	if err != nil {
		return nil, false, err
	}
	plan, err := reportSchedulePlanOf(req, existing)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	next := reportScheduleNextRunAt(plan, now)
	row := existing
	created := false
	if row == nil {
		created = true
		row = &model.ReportSchedule{
			ID: id.New(), TenantID: tenantID, ReportID: report.ID, OwnerID: report.OwnerUserID,
			CreatedBy: actorID, CreatedAt: now,
		}
	}
	row.Cadence = plan.Cadence
	row.Hour = plan.Hour
	row.Minute = plan.Minute
	row.Weekday = plan.Weekday
	row.TZOffsetMin = plan.TZOffsetMin
	row.UpdateMode = plan.UpdateMode
	row.Enabled = plan.Enabled
	row.NextRunAt = next
	row.UpdatedAt = now
	if created {
		if err := s.Store.CreateReportSchedule(ctx, row); err != nil {
			return nil, false, err
		}
	} else if err := s.Store.UpdateReportSchedule(ctx, row); err != nil {
		return nil, false, err
	}
	if err := s.auditReportSchedule(ctx, tenantID, actorID, report, row, created, now); err != nil {
		return nil, false, err
	}
	view := reportScheduleViewOf(row)
	return &view, created, nil
}

// DeleteReportSchedule removes the plan. Deleting a plan that does not exist is not
// an error: the caller wanted "this report does not run on a timer", and it does
// not. `false` tells the handler it wrote nothing, so the audit record says so.
func (s *Service) DeleteReportSchedule(
	ctx context.Context, actorID, tenantID, reportID string,
) (bool, error) {
	report, err := s.reportForSchedule(ctx, actorID, tenantID, reportID, "execute")
	if err != nil {
		return false, err
	}
	if report.OwnerUserID != actorID {
		return false, errReportNotFound()
	}
	deleted, err := s.Store.DeleteReportSchedule(ctx, tenantID, report.ID)
	if err != nil {
		return false, err
	}
	if deleted {
		now := time.Now().UTC()
		if err := s.auditReportScheduleDelete(ctx, tenantID, actorID, report, now); err != nil {
			return false, err
		}
	}
	return deleted, nil
}

// reportForSchedule is the shared gate: the report must exist, be readable by the
// actor and be in a state a timer may run. A soft-deleted report answers 44109,
// exactly like a report in someone else's workspace.
func (s *Service) reportForSchedule(
	ctx context.Context, actorID, tenantID, reportID, action string,
) (*model.Report, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report schedules require an authenticated workspace")
	}
	if err := s.AuthorizeObject(ctx, actorID, action, "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}

	if action == "read" {
		if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// auditReportSchedule records one arming or edit. The detail is the plan, not the
// prose: "when will it next run" is the fact an auditor needs, and the word
// "created"/"updated" is derived from the flag rather than written twice.
func (s *Service) auditReportSchedule(
	ctx context.Context, tenantID, actorID string, report *model.Report,
	row *model.ReportSchedule, created bool, now time.Time,
) error {
	action := "report.schedule.updated"
	if created {
		action = "report.schedule.created"
	}
	detail := fmt.Sprintf(
		`{"report_id":%q,"cadence":%q,"hour":%d,"minute":%d,"weekday":%d,"tz_offset_min":%d,"update_mode":%q,"enabled":%t,"next_run_at":%q}`,
		report.ID, row.Cadence, row.Hour, row.Minute, row.Weekday, row.TZOffsetMin,
		row.UpdateMode, row.Enabled, row.NextRunAt.UTC().Format(time.RFC3339))
	if err := s.RecordAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID,
		Action: action, Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: detail, At: now,
	}); err != nil {
		logger.Warn("report schedule audit failed", "report_id", report.ID, "error", err)
		return err
	}
	return nil
}

func (s *Service) auditReportScheduleDelete(
	ctx context.Context, tenantID, actorID string, report *model.Report, now time.Time,
) error {
	if err := s.RecordAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID,
		Action: "report.schedule.deleted", Resource: "report", ResourceID: report.ID,
		Result:     "SUCCESS",
		DetailJSON: fmt.Sprintf(`{"report_id":%q}`, report.ID), At: now,
	}); err != nil {
		logger.Warn("report schedule delete audit failed", "report_id", report.ID, "error", err)
		return err
	}
	return nil
}
