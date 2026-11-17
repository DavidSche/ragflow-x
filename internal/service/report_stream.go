package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report job snapshot and event stream (doc/288 §8.3, task T5).
//
// Two rules shape this file:
//
//   - the snapshot is the only way to learn `event_seq`, and it is read from the
//     very row that allocates that sequence. Reading it from `MAX(seq)` on the
//     event table instead would leave a window: an event committed between the
//     snapshot query and the subscription would be skipped forever (§8.3
//     "无缺口恢复").
//   - the stream never invents events and never compresses a hole. A cursor that
//     fell behind the retention window is refused with 44115 even though the
//     surviving events *could* be replayed: replaying them would look like a
//     complete resume and hide the events that were cleared (A42).
//
// The stream itself lives in the handler: flushing is an HTTP concern, and the
// service has no business knowing about `ResponseWriter`. What lives here is the
// part that must be tenant-scoped and testable without a socket — authorization,
// cursor validation, and one page of events.

// reportEventPageSize bounds one poll. It is deliberately larger than the
// template catalog and smaller than the export page: a slice writes at most a
// handful of events per section, so a page this size always drains the backlog a
// reconnect can produce in one round trip.
const reportEventPageSize = 200

// ReportTokenLedger is the disclosure half of the budget (doc/288 §8.6): the
// envelope frozen at creation plus what the ledger has settled so far. It carries
// no runtime pointer, no idempotency key and no model identity — the snapshot is
// for the owner's screen, not for diagnosis of the engine.
type ReportTokenLedger struct {
	Envelope         int64 `json:"envelope"`
	Actual           int64 `json:"actual"`
	Charged          int64 `json:"charged"`
	Released         int64 `json:"released"`
	Overage          int64 `json:"overage"`
	PendingReconcile int64 `json:"pending_reconcile"`
}

// ReportRevisionSnapshot is the content-state of the revision this job targets.
//
// It is addressed by `job.revision_id`, never by "the report's latest revision":
// once regeneration exists the two differ, and a progress screen that silently
// switched to the newest revision would report the wrong structure's sections.
type ReportRevisionSnapshot struct {
	ID                    string                 `json:"id"`
	Revision              int64                  `json:"revision"`
	Status                string                 `json:"status"`
	Completeness          string                 `json:"completeness"`
	OutlineConfirmation   string                 `json:"outline_confirmation"`
	TemplateKey           string                 `json:"template_key"`
	TemplateVersion       int64                  `json:"template_version"`
	ExecutionSnapshotHash string                 `json:"execution_snapshot_hash"`
	Metrics               map[string]interface{} `json:"metrics"`
	PublishedAt           *time.Time             `json:"published_at"`
	SupersededAt          *time.Time             `json:"superseded_at"`
}

// ReportSectionSnapshot is one section's *execution* state. The content is
// deliberately absent: this is the live progress view, and a snapshot that carried
// every section's markdown would be a multi-megabyte response per poll. Published
// content is read through the export/report surfaces instead.
//
// `status` and `quality_status` stay two fields (doc/288 R21): a section can finish
// writing and still fail its evidence check.
type ReportSectionSnapshot struct {
	ID               string     `json:"id"`
	Ord              int        `json:"ord"`
	SectionKey       string     `json:"section_key"`
	Title            string     `json:"title"`
	SectionVersion   int        `json:"section_version"`
	Status           string     `json:"status"`
	EvidenceStatus   string     `json:"evidence_status"`
	QualityStatus    string     `json:"quality_status"`
	RequiresEvidence bool       `json:"requires_evidence"`
	CitationCount    int        `json:"citation_count"`
	ClaimCount       int        `json:"claim_count"`
	Attempt          int        `json:"attempt"`
	TokensIn         int64      `json:"tokens_in"`
	TokensOut        int64      `json:"tokens_out"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

// ReportJobSnapshot is the refresh-without-loss payload (§8.3): the current truth
// plus the cursor to resume the stream from.
type ReportJobSnapshot struct {
	JobID         string `json:"job_id"`
	ReportID      string `json:"report_id"`
	JobKind       string `json:"job_kind"`
	Status        string `json:"status"`
	Stage         string `json:"stage"`
	AttemptSeq    int    `json:"attempt_seq"`
	SliceSeq      int    `json:"slice_seq"`
	ProgressDone  int    `json:"progress_done"`
	ProgressTotal int    `json:"progress_total"`
	// EventSeq is the resume cursor. It is the same number the event allocator
	// reads, so `after=event_seq` can neither skip an event nor replay a duplicate.
	EventSeq          int64                   `json:"event_seq"`
	Terminal          bool                    `json:"terminal"`
	CancelRequestedAt *time.Time              `json:"cancel_requested_at"`
	ErrorCode         string                  `json:"error_code"`
	UpdatedAt         time.Time               `json:"updated_at"`
	Tokens            ReportTokenLedger       `json:"tokens"`
	Revision          ReportRevisionSnapshot  `json:"revision"`
	Sections          []ReportSectionSnapshot `json:"sections"`
	// Gaps is `gap_json` as an array; empty (never null) until finalize writes it,
	// so a UI can map over it without a null check.
	Gaps []reportGapEntry `json:"gaps"`
}

// GetReportJobSnapshot returns the live state of one report job for its owner.
//
// Both "no such job" and "not your report" answer 44109, exactly like
// `GetReport`: a job id handed out in a URL must not become an existence oracle
// for a colleague's report (§7.6).
func (s *Service) GetReportJobSnapshot(ctx context.Context, actorID, tenantID, jobID string) (*ReportJobSnapshot, error) {
	job, err := s.reportJobForRead(ctx, actorID, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, job.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		// The revision is written in the same transaction as the job and is purged
		// with the report, so a missing one is a genuine inconsistency rather than
		// a race with a delete. Answer "gone" instead of a half aggregate.
		return nil, errReportRevisionNotFound(0)
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, job.RevisionID)
	if err != nil {
		return nil, err
	}

	snapshot := &ReportJobSnapshot{
		JobID:             job.ID,
		ReportID:          job.ReportID,
		JobKind:           job.JobKind,
		Status:            job.Status,
		Stage:             job.Stage,
		AttemptSeq:        job.AttemptSeq,
		SliceSeq:          job.SliceSeq,
		ProgressDone:      job.ProgressDone,
		ProgressTotal:     job.ProgressTotal,
		EventSeq:          job.EventSeq,
		Terminal:          job.FinalizedAt != nil || reportStatusIsTerminal(job.Status),
		CancelRequestedAt: job.CancelRequestedAt,
		ErrorCode:         job.ErrorCode,
		UpdatedAt:         job.UpdatedAt,
		Tokens: ReportTokenLedger{
			Envelope:         job.ReservedTokens,
			Actual:           job.ActualTokens,
			Charged:          job.ChargedTokens,
			Released:         job.ReleasedTokens,
			Overage:          job.OverageTokens,
			PendingReconcile: job.PendingReconcileTokens,
		},
		Revision: ReportRevisionSnapshot{
			ID:                    revision.ID,
			Revision:              revision.Revision,
			Status:                revision.Status,
			Completeness:          revision.Completeness,
			OutlineConfirmation:   revision.OutlineConfirmation,
			TemplateKey:           revision.TemplateKey,
			TemplateVersion:       revision.TemplateVersion,
			ExecutionSnapshotHash: revision.ExecutionSnapshotHash,
			Metrics:               reportJSONObject(revision.MetricsJSON),
			PublishedAt:           revision.PublishedAt,
			SupersededAt:          revision.SupersededAt,
		},
		Sections: make([]ReportSectionSnapshot, 0, len(sections)),
		Gaps:     reportGapList(revision.GapJSON),
	}
	for _, section := range sections {
		snapshot.Sections = append(snapshot.Sections, ReportSectionSnapshot{
			ID:               section.ID,
			Ord:              section.Ord,
			SectionKey:       section.SectionKey,
			Title:            section.Title,
			SectionVersion:   section.SectionVersion,
			Status:           section.Status,
			EvidenceStatus:   section.EvidenceStatus,
			QualityStatus:    section.QualityStatus,
			RequiresEvidence: section.RequiresEvidence,
			CitationCount:    section.CitationCount,
			ClaimCount:       section.ClaimCount,
			Attempt:          section.Attempt,
			TokensIn:         section.TokensIn,
			TokensOut:        section.TokensOut,
			StartedAt:        section.StartedAt,
			FinishedAt:       section.FinishedAt,
		})
	}
	return snapshot, nil
}

// ReportCursorReset is the body of 44115: the numbers a client needs to discard its
// cursor and re-read the snapshot. `MinSeq` is the smallest `seq` that still
// exists; when nothing survived (`MinSeq == EventSeq`) the client must start over
// from the snapshot's `event_seq` and expects no earlier event to reappear.
type ReportCursorReset struct {
	MinSeq   int64 `json:"min_seq"`
	EventSeq int64 `json:"event_seq"`
}

// ReportEventStream is the validated subscription: who may read, where to start,
// and how far the job has allocated. `Reset` non-nil means the caller must answer
// 44115 and must not open the stream.
type ReportEventStream struct {
	JobID      string
	RevisionID string
	After      int64
	EventSeq   int64
	Reset      *ReportCursorReset
}

// OpenReportJobEventStream validates a subscription request.
//
// The cursor rules are §8.3's, in order: `after=0` is a full replay and never
// expired (the client is not claiming continuity); `after >= event_seq` has nothing
// to read and is never expired either; everything in between must be able to prove
// continuity, which it cannot when the janitor already removed the events that
// followed it.
//
// A negative or unparsable cursor is normalized by the caller, not here: the
// handler treats garbage as "no cursor" (full replay) because replaying from the
// start is always a correct answer, while a 400 would need a seventeenth error code
// and misusing 44114 (idempotency key) for a query parameter would break the
// one-code-one-meaning rule (§8.7).
func (s *Service) OpenReportJobEventStream(ctx context.Context, actorID, tenantID, jobID string, after int64) (*ReportEventStream, error) {
	if after < 0 {
		after = 0
	}
	job, err := s.reportJobForRead(ctx, actorID, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	stream := &ReportEventStream{
		JobID:      job.ID,
		RevisionID: job.RevisionID,
		After:      after,
		EventSeq:   job.EventSeq,
	}
	if after == 0 || after >= job.EventSeq {
		return stream, nil
	}
	first, ok, err := s.Store.FirstReportEventSeq(ctx, tenantID, job.ID)
	if err != nil {
		return nil, err
	}
	switch {
	case ok && after >= first:
		return stream, nil
	case ok:
		stream.Reset = &ReportCursorReset{MinSeq: first, EventSeq: job.EventSeq}
	default:
		// Every event has been cleared while the job still claims to have written
		// some: the earliest seq that can be resumed from is the allocation itself.
		stream.Reset = &ReportCursorReset{MinSeq: job.EventSeq, EventSeq: job.EventSeq}
	}
	return stream, nil
}

// ReadReportJobEvents returns one ascending page after `afterSeq`.
//
// The tenant filter is re-applied here even though the subscription was already
// authorized: the stream outlives the authorization check, and a page read without
// a tenant would be the one query in this file that could cross a workspace
// boundary if the job id were ever constructed rather than resolved.
func (s *Service) ReadReportJobEvents(ctx context.Context, tenantID, jobID string, afterSeq int64) ([]model.ReportJobEvent, error) {
	if afterSeq < 0 {
		afterSeq = 0
	}
	return s.Store.ListReportEvents(ctx, tenantID, jobID, afterSeq, reportEventPageSize)
}

// reportJobForRead resolves a job id to its row and applies the report's access
// policy. Shared by the snapshot and the stream so the two cannot drift into
// different answers for the same id.
func (s *Service) reportJobForRead(ctx context.Context, actorID, tenantID, jobID string) (*model.ReportJob, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	if jobID == "" {
		return nil, errReportNotFound()
	}
	// Both getters answer (nil, nil) for "no such row": a missing row and a row in
	// another workspace must be indistinguishable, and neither may surface as a 500.
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
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	return job, nil
}

// reportStatusIsTerminal mirrors the finalize set (§8.4). It reads the status, not
// `finalized_at`, because a snapshot taken from a replica that has not seen the
// finalize write should still tell the UI to stop polling on `cancelled`.
func reportStatusIsTerminal(status string) bool {
	switch status {
	case model.ReportJobSucceeded, model.ReportJobPartial, model.ReportJobFailed, model.ReportJobCancelled:
		return true
	}
	return false
}

// reportJSONObject decodes a JSON object column and degrades to an empty object.
// The snapshot must not fail because a metrics blob written by an older release is
// unreadable: the numbers are a disclosure, and an empty disclosure is truthful
// where a 500 is merely loud.
func reportJSONObject(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

// reportGapList decodes `gap_json` into the same entry type finalize writes, so the
// snapshot and the `job.partial` event cannot describe the gap differently.
func reportGapList(raw string) []reportGapEntry {
	gaps := []reportGapEntry{}
	if raw == "" {
		return gaps
	}
	if err := json.Unmarshal([]byte(raw), &gaps); err != nil {
		return []reportGapEntry{}
	}
	return gaps
}
