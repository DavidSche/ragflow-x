package service

// Archiving is a lifecycle change, not a deletion and not a retention change
// (doc/297 §2). The rules it enforces, and the reason each one exists:
//
//  1. Archive refuses while a run is active (409/44104). Archiving a report that is
//     generating would either kill a run the user already paid for or promise
//     "nothing will happen" while it is happening. Neither is a lifecycle change;
//     "cancel or wait, then archive" is.
//  2. Archive refuses while a timer is armed (409/44104). Archiving does not silently
//     disable somebody's schedule: `report.schedule.updated` is an audited write, and
//     an archive that rewrote it would put a config change in the audit log under a
//     different action name. Removing the plan first is one click and keeps every
//     write with its own name.
//  3. Every operation that would produce new content refuses an archived report
//     (44104): regenerate, section retry, outline confirm, schedule arming, budget
//     extension, sharing changes. What an archived report may still do is read:
//     detail, sections, revisions, follow-ups, and — deliberately — export.
//     Exporting is *delivery of knowledge that is already frozen*, derived from the
//     stored payload with no model call, so an archived report stays forwardable.
//     "Put away" means "stop producing", not "stop handing out what exists".
//  4. Archive is not a way out of the recovery window: a soft-deleted report refuses
//     archive (44109 via the read path — it is not visible), and restoring a deleted
//     report returns it to whatever it was, archived included, because the two facts
//     live in two columns.
//  5. Archive never touches `expires_at`. Promising longer retention through the
//     archive button would be a retention contract the janitor does not honor when
//     the workspace policy changes; the workspace retention overlay is the mechanism
//     that extends time (doc/288 §7.6 / T20b).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// reportLifecycleFilter resolves the `?status=` query value of the list endpoint.
//
// Three answers, because a list has three honest questions: "what am I working on"
// (the default, `active`), "what did I put away" (`archived`), and "everything"
// (`all`). An empty value means `active` rather than `all`: making the default
// "everything" would keep archived reports in the daily view, which is the one thing
// archiving is for.
func reportLifecycleFilter(raw string) (string, error) {
	switch raw {
	case "", model.ReportStatusActive:
		return model.ReportStatusActive, nil
	case model.ReportStatusArchived:
		return model.ReportStatusArchived, nil
	case "all":
		// `all` is a question the list may answer, not a value a row may hold, so it
		// resolves to "no predicate" instead of a status the store would never match.
		return "", nil
	}
	return "", errReportOutlineInvalid(
		"status must be active, archived or all")
}

// assertReportArchivedBlocksWork is the guard every write path calls after it has
// established ownership and before it writes anything that would produce content,
// start a run or change who may see the report.
func (s *Service) assertReportArchivedBlocksWork(report *model.Report) error {
	if report == nil {
		return errReportNotFound()
	}
	if report.Status == model.ReportStatusArchived {
		return errReportInvalidTransition("report is archived; unarchive it before changing it")
	}
	return nil
}

// ArchiveReport puts a finished report away (doc/297 §2).
//
// The answer is the report detail view, the same shape restore returns, because that
// is what the client needs next: a `{"archived":true}` would force every caller to
// re-read the report it just put away, and the panel it came from renders that view.
func (s *Service) ArchiveReport(
	ctx context.Context, actorID, tenantID, reportID, reason string,
) (*ReportDetail, error) {
	report, err := s.reportForLifecycle(ctx, actorID, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status == model.ReportStatusArchived {
		return nil, errReportInvalidTransition("report is already archived")
	}
	active, err := s.Store.CountActiveReportJobs(ctx, tenantID, report.ID)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, errReportInvalidTransition(
			"report is still running; wait for it to finish or cancel it first")
	}
	schedule, err := s.Store.GetReportSchedule(ctx, tenantID, report.ID)
	if err != nil {
		return nil, err
	}
	if schedule != nil && schedule.Enabled {
		// Only an *armed* plan blocks. A disabled plan is a note the owner kept, and
		// archiving with it in place unarchives back to exactly the same view.
		return nil, errReportInvalidTransition(
			"report still has an active schedule; remove it before archiving")
	}
	now := time.Now().UTC()
	applied, err := s.Store.SetReportStatus(
		ctx, tenantID, report.ID, model.ReportStatusActive, model.ReportStatusArchived, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		// Lost the race with a concurrent archive (or with a delete that closed the
		// row between the read and the write): the CAS is the authority.
		return nil, errReportInvalidTransition("report state changed; reload and try again")
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.archived",
		Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: reportArchiveDetail(reason, model.ReportStatusActive), At: now,
	}); auditErr != nil {
		// Archived already happened. Failing now would make the user retry and be told
		// "already archived", so the gap is logged and the achieved state is returned.
		logger.Warn("report archive audit failed", "report_id", report.ID, "error", auditErr)
	}
	return s.GetReport(ctx, actorID, tenantID, report.ID)
}

// UnarchiveReport returns a report to the daily list. It restores visibility and
// writability — nothing else: a report's revisions, artifacts and evidence are the
// same rows they were before it was archived, so this is one column and no cascade.
func (s *Service) UnarchiveReport(
	ctx context.Context, actorID, tenantID, reportID string,
) (*ReportDetail, error) {
	report, err := s.reportForLifecycle(ctx, actorID, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report.Status != model.ReportStatusArchived {
		return nil, errReportInvalidTransition("report is not archived")
	}
	now := time.Now().UTC()
	applied, err := s.Store.SetReportStatus(
		ctx, tenantID, report.ID, model.ReportStatusArchived, model.ReportStatusActive, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, errReportInvalidTransition("report state changed; reload and try again")
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.unarchived",
		Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: reportArchiveDetail("", model.ReportStatusArchived), At: now,
	}); auditErr != nil {
		logger.Warn("report unarchive audit failed", "report_id", report.ID, "error", auditErr)
	}
	return s.GetReport(ctx, actorID, tenantID, report.ID)
}

// reportForLifecycle resolves the report a lifecycle write targets.
//
// The action is `delete:own`: whoever may close a report may put it away and take it
// back out, and neither is a read permission anyone could hold separately. A report
// in its recovery window is answered as "no such report" (44109) rather than
// "deleted": the lifecycle of a row nobody can see is not something a caller gets to
// ask about.
func (s *Service) reportForLifecycle(
	ctx context.Context, actorID, tenantID, reportID string,
) (*model.Report, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report lifecycle changes require an authenticated workspace")
	}
	if err := s.AuthorizeObject(ctx, actorID, "delete:own", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errReportNotFound()
		}
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}
	if report.OwnerUserID != actorID {
		// Same rule as delete and share: a colleague cannot put away a report they
		// merely read, and the answer must not reveal that it exists (§8.7).
		return nil, errReportNotFound()
	}
	return report, nil
}

// reportArchiveDetail is the audit detail. It names the transition rather than the
// intent, so `report.archived` and `report.unarchived` share one shape and a reviewer
// can read either row without knowing which action wrote it.
func reportArchiveDetail(reason, from string) string {
	payload, err := json.Marshal(map[string]interface{}{
		"from": from, "reason": strings.TrimSpace(reason),
	})
	if err != nil {
		// Two strings cannot fail to marshal; a broken encoder must not turn a
		// completed archive into a 500, so the shape degrades to the transition alone.
		return `{"from":"` + from + `"}`
	}
	return string(payload)
}
