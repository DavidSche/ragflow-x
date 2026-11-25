package service

// The assurance execution pipeline (doc/301 §5.1–§5.4, §6.6).
//
// Four layers run in a fixed order, and each one can only *lower* what the previous
// said:
//
//	① quote traceability — does the quoted text exist in the frozen snapshot?
//	② citation completeness — is there a source at all, and is it still verifiable?
//	③ semantic support — does the source actually support the sentence?
//	④ structure — is the sentence a checkable statement in the first place?
//
// The order is the insight of §5.1: a model that answers "supported" while quoting
// text that does not exist has not told us something weaker than the truth, it has
// told us nothing. So ① is mechanical, cheap and runs first; ③ is only asked when ①
// produced something to stand on.
//
// Nothing here writes `release_eligibility`: the run ends by calling
// `reportAssuranceEligibility` (pure) and handing its answer to the repository. That
// is the one-writer rule of §5.5 / G-A8.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// reportAssuranceWorker executes one run.
type reportAssuranceWorker struct{ svc *Service }

func (w *reportAssuranceWorker) Kind() string { return model.JobKindReportAssurance }

func (w *reportAssuranceWorker) Run(ctx context.Context, job *model.Job) error {
	if job == nil {
		return nil
	}
	var payload struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return fmt.Errorf("report assurance: invalid payload: %w", err)
	}
	if strings.TrimSpace(payload.RunID) == "" {
		return errors.New("report assurance: payload has no run_id")
	}
	return w.svc.runReportAssurance(ctx, job.TenantID, payload.RunID)
}

// reportAssuranceWorkerLease bounds one run. A run that outlives it is closed as
// `failed` by the reconciler rather than left `running` forever: a stuck run holds
// the 44118 business mutex for its revision, so leaving it open would block every
// later attempt to review that revision.
const reportAssuranceWorkerLease = 30 * time.Minute

// runReportAssurance is the whole pipeline for one run.
func (s *Service) runReportAssurance(ctx context.Context, tenantID, runID string) error {
	run, err := s.Store.GetAssuranceRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if run == nil {
		// The run was deleted (retention) between enqueue and execution. Nothing to
		// do, and nothing to report: a job whose subject is gone is not a failure.
		return nil
	}
	if model.ReportAssuranceTerminal(run.Status) {
		// A replayed job of a finished run must not re-evaluate: that would append
		// a second set of verdicts to the same run id.
		return nil
	}
	started, err := s.Store.StartAssuranceRun(ctx, tenantID, run.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !started {
		return nil
	}
	run.Status = model.ReportAssuranceStatusRunning

	// The lease is enforced by the reconciler, not by a context timeout here: a
	// context deadline would abort mid-batch and leave the run `running` with a
	// partial evaluation set and no terminal state.
	plan, err := s.planReportAssurance(ctx, run)
	if err != nil {
		return s.failReportAssurance(ctx, run, "plan_failed", err)
	}
	return s.executeReportAssurance(ctx, run, plan)
}

// reportAssurancePlan is everything the pipeline needs, loaded once.
//
// It is loaded up front because every stage indexes the others: claims by section,
// citations by claim, snapshots by citation. Loading lazily would make the pipeline
// N+1 over a revision with hundreds of claims.
type reportAssurancePlan struct {
	Revision   *model.ReportRevision
	Sections   []model.ReportSection
	Claims     []model.ReportClaim
	Citations  []model.ReportCitation
	Snapshots  map[string]model.ReportEvidenceSnapshot
	Candidates int
}

func (s *Service) planReportAssurance(
	ctx context.Context, run *model.ReportAssuranceRun,
) (*reportAssurancePlan, error) {
	revision, err := s.Store.GetReportRevisionByID(ctx, run.TenantID, run.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, fmt.Errorf("revision %s is gone", run.RevisionID)
	}
	sections, err := s.Store.ListReportSections(ctx, run.TenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	claims, err := s.Store.ListReportClaims(ctx, run.TenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	citations, err := s.Store.ListReportCitations(ctx, run.TenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(citations))
	seen := map[string]struct{}{}
	for _, citation := range citations {
		if _, ok := seen[citation.EvidenceSnapshotID]; ok {
			continue
		}
		seen[citation.EvidenceSnapshotID] = struct{}{}
		ids = append(ids, citation.EvidenceSnapshotID)
	}
	snapshots := map[string]model.ReportEvidenceSnapshot{}
	if len(ids) > 0 {
		rows, err := s.Store.ListReportEvidenceByIDs(ctx, run.TenantID, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			snapshots[row.ID] = row
		}
	}
	candidates, err := s.Store.CountConflictCandidates(ctx, run.TenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	return &reportAssurancePlan{
		Revision: revision, Sections: sections, Claims: claims,
		Citations: citations, Snapshots: snapshots, Candidates: int(candidates),
	}, nil
}

// executeReportAssurance runs the four layers and closes the run.
func (s *Service) executeReportAssurance(
	ctx context.Context, run *model.ReportAssuranceRun, plan *reportAssurancePlan,
) error {
	// The evaluator is chosen here rather than inside the loop so a test can hand in
	// its own and exercise the *pipeline* — the part that decides which columns a
	// verdict row carries (doc/304 E141). Everything else about the choice (mode,
	// settings, route) stays in `newReportAssuranceEvaluator`.
	return s.executeReportAssuranceWith(ctx, run, plan, s.newReportAssuranceEvaluator(ctx, run))
}

func (s *Service) executeReportAssuranceWith(
	ctx context.Context, run *model.ReportAssuranceRun, plan *reportAssurancePlan,
	evaluator reportAssuranceEvaluator,
) error {
	now := time.Now().UTC()
	sectionByID := make(map[string]model.ReportSection, len(plan.Sections))
	for _, section := range plan.Sections {
		sectionByID[section.ID] = section
	}
	citationsByClaim := map[string][]model.ReportCitation{}
	sectionCitations := map[string][]model.ReportCitation{}
	for _, citation := range plan.Citations {
		if citation.ClaimID != "" {
			citationsByClaim[citation.ClaimID] = append(citationsByClaim[citation.ClaimID], citation)
			continue
		}
		sectionCitations[citation.SectionID] = append(sectionCitations[citation.SectionID], citation)
	}

	evaluations := make([]model.ReportClaimEvaluation, 0, len(plan.Claims))
	findings := make([]model.ReportReviewFinding, 0)
	budget := run.BudgetTokens
	usedTokens := int64(0)
	budgetExhausted := false
	quotaSettled := int64(0)
	callSeq := 0

	for _, claim := range plan.Claims {
		section, ok := sectionByID[claim.SectionID]
		requiresEvidence := ok && section.RequiresEvidence
		scanScope := reportAssuranceScanScope(run)
		evaluation := model.ReportClaimEvaluation{
			ID: idNew(), TenantID: run.TenantID, RunID: run.ID, RevisionID: plan.Revision.ID,
			SectionOrd: claim.SectionOrd, ClaimID: claim.ID, ClaimType: claim.ClaimType,
			Method: model.ReportEvaluationMethodRule, MethodVersion: run.MethodVersion,
			ScanScope: scanScope, Attempt: 1, CreatedAt: now,
		}

		// Layer ④ first in *cost*, not in authority: a sentence that is not a checkable
		// statement is closed here, before any model call is paid for.
		evaluation.StructureVerdict = reportAssuranceStructureVerdict(claim)
		if !reportAssuranceClaimNeedsVerdict(claim, requiresEvidence) {
			evaluation.SupportVerdict = model.ReportVerdictNotRequired
			evaluation.StructureVerdict = model.ReportStructureNotApplicable
			evaluations = append(evaluations, evaluation)
			continue
		}
		if budgetExhausted {
			// The budget is a hard local ceiling (§6.5): the remaining claims are
			// recorded as not evaluated with a reason, never silently dropped and never
			// evaluated with a truncated prompt (which would make the verdict
			// incomparable with the same claim in another run).
			evaluation.SupportVerdict = model.ReportVerdictNotEvaluated
			evaluation.NotEvaluatedReason = model.ReportNotEvaluatedBudget
			evaluations = append(evaluations, evaluation)
			continue
		}

		claimCitations := citationsByClaim[claim.ID]
		if len(claimCitations) == 0 {
			claimCitations = sectionCitations[claim.SectionID]
		}
		check, snapshotID, evidenceIDs := reportAssuranceQuoteOf(claimCitations, plan.Snapshots)
		evaluation.QuoteStatus = check.Status
		evaluation.QuoteText = firstNonEmpty(evaluation.QuoteText, check.MatchedText)
		evaluation.QuoteSpanStart, evaluation.QuoteSpanEnd = check.SpanStart, check.SpanEnd
		evaluation.MatchedSnapshotID = snapshotID
		evaluation.EvidenceIDsJSON = encodeAssuranceIDs(evidenceIDs)

		if len(claimCitations) == 0 {
			// ② completeness: nothing to stand on. This is `insufficient_evidence`,
			// not `unsupported` — the corpus may well answer the question, the section
			// simply did not cite it.
			evaluation.SupportVerdict = model.ReportVerdictInsufficient
			evaluation.InsufficientReason = model.ReportInsufficientNoCitation
			evaluation.QuoteStatus = model.ReportQuoteStatusNotProvided
			evaluations = append(evaluations, evaluation)
			findings = append(findings, reportAssuranceFindingOf(run, claim, model.ReportFindingMissingEvidence,
				model.ReportSeverityHigh, "断言没有引用任何证据",
				"这条结论要求证据，但没有任何 citation 指向它，读者无法核对。",
				"为该断言补一条来源，或把它改写成显式的推断/建议。"))
			continue
		}

		// ③ semantic support. It runs for every quote state except `not_found`, and the
		// default here is `abstained`, not `supported`: if no judgement is produced the
		// row must say "no verdict", never "supported then capped". Defaulting to
		// `supported` made every ambiguous-quote claim land on `partially_supported`
		// without a single model call — a scored verdict with nothing behind it, which
		// is precisely the false certification this pipeline exists to prevent.
		verdict := model.ReportVerdictAbstained
		if check.Status != reportQuoteNotFound {
			// Name the judge *before* the call, not after it succeeds (doc/304 E141).
			// The row has always been created with `method=deterministic`, and nothing
			// overwrote it when the semantic layer answered, so a model verdict was
			// stored as a rule verdict with an empty `model_id` — indistinguishable
			// from a tier_0 run that never called a model, while `used_tokens > 0` in
			// the same run contradicted that reading. Stamping here also covers the
			// failure path below: a call that 503'd was still made by this evaluator,
			// and the abstain rationale without a model id cannot be attributed.
			evaluation.Method = evaluator.Method()
			evaluation.ModelID = evaluator.ModelID()
			evaluation.PromptHash = evaluator.PromptHash()
			outcome, tokensIn, tokensOut, cacheHit, cacheSource, err := s.evaluateReportClaim(
				ctx, evaluator, run, claim, section, plan, check, snapshotID)
			if err != nil {
				// A provider failure is a degraded run, not a wrong verdict: abstain and
				// keep going, so one flaky call cannot discard a whole revision's audit.
				evaluation.AbstainReason = model.ReportAbstainModelUncertain
				evaluation.SupportVerdict = model.ReportVerdictAbstained
				evaluation.Rationale = "review model call failed: " + err.Error()
				evaluations = append(evaluations, evaluation)
				continue
			}
			callSeq++
			verdict = outcome.Verdict
			applyMethodology(&evaluation, outcome)
			evaluation.TokensIn, evaluation.TokensOut = tokensIn, tokensOut
			evaluation.CacheHit = cacheHit
			evaluation.CacheSourceEvaluationID = cacheSource
			if !cacheHit {
				// Quota is reserved per call, *before* the call, and settled after it
				// (§6.5). `cache_hit` costs nothing, so it reserves nothing.
				settled, quotaErr := s.settleReportAssuranceCall(
					ctx, run, callSeq, tokensIn+tokensOut)
				if quotaErr != nil {
					logger.Warn("report assurance quota settle failed",
						"run_id", run.ID, "error", quotaErr)
				}
				quotaSettled += settled
			}
			usedTokens += tokensIn + tokensOut
			if budget > 0 && usedTokens >= budget {
				budgetExhausted = true
			}
		}

		clamped, clampAbstain := reportClampVerdict(verdict, check, claim.ClaimType, requiresEvidence)
		evaluation.SupportVerdict = clamped
		if clampAbstain != "" && evaluation.AbstainReason == "" {
			evaluation.AbstainReason = clampAbstain
			evaluation.Rationale = firstNonEmpty(evaluation.Rationale,
				"quote layer degraded the verdict: "+check.Status)
		} else if clamped == model.ReportVerdictAbstained && evaluation.AbstainReason == "" {
			// Unreachable by construction, and asserted rather than assumed: an abstain
			// with no reason is a row the metrics cannot attribute (invariant 2 of §4.3).
			evaluation.AbstainReason = model.ReportAbstainInputInvalid
		}
		if clamped == model.ReportVerdictConflicting || clamped == model.ReportVerdictUnsupported {
			findings = append(findings, reportAssuranceFindingOfFor(run, claim, clamped, evaluation))
		}
		if check.Status == reportQuoteNotFound {
			findings = append(findings, reportAssuranceFindingOf(run, claim, model.ReportFindingQuoteUnverifiable,
				model.ReportSeverityHigh, "引文无法在证据快照中定位",
				"复核模型引用了快照中不存在（或无法唯一定位）的文本；这条引文不可核对。",
				"检查提示模板与证据摘录逻辑；排查是否存在模型编造引文。"))
		}
		evaluations = append(evaluations, evaluation)
	}

	if err := s.Store.InsertClaimEvaluations(ctx, evaluations); err != nil {
		return s.failReportAssurance(ctx, run, "write_failed", err)
	}
	if len(findings) > 0 {
		if err := s.Store.CreateReviewFindings(ctx, findings); err != nil {
			// A missing finding list must not lose the verdicts: the run continues and
			// the failure is visible in the log and in `degraded`.
			logger.Warn("report assurance findings write failed", "run_id", run.ID, "error", err)
		} else {
			s.refreshOpenFindingGauge(ctx, run.TenantID, run.ReportID)
		}
	}

	status := model.ReportAssuranceStatusSucceeded
	if budgetExhausted || len(findings) > 0 {
		status = model.ReportAssuranceStatusDegraded
	}
	return s.finishReportAssurance(
		ctx, run, status, evaluations, findings, plan, usedTokens, quotaSettled, budgetExhausted)
}

// coverageCounters tallies the stored columns from the evaluation rows. It is a
// second pass over 9 verdict values rather than an increment inside the loop, so a
// `continue` in the pipeline can never leave a counter behind.
func coverageCounters(evaluations []model.ReportClaimEvaluation) repository.ReportAssuranceCounters {
	counters := repository.ReportAssuranceCounters{}
	for _, evaluation := range evaluations {
		switch evaluation.SupportVerdict {
		case model.ReportVerdictSupported:
			counters.Supported++
		case model.ReportVerdictPartiallySupported:
			counters.Partially++
		case model.ReportVerdictUnsupported:
			counters.Unsupported++
		case model.ReportVerdictConflicting:
			counters.Conflicting++
		case model.ReportVerdictOutdated:
			counters.Outdated++
		case model.ReportVerdictInsufficient:
			counters.Insufficient++
		case model.ReportVerdictAbstained:
			counters.Abstained++
			switch evaluation.AbstainReason {
			case model.ReportAbstainModelUncertain:
				counters.AbstainedUncertain++
			case model.ReportAbstainInputInvalid:
				counters.AbstainedInvalid++
			case model.ReportAbstainQuoteNotFound:
				counters.AbstainedNotFound++
			case model.ReportAbstainNoQuote:
				counters.AbstainedNoQuote++
			}
		case model.ReportVerdictNotEvaluated:
			counters.NotEvaluated++
			switch evaluation.NotEvaluatedReason {
			case model.ReportNotEvaluatedBudget:
				counters.NotEvaluatedBudget++
			case model.ReportNotEvaluatedPolicy:
				counters.NotEvaluatedPolicy++
			case model.ReportNotEvaluatedTrigger:
				counters.NotEvaluatedTrigger++
			}
		case model.ReportVerdictNotRequired:
			counters.NotRequired++
		}
		if model.ReportVerdictScored(evaluation.SupportVerdict) {
			counters.Evaluated++
		}
		switch evaluation.StructureVerdict {
		case model.ReportStructurePassed:
			counters.StructurePass++
		case model.ReportStructureFailed:
			counters.StructureFail++
		}
	}
	return counters
}

// finishReportAssurance writes the terminal state, the metrics snapshot and the
// revision projection.
func (s *Service) finishReportAssurance(
	ctx context.Context, run *model.ReportAssuranceRun, status string,
	evaluations []model.ReportClaimEvaluation, findings []model.ReportReviewFinding,
	plan *reportAssurancePlan, usedTokens, quotaSettled int64, budgetExhausted bool,
) error {
	metrics := assuranceMetricsFromEvaluations(status, run.RequestedPolicy, evaluations, nil)
	eligibility, failures := reportAssuranceEligibility(metrics)
	metricsJSON, err := json.Marshal(map[string]interface{}{
		"metrics":                   metrics,
		"release_eligibility_basis": reportAssuranceEligibilityBasis(failures),
		"budget": map[string]interface{}{
			"budget_tokens": run.BudgetTokens, "used_tokens": usedTokens,
			"overshoot_tokens": maxInt64Zero(usedTokens - run.BudgetTokens),
			"exhausted":        budgetExhausted,
			// The token counts are estimates: `ChatToModel` returns text, not usage
			// (doc/301 §14.E E19 follow-up). Saying which estimator produced them is
			// what keeps `used_tokens` an honest *record* rather than a fake meter.
			"token_source": "estimated_runes_v1",
		},
		"scan_scope":     reportAssuranceScanScope(run),
		"candidates":     plan.Candidates,
		"method_version": run.MethodVersion,
	})
	if err != nil {
		return s.failReportAssurance(ctx, run, "metrics_failed", err)
	}
	now := time.Now().UTC()
	applied, err := s.Store.FinishAssuranceRun(ctx, repository.ReportAssuranceFinish{
		TenantID: run.TenantID, RunID: run.ID, Status: status,
		ReleaseEligibility: eligibility, MetricsJSON: string(metricsJSON),
		Counters: coverageCounters(evaluations), UsedTokens: usedTokens,
		BudgetExhausted: budgetExhausted, QuotaPeriod: run.QuotaPeriod,
		QuotaSettledTokens: quotaSettled, Now: now,
	})
	if err != nil {
		return err
	}
	if !applied {
		// Another writer closed the run (operator cancel, or a reconciler). The
		// conclusion already recorded wins: a late worker must not overwrite a tier.
		return nil
	}
	if err := s.Store.ProjectAssuranceOnRevision(ctx, repository.AssuranceProjection{
		TenantID: run.TenantID, RevisionID: plan.Revision.ID, RunID: run.ID,
		Status: status, SummaryJSON: reportAssuranceSummary(run, metrics, eligibility, findings),
	}); err != nil {
		// The projection is rebuildable; the run row is the authority (§6.2).
		logger.Warn("report assurance projection failed", "run_id", run.ID, "error", err)
	}
	s.emitReportAssuranceEvents(ctx, run, status, eligibility, metrics)
	// §7.4 metrics, emitted only on the path that actually closed the run: a late
	// worker whose terminal write lost the race must not double-count the run or its
	// claims, which is exactly the `!applied` early return above.
	reportAssuranceObserve(run, status, eligibility, metrics, usedTokens, budgetExhausted, now)
	return nil
}

// reportAssuranceObserve publishes §7.4's series for one closed run.
//
// It is a free function over the run and its metrics, not a method, so that the
// emission set can be tested against a metric-registry-free value: the numbers that
// must add up (claims, quote statuses, tokens) are checked without an HTTP scrape.
func reportAssuranceObserve(
	run *model.ReportAssuranceRun, status, eligibility string,
	metrics reportAssuranceMetrics, usedTokens int64, budgetExhausted bool, now time.Time,
) {
	observer := obs.Get()
	if observer == nil {
		return
	}
	observer.IncReportAssuranceRun(status, run.Mode, eligibility, budgetExhausted)
	observer.AddReportAssuranceClaims(intMapToInt64(metrics.Verdicts))
	observer.AddReportAssuranceQuoteStatuses(intMapToInt64(metrics.QuoteStatuses))
	if !run.CreatedAt.IsZero() {
		observer.ObserveReportAssuranceDuration(now.Sub(run.CreatedAt).Seconds())
	}
	observer.AddReportAssuranceTokens(false, usedTokens)
}

// intMapToInt64 adapts a counter map to the obs signature. A nil map becomes an empty
// one: "no verdict of this kind" is not an error, and ranging over nil is the same
// answer as ranging over nothing.
func intMapToInt64(counts map[string]int) map[string]int64 {
	converted := make(map[string]int64, len(counts))
	for key, value := range counts {
		converted[key] = int64(value)
	}
	return converted
}

// reportAssuranceSummary is the small JSON the revision carries for list views.
// Deliberately not the full metrics: a list page must not pay for a 20-key blob per
// row, and a partial copy that says "not_run" until a run exists is honest.
func reportAssuranceSummary(
	run *model.ReportAssuranceRun, metrics reportAssuranceMetrics,
	eligibility string, findings []model.ReportReviewFinding,
) string {
	// `requested_policy` and the conflict counts ride along because the export gate
	// (§7.2) reads *this* row: the gate must answer the same way for a given
	// revision forever, so it may not recount findings live.
	conflicts, high := 0, 0
	for _, finding := range findings {
		if finding.Status == model.ReportFindingStatusResolved ||
			finding.Status == model.ReportFindingStatusDismissed {
			continue
		}
		switch finding.Category {
		case model.ReportFindingContradiction, model.ReportFindingSourceConflict,
			model.ReportFindingUnsupported, model.ReportFindingQuoteUnverifiable:
			conflicts++
		}
		if finding.Severity == model.ReportSeverityHigh {
			high++
		}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"release_eligibility": eligibility,
		"requested_policy":    run.RequestedPolicy,
		"coverage":            metrics.Coverage,
		"evaluated":           metrics.Evaluated,
		"denominator":         metrics.Denominator,
		"not_required_share":  metrics.NotRequiredShare,
		"denominator_small":   metrics.DenominatorSmall,
		"status":              metrics.Status,
		"conflicts":           conflicts,
		"high_findings":       high,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

// failReportAssurance closes a run that could not even be planned, so it does not
// hold the 44118 mutex forever.
func (s *Service) failReportAssurance(
	ctx context.Context, run *model.ReportAssuranceRun, code string, cause error,
) error {
	reason := cause.Error()
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	metricsJSON, _ := json.Marshal(map[string]interface{}{
		"metrics":                   reportAssuranceMetrics{Status: model.ReportAssuranceStatusFailed},
		"release_eligibility_basis": `["run_failed"]`,
		"failure":                   map[string]string{"code": code, "reason": reason},
	})
	_, err := s.Store.FinishAssuranceRun(ctx, repository.ReportAssuranceFinish{
		TenantID: run.TenantID, RunID: run.ID, Status: model.ReportAssuranceStatusFailed,
		ReleaseEligibility: model.ReportAssuranceEligibilityBlocked,
		MetricsJSON:        string(metricsJSON),
		FailureCode:        code, FailureReason: reason, Now: time.Now().UTC(),
	})
	if err != nil {
		logger.Error("report assurance terminal write failed", "run_id", run.ID, "error", err)
	}
	// The failure audit is written even when the terminal write above failed: "a run
	// was started and did not finish" is exactly the fact that would otherwise be
	// invisible — the run row would still read `running`, and only the audit would say
	// why nobody is coming back to it.
	s.recordReportAudit(ctx, run.TriggeredBy, run.TenantID, "report.assurance_failed", run.ReportID,
		map[string]any{
			"run_id": run.ID, "revision_id": run.RevisionID, "revision": run.Revision,
			"failure_code": code, "failure_reason": reason,
			// Both counters are reported as stored, not as hoped: a budget that was not
			// actually spent reads 0, so "budget_exhausted" can be checked against
			// `used_tokens` instead of being believed.
			"used_tokens": run.UsedTokens, "budget_tokens": run.BudgetTokens,
			"budget_exhausted": run.BudgetExhausted,
			"terminal_write_error": func() string {
				if err == nil {
					return ""
				}
				return err.Error()
			}(),
		})
	// `blocked` on a failed run is the one tier a failure may write: there is no
	// measurement, so nothing may be released, and the caller can retry.
	return fmt.Errorf("report assurance run %s failed (%s): %w", run.ID, code, cause)
}

// reportAssuranceScanScope is the human-readable statement of what the run looked
// at. It is a string in `metrics_json` and on every evaluation row because
// "did this run check for contradictions across documents" must be answerable from
// the record, not from the settings screen (which may have changed since).
func reportAssuranceScanScope(run *model.ReportAssuranceRun) string {
	var policy AssurancePolicyResolution
	if err := json.Unmarshal([]byte(run.PolicyJSON), &policy); err == nil && policy.ScanTopK > 0 {
		return "cited+candidate:" + itoa(policy.ScanTopK)
	}
	return "cited_only"
}

// reportAssuranceClaimNeedsVerdict decides `not_required` (doc/301 §5.2).
//
// Two conditions, both structural rather than model-derived: a section that
// declares it needs no evidence, and a claim whose type is not factual. The second
// is why `not_required_share` is a metric with a ceiling — a template that marks
// everything as an opinion would otherwise score 100% coverage on nothing.
func reportAssuranceClaimNeedsVerdict(claim model.ReportClaim, requiresEvidence bool) bool {
	switch claim.ClaimType {
	case "recommendation", "opinion", "action", "action_item", "summary":
		return false
	}
	if !requiresEvidence && claim.ClaimType != "fact" {
		return false
	}
	return true
}

// reportAssuranceStructureVerdict is layer ④: is this a checkable statement?
//
// It is mechanical on purpose. A statement that is too short to carry a predicate
// ("如下：", "3.2 风险") cannot be supported or refuted, and calling that
// `unsupported` would poison the accuracy metric with fragments.
func reportAssuranceStructureVerdict(claim model.ReportClaim) string {
	text := strings.TrimSpace(claim.ClaimText)
	if text == "" {
		return model.ReportStructureFailed
	}
	if strings.HasSuffix(text, "？") || strings.HasSuffix(text, "?") {
		// A question is not an assertion; it belongs to the outline, not the body.
		return model.ReportStructureFailed
	}
	if utf8RuneCount(text) < reportClaimMinRunes {
		return model.ReportStructureFailed
	}
	return model.ReportStructurePassed
}

// reportAssuranceQuoteOf runs layer ① over a claim's citations.
//
// The snippet stored on the citation is the *report's* excerpt, which is what the
// reader sees; the snapshot is the frozen content. Verifying the excerpt against the
// snapshot is the whole point: a snippet that is not in the snapshot is an excerpt
// that was edited after the fact.
func reportAssuranceQuoteOf(
	citations []model.ReportCitation, snapshots map[string]model.ReportEvidenceSnapshot,
) (reportQuoteCheck, string, []string) {
	evidenceIDs := make([]string, 0, len(citations))
	best := reportQuoteCheck{Status: reportQuoteNotProvided, SpanMap: reportSpanMapNone}
	matchedSnapshot := ""
	for _, citation := range citations {
		evidenceIDs = append(evidenceIDs, citation.EvidenceSnapshotID)
		snapshot, ok := snapshots[citation.EvidenceSnapshotID]
		if !ok {
			// The snapshot was purged by retention: the citation is unverifiable, which
			// is `not_found` at the quote layer and `evidence_expired` when reported.
			continue
		}
		check := reportCheckQuote(citation.Snippet, snapshot.Content)
		switch check.Status {
		case reportQuoteVerified:
			return check, citation.EvidenceSnapshotID, evidenceIDs
		case reportQuoteAmbiguous:
			if best.Status != reportQuoteAmbiguous {
				best, matchedSnapshot = check, citation.EvidenceSnapshotID
			}
		case reportQuoteNotFound:
			if best.Status == reportQuoteNotProvided {
				best = check
			}
		}
	}
	if best.Status == reportQuoteNotFound {
		// Every citation was checked and none contained its snippet.
		return best, matchedSnapshot, evidenceIDs
	}
	return best, matchedSnapshot, evidenceIDs
}

// reportAssuranceFindingOf builds a finding. `ClaimID` is set, `SectionOrd` is
// always set: a finding must be locatable (invariant 8).
func reportAssuranceFindingOf(
	run *model.ReportAssuranceRun, claim model.ReportClaim, category, severity, title, detail, action string,
) model.ReportReviewFinding {
	claimID := claim.ID
	runID := run.ID
	now := time.Now().UTC()
	return model.ReportReviewFinding{
		ID: idNew(), TenantID: run.TenantID, ReportID: run.ReportID, RevisionID: run.RevisionID,
		RunID: &runID, SectionOrd: claim.SectionOrd, ClaimID: &claimID,
		Source: "rule", Category: category, Severity: severity,
		Status: model.ReportFindingStatusOpen, Title: title, Detail: detail,
		SuggestedAction: action, CreatedAt: now, UpdatedAt: now,
	}
}

// reportAssuranceFindingOfFor maps an unsupported/conflicting verdict to a finding.
func reportAssuranceFindingOfFor(
	run *model.ReportAssuranceRun, claim model.ReportClaim, verdict string,
	evaluation model.ReportClaimEvaluation,
) model.ReportReviewFinding {
	category, severity, title := model.ReportFindingUnsupported, model.ReportSeverityHigh, "断言未被证据支持"
	if verdict == model.ReportVerdictConflicting {
		category, title = model.ReportFindingContradiction, "断言与所引证据相互矛盾"
	}
	finding := reportAssuranceFindingOf(run, claim, category, severity, title,
		firstNonEmpty(evaluation.Rationale, "复核判定为 "+verdict+"，需要人工确认。"),
		"核对该断言与来源段落；必要时改写结论或更换证据。")
	finding.EvidenceIDsJSON = evaluation.EvidenceIDsJSON
	finding.ModelID = evaluation.ModelID
	finding.PromptHash = evaluation.PromptHash
	if evaluation.Method != "" {
		finding.Source = evaluation.Method
	}
	return finding
}

// reportAssuranceEvaluator is layer ③. It is an interface so the pipeline can be
// tested without a provider, and so a later version can swap in a cross-model
// adjudicator (doc/301 §16) without touching the run bookkeeping.
type reportAssuranceEvaluator interface {
	// Evaluate returns a verdict for one claim. `cacheSource` is non-nil only on a
	// cache hit, and then the returned tokens are zero by definition.
	Evaluate(ctx context.Context, in reportAssuranceEvalInput) (reportAssuranceEvalOutcome, error)
	ModelID() string
	PromptHash() string
	Method() string
	Independence() string
}

// reportAssuranceEvalInput is everything a verdict may depend on — the same eight
// components the cache key hashes (doc/301 §6.1).
type reportAssuranceEvalInput struct {
	Claim      model.ReportClaim
	Section    model.ReportSection
	Quote      string
	SnapshotID string
	// CandidateCount is part of the input because "were uncited chunks scanned"
	// changes what a verdict means.
	CandidateCount int
	ScanScope      string
}

// reportAssuranceEvalOutcome is the evaluator's answer.
type reportAssuranceEvalOutcome struct {
	Verdict          string
	Relevance        string
	Confidence       *float64
	Rationale        string
	EvidenceIDs      []string
	EvidenceQuote    string
	ContradictionIDs []string
	TokensIn         int64
	TokensOut        int64
	CacheHit         bool
	CacheSource      *string
}

// applyMethodology copies the method columns of an outcome onto the evaluation
// row. It is a free function because the evaluation row belongs to `model` and a
// method there would be a model-layer opinion about a service-layer type.
func applyMethodology(e *model.ReportClaimEvaluation, outcome reportAssuranceEvalOutcome) {
	e.Relevance = outcome.Relevance
	e.Confidence = outcome.Confidence
	e.Rationale = outcome.Rationale
	e.EvidenceQuote = outcome.EvidenceQuote
	if outcome.Verdict != "" {
		e.SupportVerdict = outcome.Verdict
	}
}

// evaluateReportClaim is the cache-then-model step. The cache read is explicit
// (`FindCachedEvaluation`) because a unique index on `cache_key` would make the hit
// row impossible to write (§6.1).
func (s *Service) evaluateReportClaim(
	ctx context.Context, evaluator reportAssuranceEvaluator, run *model.ReportAssuranceRun,
	claim model.ReportClaim, section model.ReportSection, plan *reportAssurancePlan,
	check reportQuoteCheck, snapshotID string,
) (reportAssuranceEvalOutcome, int64, int64, bool, *string, error) {
	input := reportAssuranceEvalInput{
		Claim: claim, Section: section, Quote: check.MatchedText,
		SnapshotID: snapshotID, CandidateCount: plan.Candidates,
		ScanScope: reportAssuranceScanScope(run),
	}
	cacheKey := reportAssuranceCacheKey(
		run.MethodVersion, evaluator.ModelID(), evaluator.PromptHash(),
		claim.ClaimText, section.SectionKey, check.MatchedText,
		itoa(input.CandidateCount), run.RequestedPolicy,
	)
	cached, err := s.Store.FindCachedEvaluation(ctx, run.TenantID, cacheKey)
	if err != nil {
		return reportAssuranceEvalOutcome{}, 0, 0, false, nil, err
	}
	if cached != nil {
		source := cached.ID
		return reportAssuranceEvalOutcome{
			Verdict: cached.SupportVerdict, Relevance: cached.Relevance,
			Confidence: cached.Confidence, Rationale: cached.Rationale,
			CacheHit: true, CacheSource: &source,
		}, 0, 0, true, &source, nil
	}
	outcome, err := evaluator.Evaluate(ctx, input)
	if err != nil {
		return outcome, 0, 0, false, nil, err
	}
	return outcome, outcome.TokensIn, outcome.TokensOut, false, nil, nil
}

// settleReportAssuranceCall reserves and settles one model call's quota
// (doc/301 §6.5). The reservation id is `<run_id>:c<call_seq>`, which is idempotent
// across a retried batch: the same call cannot be charged twice.
func (s *Service) settleReportAssuranceCall(
	ctx context.Context, run *model.ReportAssuranceRun, callSeq int, tokens int64,
) (int64, error) {
	if tokens < 0 {
		tokens = 0
	}
	settings, err := s.reportAssuranceSettingsOf(ctx)
	if err != nil {
		return 0, err
	}
	if settings.MonthlyTokens <= 0 {
		// Unlimited: no bucket, no reservation, but the cost is still recorded on the
		// run (the caller adds it to `quota_settled_tokens`).
		return tokens, nil
	}
	if _, err := s.ensureReportAssuranceQuotaBucket(ctx, run.TenantID, settings.MonthlyTokens); err != nil {
		return 0, err
	}
	requestID := run.ID + ":c" + itoa(callSeq)
	subject := run.ID
	_, err = s.PreauthorizeBucketQuotaForTenant(
		ctx, run.TenantID, subject, requestID, tokens, true, false,
		model.QuotaBucketDimensionAssurance, run.TenantID)
	if err != nil {
		// A rejected reservation means the allowance ran out mid-run. The call already
		// happened, so the money is spent either way: record it and continue rather
		// than losing the verdict that was just produced.
		return 0, err
	}
	if err := s.Store.FinalizeQuotaBucketReservation(ctx, requestID, tokens); err != nil {
		return 0, err
	}
	s.observeAssuranceQuotaDrift(ctx, run)
	return tokens, nil
}

// observeAssuranceQuotaDrift publishes §7.4's drift gauge after a settle.
//
// The ratio is `(tokens_used + pending) / token_limit` for the *assurance* dimension
// of this tenant's current period. Above 1 means the per-call reservation did not
// hold — the one failure mode the §6.5 design claims to make impossible, so it must
// be observable rather than argued. A missing bucket or a non-positive limit is
// "unlimited" in this codebase, and unlimited has no ratio; the gauge is left alone
// instead of being set to 0, because 0 would read as perfect health.
func (s *Service) observeAssuranceQuotaDrift(ctx context.Context, run *model.ReportAssuranceRun) {
	observer := obs.Get()
	if observer == nil {
		return
	}
	bucket, err := s.Store.GetQuotaBucket(ctx, run.TenantID,
		model.QuotaBucketDimensionAssurance, run.TenantID, run.QuotaPeriod)
	if err != nil || bucket == nil || bucket.TokenLimit <= 0 {
		return
	}
	committed := bucket.TokensUsed + bucket.Pending
	observer.SetAssuranceQuotaDriftRatio(float64(committed) / float64(bucket.TokenLimit))
}

// refreshOpenFindingGauge republishes the open-finding counts of one report.
//
// A gauge, not a counter: the truthful number is "how many are open right now", so
// both creation and adjudication must re-read it. Severities with zero are written
// explicitly — otherwise resolving the last `high` finding would leave the series at
// its last value and the alert would never clear.
func (s *Service) refreshOpenFindingGauge(ctx context.Context, tenantID, reportID string) {
	observer := obs.Get()
	if observer == nil || reportID == "" {
		return
	}
	for _, severity := range []string{
		model.ReportSeverityHigh, model.ReportSeverityMedium,
		model.ReportSeverityLow,
	} {
		count, err := s.Store.CountReviewFindings(ctx, tenantID, repository.ReviewFindingFilter{
			ReportID: reportID, Severities: []string{severity},
			Statuses: []string{model.ReportFindingStatusOpen},
		})
		if err != nil {
			logger.Warn("report finding gauge refresh failed",
				"tenant_id", tenantID, "report_id", reportID, "severity", severity, "error", err)
			continue
		}
		observer.SetReportAssuranceOpenFindings(severity, int(count))
	}
}

// emitReportAssuranceEvents publishes the run's terminal event. It is a notify
// event, not a report job event: the assurance event stream is the run's own, and
// appending to `rgx_report_job_event` would make a report's replay show an audit it
// never ran.
func (s *Service) emitReportAssuranceEvents(
	ctx context.Context, run *model.ReportAssuranceRun, status, eligibility string,
	metrics reportAssuranceMetrics,
) {
	severity := "info"
	if status == model.ReportAssuranceStatusFailed || eligibility == model.ReportAssuranceEligibilityBlocked {
		severity = "error"
	} else if status == model.ReportAssuranceStatusDegraded {
		severity = "warn"
	}
	notifyReportAssurance(ctx, run, status, eligibility, metrics, severity)
}

// encodeAssuranceIDs is a nil-safe JSON array encoder: `null` and `[]` must not both
// appear in `evidence_ids_json`, or `evidenceIDsCount` would have to guess.
func encodeAssuranceIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			cleaned = append(cleaned, id)
		}
	}
	payload, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func maxInt64Zero(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
