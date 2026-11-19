package service

// Report archiving (doc/297 §2). Archiving is not deletion: the row stays, every
// delivered revision stays readable and downloadable, and the only thing that changes
// is what the report may still *produce*. These tests pin the two halves of that
// sentence, because a lifecycle verb that quietly became either "delete" or "freeze"
// would still look right in the UI:
//
//   - what archive must refuse (a run in flight, an armed timer) and what it must
//     leave alone (expires_at, revisions, artifacts);
//   - what an archived report may still do (read, export) and what it may not
//     (regenerate, retry, re-arm, extend, reshare);
//   - the two columns stay two columns: restoring a soft-deleted report returns it
//     archived, not active.

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestArchiveReportTakesItOutOfTheListButKeepsDelivery(t *testing.T) {
	fx := newReportFixture(t, "archive_basic")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report := fx.reportRow(t, revision.ReportID)
	before := *report

	detail, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID, "季度结束")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if detail.Report.Status != model.ReportStatusArchived {
		t.Fatalf("detail status=%q, want archived", detail.Report.Status)
	}

	// ① The lifecyle column changed and nothing else did: archiving is not a
	// retention promise (expires_at is the workspace overlay's job) and not a
	// content change (the published revision is byte-for-byte what it was).
	stored := fx.reportRow(t, report.ID)
	if stored.Status != model.ReportStatusArchived {
		t.Fatalf("stored status=%q", stored.Status)
	}
	if !stored.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("archive must not move expires_at: %v → %v", before.ExpiresAt, stored.ExpiresAt)
	}
	if stored.DeletedAt != nil {
		t.Fatalf("archive must not stamp deleted_at")
	}
	if current := fx.reportRevision(t, revision.ID); current.Status != model.ReportRevisionPublished {
		t.Fatalf("published revision must stay published, got %q", current.Status)
	}

	// ② The daily list drops it; the archived view finds it; `all` returns both.
	if rows, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, ""); err != nil {
		t.Fatalf("list active: %v", err)
	} else if total != 0 || len(rows) != 0 {
		t.Fatalf("default list must hide archived: total=%d rows=%d", total, len(rows))
	}
	if rows, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, "archived"); err != nil {
		t.Fatalf("list archived: %v", err)
	} else if total != 1 || len(rows) != 1 || rows[0].Status != model.ReportStatusArchived {
		t.Fatalf("archived view: total=%d rows=%d", total, len(rows))
	}
	if _, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, "all"); err != nil {
		t.Fatalf("list all: %v", err)
	} else if total != 1 {
		t.Fatalf("all view total=%d, want 1", total)
	}

	// ③ The audit row names the transition, not the intent.
	rows := fx.auditRows(t, "report.archived")
	if len(rows) != 1 {
		t.Fatalf("expected one report.archived row, got %d", len(rows))
	}

	// ④ Unarchive puts it back, and is itself audited.
	restored, err := fx.svc.UnarchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if restored.Report.Status != model.ReportStatusActive {
		t.Fatalf("unarchived status=%q", restored.Report.Status)
	}
	if _, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, ""); err != nil {
		t.Fatalf("list active again: %v", err)
	} else if total != 1 {
		t.Fatalf("unarchived report must be back in the daily list, total=%d", total)
	}
	if len(fx.auditRows(t, "report.unarchived")) != 1 {
		t.Fatalf("expected one report.unarchived row")
	}

	// ⑤ Repeating a verb answers the transition, not the state: a client that
	// retried a successful archive must not be told it worked twice.
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID, ""); err != nil {
		t.Fatalf("re-archive after unarchive: %v", err)
	}
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID, ""); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("archive twice: code=%d, want %d", reportErrorCode(err), codeReportInvalidTransition)
	}
	if _, err := fx.svc.UnarchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if _, err := fx.svc.UnarchiveReport(ctx, fx.owner.ID, fx.tenantID, report.ID); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("unarchive twice: code=%d, want %d", reportErrorCode(err), codeReportInvalidTransition)
	}
}

func TestArchiveReportRefusesWhileWorkIsInFlight(t *testing.T) {
	fx := newReportFixture(t, "archive_busy")
	ctx := context.Background()

	// ① A run in flight: archiving would either kill work already paid for or lie
	// about it. The answer is "cancel or wait", which is 44104.
	created := fx.createReport(t, fx.datasetLink, "req-archive-busy-0001")
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, ""); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("archive while running: code=%d, want %d", reportErrorCode(err), codeReportInvalidTransition)
	}
	if row := fx.reportRow(t, created.Report.ID); row.Status != model.ReportStatusActive {
		t.Fatalf("refused archive must leave the row active, got %q", row.Status)
	}

	// ② An armed timer: the schedule is an audited write of its own, so archiving
	// does not silently disable it. Remove the plan first.
	published := publishReport(t, fx, successSeeds())
	if _, _, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, published.ReportID,
		ReportScheduleRequest{Cadence: model.ReportScheduleCadenceDaily, Hour: intPtr(7), Minute: intPtr(0)}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, published.ReportID, ""); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("archive with armed schedule: code=%d, want %d", reportErrorCode(err), codeReportInvalidTransition)
	}
	if _, err := fx.svc.DeleteReportSchedule(ctx, fx.owner.ID, fx.tenantID, published.ReportID); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, published.ReportID, ""); err != nil {
		t.Fatalf("archive after removing the plan: %v", err)
	}

	// ③ A soft-deleted report is not visible, so its lifecycle is not answerable:
	// 44109, the same word every other call uses for "not yours to see".
	// `force=false`: an operator may delete their own report but not shorten its
	// recovery window, which is what `force` asks for.
	if err := fx.svc.SoftDeleteReport(ctx, fx.owner.ID, fx.tenantID, published.ReportID, "清理", false); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := fx.svc.UnarchiveReport(ctx, fx.owner.ID, fx.tenantID, published.ReportID); reportErrorCode(err) != codeReportAccessDenied {
		t.Fatalf("unarchive deleted: code=%d, want %d", reportErrorCode(err), codeReportAccessDenied)
	}

	// ④ Restoring a deleted report must return it *archived*, because the two facts
	// live in two columns. A restore that also unarchived would make the archive
	// button a partial delete.
	if _, err := fx.svc.RestoreReport(ctx, fx.owner.ID, fx.tenantID, published.ReportID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if row := fx.reportRow(t, published.ReportID); row.Status != model.ReportStatusArchived {
		t.Fatalf("restored report must still be archived, got %q", row.Status)
	}
}

func TestArchivedReportStillReadsAndExportsButProducesNothing(t *testing.T) {
	fx := newReportFixture(t, "archive_guard")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	reportID := revision.ReportID
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, reportID, ""); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// ① Reads are untouched: an archived report is knowledge that was delivered.
	if _, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil {
		t.Fatalf("detail of archived report: %v", err)
	}
	if revisions, err := fx.svc.ListReportRevisions(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil || len(revisions) == 0 {
		t.Fatalf("revisions of archived report: %v (n=%d)", err, len(revisions))
	}

	// ② Export is deliberately still allowed: it re-renders the stored payload with
	// no model call and no new content, and "no longer forwardable" is not what
	// putting a report away means (doc/297 §2.1).
	exported, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: reportID, Revision: revision.Revision, Format: model.ExportFormatHTML,
	})
	if err != nil {
		t.Fatalf("export an archived report: %v", err)
	}
	if exported.Artifact == nil || exported.Artifact.ArtifactStatus != model.ExportArtifactReady || !exported.Downloadable {
		t.Fatalf("archived export artifact=%v", exported.Artifact)
	}

	// ③ Every producer refuses, with the same 44104 the other transitions use.
	if _, err := fx.svc.RegenerateReport(ctx, fx.owner.ID, fx.tenantID, reportID, "req-archive-regen", RegenerateReportRequest{}); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("regenerate archived: code=%d", reportErrorCode(err))
	}
	latest, err := fx.svc.Store.GetLatestReportJob(ctx, fx.tenantID, reportID)
	if err != nil || latest == nil {
		t.Fatalf("latest job: %v", err)
	}
	if _, err := fx.svc.RetryReportSection(ctx, fx.owner.ID, fx.tenantID, latest.ID, 1); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("retry section of archived: code=%d", reportErrorCode(err))
	}
	if _, _, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID,
		ReportScheduleRequest{Cadence: model.ReportScheduleCadenceDaily, Hour: intPtr(7), Minute: intPtr(0)}); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("schedule an archived report: code=%d", reportErrorCode(err))
	}
	if _, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, reportID,
		ReportBudgetExtensionRequest{Tokens: 1000, Reason: "再来一点"}); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("extend an archived report: code=%d", reportErrorCode(err))
	}
	if _, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, reportID,
		SetReportAccessRequest{Policy: model.ReportAccessOwnerOnly}); reportErrorCode(err) != codeReportInvalidTransition {
		t.Fatalf("reshare an archived report: code=%d", reportErrorCode(err))
	}

	// ④ Nothing above left a run behind: the only job is still the delivered one.
	if current, err := fx.svc.Store.GetLatestReportJob(ctx, fx.tenantID, reportID); err != nil {
		t.Fatalf("latest job: %v", err)
	} else if current != nil && current.Status == model.ReportJobRunning {
		t.Fatalf("no run may start for an archived report, job=%s", current.Status)
	}
}

func TestArchiveReportIsOwnerOnly(t *testing.T) {
	fx := newReportFixture(t, "archive_owner")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	colleague := reportAccessActor(t, fx, "report-archive-colleague")

	// A colleague who can read the report still cannot decide it stops being a
	// working document; the answer is 44109 so the button's existence is not leaked.
	if _, err := fx.svc.ArchiveReport(ctx, colleague.ID, fx.tenantID, revision.ReportID, ""); reportErrorCode(err) != codeReportAccessDenied {
		t.Fatalf("colleague archive: code=%d, want %d", reportErrorCode(err), codeReportAccessDenied)
	}
	if row := fx.reportRow(t, revision.ReportID); row.Status != model.ReportStatusActive {
		t.Fatalf("refused archive changed the row: %q", row.Status)
	}
	// And the same for an unknown report id: archive is not an existence oracle.
	if _, err := fx.svc.ArchiveReport(ctx, fx.owner.ID, fx.tenantID, "nope", ""); reportErrorCode(err) != codeReportAccessDenied {
		t.Fatalf("archive unknown: code=%d", reportErrorCode(err))
	}
}

func TestReportListStatusFilterIsAClosedSet(t *testing.T) {
	fx := newReportFixture(t, "archive_filter")
	ctx := context.Background()
	publishReport(t, fx, successSeeds())

	// The filter is a request shape, so an unknown value is 400/44105 — the same
	// answer the other report parameters give, not an empty page that looks like
	// "you have no archived reports".
	for _, value := range []string{"archived; ", "deleted", "ARCHIVED", "0"} {
		if _, _, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, value); reportErrorCode(err) != codeReportOutlineInvalid {
			t.Fatalf("status=%q: code=%d, want %d", value, reportErrorCode(err), codeReportOutlineInvalid)
		}
	}
	// `archived` on a workspace that archived nothing is an empty page, not an error.
	if rows, total, err := fx.svc.ListReports(ctx, fx.owner.ID, fx.tenantID, 1, 20, "archived"); err != nil {
		t.Fatalf("empty archived view: %v", err)
	} else if total != 0 || len(rows) != 0 {
		t.Fatalf("archived view of a live report: total=%d rows=%d", total, len(rows))
	}
}
