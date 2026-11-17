package service

// Outline confirmation (doc/288 §8.4 / §11.1 / §11.4).
//
// The generate worker parks a running job in `awaiting_outline` and stops; nothing
// else in the system may move it out of that state. Confirmation is the human half
// of the handshake: it records the reviewer's titles, opens a *new* business attempt
// (`attempt_seq + 1`) and enqueues that attempt's first slice.
//
// The new attempt is not a bookkeeping detail. The attempt that produced the outline
// already consumed `slice_seq = 1`; reusing it would collide with the slice key of a
// finished run and make the replay guard reject the very work the reviewer just
// approved.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ConfirmReportOutlineRequest is the reviewable part of the confirmation.
//
// Titles only, and only for sections the run has not started. P0-A deliberately
// accepts no prompt edits: the execution snapshot is already frozen and describes
// the prompts that will be used, so letting the confirmation rewrite one would make
// the snapshot a record of a run that never happened (§7.3).
type ConfirmReportOutlineRequest struct {
	Sections []ConfirmReportOutlineSection `json:"sections"`
}

// ConfirmReportOutlineSection is one reviewer-edited section title.
type ConfirmReportOutlineSection struct {
	SectionKey string `json:"section_key"`
	Title      string `json:"title"`
}

// ConfirmReportOutlineResult echoes what the server did. `Queued=false` cannot
// happen on a successful call (the slice is inserted in the same transaction), so
// the field is not returned — instead the caller gets the attempt/slice identity it
// can use to follow the job.
type ConfirmReportOutlineResult struct {
	ReportID    string `json:"report_id"`
	RevisionID  string `json:"revision_id"`
	JobID       string `json:"job_id"`
	AttemptSeq  int    `json:"attempt_seq"`
	SliceSeq    int    `json:"slice_seq"`
	RuntimeJob  string `json:"runtime_job_id"`
	EditedCount int    `json:"edited_sections"`
}

// ConfirmReportOutline records the review and arms the writing attempt.
func (s *Service) ConfirmReportOutline(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
	req ConfirmReportOutlineRequest,
) (*ConfirmReportOutlineResult, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("outline confirmation requires an authenticated workspace")
	}
	if revisionNo <= 0 {
		return nil, httperr.BadRequest(codeReportOutlineInvalid, "revision must be a positive number")
	}
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}
	if err := s.assertReportOwner(ctx, actorID, report); err != nil {
		return nil, err
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	if revision.OutlineConfirmation != model.ReportOutlineRequired {
		// `auto` is frozen in the contract but unimplemented (292 D2). Answering 409
		// here is the same refusal creation gives, so a report created by a client
		// that thought it was automatic cannot be confirmed by accident either.
		return nil, httperr.New(409, codeReportInvalidTransition,
			"this revision does not require outline confirmation")
	}
	job, err := s.Store.GetLatestReportJob(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.RevisionID != revision.ID {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	if job.Status != model.ReportJobAwaitingOutline {
		// Already confirmed, cancelled, or never reached the outline. All three are
		// "this transition is not available", and none of them is retryable by
		// reloading — 44104 is the union (§8.4).
		return nil, errReportInvalidTransition("report job is not awaiting outline confirmation")
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		// A job parked in `awaiting_outline` without sections is an inconsistent
		// aggregate; confirming it would arm a slice with nothing to write.
		return nil, errReportOutlineInvalid("this revision has no outline to confirm")
	}
	titles, edited, err := normalizeReportOutlineTitles(req.Sections, sections)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	runtimeJobID := id.New()
	attemptSeq := job.AttemptSeq + 1
	sliceSeq := 1
	payload := ReportSlicePayload{
		ReportJobID: job.ID, ReportID: reportID, RevisionID: revision.ID,
		AttemptSeq: attemptSeq, SliceSeq: sliceSeq, StartOrd: reportNextStartOrd(sections),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	eventPayload, err := json.Marshal(map[string]interface{}{
		"attempt_seq": attemptSeq, "edited": edited > 0, "edited_sections": edited,
		"slice_seq": sliceSeq,
	})
	if err != nil {
		return nil, err
	}
	applied, err := s.Store.ConfirmReportOutline(ctx, repository.ReportOutlineConfirm{
		TenantID: tenantID, JobID: job.ID, RevisionID: revision.ID, Now: now,
		Titles: titles,
		Slice: repository.ReportSliceStart{
			TenantID: tenantID, JobID: job.ID, ReportID: reportID, RevisionID: revision.ID,
			AttemptSeq: attemptSeq, SliceSeq: sliceSeq, RuntimeJobID: runtimeJobID,
			RuntimeKey:  reportSliceKey(job.ID, attemptSeq, sliceSeq),
			PayloadJSON: string(encoded), Stage: model.ReportStageSectionWriting, Now: now,
		},
		Event: &model.ReportJobEvent{
			TenantID: tenantID, JobID: job.ID, Type: reportEventOutlineConfirmed,
			PayloadJSON: string(eventPayload), CreatedAt: now,
			ExpiresAt: reportExpiry(now, s.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays),
		},
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		// A cancel or a second confirmation won the race. The user's intent is not
		// wrong, it is just no longer applicable.
		return nil, errReportInvalidTransition("report job is no longer awaiting outline confirmation")
	}
	detail, err := json.Marshal(map[string]interface{}{
		"attempt_seq": attemptSeq, "slice_seq": sliceSeq, "edited_sections": edited,
		"revision": revisionNo,
	})
	if err != nil {
		return nil, err
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.outline.confirmed",
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); auditErr != nil {
		// The slice is already queued; failing the request now would tell the user
		// nothing happened when it did. The audit gap is logged loudly instead.
		logger.Warn("report outline confirmation audit failed", "report_id", reportID, "error", auditErr)
	}
	logger.Info("report outline confirmed",
		"report_id", reportID, "job_id", job.ID, "attempt_seq", attemptSeq,
		"edited_sections", edited)
	return &ConfirmReportOutlineResult{
		ReportID: reportID, RevisionID: revision.ID, JobID: job.ID,
		AttemptSeq: attemptSeq, SliceSeq: sliceSeq, RuntimeJob: runtimeJobID,
		EditedCount: edited,
	}, nil
}

// assertReportOwner enforces `owner_only` on a write that is not a governance read.
func (s *Service) assertReportOwner(ctx context.Context, actorID string, report *model.Report) error {
	if report.OwnerUserID == actorID {
		return nil
	}
	// Same rule as soft delete: another principal sees the same "not found" the
	// `owner_only` policy would give, unless they hold cross-tenant manage.
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if !s.canAccessAcrossTenant(ac, "manage") {
		return errReportNotFound()
	}
	return nil
}

// normalizeReportOutlineTitles validates the reviewer's edits against the outline.
//
// An unknown `section_key` is a 400 rather than a silent no-op: the client is
// editing a different outline than the one on the server, and dropping the edit
// would make the confirmation appear successful while the title stayed unchanged.
func normalizeReportOutlineTitles(
	input []ConfirmReportOutlineSection, sections []model.ReportSection,
) ([]repository.ReportSectionTitle, int, error) {
	if len(input) == 0 {
		return nil, 0, nil
	}
	known := make(map[string]model.ReportSection, len(sections))
	for _, section := range sections {
		known[section.SectionKey] = section
	}
	seen := make(map[string]struct{}, len(input))
	titles := make([]repository.ReportSectionTitle, 0, len(input))
	edited := 0
	for index, item := range input {
		key := strings.TrimSpace(item.SectionKey)
		if key == "" {
			return nil, 0, httperr.BadRequest(codeReportOutlineInvalid,
				"outline confirmation sections[].section_key is required")
		}
		if _, dup := seen[key]; dup {
			return nil, 0, httperr.BadRequest(codeReportOutlineInvalid,
				"outline confirmation repeats section_key "+key)
		}
		seen[key] = struct{}{}
		section, ok := known[key]
		if !ok {
			return nil, 0, httperr.BadRequest(codeReportOutlineInvalid,
				"outline confirmation names an unknown section_key "+key)
		}
		if section.Status != model.ReportSectionPending {
			// Only a section that has not been written may be retitled: a `writing`
			// or finished row carries the title it was written under.
			return nil, 0, errReportInvalidTransition(
				"section " + key + " has already started; its title can no longer change")
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			return nil, 0, httperr.BadRequest(codeReportOutlineInvalid,
				"outline confirmation requires a non-empty title for section "+key)
		}
		if len([]rune(title)) > 255 {
			return nil, 0, httperr.BadRequest(codeReportOutlineInvalid,
				"outline confirmation titles must be at most 255 characters")
		}
		if title == section.Title {
			// Not an edit: sending the same title back would set `edited=true` and
			// make the audit trail claim a change nobody made.
			continue
		}
		_ = index
		titles = append(titles, repository.ReportSectionTitle{SectionKey: key, Title: title})
		edited++
	}
	return titles, edited, nil
}
