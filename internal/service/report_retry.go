package service

// Section retry (doc/288 §7.9 §8.7 §11.4; P0-B).
//
// `POST /report-jobs/:jobId/sections/:ord/retry` gives the user the one repair
// channel a published-but-partial report offers: rewrite a single failed section
// instead of regenerating the whole revision. It is a *new* business job
// (`job_kind = section_retry`) with its own slice key, because the original
// generate job is terminal and its section rows are immutable history.
//
// The materialization follows §7.10 verbatim: any content change writes a *new*
// section row (`section_version + 1`) recording `based_on_section_version` and
// `rewrite_reason = retry`, and the old failed row is marked `superseded` — never
// updated, never deleted. The new row starts `pending` with a fresh attempt
// counter, so the retried section consumes a full slice like a first write would.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// RetryReportSectionResult is what the route echoes. `RuntimeJobID` is non-empty
// when the first slice of the retry was armed (the normal path) and empty when a
// dispatch was refused (the job stays queued for the reconcile sweep).
type RetryReportSectionResult struct {
	ReportID      string              `json:"report_id"`
	RevisionID    string              `json:"revision_id"`
	Job           model.ReportJob     `json:"job"`
	Section       model.ReportSection `json:"section"`
	SupersededID  string              `json:"superseded_section_id"`
	SupersededVer int                 `json:"superseded_version"`
	RuntimeJobID  string              `json:"runtime_job_id,omitempty"`
}

// RetryReportSection rewrites one failed section of the current published
// revision behind a `section_retry` job.
//
// Gate order (§8.7 discipline, §11.4): authorize → ownership → no active job
// (44110) → the job's revision is the current published one (44104) → the ord
// names a section (44107) → the live row is `failed` (44104). Only then does a
// single transaction create the job, supersede the old row and insert the new
// pending row, so the failure disclosures (`gap_json`) keep agreeing with the
// section rows.
func (s *Service) RetryReportSection(
	ctx context.Context, actorID, tenantID, jobID string, ord int,
) (*RetryReportSectionResult, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("section retry requires an authenticated workspace")
	}
	if ord <= 0 {
		return nil, httperr.New(404, codeReportSectionNotFound,
			"section ord must be a positive number")
	}
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, errReportNotFound()
	}
	report, err := s.Store.GetReport(ctx, tenantID, job.ReportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}
	if err := s.assertReportOwner(ctx, actorID, report); err != nil {
		return nil, err
	}
	// An archived report produces nothing new (doc/297 §2): unarchive first.
	if err := s.assertReportArchivedBlocksWork(report); err != nil {
		return nil, err
	}
	// One running job per report (§7.10): the retry must not race a regenerate,
	// another retry, or the original job still settling.
	if active, err := s.Store.CountActiveReportJobs(ctx, tenantID, report.ID); err != nil {
		return nil, err
	} else if active > 0 {
		return nil, errReportActiveJobConflict()
	}
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, job.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, httperr.New(404, codeReportRevisionNotFound,
			"report job revision is no longer available")
	}
	// Retry is the repair for the *current* content. A historical (superseded)
	// revision must not be rewritten in place — the answer there is regeneration.
	if report.CurrentRevisionID != revision.ID || revision.Status != model.ReportRevisionPublished {
		return nil, errReportInvalidTransition(
			"only the current published revision's failed section can be retried")
	}
	templateRow, payload, err := s.reportPinnedTemplate(ctx, tenantID, revision)
	if err != nil {
		return nil, err
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	live := latestSectionAtOrd(sections, ord)
	if live == nil {
		// 44107 is also the answer for "the ord was superseded and no live version
		// was requested": a retry has nothing to base itself on.
		for _, sec := range sections {
			if sec.Ord == ord && sec.Status == model.ReportSectionSuperseded {
				return nil, httperr.New(404, codeReportSectionNotFound,
					"this section is superseded and has no live version to retry")
			}
		}
		return nil, httperr.New(404, codeReportSectionNotFound,
			fmt.Sprintf("section ord %d is not part of this revision", ord))
	}
	if live.Status != model.ReportSectionFailed {
		// succeeded / skipped: nothing to repair; pending / writing: a retry is
		// already armed (double-click guard). All four are "this transition is not
		// available", and none is retryable by reloading.
		return nil, errReportInvalidTransition("only a failed section can be retried")
	}

	now := time.Now().UTC()
	// A new job id: the request's `jobID` names the *terminal* generate job, and
	// reusing it here would point the retry at history.
	newJobID := id.New()
	newSection := &model.ReportSection{
		ID: id.New(), TenantID: tenantID, RevisionID: revision.ID,
		Ord: live.Ord, SectionKey: live.SectionKey,
		SectionVersion:        live.SectionVersion + 1,
		BasedOnSectionVersion: live.SectionVersion,
		RewriteReason:         model.ReportRewriteReasonRetry,
		Title:                 live.Title,
		Query:                 live.Query,
		DependsOnSectionsJSON: live.DependsOnSectionsJSON,
		RequiresEvidence:      live.RequiresEvidence,
		Status:                model.ReportSectionPending,
		QualityStatus:         model.ReportQualityNotChecked,
		Attempt:               0,
	}
	budgetJSON, err := json.Marshal(payload.Limits)
	if err != nil {
		return nil, err
	}
	retryJob := &model.ReportJob{
		ID: newJobID, TenantID: tenantID, ReportID: report.ID, RevisionID: revision.ID,
		JobKind: model.ReportJobKindSectionRetry, Status: model.ReportJobQueued,
		Stage: model.ReportStageSectionWriting,
		// The retry rewrites exactly one section, so it is one attempt of one
		// slice. The slice key `report:<job>:<attempt>:<slice>` guards against a
		// double dispatch (§7.7 / §7.9).
		AttemptSeq: 1, SliceSeq: 1,
		ProgressTotal: 1,
		BudgetJSON:    string(budgetJSON),
		// The envelope is the pinned template's per-section cap, frozen like every
		// other report job's (doc/288 §8.6).
		ReservedTokens: reportBudgetReservation(payload.Limits, 1),
		// A retry is a fresh, freshly-metered call, so it freezes the policy in force
		// now rather than inheriting the base job's (doc/296 §2.2). The two can differ
		// legitimately: an operator may switch the workspace to `observe` precisely to
		// find out what the retries cost.
		ReportBudgetPolicy: s.reportBudgetPolicyFor(ctx, tenantID),
		IdempotencyKey:     "",
		UpdatedAt:          now,
	}
	eventPayload, err := json.Marshal(map[string]interface{}{
		"attempt_seq": retryJob.AttemptSeq, "slice_seq": retryJob.SliceSeq,
		"kind": model.ReportJobKindSectionRetry, "ord": live.Ord,
		"section_key": live.SectionKey, "section_version": newSection.SectionVersion,
		"based_on_version": live.SectionVersion,
		"template_key":     templateRow.TemplateKey, "template_version": templateRow.Version,
	})
	if err != nil {
		return nil, err
	}
	event := &model.ReportJobEvent{
		TenantID: tenantID, JobID: retryJob.ID, Type: reportEventJobQueued,
		PayloadJSON: string(eventPayload), CreatedAt: now,
		ExpiresAt: reportExpiry(now, s.ReportRetentionFor(ctx, tenantID).ReportEventDays, reportDefaultEventDays),
	}

	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateReportJob(ctx, retryJob); err != nil {
			return err
		}
		// The CAS guard is the duplicate-retry fence: if another caller already
		// superseded this failed row, we lose the race and nothing must be written.
		applied, err := tx.SupersedeReportSection(ctx, live.ID, now)
		if err != nil {
			return err
		}
		if !applied {
			return errReportInvalidTransition("this section was already superseded by a concurrent retry")
		}
		if err := tx.CreateReportSection(ctx, newSection); err != nil {
			return err
		}
		_, err = tx.AppendReportEvent(ctx, event)
		return err
	}); err != nil {
		return nil, err
	}

	// Arm the first slice like the create/confirm paths. The slice key
	// `report:<job>:1:1` refuses a double dispatch, so a replayed arming is a
	// no-op rather than a second slice.
	runtimeJobID, applied, err := s.DispatchReportJob(ctx, tenantID, retryJob.ID)
	if err != nil {
		logger.Warn("report section retry dispatch failed",
			"report_id", report.ID, "job_id", retryJob.ID, "error", err)
	}
	if !applied {
		runtimeJobID = ""
	}

	detail, err := json.Marshal(map[string]interface{}{
		"report_id": report.ID, "job_id": retryJob.ID, "revision_id": revision.ID,
		"ord": live.Ord, "section_key": live.SectionKey,
		"section_version": newSection.SectionVersion, "based_on_version": live.SectionVersion,
		"superseded_section_id": live.ID, "job_kind": model.ReportJobKindSectionRetry,
	})
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.job.created",
		Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); auditErr != nil {
		logger.Warn("report section retry audit failed", "report_id", report.ID, "error", auditErr)
		return nil, auditErr
	}
	return &RetryReportSectionResult{
		ReportID: report.ID, RevisionID: revision.ID,
		Job: *retryJob, Section: *newSection,
		SupersededID: live.ID, SupersededVer: live.SectionVersion,
		RuntimeJobID: runtimeJobID,
	}, nil
}

// latestSectionAtOrd returns the live (highest section_version) row at ord.
//
// The version comparison is explicit rather than "the last match wins": the caller
// gets a row from `ListReportSections`, whose ordering is a store detail. If that
// ORDER BY ever changed, a positional rule would hand back the *oldest* version —
// and a write against a superseded row is exactly what §7.10 forbids.
//
// A nil result means no row has this ord at all.
func latestSectionAtOrd(sections []model.ReportSection, ord int) *model.ReportSection {
	var live *model.ReportSection
	for i := range sections {
		if sections[i].Ord != ord {
			continue
		}
		if live == nil || sections[i].SectionVersion > live.SectionVersion {
			live = &sections[i]
		}
	}
	return live
}
