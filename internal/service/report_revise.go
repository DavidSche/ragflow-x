package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report regeneration (doc/288 §7.10 / §11.4 POST /api/v1/reports/:id/revisions)
// and revision-by-number reads (GET /api/v1/reports/:id/revisions/:revision).
//
// "重新生成（同条件）" is deliberately NOT a second report: it mints a new
// revision on the SAME report, `revision = current published revision + 1`, whose
// `based_on_revision` names the published base. The new run inherits the base
// revision's template (pinned version), its dataset bindings and its outline
// confirmation — the conditions are frozen from the base, not re-read from the
// caller, so a "same conditions" rerun cannot silently drift when the caller's
// request or the platform's defaults moved on (§7.10 / A38).
//
// Regeneration has its own idempotency namespace. Both creation and regeneration
// write `${user}:${clientKey}`-shaped keys into the single per-tenant unique
// index `uidx_rgx_report_job_idempotency`; if they shared a shape, a client that
// sent the same key string to POST /reports and then to POST /reports/:id/revisions
// would collide and replay the wrong aggregate. The regeneration namespace
// (`regenerate:` prefix) makes "same key = same regenerated revision+job" true
// while keeping regeneration unreachable by the creation key (§7.10 "不受创建
// 幂等键拦截").

// RegenerateReportRequest exists so the P0-A surface is explicit about what a
// caller may NOT change: there are no fields. Re-running same conditions means the
// request has nothing to say; a P0-B ability to override (rewrite only section X,
// change evidence policy) would add fields here and re-freeze them onto the new
// revision.
type RegenerateReportRequest struct{}

// RegenerateReport starts one new run on an existing, already-published report.
//
// Gates, in order: execute:report RBAC, then the report row (missing / not yours /
// in its cleanup window all answer 404/44109), then the idempotency replay, then
// "the current revision must be published" (409/44104 — there is no base to run
// "same conditions" against before the first version exists), then the single
// active-job gate (409/44110). The hard uniqueness backstop for the last two is
// the `(report_id, revision)` index: a concurrent different-key regeneration that
// passes both soft gates loses the insert race and is answered with the active-job
// conflict it actually is.
func (s *Service) RegenerateReport(
	ctx context.Context, actorID, tenantID, reportID, idempotencyKey string,
) (*CreateReportResult, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report regeneration requires an authenticated workspace")
	}
	clientKey, err := normalizeReportIdempotencyKey(idempotencyKey)
	if err != nil {
		return nil, err
	}
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	if report.DeletedAt != nil {
		// A report in its recovery window is not re-runnable. The answer is the
		// same one a purge would give (§7.6): no disclosure that it "used to exist".
		return nil, httperr.New(404, codeReportAccessDenied, "report is no longer available")
	}

	// Idempotent replay comes before any state gate: a retried POST must return
	// the revision+job this key already minted, not a 44110 because the run it
	// itself created is still going.
	storedKey := "regenerate:" + actorID + ":" + clientKey
	if existing, err := s.Store.GetReportJobByIdempotencyKey(ctx, tenantID, storedKey); err != nil {
		return nil, err
	} else if existing != nil {
		return s.replayRegeneration(ctx, tenantID, existing)
	}

	base, _, err := s.regenerationBase(ctx, tenantID, report)
	if err != nil {
		return nil, err
	}

	// The same-condition contract pins the template the base was built on. A
	// retired template still resolves by explicit version (historical runs keep
	// working), so only a vanishing row — a real deletion — refuses the rerun.
	templateRow, err := s.resolveReportTemplate(ctx, tenantID, base.TemplateKey, base.TemplateVersion)
	if err != nil {
		return nil, err
	}
	payload, err := reportTemplatePayloadOf(templateRow)
	if err != nil {
		return nil, err
	}
	if err := validateReportTemplatePayload(payload); err != nil {
		// A stored template that breaks its own contract is a server-side fault;
		// the caller's request is fine.
		logger.Warn("report template payload invalid on regeneration", "template_id", templateRow.ID, "error", err)
		return nil, httperr.Internal("report template is invalid")
	}

	now := time.Now().UTC()
	eventClock := reportExpiry(now, s.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays)
	revisionID := id.New()
	jobID := id.New()
	// Every business canary column is pinned from the base: the sections count (the
	// progress denominator) and the budget envelope come from the base's template
	// version, exactly as creation derives them — never from the live template.
	budgetJSON, err := json.Marshal(payload.Limits)
	if err != nil {
		return nil, err
	}
	revision := &model.ReportRevision{
		ID:                  revisionID,
		ReportID:            report.ID,
		TenantID:            tenantID,
		Revision:            base.Revision + 1,
		BasedOnRevision:     base.Revision,
		OutlineConfirmation: base.OutlineConfirmation,
		TemplateKey:         base.TemplateKey,
		TemplateVersion:     base.TemplateVersion,
		DatasetsJSON:        base.DatasetsJSON,
		Status:              model.ReportRevisionDraft,
		CreatedBy:           actorID,
		CreatedAt:           now,
	}
	job := &model.ReportJob{
		ID:             jobID,
		TenantID:       tenantID,
		ReportID:       report.ID,
		RevisionID:     revisionID,
		JobKind:        model.ReportJobKindGenerate,
		Status:         model.ReportJobQueued,
		Stage:          model.ReportStageOutlining,
		AttemptSeq:     1,
		SliceSeq:       1,
		ProgressTotal:  len(payload.Sections),
		BudgetJSON:     string(budgetJSON),
		ReservedTokens: reportBudgetReservation(payload.Limits, len(payload.Sections)),
		IdempotencyKey: storedKey,
		UpdatedAt:      now,
	}
	eventPayload, err := json.Marshal(map[string]interface{}{
		"attempt_seq": job.AttemptSeq, "slice_seq": job.SliceSeq,
		"template_key": base.TemplateKey, "template_version": base.TemplateVersion,
		"revision": revision.Revision, "based_on_revision": base.Revision,
		"regeneration": true,
	})
	if err != nil {
		return nil, err
	}
	event := &model.ReportJobEvent{
		TenantID: tenantID, JobID: jobID, Type: reportEventJobQueued,
		PayloadJSON: string(eventPayload), CreatedAt: now, ExpiresAt: eventClock,
	}

	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateReportRevision(ctx, revision); err != nil {
			return err
		}
		if err := tx.CreateReportJob(ctx, job); err != nil {
			return err
		}
		_, err := tx.AppendReportEvent(ctx, event)
		return err
	})
	if err != nil {
		if existing, getErr := s.Store.GetReportJobByIdempotencyKey(ctx, tenantID, storedKey); getErr == nil && existing != nil {
			return s.replayRegeneration(ctx, tenantID, existing)
		}
		// A concurrent different-key regeneration won the (report_id, revision)
		// insert race: it already holds the report's single active job, so the
		// honest answer is 44110 — not a raw constraint error.
		if active, countErr := s.Store.CountActiveReportJobs(ctx, tenantID, reportID); countErr == nil && active > 0 {
			return nil, errReportActiveJobConflict()
		}
		return nil, err
	}

	detail, err := json.Marshal(map[string]interface{}{
		"report_id": report.ID, "job_id": jobID, "revision_id": revisionID,
		"revision": revision.Revision, "based_on_revision": base.Revision,
		"template_key": base.TemplateKey, "template_version": base.TemplateVersion,
		"datasets_refresh": false, "budget_tokens": job.ReservedTokens, "idempotent": false,
	})
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.job.created",
		Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); auditErr != nil {
		logger.Warn("report regeneration audit failed", "report_id", report.ID, "error", auditErr)
		return nil, auditErr
	}
	return &CreateReportResult{Report: report, Revision: revision, Job: job, Created: true}, nil
}

// regenerationBase resolves the "same conditions" anchor and the single-job gate.
// It answers the two 409s a regeneration can hit before it may write:
//
//   - 44104 — the report's current revision is not published (never finished, or
//     retroactively superseded): there is no published base to run "同条件" against;
//   - 44110 — some non-terminal job already holds the report's single active run.
func (s *Service) regenerationBase(
	ctx context.Context, tenantID string, report *model.Report,
) (base *model.ReportRevision, active int64, err error) {
	base, err = s.Store.GetReportRevisionByID(ctx, tenantID, report.CurrentRevisionID)
	if err != nil {
		return nil, 0, err
	}
	if base == nil || base.Status != model.ReportRevisionPublished {
		return nil, 0, errReportInvalidTransition(
			"report current revision is not published and cannot be regenerated")
	}
	active, err = s.Store.CountActiveReportJobs(ctx, tenantID, report.ID)
	if err != nil {
		return nil, 0, err
	}
	if active > 0 {
		return nil, 0, errReportActiveJobConflict()
	}
	return base, active, nil
}

// replayRegeneration is the same-key answer: the revision+job this key minted,
// however far the report has moved since. `Created=false` tells the UI this was a
// retry, not a fresh run.
func (s *Service) replayRegeneration(
	ctx context.Context, tenantID string, job *model.ReportJob,
) (*CreateReportResult, error) {
	report, err := s.Store.GetReport(ctx, tenantID, job.ReportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		// The job outlived its report (purge removes both in one transaction): the
		// same non-disclosing "no longer available" answer as creation (§8.7).
		return nil, httperr.New(404, codeReportAccessDenied, "report is no longer available")
	}
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, job.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, httperr.New(404, codeReportRevisionNotFound,
			"report revision is no longer available")
	}
	return &CreateReportResult{Report: report, Revision: revision, Job: job, Created: false}, nil
}

// GetReportRevisionByNumber reads one specific revision of one report. The route
// carries the report id and the revision number instead of the revision id
// (doc/288 §11.4): a revision id is a barely-guessable global, while
// `report id + number` is scoped by the access layer and fails on the visible
// report rather than on an opaque id.
func (s *Service) GetReportRevisionByNumber(
	ctx context.Context, actorID, tenantID, reportID string, revision int64,
) (*ReportDetail, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	row, err := s.Store.GetReportRevision(ctx, tenantID, report.ID, revision)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errReportRevisionNotFound(revision)
	}
	return &ReportDetail{Report: report, Revision: row}, nil
}
