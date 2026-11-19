package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report slices (doc/288 §7.9 / §8.2).
//
// A report is executed as a chain of runtime slices: `rgx_report_job` is the only
// authoritative business state, and each `rgx_job` describes exactly one slice.
// The chain is expressed by `current_runtime_job_id` plus `attempt_seq`/`slice_seq`
// — never by "the newest job for this report", which is a query that answers a
// different question.
//
// Two transitions own the chain:
//
//	queued ──DispatchReportJob──▶ running (slice 1)
//	running ──YieldReportSlice──▶ running (slice n+1)
//
// Both write the pointer, the slice row and the `rgx_report_job_attempt` mapping in
// one transaction (repository side), because "the job is running" must imply "the
// slice it names is mapped and executable" (I1).
//
// Both transitions are also driven by the request path: CreateReport and
// RegenerateReport arm slice 1 before answering 202 (`armReportJob`), because a
// report whose first slice is never dispatched is a permanently `queued` job with
// no error attached to it. Tests drive them directly to pin the invariants.

// ReportSlicePayload is `rgx_job.payload` for a report slice. The quintuple is the
// slice identity; `start_ord` is where the slice should begin writing, so a resumed
// slice does not redo finished sections.
type ReportSlicePayload struct {
	ReportJobID string `json:"report_job_id"`
	ReportID    string `json:"report_id"`
	RevisionID  string `json:"revision_id"`
	AttemptSeq  int    `json:"attempt_seq"`
	SliceSeq    int    `json:"slice_seq"`
	StartOrd    int    `json:"start_ord"`
}

// reportSliceKey is the execution idempotency key of one slice
// (`report:<report_job_id>:<attempt_seq>:<slice_seq>`, §8.2). It is deliberately
// built from the business identity, not from a clock: a retried trigger must land
// on the same key and enqueue nothing.
func reportSliceKey(reportJobID string, attemptSeq, sliceSeq int) string {
	return fmt.Sprintf("report:%s:%d:%d", reportJobID, attemptSeq, sliceSeq)
}

// parseReportSlicePayload rejects a payload that cannot address a slice. An
// unparsable payload is a programming error, not user input, so it is returned as
// an error instead of being repaired with defaults.
func parseReportSlicePayload(raw string) (ReportSlicePayload, error) {
	var payload ReportSlicePayload
	if raw == "" {
		return payload, fmt.Errorf("report slice payload is empty")
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return payload, err
	}
	if payload.ReportJobID == "" || payload.AttemptSeq < 1 || payload.SliceSeq < 1 {
		return payload, fmt.Errorf("report slice payload is incomplete: %s", raw)
	}
	return payload, nil
}

// owns answers "may this runtime job still act for this business job?".
//
// Triple equality is the stale-slice guard (§8.2): a slice that was superseded by a
// yield sees a different `slice_seq`, and a regenerated job sees a different
// `attempt_seq`. Both must abandon silently — their work belongs to a run that is
// no longer the report's current one.
func (p ReportSlicePayload) owns(job *model.ReportJob, runtimeJobID string) bool {
	if job == nil || runtimeJobID == "" {
		return false
	}
	return job.ID == p.ReportJobID &&
		job.AttemptSeq == p.AttemptSeq &&
		job.SliceSeq == p.SliceSeq &&
		job.CurrentRuntimeJobID == runtimeJobID
}

// reportNextStartOrd is the ord a resumed slice should begin at: the first section
// that is not `succeeded` yet. `succeeded` is the only terminal status that must not
// be redone — a `failed` section is retried by a later slice on purpose. A
// `superseded` row is finished history (its live replacement carries the real
// status), so it never anchors a resume either.
func reportNextStartOrd(sections []model.ReportSection) int {
	next := 1
	for _, section := range sections {
		if section.Ord >= next {
			next = section.Ord + 1
		}
		if section.Status == model.ReportSectionSuperseded {
			continue
		}
		if section.Status != model.ReportSectionSucceeded {
			return section.Ord
		}
	}
	// Nothing is unfinished, so the resume point is one past the last section. `ord`
	// is 1-based, which is why this cannot be `len(sections)`: with five sections that
	// would point at the fifth, and a resumed slice would redo work it just finished.
	return next
}

// DispatchReportJob arms the first runtime slice of a queued job.
//
// Idempotent by construction: `applied=false` means the job is not `queued` any
// more (a second dispatch won, or the user cancelled first), which is a normal
// outcome and not an error. The returned runtime id is empty in that case.
func (s *Service) DispatchReportJob(ctx context.Context, tenantID, jobID string) (string, bool, error) {
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return "", false, err
	}
	if job == nil || job.FinalizedAt != nil || job.Status != model.ReportJobQueued ||
		job.CurrentRuntimeJobID != "" {
		return "", false, nil
	}
	// Admission control (doc/297 §3). This is the single door from `queued` to
	// `running`, so the workspace's concurrency limit lives here and nowhere else:
	// creation, replay, retry, the schedule tick and the crash-recovery sweep all
	// pass through it, and none of them can start a fourth run by taking a different
	// path.
	//
	// A run over the limit is NOT an error and never a 429: it stays `queued` with no
	// runtime pointer, `applied=false` describes "nothing was armed", and the
	// reconcile sweep asks again on its next pass. That is the whole point of a limit
	// that "only defers": the request is accepted, the aggregate exists, the token
	// envelope is frozen, and the work starts as soon as a slot frees. Killing or
	// rejecting it would turn a capacity fact into a lost report.
	admitted, err := s.reportAdmissionAllows(ctx, tenantID)
	if err != nil {
		return "", false, err
	}
	if !admitted {
		return "", false, nil
	}
	now := time.Now().UTC()
	sections, err := s.Store.ListReportSections(ctx, tenantID, job.RevisionID)
	if err != nil {
		return "", false, err
	}
	start, err := reportSliceStart(job, job.SliceSeq, reportNextStartOrd(sections), now)
	if err != nil {
		return "", false, err
	}
	applied, err := s.Store.DispatchReportSlice(ctx, start)
	if err != nil {
		return "", false, err
	}
	if !applied {
		return "", false, nil
	}
	return start.RuntimeJobID, true, nil
}

// ReportMaxConcurrentRuns is how many report runs one workspace may have executing
// at the same time (doc/297 §3).
//
// It is a product number rather than an environment knob: a report run holds a model,
// a retrieval budget and a row of the engine's worker pool for minutes, and the number
// that keeps a workspace honest about that is a decision about the product, not about
// the host. Operators who genuinely need more are changing the shape of the feature,
// and a variable would have let that happen silently in one environment.
//
// The count is of *execution slices*, not of reports: each running report holds
// exactly one `report_generate` runtime job at a time (a yield replaces it, a parked
// `awaiting_outline` run holds none), so this is the number of runs actually burning
// engine time. A report waiting for outline confirmation therefore does not consume a
// slot — it is waiting on a human, not on the worker.
const ReportMaxConcurrentRuns = 3

// reportAdmissionAllows answers "may another run start in this workspace now".
//
// `active` counts the workspace's queued and running `report_generate` slices, which
// is what the engine is committed to; stale `running` rows from a dead worker hold a
// slot only until the stale sweep finalizes them, and that bound is deliberate — a
// limit that could be exceeded by guessing races is not a limit.
func (s *Service) reportAdmissionAllows(ctx context.Context, tenantID string) (bool, error) {
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindReportGenerate, tenantID, "")
	if err != nil {
		return false, err
	}
	return active < int64(ReportMaxConcurrentRuns), nil
}

// reportConcurrencyOf snapshots the limit for a caller that must explain a deferral.
// It is read after the fact on purpose: the numbers describe the workspace at the
// moment the answer is written, and a deferral is only ever explained by "what was
// already running".
func (s *Service) reportConcurrencyOf(ctx context.Context, tenantID string) (*ReportConcurrencyView, error) {
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindReportGenerate, tenantID, "")
	if err != nil {
		return nil, err
	}
	return &ReportConcurrencyView{Limit: ReportMaxConcurrentRuns, Active: active}, nil
}

// YieldReportSlice hands the job to its next slice at a checkpoint (the
// 0.8×JobTimeout yield, §8.2).
//
// It is a no-op for a slice that no longer owns the job: the guard is the same
// triple as `owns`, so a late checkpoint from a superseded slice cannot bump the
// numbering a second time. `progressDone` is written in the same statement as the
// new slice, so progress can never move without the slice that explains it.
func (s *Service) YieldReportSlice(
	ctx context.Context, tenantID, jobID string, attemptSeq, sliceSeq int,
	runtimeJobID string, progressDone int,
) (bool, error) {
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return false, err
	}
	if job == nil || job.FinalizedAt != nil || job.Status != model.ReportJobRunning {
		return false, nil
	}
	payload := ReportSlicePayload{
		ReportJobID: jobID, AttemptSeq: job.AttemptSeq, SliceSeq: job.SliceSeq,
	}
	if !payload.owns(job, runtimeJobID) || attemptSeq != job.AttemptSeq || sliceSeq != job.SliceSeq {
		return false, nil
	}
	if progressDone < 0 {
		progressDone = job.ProgressDone
	}
	if job.ProgressTotal > 0 && progressDone > job.ProgressTotal {
		// The counter is a fraction of the declared sections; exceeding the
		// denominator would render as >100% in every consumer.
		progressDone = job.ProgressTotal
	}
	now := time.Now().UTC()
	sections, err := s.Store.ListReportSections(ctx, tenantID, job.RevisionID)
	if err != nil {
		return false, err
	}
	next, err := reportSliceStart(job, job.SliceSeq+1, reportNextStartOrd(sections), now)
	if err != nil {
		return false, err
	}
	next.Stage = model.ReportStageSectionWriting
	eventPayload, err := json.Marshal(map[string]interface{}{
		"progress_done": progressDone, "progress_total": job.ProgressTotal,
		"slice_seq": next.SliceSeq,
	})
	if err != nil {
		return false, err
	}
	event := &model.ReportJobEvent{
		TenantID: tenantID, JobID: jobID, Type: reportEventJobProgress,
		PayloadJSON: string(eventPayload), CreatedAt: now,
		ExpiresAt: reportExpiry(now, s.ReportRetentionFor(ctx, tenantID).ReportEventDays, reportDefaultEventDays),
	}
	return s.Store.AdvanceReportSlice(ctx, repository.ReportSliceAdvance{
		Slice: next, PreviousRuntimeJobID: runtimeJobID,
		ProgressDone: progressDone, Event: event,
	})
}

// reportSliceStart builds the repository request for one slice: identity, the
// runtime id and the payload that the worker will read back.
func reportSliceStart(
	job *model.ReportJob, sliceSeq, startOrd int, now time.Time,
) (repository.ReportSliceStart, error) {
	payload := ReportSlicePayload{
		ReportJobID: job.ID, ReportID: job.ReportID, RevisionID: job.RevisionID,
		AttemptSeq: job.AttemptSeq, SliceSeq: sliceSeq, StartOrd: startOrd,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return repository.ReportSliceStart{}, err
	}
	runtimeJobID := id.New()
	return repository.ReportSliceStart{
		TenantID: job.TenantID, JobID: job.ID, ReportID: job.ReportID,
		RevisionID: job.RevisionID, AttemptSeq: job.AttemptSeq, SliceSeq: sliceSeq,
		RuntimeJobID: runtimeJobID,
		RuntimeKey:   reportSliceKey(job.ID, job.AttemptSeq, sliceSeq),
		PayloadJSON:  string(encoded),
		Stage:        model.ReportStageOutlining,
		Now:          now,
	}, nil
}
