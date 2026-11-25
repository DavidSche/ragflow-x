package service

// The report generate worker (doc/288 §8.2, §8.6, §9.3, §9.4, §14).
//
// One `rgx_job` of kind `report_generate` is one *slice* of a report (T4). It does
// exactly three things, in this order:
//
//  1. own check — a slice that was superseded by a yield or by a cancel must
//     abandon silently, because its work belongs to a run that is no longer the
//     report's current one (§8.2);
//  2. outline — the first slice freezes the execution snapshot, materialises the
//     section rows and parks the job in `awaiting_outline` (§7.3, §11.1);
//  3. sections — each section is reserve → retrieve → begin → call → settle →
//     commit, and the slice yields at 0.8× its lease (§7.9, §8.6).
//
// The worker never writes a terminal job status: `succeeded`/`partial`/`failed`/
// `cancelled` belong to `FinalizeReportJob` alone. Everything here writes section
// rows and asks for finalization.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

const (
	// reportSectionMaxAttempts bounds how often one section may be started before
	// it is closed as failed. Without a bound a provider that answers with junk
	// would be retried by every slice until the budget envelope is gone.
	reportSectionMaxAttempts = 2
	// reportDefaultSliceSeconds is the lease assumption when the pinned template
	// did not declare `slice_max_seconds` (validation allows zero).
	reportDefaultSliceSeconds = 600
	// reportSliceYieldFloor keeps the checkpoint margin meaningful for a very
	// short slice: yielding 6 seconds before the deadline is not a checkpoint.
	reportSliceYieldFloor = 30 * time.Second
	// reportRetrievalPageSize is the number of chunks requested per section. The
	// template's `retrieval.top_k` wins; this is the fallback when it is unset.
	reportRetrievalPageSize = 8
)

// reportGenerateWorker executes report slices.
type reportGenerateWorker struct{ svc *Service }

func (w *reportGenerateWorker) Kind() string { return model.JobKindReportGenerate }

func (w *reportGenerateWorker) Run(ctx context.Context, job *model.Job) error {
	if job == nil {
		return nil
	}
	payload, err := parseReportSlicePayload(job.Payload)
	if err != nil {
		return err
	}
	return w.svc.runReportSlice(ctx, job, payload)
}

// runReportSlice is the slice entry point.
func (s *Service) runReportSlice(
	ctx context.Context, runtimeJob *model.Job, payload ReportSlicePayload,
) error {
	tenantID := runtimeJob.TenantID
	business, err := s.Store.GetReportJob(ctx, tenantID, payload.ReportJobID)
	if err != nil {
		return err
	}
	if business == nil || business.FinalizedAt != nil {
		// The report is done or gone; a late slice has nothing to add and must not
		// resurrect it.
		return nil
	}
	if !payload.owns(business, runtimeJob.ID) {
		logger.Info("report slice is no longer current",
			"report_job_id", payload.ReportJobID, "attempt_seq", payload.AttemptSeq,
			"slice_seq", payload.SliceSeq, "runtime_job_id", runtimeJob.ID)
		return nil
	}
	if business.Status == model.ReportJobCancelRequested {
		// Cancellation converges through finalize, never through the runner: the
		// sections already written are the cancel disclosure (§8.8).
		_, err := s.BeginReportFinalization(ctx, tenantID, business.ID,
			payload.AttemptSeq, payload.SliceSeq, runtimeJob.ID)
		return err
	}
	if business.Status != model.ReportJobRunning {
		return nil
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, business.RevisionID)
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		return s.runReportOutlineStep(ctx, runtimeJob, business)
	}
	return s.runReportSectionSlices(ctx, runtimeJob, business, payload, sections)
}

// convergeReportJob asks for finalization and reports the cause.
//
// It is used when the slice cannot do its job for a reason that will not improve
// by retrying (a broken snapshot, a missing template, a ledger inconsistency). The
// cause is still returned so the runner records it on the runtime row: an operator
// reading `rgx_job.last_error` should find the reason, not just "slice finished".
func (s *Service) convergeReportJob(
	ctx context.Context, business *model.ReportJob, runtimeJobID string, stage string, cause error,
) error {
	logger.Warn("report slice converging to finalize",
		"report_job_id", business.ID, "stage", stage, "error", cause)
	if _, err := s.BeginReportFinalization(ctx, business.TenantID, business.ID,
		business.AttemptSeq, business.SliceSeq, runtimeJobID); err != nil {
		return err
	}
	return cause
}

// runReportOutlineStep freezes the run's conditions and materialises the sections.
//
// Nothing is called on the model: the outline is the pinned template's own
// section list. A model that invents sections would make "declared section
// missing" a normal outcome instead of a failure, and the reviewer would be
// confirming a shape they cannot predict.
func (s *Service) runReportOutlineStep(
	ctx context.Context, runtimeJob *model.Job, business *model.ReportJob,
) error {
	tenantID := runtimeJob.TenantID
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, business.RevisionID)
	if err != nil {
		return err
	}
	if revision == nil {
		return nil
	}
	report, err := s.Store.GetReport(ctx, tenantID, business.ReportID)
	if err != nil {
		return err
	}
	if report == nil {
		return nil
	}
	templateRow, templatePayload, err := s.reportPinnedTemplate(ctx, tenantID, revision)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageOutlining, err)
	}
	bindings, err := parseReportDatasetBindings(revision.DatasetsJSON)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageOutlining, err)
	}
	writer, err := s.resolveReportWriterModel(ctx)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageOutlining, err)
	}
	// A failed engine probe is not a reason to refuse the run: it caps the
	// disclosure (`probe_limited` already allowed creation), it does not stop
	// generation. The snapshot records the empty version so a reader can tell
	// "unknown" from "recorded".
	engineVersion, versionErr := s.RAGFlow.EngineVersion(ctx)
	if versionErr != nil {
		logger.Warn("report outline: engine version probe failed", "error", versionErr)
		engineVersion = ""
	}
	limits := templatePayload.Limits
	outline, sections := buildReportOutline(
		tenantID, revision.ID, report.Question, templateRow.TemplateKey, templateRow.Version, templatePayload)
	// The reviewer confirms titles; the preview is what makes that confirmation
	// informed (doc/288 §9.6). Frozen on the section rows in the same transaction
	// as the outline, so "what did the knowledge base hold when this was approved"
	// stays answerable after the run.
	previews := s.attachReportEvidencePreview(ctx, tenantID, sections, bindings, templatePayload.Retrieval)
	// R17: compiled artifacts become *locators* for the section that retrieved from
	// the documents they were built from. They are frozen here, with the outline, and
	// never reach the citation chain (doc/295 A54).
	s.attachReportLocationHints(ctx, tenantID, sections, previews)
	// The same locators are copied onto the outline payload the confirmation screen
	// reads. The section rows are the source of truth (the writer reads them); the
	// outline copy exists because the reviewer approves the outline, not the rows.
	for index := range outline.Sections {
		if index >= len(sections) {
			break
		}
		for _, hint := range parseReportLocationHints(sections[index].LocationHintsJSON) {
			if line := reportLocationHintText(hint); line != "" {
				outline.Sections[index].LocationHints = append(
					outline.Sections[index].LocationHints, line)
			}
		}
	}

	// The candidate width is resolved *once*, through the same resolver the review
	// trigger uses (template > tenant > platform), and frozen into the snapshot. The
	// number the sections below freeze is therefore the number a later review reads,
	// even if an operator raises the setting in between (§5.3.1 "冻结即定稿").
	resolvedScanTopK, resolvedScanSource := s.reportCandidateScanTopK(
		ctx, templatePayload.Retrieval.ScanTopK)

	snapshot := reportExecutionSnapshot{
		TemplateKey: templateRow.TemplateKey, TemplateVersion: templateRow.Version,
		EngineVersion: engineVersion, Model: writer, Datasets: bindings,
		Retrieval: reportSnapshotRetrieval{
			TopK:            reportRetrievalTopK(templatePayload.Retrieval.TopK),
			ThresholdPolicy: strings.TrimSpace(templatePayload.Retrieval.ThresholdPolicy),
			// P0-A sends no absolute cutoff: the workspace stores no calibrated
			// similarity value yet, and inventing one would silently empty the
			// report when the engine or the embedding model changes (§9.4).
			SimilarityThreshold: nil,
			ThresholdSource:     "engine_default",
			MinEvidence:         templatePayload.Retrieval.MinEvidence,
			ScanTopK:            resolvedScanTopK,
			ScanTopKSource:      resolvedScanSource,
		},
		Generation: reportSnapshotGeneration{
			// ChatToModel exposes no sampling knob. Saying so is the honest record.
			Temperature:         "provider_default",
			MaxTokensPerSection: limits.MaxTokensPerSection,
			Stream:              false,
			Sections:            len(sections),
			BudgetTokens:        business.ReservedTokens,
		},
		PromptHashes: reportPromptHashes(templatePayload),
	}
	snapshotJSON, snapshotHash, err := encodeReportExecutionSnapshot(snapshot)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageOutlining, err)
	}
	outlineJSON, err := json.Marshal(outline)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageOutlining, err)
	}
	now := time.Now().UTC()
	eventPayload, err := json.Marshal(map[string]interface{}{
		"revision_id": revision.ID, "sections": len(sections),
		"template_key": templateRow.TemplateKey, "template_version": templateRow.Version,
		"outline_confirmation": revision.OutlineConfirmation,
		"sections_detail":      outline.Sections,
	})
	if err != nil {
		return err
	}
	applied, err := s.Store.BeginReportOutline(ctx, repository.ReportOutlineStart{
		TenantID: tenantID, JobID: business.ID, RevisionID: revision.ID,
		RuntimeJobID: runtimeJob.ID, AttemptSeq: business.AttemptSeq, SliceSeq: business.SliceSeq,
		Now: now, OutlineJSON: string(outlineJSON),
		SnapshotJSON: snapshotJSON, SnapshotHash: snapshotHash, EngineVersion: engineVersion,
		Sections: sections,
		Event: &model.ReportJobEvent{
			TenantID: tenantID, JobID: business.ID, Type: reportEventOutlineReady,
			PayloadJSON: string(eventPayload), CreatedAt: now,
			ExpiresAt: reportExpiry(now, s.ReportRetentionFor(ctx, tenantID).ReportEventDays, reportDefaultEventDays),
		},
	})
	if err != nil {
		return err
	}
	if !applied {
		// The job was cancelled or superseded between our read and the CAS. The
		// cancel path in `runReportSlice` already converged it.
		return nil
	}
	logger.Info("report outline ready",
		"report_job_id", business.ID, "sections", len(sections),
		"template_key", templateRow.TemplateKey, "template_version", templateRow.Version)
	return nil
}

// reportPinnedTemplate resolves the template version the revision pinned. It is
// re-read (not carried in the payload) because the snapshot hash must describe the
// exact bytes that were used, and an admin may have published a new version in
// between — the pinned version is immutable, so this read cannot drift.
func (s *Service) reportPinnedTemplate(
	ctx context.Context, tenantID string, revision *model.ReportRevision,
) (*model.ReportTemplate, *ReportTemplatePayload, error) {
	// `resolveReportTemplate`, not `GetReportTemplate`: the pinned version may live
	// in the platform baseline (tenant_id=''), and a workspace read alone would make
	// every un-forked template unfindable at generation time.
	row, err := s.resolveReportTemplate(ctx, tenantID, revision.TemplateKey, revision.TemplateVersion)
	if err != nil {
		return nil, nil, err
	}
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		return nil, nil, err
	}
	if err := validateReportTemplatePayload(payload); err != nil {
		// A stored template that breaks its own contract is our fault, not the
		// caller's: the run stops instead of writing sections from a half-valid ask.
		return nil, nil, fmt.Errorf("report template %s is invalid: %w", row.TemplateKey, err)
	}
	return row, payload, nil
}

// resolveReportWriterModel resolves the workspace default chat model into the
// provider/instance/model triple `ChatToModel` needs.
//
// The composite reference (`name@instance@provider`) is resolved through the same
// helper the dataset parse path uses: re-implementing the `@` split here is how the
// two call sites drifted apart before.
func (s *Service) resolveReportWriterModel(ctx context.Context) (reportSnapshotModel, error) {
	reference, err := s.getCurrentDefault(ctx, "chat")
	if err != nil {
		return reportSnapshotModel{}, fmt.Errorf("report writer model: %w", err)
	}
	if strings.TrimSpace(reference) == "" {
		return reportSnapshotModel{}, fmt.Errorf("report writer model: workspace has no default chat model")
	}
	models, err := s.RAGFlow.ListModels(ctx, "chat")
	if err != nil {
		return reportSnapshotModel{}, fmt.Errorf("report writer model: %w", err)
	}
	resolved, ok := resolveDatasetEmbeddingReference(models, reference)
	if !ok {
		return reportSnapshotModel{}, fmt.Errorf(
			"report writer model %q does not match exactly one registered chat model", reference)
	}
	name := strings.TrimSpace(resolved.Name)
	if name == "" {
		return reportSnapshotModel{}, fmt.Errorf("report writer model %q has no model name", reference)
	}
	return reportSnapshotModel{
		ModelID: reference, Name: name,
		Provider: strings.TrimSpace(resolved.Provider), Instance: strings.TrimSpace(resolved.Instance),
	}, nil
}

// reportPromptHashes records the identity of the prompt skeleton in the snapshot.
// Per-section hashes are written on the section row when the call is actually made
// (they include the retrieved evidence, which does not exist yet).
func reportPromptHashes(payload *ReportTemplatePayload) map[string]string {
	hashes := make(map[string]string, len(payload.Sections)+2)
	hashes["system"] = sha256Hex(reportWriterSystemPrompt)
	for _, section := range payload.Sections {
		hashes["instruction:"+section.Key] = sha256Hex(strings.TrimSpace(section.Prompt))
	}
	return hashes
}

// runReportSectionSlices writes sections until the lease runs out, the job is
// cancelled, or every section reached a terminal state.
func (s *Service) runReportSectionSlices(
	ctx context.Context, runtimeJob *model.Job, business *model.ReportJob,
	payload ReportSlicePayload, sections []model.ReportSection,
) error {
	tenantID := runtimeJob.TenantID
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, business.RevisionID)
	if err != nil {
		return err
	}
	if revision == nil {
		return nil
	}
	snapshot, err := parseReportExecutionSnapshot(revision.ExecutionSnapshotJSON)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageSectionWriting, err)
	}
	// Immutable section history must not participate in a slice: a superseded row
	// is the runner's record of a *finished* older version, not a live section, so
	// it is filtered out of the working set before any status walk (nextSection,
	// allTerminal, skipRemaining) can misread it as pending or as an exhausted gap.
	sections = reportActiveSections(sections)
	report, err := s.Store.GetReport(ctx, tenantID, business.ReportID)
	if err != nil {
		return err
	}
	if report == nil {
		return nil
	}
	_, templatePayload, err := s.reportPinnedTemplate(ctx, tenantID, revision)
	if err != nil {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageSectionWriting, err)
	}
	if templatePayload.Limits.MaxTokensPerSection <= 0 {
		return s.convergeReportJob(ctx, business, runtimeJob.ID, model.ReportStageSectionWriting,
			errReportLedgerInconsistent("template declares no per-section token envelope"))
	}
	run := reportSectionRun{
		svc: s, ctx: ctx, tenantID: tenantID, business: business, revision: revision,
		report: report, snapshot: snapshot, template: templatePayload,
		runtimeJobID: runtimeJob.ID, attemptSeq: payload.AttemptSeq, sliceSeq: payload.SliceSeq,
		sections: sections, stage: model.ReportStageSectionWriting,
		lease:    reportSliceLease(templatePayload.Limits),
		scanTopK: snapshot.Retrieval.ScanTopK,
	}
	return run.execute()
}

// reportSectionRun carries one slice's working state. It is a struct rather than a
// dozen parameters because every step needs the same six facts, and a positional
// call with six adjacent strings is a bug waiting to happen.
type reportSectionRun struct {
	svc          *Service
	ctx          context.Context
	tenantID     string
	business     *model.ReportJob
	revision     *model.ReportRevision
	report       *model.Report
	snapshot     reportExecutionSnapshot
	template     *ReportTemplatePayload
	runtimeJobID string
	attemptSeq   int
	sliceSeq     int
	sections     []model.ReportSection
	stage        string
	lease        time.Duration
	// budgetBlocked records that the gate refused a reservation. The remaining
	// sections are then skipped in one write so the disclosure says
	// `budget_exhausted` instead of "someone forgot them".
	budgetBlocked bool
	truncated     int
	// scanTopK is the revision's frozen conflict-candidate width, copied from
	// `snapshot.Retrieval.ScanTopK` when the run is built (T04b).
	scanTopK int
	// reuseApplied reports how many sections this slice inherited from the base
	// revision instead of writing them (R14). It is logged and disclosed in the
	// slice checkpoint event; a section that was reused always says so on its own
	// row (`rewrite_reason=reuse`), so the count is not the only evidence.
	reuseApplied int
}

// execute drives the section loop.
func (r *reportSectionRun) execute() error {
	// Reuse happens before the first section is picked, not per section: inheriting a
	// body after the writer already retrieved evidence for it would pay for the call
	// and then throw the answer away. A declined reuse (`applied=false`) is normal —
	// the section is simply written normally below.
	reused, err := r.svc.applyReportSectionReuse(
		r.ctx, r.tenantID, r.business.ID, r.stage, r.revision, r.sections, time.Now().UTC())
	if err != nil {
		return err
	}
	r.reuseApplied = reused
	if reused > 0 {
		// The working set is re-read: the rows this just filled in are now
		// `succeeded`, and the loop's own status walk must see that rather than the
		// stale `pending` copies it was handed.
		fresh, err := r.svc.Store.ListReportSections(r.ctx, r.tenantID, r.revision.ID)
		if err != nil {
			return err
		}
		r.sections = reportActiveSections(fresh)
	}
	for {
		if err := r.ctx.Err(); err != nil {
			// The lease is gone. Nothing is rolled back: finished sections stay
			// finished and the rest are picked up by the retried slice.
			return err
		}
		// Cancel is checked between sections: a cancel that waits for the whole
		// slice to finish is not a cancel (§8.8).
		current, err := r.svc.Store.GetReportJob(r.ctx, r.tenantID, r.business.ID)
		if err != nil {
			return err
		}
		if current == nil || current.FinalizedAt != nil {
			return nil
		}
		if current.Status == model.ReportJobCancelRequested {
			r.business = current
			return r.requestFinalization()
		}
		payload := ReportSlicePayload{
			ReportJobID: current.ID, ReportID: current.ReportID, RevisionID: current.RevisionID,
			AttemptSeq: r.attemptSeq, SliceSeq: r.sliceSeq,
		}
		if !payload.owns(current, r.runtimeJobID) || current.Status != model.ReportJobRunning {
			return nil
		}
		r.business = current

		if err := r.closeInterruptedSections(); err != nil {
			return err
		}
		section, gapReason := r.nextSection()
		if section == nil {
			if gapReason != "" {
				// Nothing writable is left, but something is still open (a
				// dependency failed, or the section exhausted its attempts).
				// Skipping is the honest close: it is disclosed in `gap_json`
				// instead of pretending the work is still coming. The quality flag is
				// `not_checked`, not `failed`: a section that never ran was never
				// judged by the quality gate, and `failed` would claim it was.
				closed, err := r.skipRemaining(gapReason, model.ReportQualityNotChecked)
				if err != nil {
					return err
				}
				if closed == 0 {
					// Nothing moved: a second pass would loop on the same rows.
					// Leaving them to the caller keeps the loop bounded — finalize
					// discloses whatever is still unwritten instead of hanging.
					logger.Warn("report slice has nothing left to close",
						"report_job_id", r.business.ID, "reason", gapReason)
					break
				}
				continue
			}
			break
		}
		if reportSliceShouldYield(r.ctx, r.lease) {
			return r.checkpoint()
		}
		terminal, blocked, err := r.writeSection(section)
		if err != nil {
			return err
		}
		if blocked {
			r.budgetBlocked = true
			break
		}
		if !terminal {
			return nil
		}
	}
	if r.budgetBlocked {
		// Same rule as above: the sections this closes were never attempted.
		if _, err := r.skipRemaining(model.ReportGapBudgetExhausted, model.ReportQualityNotChecked); err != nil {
			return err
		}
	}
	if r.allTerminal() {
		return r.requestFinalization()
	}
	// Sections remain and the lease is fine: yield so a fresh slice continues.
	return r.checkpoint()
}

// requestFinalization moves the job to `finalizing` and hands it to the finalize
// Outbox path.
//
// It takes no stage argument on purpose: the stage is written by
// `BeginReportFinalization` (`assembling`) in the same statement as the status, so
// no caller can pass a value that is only logged and never stored.
func (r *reportSectionRun) requestFinalization() error {
	applied, err := r.svc.BeginReportFinalization(
		r.ctx, r.tenantID, r.business.ID, r.attemptSeq, r.sliceSeq, r.runtimeJobID)
	if err != nil {
		return err
	}
	logger.Info("report slice requested finalization",
		"report_job_id", r.business.ID, "applied", applied)
	return nil
}

// checkpoint yields the slice and records the progress it reached.
func (r *reportSectionRun) checkpoint() error {
	done := 0
	for _, section := range r.sections {
		if section.Status == model.ReportSectionSucceeded {
			done++
		}
	}
	applied, err := r.svc.YieldReportSlice(r.ctx, r.tenantID, r.business.ID,
		r.attemptSeq, r.sliceSeq, r.runtimeJobID, done)
	if err != nil {
		return err
	}
	logger.Info("report slice yielded",
		"report_job_id", r.business.ID, "slice_seq", r.sliceSeq,
		"progress_done", done, "applied", applied)
	return nil
}

// nextSection picks the next section to write, or returns the gap reason that
// explains why nothing writable is left.
//
// Order is `ord` (the template's order), because `depends_on` is a promise about
// content and a random order would break it. A section whose dependency did not
// succeed is not writable this run: it is reported back as a skip reason.
func (r *reportSectionRun) nextSection() (*model.ReportSection, string) {
	done := make(map[string]model.ReportSection, len(r.sections))
	for _, section := range r.sections {
		done[section.SectionKey] = section
	}
	unwritable := ""
	for index := range r.sections {
		section := &r.sections[index]
		if reportSectionIsTerminal(section.Status) {
			continue
		}
		if section.Attempt >= reportSectionMaxAttempts {
			// Out of attempts: this section is a `failed` gap, not a pending one.
			return nil, model.ReportGapFailed
		}
		blockedBy := ""
		for _, dependency := range reportSectionDependencies(section) {
			prior, ok := done[dependency]
			if !ok || prior.Status == model.ReportSectionSkipped ||
				prior.Status == model.ReportSectionFailed {
				blockedBy = dependency
				break
			}
		}
		if blockedBy != "" {
			// Its dependency will never succeed in this run, so this section is not
			// "still pending": it is skipped, and the gap list will say so.
			if unwritable == "" {
				unwritable = model.ReportGapSkipped
			}
			continue
		}
		return section, ""
	}
	return nil, unwritable
}

// writeSection runs one section through reserve → retrieve → begin → call →
// settle → commit, and retries the model half while §7.9 allows it.
//
// `terminal` says whether the section reached a terminal state (so the loop may
// continue); `blocked` says the budget gate refused, which ends the loop.
func (r *reportSectionRun) writeSection(section *model.ReportSection) (bool, bool, error) {
	for {
		terminal, blocked, retry, err := r.writeSectionAttempt(section)
		if err != nil || terminal || blocked || !retry {
			return terminal, blocked, err
		}
		// The row is still `writing` and `section.Attempt` still names the attempt
		// that just failed, so the retry re-begins it as `attempt+1`. Nothing may
		// mistake it for a dead slice's wreck: it never leaves this call.
	}
}

// writeSectionAttempt is one attempt of `writeSection`.
//
// `retry` reports that the attempt failed in a way §7.9 retries *inside the same
// slice* (`rgx_report_section.attempt ≤ 2`) and that another call is still
// allowed. Live traffic is why this exists: one 60 s read timeout on the chat
// route closed a section on attempt 1, which skipped every section depending on
// it and published a 3/5 report — two sections lost to one transient stall, with
// `reportSectionMaxAttempts` unreachable because a `failed` row is terminal.
func (r *reportSectionRun) writeSectionAttempt(section *model.ReportSection) (terminal, blocked, retry bool, err error) {
	now := time.Now().UTC()
	charge, err := r.svc.ReserveReportCharge(r.ctx, r.tenantID, ReportChargeCall{
		JobID: r.business.ID, SectionID: section.ID, AttemptSeq: r.attemptSeq,
		SliceSeq: r.sliceSeq, SectionAttempt: section.Attempt + 1,
		ReservedTokens: r.reservedTokens(), ModelID: r.snapshot.Model.ModelID,
		RuntimeJobID: r.runtimeJobID,
	}, now)
	if err != nil {
		if reportCodeOf(err) == codeReportBudgetExhausted {
			logger.Info("report budget exhausted",
				"report_job_id", r.business.ID, "section_id", section.ID, "error", err)
			return false, true, false, nil
		}
		return false, false, false, err
	}
	evidence, truncated, plan, retrieveErr := r.gatherEvidence(section)
	if retrieveErr != nil {
		// Nothing was sent to the model, so the reservation is refunded rather than
		// written down as unknown spend.
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, false, err
		}
		terminal, blocked, err = r.closeSection(section, model.ReportSectionFailed, retrieveErr, now)
		return terminal, blocked, false, err
	}
	if truncated {
		r.truncated++
	}
	instruction := r.sectionInstruction(section.SectionKey)
	prompt := reportSectionPrompt(r.report.Question, *section, instruction,
		sectionMaxWords(r.template, section.SectionKey), r.priorSections(section), evidence,
		parseReportLocationHints(section.LocationHintsJSON))

	applied, err := r.svc.Store.BeginReportSection(r.ctx, repository.ReportSectionStart{
		TenantID: r.tenantID, JobID: r.business.ID, RevisionID: r.revision.ID,
		SectionID: section.ID, PreviousAttempt: section.Attempt, Now: now,
		PromptHash: hashReportPrompt(prompt), Stage: model.ReportStageSectionWriting,
		Event: r.event(reportEventSectionStarted, section, map[string]interface{}{
			"attempt": section.Attempt + 1, "evidence": len(evidence), "truncated": truncated,
			"research_depth": plan.Depth, "research_planning": plan.Planning,
		}, now),
	})
	if err != nil {
		return false, false, false, err
	}
	if !applied {
		// Another slice already owns this section: release and stop, its outcome is
		// not ours to write.
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, false, err
		}
		return false, false, false, nil
	}
	section.Attempt++
	section.Status = model.ReportSectionWriting

	raw, callErr := r.svc.RAGFlow.ChatToModel(r.ctx,
		r.snapshot.Model.Provider, r.snapshot.Model.Instance, r.snapshot.Model.Name,
		prompt, false, false)
	// doc/304 §4 E154: `now` above is the instant `BeginReportSection` wrote into
	// `started_at`. Reusing it for the writes below made `finished_at` a second
	// copy of the start instant, so `finished_at - started_at` was structurally 0
	// — live 2026-11-25: 5/5 sections of a published revision, all 419-byte hints,
	// all `started_at == finished_at` to the microsecond. §7 item 23's "section
	// duration" therefore had no reader and no reading. One write, one instant:
	// settlement, evidence expiry and the commit's `finished_at` all carry this
	// value, and `reportFinishClock` states "after the start" instead of hoping
	// two clock reads land in different ticks.
	now = reportFinishClock(now)
	if callErr != nil {
		return r.handleCallFailure(section, charge, callErr)
	}
	// The raw endpoint answers with a string: the shape is the caller's problem
	// (see `decodeReportModelReply`), and a body we cannot read is a failed section,
	// not a broken job.
	reply, decodeErr := decodeReportModelReply(raw)
	if decodeErr != nil {
		return r.handleCallFailure(section, charge, decodeErr)
	}
	if !reply.UsageReported {
		// The estimate is made here, not in the decoder, because only this frame
		// knows the prompt that was sent. Settling at zero would report a free
		// section that the provider did bill (§8.6).
		reply.TokensIn = estimateReportTokens(prompt)
		reply.TokensOut = estimateReportTokens(reply.Text)
	}
	settled, err := r.settleCall(charge, reply, now)
	if err != nil {
		return false, false, false, err
	}
	if !settled {
		return false, false, false, fmt.Errorf("report charge %s could not be settled", charge.LedgerKey)
	}
	if detail, isEnvelope := reportModelErrorContent(reply.Text); isEnvelope {
		// The provider answered — and was billed — but what came back is the engine's
		// component-error envelope, not a section (see `reportModelErrorContent`).
		// The charge stays settled because a call really happened; the *content* must
		// not, so this is an unusable attempt that retries like any other, and a
		// section that keeps getting envelopes closes `failed` and is disclosed as a
		// gap. Either way the deliverable never carries the error string.
		logger.Warn("report section answered with a provider error envelope",
			"report_job_id", r.business.ID, "ord", section.Ord,
			"section_key", section.SectionKey, "detail", detail)
		return r.retryOrClose(section, fmt.Errorf(
			"provider returned an error envelope instead of a section: %s", detail), now)
	}
	if reportNormalizeContent(reply.Text) == "" {
		// The call happened and was billed, but the answer was empty: an unusable
		// attempt, so it retries like any other (§7.9) instead of ending the section.
		return r.retryOrClose(section, errors.New("model returned an empty section"), now)
	}
	terminal, blocked, err = r.commitSection(section, reply, evidence, plan, now)
	return terminal, blocked, false, err
}

// retryOrClose ends one *model* attempt: an answer the model did not produce
// usefully gets another call while `reportSectionMaxAttempts` allows, and is
// closed `failed` only once the attempts are spent.
//
// The retry is safe for the ledger: every reservation allocates its own
// `call_seq`, and an unanswered call is written down as an orphan, which the
// budget gate discloses but does not subtract a second time (§8.6).
func (r *reportSectionRun) retryOrClose(
	section *model.ReportSection, cause error, now time.Time,
) (bool, bool, bool, error) {
	if reportSectionRetryable(cause) && section.Attempt < reportSectionMaxAttempts {
		logger.Warn("report section retrying",
			"report_job_id", r.business.ID, "ord", section.Ord,
			"section_key", section.SectionKey, "attempt", section.Attempt,
			"max_attempts", reportSectionMaxAttempts, "error", cause)
		return false, false, true, nil
	}
	terminal, blocked, err := r.closeSection(section, model.ReportSectionFailed, cause, now)
	return terminal, blocked, false, err
}

// handleCallFailure ends one section after a provider error.
//
// The billing story depends on whether the provider answered at all: a 4xx
// business refusal means nothing was generated and the reservation is refunded,
// while a transport failure or a timeout means we genuinely do not know, and the
// reservation becomes an orphan that reconciliation will account for (§8.6).
func (r *reportSectionRun) handleCallFailure(
	section *model.ReportSection, charge *model.ReportCharge, callErr error,
) (bool, bool, bool, error) {
	now := time.Now().UTC()
	if reportProviderAnswered(callErr) {
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, false, err
		}
	} else if _, err := r.svc.Store.OrphanReportCharge(r.ctx, repository.ReportChargeCandidate{
		ID: charge.ID, TenantID: r.tenantID, JobID: r.business.ID, LedgerKey: charge.LedgerKey,
		ReservedTokens: charge.ReservedTokens, Reason: model.ReportReconcileProviderUnconfirmed,
	}, now); err != nil {
		return false, false, false, err
	}
	return r.retryOrClose(section, callErr, now)
}

// reportSectionRetryable says whether one *model* attempt deserves another call
// inside the same slice (§7.9: `rgx_report_section.attempt ≤ 2`).
//
// A transport failure, a timeout or an unreadable/blank answer is the model
// failing to answer, and the next call may well succeed; a business refusal (a
// 4xx in which the provider answered and said no) is not retried, because the
// same request would be refused again and the retry would only burn budget. A
// cancelled call is the *job* being cancelled, so it must not be retried either.
func reportSectionRetryable(cause error) bool {
	var upstream *ragflow.Error
	if !errors.As(cause, &upstream) {
		// Decoder/policy failures (unreadable body, empty answer, unusable text)
		// belong to the attempt, not to the job.
		return true
	}
	switch upstream.Type {
	case ragflow.ErrorTypeTransport, ragflow.ErrorTypeTimeout, ragflow.ErrorTypeProtocol:
		return true
	default:
		return false
	}
}

// reportFinishClock is the instant a section's post-call writes are stamped with.
//
// `reportSectionRun.writeSectionAttempt` opens with `now := time.Now().UTC()` and
// hands that value to `BeginReportSection`, which is what `started_at` ends up
// holding. Every finish write needs a *later* instant, so this returns
// `max(now, started+1µs)`: the floor is what makes "finished after started" true by
// construction rather than by clock resolution, which is the same lesson E153
// taught about `checked_at` (doc/304 §4).
func reportFinishClock(started time.Time) time.Time {
	now := time.Now().UTC()
	if floor := started.Add(time.Microsecond); !now.After(floor) {
		return floor
	}
	return now
}

// settleCall converts the reservation into the measured cost.
func (r *reportSectionRun) settleCall(
	charge *model.ReportCharge, reply reportModelReply, now time.Time,
) (bool, error) {
	actual := reply.TokensIn + reply.TokensOut
	if actual <= 0 {
		return false, errReportLedgerInconsistent("provider reported no token usage")
	}
	return r.svc.SettleReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, actual, now)
}

// commitSection writes the section body, its citations and their evidence.
//
// A section that required evidence but cites nothing is still committed: refusing
// the text would throw away the one honest thing it did (saying the sources do not
// cover the question). It is disclosed instead — `evidence_status=unsupported` and
// `quality_status=needs_review`.
func (r *reportSectionRun) commitSection(
	section *model.ReportSection, reply reportModelReply,
	evidence []reportEvidenceItem, plan reportResearchPlan, now time.Time,
) (bool, bool, error) {
	content := reportNormalizeContent(reply.Text)
	if content == "" {
		return r.closeSection(section, model.ReportSectionFailed,
			errors.New("model returned an empty section"), now)
	}
	// A3c: a marker that no evidence item can answer is a fabricated reference, not
	// a citation with a missing row. It is removed from the body (so the delivered
	// text never shows a number the appendix cannot print) and the section is
	// flagged for review (so the rewrite is not silent). The two together are what
	// makes 标记 ↔ 引用行 consistent by construction instead of by hope.
	dangling := reportDanglingCitationMarkers(content, len(evidence))
	if len(dangling) > 0 {
		content = reportStripCitationMarkers(content, dangling)
		if content == "" {
			return r.closeSection(section, model.ReportSectionFailed,
				errors.New("model answered with nothing but unresolvable citations"), now)
		}
	}
	cited := reportCitedIndexes(content, len(evidence))
	baseEvidenceStatus := reportEvidenceStatus(
		section.RequiresEvidence, len(evidence), len(cited), r.template.Retrieval.MinEvidence)
	// R12 (doc/288 §9.3): the P0 rule says "did this section cite enough evidence";
	// it cannot see that two cited documents disagree about a number. The signals are
	// computed from the evidence the writer was shown and only ever *escalate* the
	// disclosure — `unsupported` and `not_required` stay as they are, because a
	// section that never claimed support cannot become "conflicting".
	signals := reportEvidenceSignals(evidence)
	evidenceStatus := reportEvidenceStatusWithSignals(baseEvidenceStatus, signals)
	// The P0 quality gate is mechanical, and these are its only two rules (§14.2):
	// the section cites evidence when its template requires evidence, and the
	// citations actually resolve. Anything else — fewer citations than the template
	// asked for, or citations the retriever could not support — is `needs_review`,
	// a disclosure that never blocks publishing. A section whose template never
	// asked for evidence stays `not_checked`: there is nothing for the gate to say,
	// and saying `passed` would invent a verdict.
	qualityStatus := reportSectionQualityStatus(evidenceStatus)
	if len(dangling) > 0 && qualityStatus != model.ReportQualityFailed {
		qualityStatus = model.ReportQualityNeedsReview
	}
	citations := make([]model.ReportCitation, 0, len(cited))
	for _, item := range evidence {
		if !cited[item.Index] {
			continue
		}
		citations = append(citations, r.citation(section, item, now))
	}
	// R11 (doc/288 §9.2): the claims are extracted from the body that was just
	// written and stored beside it, so "which sentence asserted this, and did the
	// cited chunk support it" stops being a re-read of the prose. The same pass
	// supplies `citation.claim_id`, which is what makes 标记 ↔ 引用行 ↔ claim one
	// chain instead of three agreements by convention.
	drafts := reportExtractClaims(content, evidence)
	claims := reportClaimRows(drafts, r.tenantID, r.revision.ID, section)
	claimOfMarker := reportClaimCitationLinks(drafts)
	claimIDOfOrd := make(map[int]string, len(claims))
	for _, claim := range claims {
		claimIDOfOrd[claim.Ord] = claim.ID
	}
	for index := range citations {
		// The citation points at the claim that first used its marker: the unique
		// index is `(section, ord)`, so "the first sentence to cite chunk 3" is the
		// only stable answer when several sentences share a marker.
		citations[index].ClaimID = claimIDOfOrd[claimOfMarker[citations[index].Ord]]
	}
	// The evidence clock is `max(report_days, report_evidence_days)`, never the
	// report clock alone: a snapshot that expires before the report citing it turns
	// "有据可查" into a false promise (doc/288 §7.6 invariant ②).
	evidenceRows, err := reportEvidenceRows(evidence, cited, r.tenantID,
		r.evidenceExpiry(now), now)
	if err != nil {
		return r.closeSection(section, model.ReportSectionFailed, err, now)
	}

	commit := repository.ReportSectionCommit{
		TenantID: r.tenantID, JobID: r.business.ID, Now: now,
		Section: model.ReportSection{
			ID: section.ID, RevisionID: r.revision.ID, Attempt: section.Attempt,
			ContentMD: content, ContentHash: hashReportContent(content),
			TokensIn: reply.TokensIn, TokensOut: reply.TokensOut,
			CitationCount: len(citations), ClaimCount: len(claims),
			EvidenceStatus: evidenceStatus, QualityStatus: qualityStatus,
			// R18's disclosure travels with the body: `standard` for a normal run,
			// and one of the three fallback words when deep research degraded.
			ResearchPlanJSON: researchPlanJSON(plan),
			// §5.8 item 1: the pool this section's body was written from, frozen next
			// to the body so "the writer had thin evidence" is answerable later. The
			// three values are pointers because a section that never retrieved
			// (`requires_evidence=false`) has no pool to describe, and `0 hits` would
			// be a claim about the corpus that nobody checked.
			PoolHitChunks:      reportPoolHitChunks(section, evidence),
			PoolInsufficient:   reportPoolInsufficient(section, evidence, r.template.Retrieval.MinEvidence),
			PoolCandidateCount: reportPoolCandidateCount(section, evidence, cited),
		},
		Citations: citations, Claims: claims, Evidence: evidenceRows,
		Stage: model.ReportStageSectionWriting,
		Event: r.event(reportEventSectionSucceeded, section, map[string]interface{}{
			"citation_count": len(citations), "evidence_status": evidenceStatus,
			"quality_status": qualityStatus, "tokens_in": reply.TokensIn,
			"dangling_citations": dangling,
			"tokens_out":         reply.TokensOut, "usage_estimated": !reply.UsageReported,
			"claim_count":           len(claims),
			"evidence_signal_count": len(signals),
			"evidence_signals":      reportEvidenceSignalKinds(signals),
			"research_depth":        plan.Depth,
			"research_planning":     plan.Planning,
			"research_merged":       plan.Merged,
		}, now),
	}
	applied, err := r.svc.Store.CommitReportSection(r.ctx, commit)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// A closing write won the race: the section is already `failed` and this
			// body must not overwrite it.
			return true, false, nil
		}
		return false, false, err
	}
	if !applied {
		return true, false, nil
	}
	// T04b / §5.3.1: freeze the *uncited* part of this section's retrieval pool.
	//
	// Frozen at generation, not at review time: a review that re-retrieved would be
	// looking at today's index and calling it "what the writer saw". The default
	// `scan_top_k=0` freezes nothing at all, so an installation that never opts in
	// stores no corpus copy it did not already store as a citation.
	//
	// A failure here is logged and dropped: candidates are advisory (they can only
	// add a finding), and failing the section over them would trade a real body for a
	// better list of suspects.
	r.freezeCandidates(section, evidence, cited, now)
	section.Status = model.ReportSectionSucceeded
	section.ContentMD = content
	section.CitationCount = len(citations)
	section.ClaimCount = len(claims)
	section.EvidenceStatus = evidenceStatus
	section.QualityStatus = qualityStatus
	logger.Info("report section written",
		"report_job_id", r.business.ID, "ord", section.Ord, "section_key", section.SectionKey,
		"citations", len(citations), "claims", len(claims),
		"evidence_status", evidenceStatus)
	return true, false, nil
}

// reportPoolHitChunks is `NULL` for a section that required no evidence: it never
// asked the corpus a question, so the number of chunks it happened to see is not a
// measurement of anything. Otherwise it is the pool width, and `0` is a real answer
// ("the query matched nothing").
func reportPoolHitChunks(section *model.ReportSection, evidence []reportEvidenceItem) *int {
	if !section.RequiresEvidence {
		return nil
	}
	hits := len(evidence)
	return &hits
}

// reportPoolInsufficient compares the pool to the template's `min_evidence` — the
// same threshold `evidence_status` uses, so the two disclosures cannot disagree about
// what "thin" means. It is advisory like the outline-time preview, and `NULL` (rather
// than `false`) is the answer for a section that was never going to cite anything.
func reportPoolInsufficient(
	section *model.ReportSection, evidence []reportEvidenceItem, minEvidence int,
) *bool {
	if !section.RequiresEvidence {
		return nil
	}
	insufficient := len(evidence) < minEvidence
	if minEvidence <= 0 {
		// A template that declares no floor has no thin/adequate split to report: "we
		// found less than zero" is not a finding.
		return nil
	}
	return &insufficient
}

// reportPoolCandidateCount is how many pool chunks the body did *not* cite: the width
// of what a review would contrast against. It counts the whole uncited remainder, not
// the frozen `scan_top_k` subset — "how much was there" and "how much did we freeze"
// are different facts and the panel already discloses the second.
func reportPoolCandidateCount(
	section *model.ReportSection, evidence []reportEvidenceItem, cited map[int]bool,
) *int {
	if !section.RequiresEvidence {
		return nil
	}
	uncited := 0
	for _, item := range evidence {
		if !cited[item.Index] {
			uncited++
		}
	}
	return &uncited
}

// freezeCandidates persists up to `scan_top_k` uncited chunks for one section.
//
// `pool_rank` is the retrieval order, so a reviewer sees "the runner-up source" rather
// than an arbitrary sample: a contradiction candidate that ranked 2nd is a much
// stronger signal than one that ranked 40th out of 40.
func (r *reportSectionRun) freezeCandidates(
	section *model.ReportSection, evidence []reportEvidenceItem, cited map[int]bool, now time.Time,
) {
	// Read the width from the *frozen* snapshot, not from the template: the template
	// may have been republished between the outline and this section, and a section's
	// pool must be frozen with the revision's own policy (T04b / §5.3.1).
	topK := r.scanTopK
	if topK <= 0 {
		return
	}
	rows := make([]model.ReportConflictCandidate, 0, topK)
	for _, item := range evidence {
		if len(rows) >= topK {
			break
		}
		if cited[item.Index] {
			continue
		}
		source := item.SourceContent
		if strings.TrimSpace(source) == "" {
			source = item.Content
		}
		window, strategy, ok := reportEvidenceWindow(source)
		if !ok {
			continue
		}
		contentHash := hashReportContent(window)
		rows = append(rows, model.ReportConflictCandidate{
			// The id is derived from `(section, chunk)` so a retried section rewrites the
			// same candidate rows instead of accumulating a second copy of the pool.
			ID:       reportRowID("candidate", section.ID, item.ChunkID),
			TenantID: r.tenantID, ReportID: r.report.ID, RevisionID: r.revision.ID,
			SectionOrd: section.Ord, SectionID: section.ID,
			DatasetID: r.localDatasetID(item.EngineDataset), DocumentID: item.DocumentID,
			ChunkID: item.ChunkID, ChunkOrd: item.Index,
			Content: reportEvidenceSnippet(window), ContentHash: contentHash,
			ContentSize: int64(len([]rune(window))),
			Truncated:   strategy != model.ReportEvidenceTruncationNone, TruncationStrategy: strategy,
			PoolRank:     item.Index,
			ACLScopeJSON: r.permissionScopeJSON(),
			CreatedAt:    now,
			// The candidate clock is `min(created+evidence_ttl, report.expires_at)`
			// (§5.3.1): it may never outlive the snapshot it was compared against, and a
			// candidate that outlived its *report* would be the only copy of a chunk the
			// workspace promised to delete.
			ExpiresAt: reportExpiryPtr(reportEarliestExpiry(r.evidenceExpiry(now), r.report.ExpiresAt)),
		})
	}
	if len(rows) == 0 {
		return
	}
	if err := r.svc.Store.InsertConflictCandidates(r.ctx, rows); err != nil {
		logger.Warn("report candidate freeze failed",
			"report_job_id", r.business.ID, "ord", section.Ord, "error", err)
		return
	}
	r.svc.recordReportAudit(r.ctx, r.report.OwnerUserID, r.tenantID,
		"report.assurance_candidates_frozen", r.report.ID, map[string]any{
			"section_ord": section.Ord, "frozen_count": len(rows),
			"scan_top_k": topK, "acl_scope_hash": hashReportContent(r.permissionScopeJSON()),
		})
}

// reportEarliestExpiry returns whichever clock runs out first. A missing clock (zero)
// means "that side has no deadline", so the other one stands: inventing "now" for a
// missing report clock would purge a candidate the report still needs, and returning
// zero would mean "never", which is the opposite of what a retention clock says.
func reportEarliestExpiry(first, second time.Time) time.Time {
	if first.IsZero() {
		return second
	}
	if second.IsZero() {
		return first
	}
	if second.Before(first) {
		return second
	}
	return first
}

// reportExpiryPtr is `&t` for a value that is computed once — Go's addressability
// rule makes the inline form a compile error, and a nil expiry would mean "never",
// which is the opposite of what the retention clock says.
func reportExpiryPtr(t time.Time) *time.Time {
	return &t
}

// closeSection writes the honest ending of one attempt.
func (r *reportSectionRun) closeSection(
	section *model.ReportSection, status string, cause error, now time.Time,
) (bool, bool, error) {
	qualityStatus := model.ReportQualityFailed
	evidenceStatus := model.ReportEvidenceUnsupported
	if status == model.ReportSectionSkipped {
		qualityStatus = model.ReportQualityNotChecked
	}
	payload := map[string]interface{}{
		"reason": reportErrorReason(cause), "ord": section.Ord,
		"section_key": section.SectionKey, "title": section.Title,
		"error_code": reportCodeString(cause),
	}
	applied, err := r.svc.Store.CloseReportSection(r.ctx, repository.ReportSectionClose{
		TenantID: r.tenantID, JobID: r.business.ID, RevisionID: r.revision.ID,
		SectionID: section.ID, Attempt: section.Attempt, Status: status,
		QualityStatus: qualityStatus, EvidenceStatus: evidenceStatus, Now: now,
		Stage: model.ReportStageSectionWriting,
		Event: r.event(reportEventSectionFailed, section, payload, now),
		Audit: r.sectionFailureAudit(section, payload, now),
	})
	if err != nil {
		return false, false, err
	}
	if applied {
		section.Status = status
		section.QualityStatus = qualityStatus
		section.EvidenceStatus = evidenceStatus
		section.GapReason = model.ReportGapFailed
	}
	logger.Warn("report section closed",
		"report_job_id", r.business.ID, "ord", section.Ord, "status", status, "error", cause)
	return true, false, nil
}

// sectionFailureAudit is the `report.section.failed` ledger row (doc/288 §11.3).
//
// The event tells the SSE reader that a section ended failed; the audit row is
// what answers "who can look at this and why" months later, after the job event
// has been purged by its own retention clock. Neither substitutes for the other,
// which is why both are written in the same transaction.
//
// The writer is the runner, not a person, so the row carries no UserID — exactly
// like `report.charge.reconciled`. Inventing a system user id would make the
// audit claim an actor that does not exist.
func (r *reportSectionRun) sectionFailureAudit(
	section *model.ReportSection, payload map[string]interface{}, now time.Time,
) *model.AuditLog {
	detail, err := json.Marshal(map[string]interface{}{
		"report_job_id": r.business.ID,
		"revision_id":   r.revision.ID,
		"revision":      r.revision.Revision,
		"section_key":   section.SectionKey,
		"section_ord":   section.Ord,
		"attempt":       section.Attempt,
		"reason":        payload["reason"],
		"error_code":    payload["error_code"],
	})
	if err != nil {
		// json.Marshal of strings and ints cannot fail; if it ever did, an audit
		// row with no detail still beats a failed section nobody can trace.
		detail = []byte(`{}`)
	}
	return &model.AuditLog{
		TenantID: r.tenantID, Action: "report.section.failed",
		Resource: "report", ResourceID: r.report.ID,
		Result: "FAILURE", DetailJSON: string(detail), At: now,
	}
}

// closeInterruptedSections ends the attempts a dead slice left behind.
//
// A `writing` row seen at the top of this loop cannot belong to this slice: the
// loop only marks a section writing inside `writeSection`, and that call either
// commits or closes it before returning. So the row is the wreck of a slice that
// died mid-call (a crashed worker, an aborted commit). Nothing can ever close it
// on its own — the owner is gone — so the honest close is here, and the
// `attempt` CAS in `CloseReportSection` still refuses the write if another owner
// got there first.
func (r *reportSectionRun) closeInterruptedSections() error {
	now := time.Now().UTC()
	for index := range r.sections {
		section := &r.sections[index]
		if section.Status != model.ReportSectionWriting {
			continue
		}
		logger.Warn("report section interrupted",
			"report_job_id", r.business.ID, "ord", section.Ord,
			"section_key", section.SectionKey, "attempt", section.Attempt)
		if _, _, err := r.closeSection(section, model.ReportSectionFailed,
			errors.New("section write was interrupted before it committed"), now); err != nil {
			return err
		}
	}
	return nil
}

// skipRemaining closes every section that never started.
//
// `gapReason` is the disclosure the reader sees: `budget_exhausted` when the
// envelope ran out, `skipped` when a dependency failed first. The two are not
// interchangeable — one says "buy more budget", the other says "fix the source".
//
// The selection is narrower than the store's guard (`status IN (pending, failed)`)
// on purpose: a `writing` row is not this call's to close, and marking it `skipped`
// in memory only would make `allTerminal()` lie — the job would then ask for
// finalization with a section still in flight (the live hang that motivated
// `closeInterruptedSections`).
func (r *reportSectionRun) skipRemaining(gapReason, qualityStatus string) (int, error) {
	pending := make([]string, 0, len(r.sections))
	events := make([]*model.ReportJobEvent, 0, len(r.sections))
	now := time.Now().UTC()
	for index := range r.sections {
		section := &r.sections[index]
		if !reportSectionSkippable(section.Status) {
			continue
		}
		pending = append(pending, section.ID)
		events = append(events, r.event(reportEventSectionFailed, section, map[string]interface{}{
			"ord": section.Ord, "section_key": section.SectionKey, "title": section.Title,
			"reason": gapReason, "error_code": skipErrorCode(gapReason),
		}, now))
		section.Status = model.ReportSectionSkipped
		section.QualityStatus = qualityStatus
		section.GapReason = gapReason
	}
	if len(pending) == 0 {
		return 0, nil
	}
	applied, err := r.svc.Store.SkipReportSections(r.ctx, repository.ReportSectionSkip{
		TenantID: r.tenantID, JobID: r.business.ID, RevisionID: r.revision.ID,
		SectionIDs: pending, QualityStatus: qualityStatus,
		EvidenceStatus: model.ReportEvidenceNotRequired, GapReason: gapReason, Now: now,
		Stage: model.ReportStageSectionWriting, Events: events,
	})
	if err != nil {
		return 0, err
	}
	return int(applied), nil
}

// reportSectionSkippable is the narrow slice of the store's guard this runner may
// use: only rows that never started. The store also accepts `failed` rows, but
// relabelling one of those here would overwrite the reason it failed with
// "budget exhausted" / "skipped" — a disclosure lie, not a skip.
func reportSectionSkippable(status string) bool {
	return status == model.ReportSectionPending
}

// skipErrorCode maps a skip reason to the code an operator can look up. Only the
// budget case has one: a dependency failure is not an API error.
func skipErrorCode(gapReason string) string {
	if gapReason == model.ReportGapBudgetExhausted {
		return reportCodeString(errReportBudgetExhausted(""))
	}
	return ""
}

// allTerminal reports whether every section has an outcome.
func (r *reportSectionRun) allTerminal() bool {
	for _, section := range r.sections {
		if !reportSectionIsTerminal(section.Status) {
			return false
		}
	}
	return true
}

// reservedTokens is the per-call envelope. The reservation is the template's
// per-section cap, not an estimate: the gate compares it against the envelope
// frozen at creation, so it must be the same number the envelope was built from.
func (r *reportSectionRun) reservedTokens() int64 {
	if r.template.Limits.MaxTokensPerSection <= 0 {
		return 1
	}
	return int64(r.template.Limits.MaxTokensPerSection)
}

// retrieve fetches the section's evidence from the frozen dataset bindings.
func (r *reportSectionRun) retrieve(section *model.ReportSection) ([]reportEvidenceItem, bool, error) {
	return r.retrieveQuery(section, section.Query)
}

// retrieveQuery is one retrieval against the frozen bindings. R18's planner supplies
// the query; the standard path passes the section query itself, so both depths go
// through one code path and one bounding rule.
func (r *reportSectionRun) retrieveQuery(
	section *model.ReportSection, query string,
) ([]reportEvidenceItem, bool, error) {
	engineDatasetIDs := make([]string, 0, len(r.snapshot.Datasets))
	for _, binding := range r.snapshot.Datasets {
		engineDatasetIDs = append(engineDatasetIDs, binding.EngineDatasetID)
	}
	req := ragflow.RetrieveDatasetsRequest{
		DatasetIDs: engineDatasetIDs, Question: query,
		Page: 1, PageSize: r.snapshot.Retrieval.TopK,
		// `nil` is the honest value: the workspace holds no calibrated cutoff, so the
		// engine applies its own. The snapshot records the policy and the absence.
		SimilarityThreshold: r.snapshot.Retrieval.SimilarityThreshold,
		TopK:                r.snapshot.Retrieval.TopK,
	}
	result, err := r.svc.RAGFlow.RetrieveDatasets(r.ctx, req)
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, nil
	}
	items := make([]reportEvidenceItem, 0, len(result.Chunks))
	for _, chunk := range result.Chunks {
		content := stringFromAny(chunk["content"])
		if content == "" {
			continue
		}
		// The engine's chunk identifier is not one name: `/retrieval` on RAGFlow
		// v1.0.0 answers `id` (and `row_id`), while the older envelope used
		// `chunk_id`. Reading only `chunk_id` produced snapshots whose chunk was
		// nameless, i.e. evidence a reader cannot trace back to a source chunk.
		chunkID := firstNonEmptyString(stringFromAny(chunk["chunk_id"]),
			stringFromAny(chunk["id"]), stringFromAny(chunk["row_id"]))
		if chunkID == "" {
			// An evidence row that cannot name its chunk is untraceable: disclose it
			// as missing evidence rather than store a citation into nowhere.
			logger.Warn("report evidence chunk carries no identifier",
				"report_job_id", r.business.ID, "ord", section.Ord,
				"dataset_id", stringFromAny(chunk["dataset_id"]))
			continue
		}
		documentID := stringFromAny(chunk["document_id"])
		if documentID == "" {
			documentID = stringFromAny(chunk["doc_id"])
		}
		engineDataset := stringFromAny(chunk["dataset_id"])
		localDataset := r.localDatasetID(engineDataset)
		items = append(items, reportEvidenceItem{
			ChunkID:       chunkID,
			DatasetID:     localDataset,
			DocumentID:    documentID,
			EngineDataset: engineDataset,
			DatasetLabel:  r.datasetLabel(engineDataset),
			DocumentLabel: reportChunkDocumentLabel(chunk, documentID),
			Content:       content,
			SourceContent: strings.TrimSpace(content),
			ContentHash:   sha256Hex(strings.TrimSpace(content)),
			Score:         reportChunkScore(chunk),
			ScoreType:     reportChunkScoreType(chunk),
		})
	}
	bounded, truncated := boundReportEvidence(items)
	return bounded, truncated, nil
}

// datasetLabel maps an engine dataset id back to the frozen binding, so a prompt
// never names a dataset the report was not allowed to use.
func (r *reportSectionRun) datasetLabel(engineDatasetID string) string {
	for _, binding := range r.snapshot.Datasets {
		if binding.EngineDatasetID == engineDatasetID {
			return binding.DatasetID
		}
	}
	return engineDatasetID
}

// priorSections is the coherence hint: the sections this one declares, limited to
// the ones already written.
func (r *reportSectionRun) priorSections(section *model.ReportSection) []reportPriorSection {
	dependencies := reportSectionDependencies(section)
	if len(dependencies) == 0 {
		return nil
	}
	prior := make([]reportPriorSection, 0, len(dependencies))
	for _, dependency := range dependencies {
		for _, candidate := range r.sections {
			if candidate.SectionKey != dependency || candidate.Status != model.ReportSectionSucceeded {
				continue
			}
			excerpt := strings.TrimSpace(candidate.ContentMD)
			if runes := []rune(excerpt); len(runes) > reportPriorSectionExcerptRunes {
				excerpt = string(runes[:reportPriorSectionExcerptRunes])
			}
			prior = append(prior, reportPriorSection{Title: candidate.Title, Excerpt: excerpt})
		}
	}
	return prior
}

// sectionInstruction is the template's wording for one section, looked up by key
// rather than by index so a reordered template cannot attach the wrong ask.
func (r *reportSectionRun) sectionInstruction(sectionKey string) string {
	for _, declared := range r.template.Sections {
		if declared.Key == sectionKey {
			return declared.Prompt
		}
	}
	return ""
}

// citation builds one citation row. `EvidenceSnapshotID` is filled in by the
// repository, which is the only place that knows the deduped snapshot id.
func (r *reportSectionRun) citation(
	section *model.ReportSection, item reportEvidenceItem, now time.Time,
) model.ReportCitation {
	snippet := reportEvidenceSnippet(item.Content)
	return model.ReportCitation{
		ID:         reportRowID("citation", section.ID, fmt.Sprint(item.Index)),
		TenantID:   r.tenantID,
		RevisionID: r.revision.ID, SectionID: section.ID, SectionOrd: section.Ord,
		SectionVersion: section.SectionVersion, Ord: item.Index,
		SourceDatasetID:       r.localDatasetID(item.EngineDataset),
		SourceEngineDatasetID: item.EngineDataset,
		SourceDocumentID:      item.DocumentID,
		DocumentLabel:         item.DocumentLabel,
		SourceChunkID:         item.ChunkID,
		SourceContentHash:     item.ContentHash,
		ReferencedAt:          now,
		ReferencedBy:          r.report.OwnerUserID,
		RetrievalScore:        item.Score,
		RetrievalScoreType:    reportScoreTypeOrHybrid(item.ScoreType),
		Snippet:               snippet,
		UsedInText:            true,
		// EvidenceUsage (doc/288 §7.5.1, A37). The snapshot row is deduplicated by
		// content, so it cannot hold "who cited this and under what range" — the
		// second citer would inherit the first one's answer. These two columns are
		// the per-reference half, and they stay empty only if the run has no frozen
		// dataset list at all.
		SourcePermissionScopeJSON: r.permissionScopeJSON(),
		ReferenceWindowJSON:       reportCitationReferenceWindowJSON(snippet),
	}
}

// reportCitationPermissionScope is the "当时的授权范围" half of EvidenceUsage.
//
// It records the dataset set the run was frozen to, because that *is* the scope the
// retrieval ran under: `parseReportDatasetBindings` refuses to retrieve from
// "whatever the workspace holds today" (§13), so the frozen list is the only honest
// answer to "this citation could have come from where".
type reportCitationPermissionScope struct {
	Datasets []reportCitationScopeDataset `json:"datasets"`
}

type reportCitationScopeDataset struct {
	DatasetID       string `json:"dataset_id"`
	EngineDatasetID string `json:"engine_dataset_id"`
}

func (r *reportSectionRun) permissionScopeJSON() string {
	scope := reportCitationPermissionScope{
		Datasets: make([]reportCitationScopeDataset, 0, len(r.snapshot.Datasets)),
	}
	for _, binding := range r.snapshot.Datasets {
		scope.Datasets = append(scope.Datasets, reportCitationScopeDataset{
			DatasetID:       binding.DatasetID,
			EngineDatasetID: binding.EngineDatasetID,
		})
	}
	encoded, err := json.Marshal(scope)
	if err != nil {
		// Cannot happen for two string fields; an empty column is a visible gap in an
		// audit query, which is the right failure mode for "we could not record it".
		return ""
	}
	return string(encoded)
}

// reportCitationReferenceWindow is the other half: where in the frozen content the
// snippet sits.
//
// The offsets are rune indices into `rgx_report_evidence_snapshot.content`, and the
// window is always a prefix (`start=0`, `end` = rune length of the snippet). That is
// not a simplification: §7.5 makes the snapshot a prefix window and the snippet a
// shorter prefix of the same text, which is exactly why A33's substring assertion
// can hold instead of "search for the quoted sentence and hope".
func reportCitationReferenceWindowJSON(snippet string) string {
	window := struct {
		Start int    `json:"start"`
		End   int    `json:"end"`
		Unit  string `json:"unit"`
	}{Start: 0, End: len([]rune(snippet)), Unit: "rune"}
	encoded, err := json.Marshal(window)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// localDatasetID maps an engine dataset id back to the workspace dataset id, which
// is the id a reader can actually open.
func (r *reportSectionRun) localDatasetID(engineDatasetID string) string {
	for _, binding := range r.snapshot.Datasets {
		if binding.EngineDatasetID == engineDatasetID {
			return binding.DatasetID
		}
	}
	return ""
}

// event builds one broadcast event for the section stream.
func (r *reportSectionRun) event(
	eventType string, section *model.ReportSection, payload map[string]interface{}, now time.Time,
) *model.ReportJobEvent {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["ord"] = section.Ord
	payload["section_key"] = section.SectionKey
	payload["title"] = section.Title
	payload["section_id"] = section.ID
	if encoded, err := json.Marshal(payload); err == nil {
		return &model.ReportJobEvent{
			TenantID: r.tenantID, JobID: r.business.ID, Type: eventType,
			PayloadJSON: string(encoded), CreatedAt: now,
			ExpiresAt: reportExpiry(now, r.svc.ReportRetentionFor(r.ctx, r.tenantID).ReportEventDays, reportDefaultEventDays),
		}
	}
	return nil
}

// reportEvidenceSnippet is the citation's disclosure snippet as a *literal prefix*
// of the chunk text.
//
// It differs from truncateKnowledgeText on purpose: that helper appends "..." so a
// reader knows the text continues, which would stop the result from being a substring
// of the source. A33 requires the snapshot to contain the citation snippet verbatim,
// so the citation-side snippet carries no ellipsis. The ellipsis stays where it
// belongs — in prose read by a human, not in a pointer meant to be verified.
func reportEvidenceSnippet(content string) string {
	trimmed := strings.TrimSpace(content)
	runes := []rune(trimmed)
	if len(runes) <= reportEvidenceSnippetChars {
		return trimmed
	}
	return strings.TrimSpace(string(runes[:reportEvidenceSnippetChars]))
}

// reportEvidenceWindow fits one chunk into the per-snapshot byte budget (§7.5:
// ≤8KB) without splitting a rune, and reports which strategy produced it.
//
// The window is a prefix, which is what makes A33 provable: the citation snippet is
// itself a prefix of the same text, so a prefix window at least as long as the
// snippet always contains it. A window chosen for "relevance" instead would need the
// snippet located first, and would fail exactly when the interesting sentence sat
// past the budget.
func reportEvidenceWindow(content string) (string, string, bool) {
	if len(content) <= reportEvidenceSnapshotMaxBytes {
		return content, model.ReportEvidenceTruncationNone, true
	}
	runes := []rune(content)
	size, limit := 0, 0
	for limit < len(runes) {
		width := utf8.RuneLen(runes[limit])
		if width < 0 || size+width > reportEvidenceSnapshotMaxBytes {
			break
		}
		size += width
		limit++
	}
	if limit == 0 {
		return "", model.ReportEvidenceTruncationNone, false
	}
	return string(runes[:limit]), model.ReportEvidenceTruncationExcerptWindow, true
}

// reportEvidenceRows builds the evidence snapshots of the cited chunks.
//
// Only cited chunks are snapshotted: an uncited chunk is retrieval noise, and
// storing it would inflate the evidence table with text nothing references. The
// row keeps both hashes on purpose — `source_content_hash` identifies the upstream
// chunk, `content_hash` identifies the (possibly truncated) copy we stored.
//
// The snippet check is the write-side half of A33: a snapshot that does not contain
// the snippet it is cited for is not evidence, it is a broken pointer, and the honest
// answer is to refuse the commit (44103) rather than store a row the appendix cannot
// back up.
// evidenceExpiry is the frozen clock of every snapshot this run writes.
//
// It is a named method rather than an inline call so the one rule that matters here
// can be tested directly: the evidence clock never runs out before the report that
// cites it (§7.6 invariant ②), and it is resolved through the same helper the
// janitor's contract describes, so a *longer* `report_evidence_days` takes effect.
func (r *reportSectionRun) evidenceExpiry(now time.Time) time.Time {
	return reportExpiry(now, r.svc.ReportRetentionFor(r.ctx, r.tenantID).EffectiveReportEvidenceTTL(), reportDefaultTTLDays)
}

func reportEvidenceRows(
	evidence []reportEvidenceItem, cited map[int]bool, tenantID string,
	expiresAt, now time.Time,
) ([]model.ReportEvidenceSnapshot, error) {
	rows := make([]model.ReportEvidenceSnapshot, 0, len(cited))
	for _, item := range evidence {
		if !cited[item.Index] {
			continue
		}
		// The snapshot freezes the chunk as retrieved, not the prompt excerpt
		// (`SourceContent`): the appendix is read weeks later by someone checking the
		// claim, and a 1200-rune prompt window relabelled `truncated=false` would be a
		// copy that overstates its own completeness.
		source := item.SourceContent
		if strings.TrimSpace(source) == "" {
			source = item.Content
		}
		snippet := reportEvidenceSnippet(item.Content)
		window, strategy, ok := reportEvidenceWindow(source)
		if !ok || !strings.Contains(window, snippet) {
			// 44103: the snapshot cannot hold the text it is cited for. This is a
			// "cannot happen" (the window is a prefix, the snippet a shorter prefix),
			// so a human must look at the chunk rather than receive a silently weaker
			// appendix.
			return nil, httperr.New(500, codeReportEvidenceSnapshotRejected,
				"evidence snapshot for chunk "+item.ChunkID+" cannot contain its citation snippet")
		}
		rows = append(rows, model.ReportEvidenceSnapshot{
			ID: reportRowID("evidence", tenantID, item.ChunkID, item.ContentHash), TenantID: tenantID,
			DatasetID: item.DatasetID, DocumentID: item.DocumentID,
			ChunkID: item.ChunkID, ChunkOrd: item.Index,
			SourceContentHash:  item.ContentHash,
			SourceContentSize:  int64(len([]rune(source))),
			Content:            window,
			ContentHash:        hashReportContent(window),
			ContentSize:        int64(len([]rune(window))),
			Truncated:          strategy != model.ReportEvidenceTruncationNone,
			TruncationStrategy: strategy,
			CreatedAt:          now, ExpiresAt: expiresAt,
		})
	}
	return rows, nil
}

// reportSectionDependencies decodes `depends_on_sections_json`, tolerating the
// empty column (a template section without dependencies).
func reportSectionDependencies(section *model.ReportSection) []string {
	raw := strings.TrimSpace(section.DependsOnSectionsJSON)
	if raw == "" {
		return nil
	}
	var dependencies []string
	if err := json.Unmarshal([]byte(raw), &dependencies); err != nil {
		return nil
	}
	return dependencies
}

// reportSectionIsTerminal mirrors the finalize derivation's definition of "written":
// these are the statuses a later slice must not touch.
// reportActiveSections drops superseded rows from a revision's section list so
// every status walk treats them as finished history. The newest row per ord is
// kept (ListReportSections orders by section_version ASC, so the last row of a
// repeated ord is its live version).
func reportActiveSections(sections []model.ReportSection) []model.ReportSection {
	active := sections[:0]
	for _, section := range sections {
		if section.Status == model.ReportSectionSuperseded {
			continue
		}
		active = append(active, section)
	}
	return active
}

func reportSectionIsTerminal(status string) bool {
	switch status {
	case model.ReportSectionSucceeded, model.ReportSectionFailed, model.ReportSectionSkipped,
		// A superseded row is immutable history: it was replaced by a newer
		// section_version and must never be re-run, skipped again or counted as
		// pending (doc/288 §7.10).
		model.ReportSectionSuperseded:
		return true
	}
	return false
}

// reportSliceLease is the slice's expected lifetime: the template's
// `slice_max_seconds`, which validation keeps at or below the runtime job timeout.
func reportSliceLease(limits ReportTemplateLimits) time.Duration {
	seconds := limits.SliceMaxSeconds
	if seconds <= 0 {
		seconds = reportDefaultSliceSeconds
	}
	return time.Duration(seconds) * time.Second
}

// reportSliceShouldYield implements the 0.8× checkpoint (§7.9): the slice stops
// starting new sections while 20% of its lease is left, so the section in flight
// can finish and the checkpoint can be written.
func reportSliceShouldYield(ctx context.Context, lease time.Duration) bool {
	deadline, ok := ctx.Deadline()
	if !ok || lease <= 0 {
		return false
	}
	margin := lease / 5
	if margin < reportSliceYieldFloor {
		margin = reportSliceYieldFloor
	}
	return time.Until(deadline) <= margin
}

// reportProviderAnswered distinguishes "the provider refused" from "we do not know".
//
// A business refusal (4xx with an envelope) means no tokens were produced, so the
// reservation is refunded. Anything else — transport, timeout, protocol, 5xx, 429 —
// may have been billed upstream and becomes a reconciliation item instead.
func reportProviderAnswered(err error) bool {
	var upstream *ragflow.Error
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.Type == ragflow.ErrorTypeBusiness {
		return true
	}
	return upstream.HTTPStatus >= 400 && upstream.HTTPStatus < 500 && upstream.HTTPStatus != 429
}

// reportCodeOf reads an `httperr` code out of an error chain.
func reportCodeOf(err error) int {
	var httpErr *httperr.Error
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}
	return 0
}

// reportCodeString renders the error code for an event payload, or "" when the
// failure was not an API-level refusal.
func reportCodeString(err error) string {
	if code := reportCodeOf(err); code > 0 {
		return fmt.Sprintf("%d", code)
	}
	return ""
}

// reportErrorReason is the disclosure text of one failed section. It is the
// upstream message truncated to something a reader can hold in their head; the full
// text stays in the runtime job's log.
func reportErrorReason(err error) string {
	if err == nil {
		return ""
	}
	return truncateKnowledgeText(err.Error(), reportEvidenceSnippetChars)
}

// reportRetrievalTopK is the per-section chunk count.
func reportRetrievalTopK(declared int) int {
	if declared > 0 {
		return declared
	}
	return reportRetrievalPageSize
}

// reportChunkScore reads the retrieval score the engine reported, whichever of its
// two names it used. Zero is a legitimate score, so absence and zero are not
// distinguished — the column is a ranking hint, not a decision.
func reportChunkScore(chunk map[string]interface{}) float64 {
	if value, ok := chunk["similarity"].(float64); ok {
		return value
	}
	if value, ok := chunk["vector_similarity"].(float64); ok {
		return value
	}
	return 0
}

// reportChunkScoreType names the signal behind the score the engine reported.
//
// The engine answers all three keys (`similarity`, `vector_similarity`,
// `term_similarity`), so the components are what tell the cases apart: both present
// means the number is a fused ranking, only one present means the other contributed
// nothing. A lone `similarity` is still the engine's fused score — labelling it
// `vector` would claim a comparability with a raw cosine score that the number does
// not have (§7.5: the three values are kept apart precisely because the numbers are
// not comparable).
func reportChunkScoreType(chunk map[string]interface{}) string {
	_, vector := chunk["vector_similarity"].(float64)
	_, term := chunk["term_similarity"].(float64)
	switch {
	case vector && term:
		return model.ReportRetrievalScoreHybrid
	case term:
		return model.ReportRetrievalScoreTerm
	case vector:
		return model.ReportRetrievalScoreVector
	default:
		return model.ReportRetrievalScoreHybrid
	}
}

// reportScoreTypeOrHybrid keeps the closed set closed: a citation row must never
// carry an empty or unknown type, because the appendix prints it and a blank there
// reads as "unit unknown" rather than "unrecorded".
func reportScoreTypeOrHybrid(kind string) string {
	switch kind {
	case model.ReportRetrievalScoreVector, model.ReportRetrievalScoreTerm,
		model.ReportRetrievalScoreHybrid:
		return kind
	default:
		return model.ReportRetrievalScoreHybrid
	}
}

// sectionMaxWords is the section's length budget from the pinned template.
func sectionMaxWords(payload *ReportTemplatePayload, sectionKey string) int {
	for _, declared := range payload.Sections {
		if declared.Key != sectionKey {
			continue
		}
		if declared.MaxWords > 0 {
			return declared.MaxWords
		}
		break
	}
	return reportSectionWordDefault
}

// reportRowIDMaxLen is the widest id this helper may produce: the snapshot and
// citation tables declare `varchar(32)` on both SQLite and PostgreSQL, and
// PostgreSQL enforces it (`22001 value too long for type character varying(32)`).
//
// The budget is real, not theoretical: `"evidence" + "-" + 24 hex` is 33\, and a
// live run lost the section that way — the commit transaction aborted while the
// row stayed `writing`, which then read as work in flight forever.
const reportRowIDMaxLen = 32

// reportRowID derives a deterministic row id from its identity, so a retried write
// collides instead of duplicating.
func reportRowID(prefix string, parts ...string) string {
	digest := sha256Hex(strings.Join(parts, "\x00"))
	keep := reportRowIDMaxLen - len(prefix) - 1
	if keep > len(digest) {
		keep = len(digest)
	}
	if keep < 1 {
		keep = 1
	}
	return prefix + "-" + digest[:keep]
}

// reportClaimRows shapes the extracted claims into rows of the new section version.
//
// The ids are derived from `(section_id, ord)`, which is exactly the unique index of
// the table: a commit replayed after a transport failure would try to insert the
// same ids and fail loudly instead of writing a second claim set for the same body.
func reportClaimRows(
	drafts []reportClaimDraft, tenantID, revisionID string, section *model.ReportSection,
) []model.ReportClaim {
	rows := make([]model.ReportClaim, 0, len(drafts))
	for _, draft := range drafts {
		rows = append(rows, model.ReportClaim{
			ID:         reportRowID("claim", section.ID, strconv.Itoa(draft.Ord)),
			TenantID:   tenantID,
			RevisionID: revisionID,
			SectionID:  section.ID, SectionOrd: section.Ord,
			SectionVersion: section.SectionVersion,
			Ord:            draft.Ord, ClaimText: draft.Text,
			ClaimType: draft.Type, SupportStatus: draft.Support,
		})
	}
	return rows
}

// reportEvidenceSignalKinds lists the signal kinds for the event payload. The
// payload is what an SSE reader shows while the report is still being written, and
// "this section's sources disagree" belongs there: it is the disclosure the user can
// still act on, unlike the finished appendix.
func reportEvidenceSignalKinds(signals []reportEvidenceSignal) []string {
	kinds := make([]string, 0, len(signals))
	for _, signal := range signals {
		if !containsString(kinds, signal.Kind) {
			kinds = append(kinds, signal.Kind)
		}
	}
	return kinds
}
