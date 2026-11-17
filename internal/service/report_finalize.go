package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report finalize (doc/288 §8.2 AfterSettle / §7.10 publish matrix / A34–A46).
//
// The whole file exists because of one rule: **a terminal state may only be
// written after the runtime slice has settled, and it must be written exactly
// once**. Splitting that into "the runner writes the status" plus "someone else
// publishes the revision" produces the two failure modes the design review
// named: a report whose status says `succeeded` but whose content is not
// published (I2'), and a report stuck in `finalizing` with no durable trace that
// it needs finishing.
//
// So the flow is three steps and each step has one owner:
//
//	Run()           → BeginReportFinalization: status=finalizing + `job.finalizing`
//	                  + the `report_job_finalize` outbox row, all in ONE transaction.
//	outbox consumer → FinalizeReportJob: the gates, then the derived decision.
//	repository      → ApplyReportFinalization: terminal status + revision publish +
//	                  ledger settlement + terminal event, all in ONE transaction.
//
// Three gates guard the terminal write (§8.4 "执行完成"):
//
//  1. the request refers to the *current* runtime slice (`runtime_job_id` ==
//     `current_runtime_job_id`) — a superseded slice must not close the job;
//  2. the runtime slice is no longer `queued`/`running` — writing a terminal
//     state while the slice still runs would let a late slice flip it back (A34);
//  3. no section is left `pending`/`writing` when the runtime slice reports
//     success — a runner is not allowed to call itself done early.
//
// What this file deliberately does NOT do: compute the report body, the artifact,
// or the per-section quality gate. Those belong to the worker slices; finalize
// reads their rows and decides what may be published.

// ReportFinalizeOutcome tells the caller what happened to a finalize request.
// The outbox consumer needs the distinction to decide between "done, mark
// published", "not yet, retry later" and "not mine, drop it".
type ReportFinalizeOutcome string

const (
	// ReportFinalizeApplied means this call wrote the terminal state.
	ReportFinalizeApplied ReportFinalizeOutcome = "applied"
	// ReportFinalizeAlreadyDone means an earlier delivery (or a reconcile pass)
	// already finalized the job. The caller treats it as success: the work is
	// done, this copy just lost the race (A45).
	ReportFinalizeAlreadyDone ReportFinalizeOutcome = "already_finalized"
	// ReportFinalizeStale means the request came from a runtime slice that no
	// longer owns the job. Dropping it is correct; the owning slice finalizes
	// (A44 ③).
	ReportFinalizeStale ReportFinalizeOutcome = "stale"
	// ReportFinalizeDeferred means a gate is not satisfied yet. Nothing was
	// written; the request must be retried (A44 ②).
	ReportFinalizeDeferred ReportFinalizeOutcome = "deferred"
	// ReportFinalizeGone means the report job no longer exists (purged by the
	// retention janitor). Nothing to finish and nothing to retry.
	ReportFinalizeGone ReportFinalizeOutcome = "gone"
)

// reportCitationCoverageFloor is the only §14.2 gate that degrades the revision
// flag. The evidence-availability and unsupported-ratio gates are banners, and
// `needs_review` is a per-section verdict; folding them into `quality_flag` would
// collapse three different statements into one.
const reportCitationCoverageFloor = 0.8

// reportFinalizePayload is the `report_job_finalize` outbox payload. It carries
// the runtime slice identity, not just the job id: without it a delayed delivery
// from a superseded slice could finalize a job that a newer slice is still
// working on (§8.2 "过期 slice 自弃").
type reportFinalizePayload struct {
	ReportJobID  string `json:"report_job_id"`
	RuntimeJobID string `json:"runtime_job_id"`
	AttemptSeq   int    `json:"attempt_seq"`
	SliceSeq     int    `json:"slice_seq"`
}

// reportGapEntry is one row of `rgx_report_revision.gap_json` — the disclosure a
// `partial` report has to carry (§7.10: 缺失节 key + 原因 + 影响范围, §12: 错误码).
// It is persisted rather than derived at read time, so the banner, the DOCX
// integrity statement and the `job.partial` payload can never disagree.
type reportGapEntry struct {
	SectionKey string `json:"section_key"`
	Ord        int    `json:"ord"`
	Title      string `json:"title,omitempty"`
	Reason     string `json:"reason"`
	// ErrorCode is the report-family code explaining the reason, when one exists.
	// Generic section failures have none, and inventing a code would make the
	// closed set lie.
	ErrorCode string `json:"error_code,omitempty"`
	Impact    string `json:"impact"`
}

// reportQualityMetrics is `rgx_report_revision.metrics_json`. §14.2 calls the gate
// conclusions "可回归的产品行为，不是开发者日志", so the numbers are stored with
// their denominator: a bare 1.0 could otherwise mean "all good" or "nothing to
// check".
type reportQualityMetrics struct {
	// StructureCompleteness counts declared `section_key` values that exist as
	// rows. It is 1.0 even when a section failed, because the gap is disclosed
	// through `completeness`+`gap_json` instead (K72).
	StructureCompleteness  float64 `json:"structure_completeness"`
	CitationCoverage       float64 `json:"citation_coverage"`
	EvidenceAvailability   float64 `json:"evidence_availability"`
	UnsupportedRatio       float64 `json:"unsupported_ratio"`
	AttributionConsistency float64 `json:"attribution_consistency"`
	QualityGatePassRate    float64 `json:"quality_gate_pass_rate"`
	QualityNotChecked      int     `json:"quality_not_checked"`
	// QualityFlag is the closed set {ok, degraded} (K37).
	QualityFlag string `json:"quality_flag"`
	// EvidenceSections is `E` in §14.1 — the number of declared sections whose
	// claims need citations, i.e. the denominator of every structural ratio.
	EvidenceSections int `json:"evidence_sections"`
}

// BeginReportFinalization closes the current slice and durably asks for the
// terminal write (§8.2 AfterSettle).
//
// It is called by the runner, never by a handler: the decision to finish belongs
// to the slice that owns the job. `applied=false` means the status/attempt/slice
// guard did not match — the slice was superseded — and is not an error.
func (s *Service) BeginReportFinalization(
	ctx context.Context, tenantID, jobID string, attemptSeq, sliceSeq int, runtimeJobID string,
) (bool, error) {
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return false, err
	}
	if job == nil || job.FinalizedAt != nil || job.CurrentRuntimeJobID != runtimeJobID {
		return false, nil
	}
	now := time.Now().UTC()
	eventExpiry := reportExpiry(now, s.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays)
	sections, err := s.Store.ListReportSections(ctx, tenantID, job.RevisionID)
	if err != nil {
		return false, err
	}
	completed, failed := reportOrdBuckets(sections)
	finalizingPayload, err := json.Marshal(map[string]interface{}{
		"attempt_seq": attemptSeq, "slice_seq": sliceSeq,
		"completed_ords": completed, "failed_ords": failed,
	})
	if err != nil {
		return false, err
	}
	outboxPayload, err := json.Marshal(reportFinalizePayload{
		ReportJobID: jobID, RuntimeJobID: runtimeJobID,
		AttemptSeq: attemptSeq, SliceSeq: sliceSeq,
	})
	if err != nil {
		return false, err
	}
	return s.Store.BeginReportFinalization(ctx, repository.ReportFinalizationStart{
		TenantID: tenantID, JobID: jobID, AttemptSeq: attemptSeq, SliceSeq: sliceSeq, Now: now,
		Event: &model.ReportJobEvent{
			TenantID: tenantID, JobID: jobID, Type: reportEventJobFinalizing,
			PayloadJSON: string(finalizingPayload), CreatedAt: now, ExpiresAt: eventExpiry,
		},
		Outbox: &model.OutboxEvent{
			TenantID: tenantID, EventType: model.EventTypeReportJobFinalize,
			AggregateType: model.AggregateTypeReportJob, AggregateID: jobID,
			Payload: string(outboxPayload), OccurredAt: now,
		},
	})
}

// FinalizeReportJob applies the gates and then the derived decision. It is the
// single entry point for both the outbox consumer and a future reconcile pass,
// which makes "double trigger finalizes once" true by construction rather than by
// convention (A34/A45).
func (s *Service) FinalizeReportJob(
	ctx context.Context, tenantID, jobID, runtimeJobID string, now time.Time,
) (ReportFinalizeOutcome, error) {
	job, err := s.Store.GetReportJob(ctx, tenantID, jobID)
	if err != nil {
		return "", err
	}
	if job == nil {
		return ReportFinalizeGone, nil
	}
	if job.FinalizedAt != nil {
		return ReportFinalizeAlreadyDone, nil
	}
	// Gate 1: only the slice that currently owns the job may finish it.
	if job.CurrentRuntimeJobID != runtimeJobID {
		return ReportFinalizeStale, nil
	}
	if job.Status != model.ReportJobFinalizing {
		// `Begin` writes `finalizing` in the same transaction as the outbox row, so
		// this is only reachable for a hand-written event or a re-driven job. It is
		// not ours to decide.
		return ReportFinalizeDeferred, nil
	}
	// Gate 2: the runtime slice must have settled. Writing a terminal state while
	// its `rgx_job` is still running is exactly the "终态 + running" window A34
	// forbids.
	//
	// An empty runtime id is not a missing slice: a job cancelled before its first
	// dispatch never got one, and there is nothing to wait for. Requiring a row
	// here would make such a report uncancellable, which is worse than the state it
	// protects against.
	runtimeStatus := ""
	if runtimeJobID != "" {
		runtimeJob, err := s.Store.GetJob(ctx, tenantID, runtimeJobID)
		if err != nil {
			return "", err
		}
		if runtimeJob == nil {
			// The runtime row is gone while the business job still points at it.
			// Retrying is the honest answer: finalizing on a slice we cannot inspect
			// would be guessing. The failure stays visible in the outbox projection.
			return "", fmt.Errorf("report finalize: runtime job %s not found", runtimeJobID)
		}
		if !reportRuntimeSettled(runtimeJob.Status) {
			return ReportFinalizeDeferred, nil
		}
		runtimeStatus = runtimeJob.Status
	}
	revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, job.RevisionID)
	if err != nil {
		return "", err
	}
	if revision == nil {
		// Report purged between the reads: nothing to publish, nothing to write.
		return ReportFinalizeGone, nil
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return "", err
	}
	templateKeys, evidenceKeys, err := s.reportPinnedTemplateSections(ctx, tenantID, revision)
	if err != nil {
		return "", err
	}
	decision := deriveReportFinalDecision(reportFinalizeFacts{
		Job: job, RuntimeStatus: runtimeStatus, Sections: sections,
		TemplateKeys: templateKeys, EvidenceKeys: evidenceKeys,
	})
	if decision.Deferred {
		// Gate 3: the slice claims success but sections are still `pending`/
		// `writing`. Finalizing now would either invent `failed` for work in
		// flight or publish holes; waiting is the only answer that does neither.
		logger.Warn("report finalize deferred: sections still in progress",
			"report_job_id", jobID, "runtime_job_id", runtimeJobID,
			"pending", decision.Pending, "writing", decision.Writing)
		return ReportFinalizeDeferred, nil
	}
	gapJSON, err := json.Marshal(decision.Gap)
	if err != nil {
		return "", err
	}
	metricsJSON, err := json.Marshal(decision.Metrics)
	if err != nil {
		return "", err
	}
	event, err := reportTerminalEvent(tenantID, job, decision, string(gapJSON), now,
		reportExpiry(now, s.currentRetentionPolicy().ReportEventDays, reportDefaultEventDays))
	if err != nil {
		return "", err
	}
	applied, err := s.Store.ApplyReportFinalization(ctx, repository.ReportFinalizationPlan{
		TenantID: tenantID, JobID: jobID, ReportID: job.ReportID, RevisionID: job.RevisionID,
		Status: decision.Status, Completeness: decision.Completeness,
		GapJSON: string(gapJSON), ErrorCode: decision.ErrorCode,
		Publish: decision.Publish, MetricsJSON: string(metricsJSON),
		Now: now, Event: event,
	})
	if err != nil {
		return "", err
	}
	if !applied {
		return ReportFinalizeAlreadyDone, nil
	}
	logger.Info("report job finalized",
		"report_job_id", jobID, "status", decision.Status,
		"completeness", decision.Completeness, "published", decision.Publish,
		"gaps", len(decision.Gap), "structure_completeness", decision.Metrics.StructureCompleteness,
	)
	return ReportFinalizeApplied, nil
}

// reportFinalizeFacts is everything the derivation may look at. It is a struct
// rather than a long parameter list so the pure function below can be tested
// without a database.
type reportFinalizeFacts struct {
	Job           *model.ReportJob
	RuntimeStatus string
	Sections      []model.ReportSection
	// TemplateKeys is the ordered list of `section_key` the pinned template
	// declares; EvidenceKeys marks the ones whose claims need citations (`E`).
	TemplateKeys []string
	EvidenceKeys map[string]bool
}

// reportFinalDecision is the outcome of the derivation, still pure: no clock, no
// ids, no database.
type reportFinalDecision struct {
	Status       string
	Publish      bool
	Completeness string
	ErrorCode    string
	Gap          []reportGapEntry
	// MissingKeys lists declared `section_key` values with no row at all. It is
	// the structural failure detail (§14.2 "不静默补节") and it travels in the
	// `job.failed` payload rather than in `gap_json`, because a revision that is
	// not published has no banner to attach a gap list to.
	MissingKeys []string
	Metrics     reportQualityMetrics
	FailedOrds  []int
	SkippedOrds []int
	// CompletedOrds is `job.cancelled`'s disclosure: a cancel that keeps finished
	// work must say which sections it kept (§8.8).
	CompletedOrds []int
	// Deferred is set when a gate inside the derivation fails; `Pending`/`Writing`
	// say why, so the caller can log it instead of guessing.
	Deferred bool
	Pending  int
	Writing  int
}

// deriveReportFinalDecision implements §7.10's four-row publish matrix in the
// documented priority order: 取消 > 失败 > partial > succeeded. Every branch is
// reachable from a fact in the database, which keeps `partial` from being dead
// code (K72) and keeps a failed section from being mistaken for a failed report
// (§14.2 口径).
func deriveReportFinalDecision(facts reportFinalizeFacts) reportFinalDecision {
	// Both lists start non-nil: `gap_json` is a JSON array contract, and a nil
	// slice would marshal to `null` and force every reader to special-case it.
	decision := reportFinalDecision{
		Gap: []reportGapEntry{}, MissingKeys: []string{},
		FailedOrds: []int{}, SkippedOrds: []int{}, CompletedOrds: []int{},
	}
	present := make(map[string]model.ReportSection, len(facts.Sections))
	pending, writing := 0, 0
	succeeded, failed, skipped := 0, 0, 0
	for _, section := range facts.Sections {
		if section.Status == model.ReportSectionSuperseded {
			// A superseded row is immutable history (§7.10). It must not shadow
			// the live row in `present` (keyed by section_key) nor feed any
			// tally, even though `ord ASC, section_version ASC` happening to put
			// the live row last would usually win anyway — this makes the rule
			// explicit instead of ordering-dependent.
			continue
		}
		present[section.SectionKey] = section
		switch section.Status {
		case model.ReportSectionSucceeded:
			succeeded++
			decision.CompletedOrds = append(decision.CompletedOrds, section.Ord)
		case model.ReportSectionFailed:
			failed++
			decision.FailedOrds = append(decision.FailedOrds, section.Ord)
		case model.ReportSectionSkipped:
			skipped++
			decision.SkippedOrds = append(decision.SkippedOrds, section.Ord)
		case model.ReportSectionPending:
			pending++
		case model.ReportSectionWriting:
			writing++
		}
	}
	decision.Pending, decision.Writing = pending, writing
	for _, key := range facts.TemplateKeys {
		if _, ok := present[key]; !ok {
			decision.MissingKeys = append(decision.MissingKeys, key)
		}
	}
	decision.Metrics = deriveReportQualityMetrics(facts, present, len(decision.MissingKeys))

	// A slice that reports success while a section is still `pending`/`writing`
	// never wrote it and never will: deferring is the only answer that neither
	// invents an outcome nor publishes a hole. A slice that reports failure is
	// different — the unwritten sections are simply unreachable, and `skipped`/
	// `failed` wording describes what happened.
	//
	// A cancel request is the exception: the slice stopped *because* it was told
	// to, so the unwritten sections are not "still in progress", they are
	// unreachable. Deferring there would loop forever — every retry sees the same
	// pending rows and the same finished slice.
	if (pending > 0 || writing > 0) && facts.RuntimeStatus == model.JobStatusSucceeded &&
		facts.Job.CancelRequestedAt == nil {
		decision.Deferred = true
		return decision
	}

	// Every non-succeeded section becomes a disclosed gap. A gap on an existing
	// row is what makes `partial` publishable instead of a silent omission.
	for _, section := range facts.Sections {
		switch section.Status {
		case model.ReportSectionFailed:
			decision.Gap = append(decision.Gap, reportGapEntry{
				SectionKey: section.SectionKey, Ord: section.Ord, Title: section.Title,
				Reason: model.ReportGapFailed, Impact: model.ReportGapImpactSectionMissing,
			})
		case model.ReportSectionSkipped:
			reason, code := model.ReportGapSkipped, ""
			if section.QualityStatus == model.ReportQualityFailed {
				// The budget bail-out marks the row `skipped` + `quality_status=failed`
				// (the runner has no other column to record "why"), and §7.10 names
				// `budget_exhausted` for exactly that case.
				reason, code = model.ReportGapBudgetExhausted, strconv.Itoa(codeReportBudgetExhausted)
			}
			decision.Gap = append(decision.Gap, reportGapEntry{
				SectionKey: section.SectionKey, Ord: section.Ord, Title: section.Title,
				Reason: reason, ErrorCode: code, Impact: model.ReportGapImpactSectionMissing,
			})
		case model.ReportSectionPending, model.ReportSectionWriting:
			// Only reachable when the slice did not succeed: the runner died mid
			// section, and the honest description is "not written".
			decision.Gap = append(decision.Gap, reportGapEntry{
				SectionKey: section.SectionKey, Ord: section.Ord, Title: section.Title,
				Reason: model.ReportGapFailed, Impact: model.ReportGapImpactSectionMissing,
			})
		}
	}

	structureOK := len(decision.MissingKeys) == 0
	contentComplete := failed == 0 && skipped == 0 && pending == 0 && writing == 0

	// Priority 1: cancellation wins. The one exception §7.10 allows is a report
	// whose content is already complete and structurally valid — throwing that
	// away would discard finished work, so it is published in full while the job
	// keeps its honest `cancelled` status.
	if facts.Job.CancelRequestedAt != nil {
		decision.Status = model.ReportJobCancelled
		if structureOK && contentComplete && succeeded > 0 {
			decision.Publish = true
			decision.Completeness = model.ReportCompletenessFull
		}
		return decision
	}
	// Priority 2: structural failure — not publishable at all (no silent patching).
	if !structureOK {
		decision.Status = model.ReportJobFailed
		decision.ErrorCode = strconv.Itoa(codeReportOutlineInvalid)
		return decision
	}
	// Priority 3: the run produced nothing.
	if succeeded == 0 {
		decision.Status = model.ReportJobFailed
		decision.ErrorCode = strconv.Itoa(codeReportOutlineInvalid)
		return decision
	}
	// Priority 4: structure fine, content has holes → publishable `partial` with a
	// mandatory disclosure.
	if !contentComplete {
		decision.Status = model.ReportJobPartial
		decision.Publish = true
		decision.Completeness = model.ReportCompletenessPartial
		decision.ErrorCode = strconv.Itoa(codeReportBudgetExhausted)
		return decision
	}
	decision.Status = model.ReportJobSucceeded
	decision.Publish = true
	decision.Completeness = model.ReportCompletenessFull
	return decision
}

// deriveReportQualityMetrics computes §14.1 with the two denominators the design
// review separated (K30): structural ratios over `E` (declared evidence sections),
// execution ratios over the sections that actually ran.
func deriveReportQualityMetrics(
	facts reportFinalizeFacts, present map[string]model.ReportSection, missingCount int,
) reportQualityMetrics {
	total := len(facts.TemplateKeys)
	metrics := reportQualityMetrics{QualityFlag: model.ReportQualityFlagOK}
	if total > 0 {
		metrics.StructureCompleteness = roundRatio(float64(total-missingCount) / float64(total))
	}
	if metrics.StructureCompleteness < 1 {
		// The ratios below only mean something for a structurally complete
		// revision: reporting "citation coverage 0.0" for a report that failed
		// structure would look like an evidence problem instead of a structure one.
		return metrics
	}
	evidenceTotal := 0
	withCitation, supported, unsupported := 0, 0, 0
	attributionDenominator, attributionOK := 0, 0
	gateDenominator, gatePassed := 0, 0
	for key, requiresEvidence := range facts.EvidenceKeys {
		if !requiresEvidence {
			continue
		}
		evidenceTotal++
		section, ok := present[key]
		if !ok {
			continue
		}
		if section.CitationCount >= 1 {
			withCitation++
		}
		switch section.EvidenceStatus {
		case model.ReportEvidenceSupported, model.ReportEvidencePartiallySupported:
			supported++
		case model.ReportEvidenceUnsupported, model.ReportEvidenceOutdated, model.ReportEvidenceConflicting:
			unsupported++
		}
		if section.Status != model.ReportSectionSucceeded {
			continue
		}
		gateDenominator++
		switch section.QualityStatus {
		case model.ReportQualityPassed:
			gatePassed++
		case model.ReportQualityNotChecked:
			metrics.QualityNotChecked++
		}
		if section.CitationCount >= 1 {
			attributionDenominator++
			if section.QualityStatus != model.ReportQualityNeedsReview {
				attributionOK++
			}
		}
	}
	metrics.EvidenceSections = evidenceTotal
	if evidenceTotal > 0 {
		metrics.CitationCoverage = roundRatio(float64(withCitation) / float64(evidenceTotal))
		metrics.EvidenceAvailability = roundRatio(float64(supported) / float64(evidenceTotal))
		metrics.UnsupportedRatio = roundRatio(float64(unsupported) / float64(evidenceTotal))
	}
	// A zero denominator means "there was nothing to check", which is consistent,
	// not degraded. Store 1.0 and let `EvidenceSections` / the section rows show
	// what the denominator was — a stored 0.0 would read as a failure.
	metrics.AttributionConsistency = 1
	if attributionDenominator > 0 {
		metrics.AttributionConsistency = roundRatio(float64(attributionOK) / float64(attributionDenominator))
	}
	metrics.QualityGatePassRate = 1
	if gateDenominator > 0 {
		metrics.QualityGatePassRate = roundRatio(float64(gatePassed) / float64(gateDenominator))
	}
	// §14.2: only the citation-coverage gate degrades the revision flag.
	if metrics.CitationCoverage < reportCitationCoverageFloor {
		metrics.QualityFlag = model.ReportQualityFlagDegraded
	}
	return metrics
}

// roundRatio keeps stored ratios readable (0.6667 instead of 0.6666666666666666)
// so a metrics diff between two runs is about the report, not about float noise.
func roundRatio(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// reportOrdBuckets splits section rows into "written" and "not written" ords for
// the `job.finalizing` payload. It is a progress snapshot, not a decision.
func reportOrdBuckets(sections []model.ReportSection) (completed, failed []int) {
	for _, section := range sections {
		switch section.Status {
		case model.ReportSectionSucceeded:
			completed = append(completed, section.Ord)
		case model.ReportSectionFailed, model.ReportSectionSkipped:
			failed = append(failed, section.Ord)
		}
	}
	return completed, failed
}

// reportRuntimeSettled reports whether a runtime job has stopped running. A
// `finalizing` business job whose slice is still `running` must wait: that is the
// A34 assertion, and it is the only reason `finalizing` is not terminal.
func reportRuntimeSettled(status string) bool {
	switch status {
	case model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusCanceled:
		return true
	}
	return false
}

// reportTerminalEvent builds the terminal broadcast event. Exactly one of the four
// terminal types is emitted per job, inside the terminal transaction: an event
// committed separately could announce a state that rolled back.
func reportTerminalEvent(
	tenantID string, job *model.ReportJob, decision reportFinalDecision, gapJSON string,
	now, expiresAt time.Time,
) (*model.ReportJobEvent, error) {
	eventType := reportEventJobFailed
	switch decision.Status {
	case model.ReportJobSucceeded:
		eventType = reportEventJobSucceeded
	case model.ReportJobPartial:
		eventType = reportEventJobPartial
	case model.ReportJobCancelled:
		eventType = reportEventJobCancelled
	}
	payload := map[string]interface{}{
		"revision_id": job.RevisionID,
		"published":   decision.Publish,
	}
	if decision.Completeness != "" {
		payload["completeness"] = decision.Completeness
	}
	if decision.ErrorCode != "" {
		payload["error_code"] = decision.ErrorCode
	}
	switch eventType {
	case reportEventJobPartial:
		payload["failed_ords"] = decision.FailedOrds
		payload["skipped_ords"] = decision.SkippedOrds
		// gap_json is embedded as raw JSON, not as a string: double-encoding would
		// force every consumer to unmarshal twice and would let the two copies drift.
		payload["gap_json"] = json.RawMessage(gapJSON)
	case reportEventJobFailed:
		payload["missing_section_keys"] = decision.MissingKeys
		payload["failed_ords"] = decision.FailedOrds
	case reportEventJobCancelled:
		// Non-nil on purpose: `null` would make every consumer special-case "empty".
		payload["completed_ords"] = decision.CompletedOrds
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &model.ReportJobEvent{
		TenantID: tenantID, JobID: job.ID, Type: eventType,
		PayloadJSON: string(encoded), CreatedAt: now, ExpiresAt: expiresAt,
	}, nil
}

// reportPinnedTemplateSections loads the section keys of the template version the
// revision pinned.
//
// A missing template row is not survivable by guessing: without the declared keys
// there is no way to answer "is the structure complete?", and answering 1.0 by
// assuming "whatever we have is enough" would silently publish a truncated report.
// Templates are retired, never deleted, so a missing row is a data-integrity error
// and is returned as one.
func (s *Service) reportPinnedTemplateSections(
	ctx context.Context, tenantID string, revision *model.ReportRevision,
) ([]string, map[string]bool, error) {
	row, err := s.resolveReportTemplate(ctx, tenantID, revision.TemplateKey, int64(revision.TemplateVersion))
	if err != nil {
		return nil, nil, fmt.Errorf("report finalize: pinned template %s@%d: %w",
			revision.TemplateKey, revision.TemplateVersion, err)
	}
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(payload.Sections))
	evidence := make(map[string]bool, len(payload.Sections))
	for _, section := range payload.Sections {
		keys = append(keys, section.Key)
		evidence[section.Key] = section.RequiresEvidence
	}
	return keys, evidence, nil
}

// RecordReportExecutionSnapshot freezes the generation conditions of a revision
// exactly once and reports whether this call wrote it (§7.7 / A8 可复现).
//
// The payload is supplied by the caller (the outline step owns its schema); this
// function owns only the invariant — canonical JSON in, sha256 of that JSON out,
// never overwritten. `applied=false` is a normal outcome for a retried step.
func (s *Service) RecordReportExecutionSnapshot(
	ctx context.Context, tenantID, revisionID string, snapshot interface{},
) (bool, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(encoded)
	return s.Store.RecordReportExecutionSnapshot(
		ctx, tenantID, revisionID, string(encoded), hex.EncodeToString(sum[:]))
}

// reportOutboxHandler consumes one durable internal delivery event.
type reportOutboxHandler func(ctx context.Context, s *Service, event *model.OutboxEvent, now time.Time) error

// reportOutboxHandlers is the event_type → consumer registry (§8.2). A map, not an
// if-chain, because the startup check below must answer "is every required type
// covered?" without re-reading dispatch code.
var reportOutboxHandlers = map[string]reportOutboxHandler{
	model.EventTypeReportJobFinalize: dispatchReportJobFinalize,
}

// requiredReportOutboxEventTypes is the set that MUST have a consumer. Missing one
// turns the corresponding business state into a dead end: a `finalizing` report
// whose finish request nobody reads stays finalizing forever, and nothing would say
// so (A46 / 292 D15 "拒绝静默失效").
var requiredReportOutboxEventTypes = []string{
	model.EventTypeReportJobFinalize,
}

// MustHaveReportOutboxHandlers verifies that every required delivery type has a
// registered consumer. It returns an error (not a warning) so startup can refuse to
// run a build that would silently strand reports.
func (s *Service) MustHaveReportOutboxHandlers() error {
	var missing []string
	for _, eventType := range requiredReportOutboxEventTypes {
		if reportOutboxHandlers[eventType] == nil {
			missing = append(missing, eventType)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("report outbox consumers missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

// dispatchReportJobFinalize is the `report_job_finalize` consumer. Its job is small
// on purpose: decide whether to finish, and translate the outcome into outbox
// bookkeeping. All business derivation lives in FinalizeReportJob.
func dispatchReportJobFinalize(ctx context.Context, s *Service, event *model.OutboxEvent, now time.Time) error {
	var payload reportFinalizePayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil || payload.ReportJobID == "" {
		message := "invalid report job finalize payload"
		if err != nil {
			message = err.Error()
		}
		if retryErr := s.Store.RetryOutboxEvent(
			ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)), message,
		); retryErr != nil {
			return retryErr
		}
		return errors.New(message)
	}
	outcome, err := s.FinalizeReportJob(ctx, event.TenantID, payload.ReportJobID, payload.RuntimeJobID, now)
	if err != nil {
		// A real failure: keep the row retryable and say why. It stays visible in
		// the outbox projection instead of disappearing into a log line.
		if retryErr := s.Store.RetryOutboxEvent(
			ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)), err.Error(),
		); retryErr != nil {
			return retryErr
		}
		return err
	}
	if outcome == ReportFinalizeDeferred {
		// Deliberate yield, not a failure: the slice has not settled. Backoff keeps
		// the retry observable (`next_retry_at`, `last_error`) instead of burning the
		// lease.
		return s.Store.RetryOutboxEvent(
			ctx, event.ID, event.Attempts+1, now.Add(nextOutboxRetryDelay(event.Attempts)),
			"report finalize deferred: runtime slice has not settled",
		)
	}
	_, err = s.Store.MarkOutboxEventPublished(ctx, event.ID, now, "report_job_finalize:"+string(outcome))
	return err
}

// ---------------------------------------------------------------------------
// Reconcile (the second trigger of the terminal write)
// ---------------------------------------------------------------------------

const (
	reportFinalizeReconcileInterval   = time.Minute
	reportFinalizeReconcileBatch      = 20
	reportFinalizeReconcileStaleAfter = 30 * time.Second
)

// ProcessReportFinalizeReconciler finishes jobs that asked for a terminal write
// but never got one (doc/288 §8.2 / A35 ④).
//
// It is a safety net, not a second implementation: it calls the same
// FinalizeReportJob, so the gates and the derivation are shared. A candidate whose
// runtime slice is still running is simply deferred and retried on the next sweep —
// the sweep never forces a terminal state "because it is old".
//
// Two classes of stuck job are swept, in this order:
//
//  1. `finalizing` — the durable request exists but the write never landed;
//  2. `running`/`cancel_requested` — nobody holds the job any more: its slice
//     settled without asking (a worker whose own begin failed, exhausted its
//     runtime retries), or it was cancelled before it ever got a slice.
//
// The same sweep runs reconcile class 4 of §7.9 (stale charge reservations). It is
// independent of the two job classes above: an orphaned reservation changes no
// report state, it only stops a dead call from pinning budget forever.
//
// Class 2 is what makes a cancel on a `queued` report reach `cancelled` at all: no
// slice means no checkpoint, and a state that can only be left by a checkpoint that
// no longer exists is a dead end.
func (s *Service) ProcessReportFinalizeReconciler(ctx context.Context, limit int) error {
	now := time.Now().UTC()
	staleBefore := now.Add(-reportFinalizeReconcileStaleAfter)
	candidates, err := s.Store.ListStaleFinalizingReportJobs(ctx, staleBefore, limit)
	if err != nil {
		return err
	}
	var applied, deferred int
	var failures []error
	for _, candidate := range candidates {
		outcome, err := s.FinalizeReportJob(ctx, candidate.TenantID, candidate.JobID, candidate.RuntimeJobID, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("report job %s: %w", candidate.JobID, err))
			continue
		}
		switch outcome {
		case ReportFinalizeApplied:
			applied++
		case ReportFinalizeDeferred:
			deferred++
		}
	}
	converged, skipped, rebuilt, err := s.convergeStaleActiveReportJobs(ctx, staleBefore, limit)
	if err != nil {
		failures = append(failures, err)
	}
	charges, err := s.SweepStaleReportCharges(ctx, limit)
	if err != nil {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		// Return the first failure so the worker job records a visible error while
		// the remaining candidates still got their chance above.
		return failures[0]
	}
	if len(candidates) > 0 || converged > 0 || skipped > 0 || rebuilt > 0 || charges.Orphaned > 0 {
		logger.Info("report finalize reconcile swept",
			"finalizing_candidates", len(candidates), "applied", applied, "deferred", deferred,
			"converged", converged, "still_live", skipped, "rebuilt_attempts", rebuilt,
			"orphaned_charges", charges.Orphaned)
	}
	return nil
}

// convergeStaleActiveReportJobs asks for a terminal write on active jobs whose
// slice is gone or has settled. It returns how many were converged and how many
// still have a live slice (and were therefore left to that slice's checkpoint;
// among those, `rebuilt` is how many had a missing attempt mapping that was
// restored — doc/288 §7.9 class 3).
func (s *Service) convergeStaleActiveReportJobs(
	ctx context.Context, staleBefore time.Time, limit int,
) (converged, live, rebuilt int, err error) {
	candidates, err := s.Store.ListStaleActiveReportJobs(ctx, staleBefore, limit)
	if err != nil {
		return 0, 0, 0, err
	}
	var failures []error
	for _, candidate := range candidates {
		job, err := s.Store.GetReportJob(ctx, candidate.TenantID, candidate.JobID)
		if err != nil {
			failures = append(failures, fmt.Errorf("report job %s: %w", candidate.JobID, err))
			continue
		}
		if job == nil || job.FinalizedAt != nil {
			continue
		}
		if job.CurrentRuntimeJobID != "" {
			runtimeJob, err := s.Store.GetJob(ctx, candidate.TenantID, job.CurrentRuntimeJobID)
			if err != nil {
				failures = append(failures, fmt.Errorf("report job %s: %w", candidate.JobID, err))
				continue
			}
			if runtimeJob != nil && !reportRuntimeSettled(runtimeJob.Status) {
				// The slice is alive: its own checkpoint owns the convergence.
				// Class 3 still verifies its attempt mapping — a crash before the
				// mapping insert leaves the job declaring a slice the ledger cannot
				// see. Rebuild the row idempotently on the triple; breadcrumb the
				// repair so it is observable but never converges (still a live slice).
				repair, rerr := s.Store.RebuildReportAttemptIfMissing(ctx, repository.ReportAttemptRebuild{
					TenantID: job.TenantID, JobID: job.ID,
					AttemptSeq: job.AttemptSeq, SliceSeq: job.SliceSeq,
					RuntimeJobID:  job.CurrentRuntimeJobID,
					RuntimeJobKey: reportSliceKey(job.ID, job.AttemptSeq, job.SliceSeq),
				})
				if rerr != nil {
					failures = append(failures, fmt.Errorf("report job %s: %w", candidate.JobID, rerr))
					continue
				}
				if repair {
					rebuilt++
				}
				live++
				continue
			}
		}
		// Begin is the only door to a terminal state, and it is conditional on
		// attempt/slice/status, so a job that moved under us simply says no.
		applied, err := s.BeginReportFinalization(
			ctx, candidate.TenantID, job.ID, job.AttemptSeq, job.SliceSeq, job.CurrentRuntimeJobID)
		if err != nil {
			failures = append(failures, fmt.Errorf("report job %s: %w", candidate.JobID, err))
			continue
		}
		if applied {
			converged++
		}
	}
	if len(failures) > 0 {
		return converged, live, rebuilt, failures[0]
	}
	return converged, live, rebuilt, nil
}

// ScheduleReportFinalizeReconciler keeps exactly one sweep durably queued. The
// singleton is expressed as "no other active job of this kind", so a crash leaves
// either a queued job or a stale one that the runner retries.
func (s *Service) ScheduleReportFinalizeReconciler(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindReportFinalizeReconcile, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	runAfter := time.Now().UTC().Add(reportFinalizeReconcileInterval)
	return s.Runner.Enqueue(ctx, model.JobKindReportFinalizeReconcile,
		"report_finalize_reconcile:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0)
}

// reportFinalizeReconcileWorker runs one sweep and schedules the next.
type reportFinalizeReconcileWorker struct {
	svc *Service
}

func (w *reportFinalizeReconcileWorker) Kind() string { return model.JobKindReportFinalizeReconcile }

func (w *reportFinalizeReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	if err := w.svc.ProcessReportFinalizeReconciler(ctx, reportFinalizeReconcileBatch); err != nil {
		logger.Warn("report finalize reconcile failed", "job_id", job.ID, "error", err)
		return err
	}
	if _, err := w.svc.ScheduleReportFinalizeReconciler(ctx, job.ID); err != nil {
		logger.Warn("report finalize reconcile reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}
