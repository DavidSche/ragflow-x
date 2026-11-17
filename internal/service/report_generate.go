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
	"strings"
	"time"

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
			ExpiresAt: reportExpiry(now, s.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays),
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
		lease: reportSliceLease(templatePayload.Limits),
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
}

// execute drives the section loop.
func (r *reportSectionRun) execute() error {
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
			return r.requestFinalization(r.stage)
		}
		payload := ReportSlicePayload{
			ReportJobID: current.ID, ReportID: current.ReportID, RevisionID: current.RevisionID,
			AttemptSeq: r.attemptSeq, SliceSeq: r.sliceSeq,
		}
		if !payload.owns(current, r.runtimeJobID) || current.Status != model.ReportJobRunning {
			return nil
		}
		r.business = current

		section, gapReason := r.nextSection()
		if section == nil {
			if gapReason != "" {
				// Nothing writable is left, but something is still open (a
				// dependency failed, or the section exhausted its attempts).
				// Skipping is the honest close: it is disclosed in `gap_json`
				// instead of pretending the work is still coming.
				if err := r.skipRemaining(gapReason, model.ReportQualityFailed); err != nil {
					return err
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
		if err := r.skipRemaining(model.ReportGapBudgetExhausted, model.ReportQualityFailed); err != nil {
			return err
		}
	}
	if r.allTerminal() {
		return r.requestFinalization(model.ReportStageAssembling)
	}
	// Sections remain and the lease is fine: yield so a fresh slice continues.
	return r.checkpoint()
}

// requestFinalization hands the job to the finalize Outbox path.
func (r *reportSectionRun) requestFinalization(stage string) error {
	applied, err := r.svc.BeginReportFinalization(
		r.ctx, r.tenantID, r.business.ID, r.attemptSeq, r.sliceSeq, r.runtimeJobID)
	if err != nil {
		return err
	}
	logger.Info("report slice requested finalization",
		"report_job_id", r.business.ID, "stage", stage, "applied", applied)
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
// settle → commit.
//
// `terminal` says whether the section reached a terminal state (so the loop may
// continue); `blocked` says the budget gate refused, which ends the loop.
func (r *reportSectionRun) writeSection(section *model.ReportSection) (bool, bool, error) {
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
			return false, true, nil
		}
		return false, false, err
	}
	evidence, truncated, retrieveErr := r.retrieve(section)
	if retrieveErr != nil {
		// Nothing was sent to the model, so the reservation is refunded rather than
		// written down as unknown spend.
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, err
		}
		return r.closeSection(section, model.ReportSectionFailed, retrieveErr, now)
	}
	if truncated {
		r.truncated++
	}
	instruction := r.sectionInstruction(section.SectionKey)
	prompt := reportSectionPrompt(r.report.Question, *section, instruction,
		sectionMaxWords(r.template, section.SectionKey), r.priorSections(section), evidence)

	applied, err := r.svc.Store.BeginReportSection(r.ctx, repository.ReportSectionStart{
		TenantID: r.tenantID, JobID: r.business.ID, RevisionID: r.revision.ID,
		SectionID: section.ID, PreviousAttempt: section.Attempt, Now: now,
		PromptHash: hashReportPrompt(prompt), Stage: model.ReportStageSectionWriting,
		Event: r.event(reportEventSectionStarted, section, map[string]interface{}{
			"attempt": section.Attempt + 1, "evidence": len(evidence), "truncated": truncated,
		}, now),
	})
	if err != nil {
		return false, false, err
	}
	if !applied {
		// Another slice already owns this section: release and stop, its outcome is
		// not ours to write.
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, err
		}
		return false, false, nil
	}
	section.Attempt++
	section.Status = model.ReportSectionWriting

	raw, callErr := r.svc.RAGFlow.ChatToModel(r.ctx,
		r.snapshot.Model.Provider, r.snapshot.Model.Instance, r.snapshot.Model.Name,
		prompt, false, false)
	if callErr != nil {
		return r.handleCallFailure(section, charge, evidence, callErr)
	}
	// The raw endpoint answers with a string: the shape is the caller's problem
	// (see `decodeReportModelReply`), and a body we cannot read is a failed section,
	// not a broken job.
	reply, decodeErr := decodeReportModelReply(raw)
	if decodeErr != nil {
		return r.handleCallFailure(section, charge, evidence, decodeErr)
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
		return false, false, err
	}
	if !settled {
		return false, false, fmt.Errorf("report charge %s could not be settled", charge.LedgerKey)
	}
	return r.commitSection(section, reply, evidence, now)
}

// handleCallFailure ends one section after a provider error.
//
// The billing story depends on whether the provider answered at all: a 4xx
// business refusal means nothing was generated and the reservation is refunded,
// while a transport failure or a timeout means we genuinely do not know, and the
// reservation becomes an orphan that reconciliation will account for (§8.6).
func (r *reportSectionRun) handleCallFailure(
	section *model.ReportSection, charge *model.ReportCharge,
	evidence []reportEvidenceItem, callErr error,
) (bool, bool, error) {
	now := time.Now().UTC()
	if reportProviderAnswered(callErr) {
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			return false, false, err
		}
	} else if _, err := r.svc.Store.OrphanReportCharge(r.ctx, repository.ReportChargeCandidate{
		ID: charge.ID, TenantID: r.tenantID, JobID: r.business.ID, LedgerKey: charge.LedgerKey,
		ReservedTokens: charge.ReservedTokens, Reason: model.ReportReconcileProviderUnconfirmed,
	}, now); err != nil {
		return false, false, err
	}
	_ = evidence
	return r.closeSection(section, model.ReportSectionFailed, callErr, now)
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
	evidence []reportEvidenceItem, now time.Time,
) (bool, bool, error) {
	content := reportNormalizeContent(reply.Text)
	if content == "" {
		return r.closeSection(section, model.ReportSectionFailed,
			errors.New("model returned an empty section"), now)
	}
	cited := reportCitedIndexes(content, len(evidence))
	evidenceStatus := reportEvidenceStatus(
		section.RequiresEvidence, len(evidence), len(cited), r.template.Retrieval.MinEvidence)
	qualityStatus := model.ReportQualityNotChecked
	if section.RequiresEvidence && evidenceStatus == model.ReportEvidenceUnsupported {
		qualityStatus = model.ReportQualityNeedsReview
	}
	citations := make([]model.ReportCitation, 0, len(cited))
	for _, item := range evidence {
		if !cited[item.Index] {
			continue
		}
		citations = append(citations, r.citation(section, item, now))
	}
	evidenceRows := reportEvidenceRows(evidence, cited, r.tenantID,
		reportExpiry(now, r.svc.currentRetentionPolicy().ReportDays, reportDefaultTTLDays), now)

	commit := repository.ReportSectionCommit{
		TenantID: r.tenantID, JobID: r.business.ID, Now: now,
		Section: model.ReportSection{
			ID: section.ID, RevisionID: r.revision.ID, Attempt: section.Attempt,
			ContentMD: content, ContentHash: hashReportContent(content),
			TokensIn: reply.TokensIn, TokensOut: reply.TokensOut,
			CitationCount: len(citations), ClaimCount: 0,
			EvidenceStatus: evidenceStatus, QualityStatus: qualityStatus,
		},
		Citations: citations, Evidence: evidenceRows, Stage: model.ReportStageSectionWriting,
		Event: r.event(reportEventSectionSucceeded, section, map[string]interface{}{
			"citation_count": len(citations), "evidence_status": evidenceStatus,
			"quality_status": qualityStatus, "tokens_in": reply.TokensIn,
			"tokens_out": reply.TokensOut, "usage_estimated": !reply.UsageReported,
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
	section.Status = model.ReportSectionSucceeded
	section.ContentMD = content
	section.CitationCount = len(citations)
	section.EvidenceStatus = evidenceStatus
	section.QualityStatus = qualityStatus
	logger.Info("report section written",
		"report_job_id", r.business.ID, "ord", section.Ord, "section_key", section.SectionKey,
		"citations", len(citations), "evidence_status", evidenceStatus)
	return true, false, nil
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
	})
	if err != nil {
		return false, false, err
	}
	if applied {
		section.Status = status
		section.QualityStatus = qualityStatus
		section.EvidenceStatus = evidenceStatus
	}
	logger.Warn("report section closed",
		"report_job_id", r.business.ID, "ord", section.Ord, "status", status, "error", cause)
	return true, false, nil
}

// skipRemaining closes every section that never started.
//
// `gapReason` is the disclosure the reader sees: `budget_exhausted` when the
// envelope ran out, `skipped` when a dependency failed first. The two are not
// interchangeable — one says "buy more budget", the other says "fix the source".
func (r *reportSectionRun) skipRemaining(gapReason, qualityStatus string) error {
	pending := make([]string, 0, len(r.sections))
	events := make([]*model.ReportJobEvent, 0, len(r.sections))
	now := time.Now().UTC()
	for index := range r.sections {
		section := &r.sections[index]
		if reportSectionIsTerminal(section.Status) {
			continue
		}
		pending = append(pending, section.ID)
		events = append(events, r.event(reportEventSectionFailed, section, map[string]interface{}{
			"ord": section.Ord, "section_key": section.SectionKey, "title": section.Title,
			"reason": gapReason, "error_code": skipErrorCode(gapReason),
		}, now))
		section.Status = model.ReportSectionSkipped
		section.QualityStatus = qualityStatus
	}
	if len(pending) == 0 {
		return nil
	}
	_, err := r.svc.Store.SkipReportSections(r.ctx, repository.ReportSectionSkip{
		TenantID: r.tenantID, JobID: r.business.ID, RevisionID: r.revision.ID,
		SectionIDs: pending, QualityStatus: qualityStatus,
		EvidenceStatus: model.ReportEvidenceNotRequired, Now: now,
		Stage: model.ReportStageSectionWriting, Events: events,
	})
	return err
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
	engineDatasetIDs := make([]string, 0, len(r.snapshot.Datasets))
	for _, binding := range r.snapshot.Datasets {
		engineDatasetIDs = append(engineDatasetIDs, binding.EngineDatasetID)
	}
	req := ragflow.RetrieveDatasetsRequest{
		DatasetIDs: engineDatasetIDs, Question: section.Query,
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
		documentID := stringFromAny(chunk["document_id"])
		if documentID == "" {
			documentID = stringFromAny(chunk["doc_id"])
		}
		engineDataset := stringFromAny(chunk["dataset_id"])
		localDataset := r.localDatasetID(engineDataset)
		items = append(items, reportEvidenceItem{
			ChunkID:       stringFromAny(chunk["chunk_id"]),
			DatasetID:     localDataset,
			DocumentID:    documentID,
			EngineDataset: engineDataset,
			DatasetLabel:  r.datasetLabel(engineDataset),
			DocumentLabel: firstNonEmptyString(
				stringFromAny(chunk["document_name"]), stringFromAny(chunk["doc_name"]), documentID),
			Content:     content,
			ContentHash: sha256Hex(strings.TrimSpace(content)),
			Score:       reportChunkScore(chunk),
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
	return model.ReportCitation{
		ID:         reportRowID("citation", section.ID, fmt.Sprint(item.Index)),
		TenantID:   r.tenantID,
		RevisionID: r.revision.ID, SectionID: section.ID, SectionOrd: section.Ord,
		SectionVersion: section.SectionVersion, Ord: item.Index,
		SourceDatasetID:       r.localDatasetID(item.EngineDataset),
		SourceEngineDatasetID: item.EngineDataset,
		SourceDocumentID:      item.DocumentID,
		SourceChunkID:         item.ChunkID,
		SourceContentHash:     item.ContentHash,
		ReferencedAt:          now,
		ReferencedBy:          r.report.OwnerUserID,
		RetrievalScore:        item.Score,
		RetrievalScoreType:    "similarity",
		Snippet:               truncateKnowledgeText(item.Content, reportEvidenceSnippetChars),
		UsedInText:            true,
	}
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
			ExpiresAt: reportExpiry(now, r.svc.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays),
		}
	}
	return nil
}

// reportEvidenceRows builds the evidence snapshots of the cited chunks.
//
// Only cited chunks are snapshotted: an uncited chunk is retrieval noise, and
// storing it would inflate the evidence table with text nothing references. The
// row keeps both hashes on purpose — `source_content_hash` identifies the upstream
// chunk, `content_hash` identifies the (possibly truncated) copy we stored.
func reportEvidenceRows(
	evidence []reportEvidenceItem, cited map[int]bool, tenantID string,
	expiresAt, now time.Time,
) []model.ReportEvidenceSnapshot {
	rows := make([]model.ReportEvidenceSnapshot, 0, len(cited))
	for _, item := range evidence {
		if !cited[item.Index] {
			continue
		}
		rows = append(rows, model.ReportEvidenceSnapshot{
			ID: reportRowID("evidence", tenantID, item.ChunkID, item.ContentHash), TenantID: tenantID,
			DatasetID: item.DatasetID, DocumentID: item.DocumentID,
			ChunkID: item.ChunkID, ChunkOrd: item.Index,
			SourceContentHash: item.ContentHash,
			SourceContentSize: int64(len([]rune(item.Content))),
			Content:           item.Content,
			ContentHash:       hashReportContent(item.Content),
			ContentSize:       int64(len([]rune(item.Content))),
			CreatedAt:         now, ExpiresAt: expiresAt,
		})
	}
	return rows
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

// reportRowID derives a deterministic row id from its identity, so a retried write
// collides instead of duplicating.
func reportRowID(prefix string, parts ...string) string {
	return prefix + "-" + sha256Hex(strings.Join(parts, "\x00"))[:24]
}
