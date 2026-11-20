package service

// Small shared helpers and the default semantic evaluator for the assurance
// pipeline (doc/301 §5.1 layer ③, §5.4, §6.6).
//
// The evaluator is the only place in the pipeline that costs money, and therefore
// the only place that must be honest about what it does *not* know: a missing
// review model is `abstained/model_uncertain` on every claim, never `supported`.
// A revision must be able to reach tier_0 with no model configured at all — that is
// what "quote traceability is mechanical" buys (§5.1), and it is why v0.7.0 can
// ship the audit before it ships the reviewer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const (
	// The layer ④ length floor is `reportClaimMinRunes` from report_claims.go: one
	// definition, so the assurance structure layer and the claim extractor agree
	// about what a claim is.
	//
	// reportAssurancePromptVersion is hashed into `prompt_hash`. Bumping it
	// invalidates every cached verdict by construction, which is exactly what
	// must happen when the question asked of the model changes (§6.1).
	reportAssurancePromptVersion = "assurance-claim-eval.v2"
)

// idNew / itoa / firstNonEmpty are local aliases so the assurance files read as
// one vocabulary. They are thin on purpose: a second id generator or a second
// integer formatter would be two places to change.
func idNew() string { return id.New() }

// estimateAssuranceTokens is the token estimator, and saying so in the record is
// the point: `ChatToModel` returns text, not usage (doc/301 §14.E E19), so
// `used_tokens` is an estimate and `metrics_json.budget.token_source` names the
// estimator that produced it. The alternative — pretending the number is measured
// — would make a 30% budget overshoot look like a bug instead of an estimator
// difference.
//
// CJK is counted at ~1.6 runes per token and ASCII at ~4 chars per token, which is
// the conservative half of the usual rule of thumb for Qwen-class tokenizers.
func estimateAssuranceTokens(text string) int64 {
	if text == "" {
		return 0
	}
	cjk, ascii := 0, 0
	for _, r := range text {
		if r > 0x7f {
			cjk++
			continue
		}
		ascii++
	}
	tokens := float64(cjk)/1.6 + float64(ascii)/4.0
	if tokens < 1 {
		return 1
	}
	return int64(tokens) + 1
}

// newReportAssuranceEvaluator picks the evaluator the run was frozen with.
//
// The decision is per run, not per claim: two claims of one run evaluated by two
// different models would make the run's accuracy unauditable.
func (s *Service) newReportAssuranceEvaluator(run *model.ReportAssuranceRun) reportAssuranceEvaluator {
	if run.Mode == model.ReportAssuranceModeCitationAudit {
		// `citation_audit` is QA-2's offline sample: layer ① only, no model call, no
		// cost. It exists so sample checks can run on a schedule without a budget.
		return &reportAssuranceRuleEvaluator{}
	}
	if strings.TrimSpace(run.ReviewModelID) == "" {
		// No reviewer configured: the mechanical layers still produce a verdict for
		// every claim, and the semantic layer abstains. That is a tier_0 run, not a
		// failure (§5.1.1).
		return &reportAssuranceRuleEvaluator{}
	}
	return &reportAssuranceModelEvaluator{
		svc: s, modelID: run.ReviewModelID, provider: run.ReviewProvider,
		instance: run.ReviewDeployment, independence: run.Independence,
	}
}

// reportAssuranceRuleEvaluator answers from the mechanical layers alone. It is the
// default and the fallback: `verdict` is what the quote/citation checks can prove.
type reportAssuranceRuleEvaluator struct{}

func (reportAssuranceRuleEvaluator) ModelID() string      { return "" }
func (reportAssuranceRuleEvaluator) PromptHash() string   { return "" }
func (reportAssuranceRuleEvaluator) Method() string       { return model.ReportEvaluationMethodRule }
func (reportAssuranceRuleEvaluator) Independence() string { return model.ReportIndependenceUnknown }

// Evaluate returns "no opinion": the pipeline's own layers already decided, and
// reporting `supported` here would be the fabricated verdict §5.1 warns about.
func (reportAssuranceRuleEvaluator) Evaluate(
	ctx context.Context, in reportAssuranceEvalInput,
) (reportAssuranceEvalOutcome, error) {
	return reportAssuranceEvalOutcome{
		Verdict:   model.ReportVerdictAbstained,
		Rationale: "no review model configured; only the mechanical quote/citation layers ran",
	}, nil
}

// reportAssuranceModelEvaluator asks a second model whether the claim follows from
// the quoted text (doc/301 §5.4).
type reportAssuranceModelEvaluator struct {
	svc          *Service
	modelID      string
	provider     string
	instance     string
	independence string
}

func (e *reportAssuranceModelEvaluator) ModelID() string      { return e.modelID }
func (e *reportAssuranceModelEvaluator) PromptHash() string   { return reportAssurancePromptHash() }
func (e *reportAssuranceModelEvaluator) Method() string       { return model.ReportEvaluationMethodSemantic }
func (e *reportAssuranceModelEvaluator) Independence() string { return e.independence }

// reportAssurancePromptHash hashes the prompt *template*, not the filled prompt:
// the claim and the quote already enter the cache key separately, and hashing the
// filled text twice would just double the collision surface.
func reportAssurancePromptHash() string {
	sum := sha256.Sum256([]byte(reportAssurancePromptVersion + "\x00" + reportAssuranceSystemPrompt))
	return hex.EncodeToString(sum[:])
}

const reportAssuranceSystemPrompt = `你是企业知识库报告的证据复核员。你的唯一任务是判断：给定的「证据原文」是否支持「报告断言」。
严格输出一行 JSON，不要输出任何其他内容：
{"verdict":"supported|partially_supported|unsupported|conflicting|insufficient_evidence","relevance":"on_topic|off_topic","confidence":0.0,"rationale":"不超过80字的中文理由","quote":"证据原文中支撑该断言的最短连续片段"}
规则：
1. 只依据给出的证据原文判断，不得使用任何外部知识。
2. quote 必须逐字取自证据原文；若证据原文中不存在可直接引用的片段，quote 置为空字符串。
3. 证据与断言矛盾时用 conflicting；证据不足以下判断时用 insufficient_evidence；证据只支持断言的一部分（范围、程度、时间被放大）时用 partially_supported。
4. rationale 说明"为什么"，不要复述断言。`

// Evaluate calls the review model and parses the strict answer.
func (e *reportAssuranceModelEvaluator) Evaluate(
	ctx context.Context, in reportAssuranceEvalInput,
) (reportAssuranceEvalOutcome, error) {
	prompt := buildReportAssurancePrompt(in)
	answer, err := e.svc.RAGFlow.ChatToModel(ctx, e.provider, e.instance, e.modelID, prompt, false, false)
	if err != nil {
		return reportAssuranceEvalOutcome{}, err
	}
	outcome := parseReportAssuranceAnswer(answer)
	outcome.TokensIn = estimateAssuranceTokens(prompt)
	outcome.TokensOut = estimateAssuranceTokens(answer)
	return outcome, nil
}

// buildReportAssurancePrompt fills the template. The section title is included
// because "这句话在本节语境下是否成立" is a real question the claim text alone
// cannot answer, and it is part of the cache key so the extra context is accounted
// for.
func buildReportAssurancePrompt(in reportAssuranceEvalInput) string {
	var builder strings.Builder
	builder.WriteString(reportAssuranceSystemPrompt)
	builder.WriteString("\n\n【章节】")
	builder.WriteString(in.Section.Title)
	builder.WriteString("\n【报告断言】")
	builder.WriteString(in.Claim.ClaimText)
	builder.WriteString("\n【证据原文】")
	builder.WriteString(in.Quote)
	if in.CandidateCount > 0 {
		// Telling the model that the pool was widened changes what
		// `insufficient_evidence` means: it was allowed to look beyond the citation
		// and still found nothing.
		builder.WriteString("\n【说明】除上述引文外，本节还冻结了 ")
		builder.WriteString(itoa(in.CandidateCount))
		builder.WriteString(" 条未被引用但可能相关的片段；若引文不足以支持断言，请直接判定 insufficient_evidence。")
	}
	return builder.String()
}

// parseReportAssuranceAnswer is deliberately lenient about *wrapper* and strict
// about *content*: a model that wraps the JSON in a code fence still told us
// something, but a model that omits `verdict` told us nothing, and an unparsable
// answer becomes `abstained/model_uncertain` rather than a guessed verdict.
func parseReportAssuranceAnswer(answer string) reportAssuranceEvalOutcome {
	payload := extractAssuranceJSON(answer)
	if payload == "" {
		return reportAssuranceEvalOutcome{
			Verdict:   model.ReportVerdictAbstained,
			Rationale: "review model did not return parsable JSON",
		}
	}
	var parsed struct {
		Verdict    string   `json:"verdict"`
		Relevance  string   `json:"relevance"`
		Confidence *float64 `json:"confidence"`
		Rationale  string   `json:"rationale"`
		Quote      string   `json:"quote"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return reportAssuranceEvalOutcome{
			Verdict:   model.ReportVerdictAbstained,
			Rationale: "review model JSON did not match the contract",
		}
	}
	verdict := strings.TrimSpace(parsed.Verdict)
	if !reportAssuranceVerdictFromModel(verdict) {
		// The closed set is enforced here and not by trust: `not_required` and
		// `not_evaluated` are policy decisions the model must not be able to make.
		return reportAssuranceEvalOutcome{
			Verdict:   model.ReportVerdictAbstained,
			Rationale: "review model returned a verdict outside the closed set: " + verdict,
		}
	}
	relevance := strings.TrimSpace(parsed.Relevance)
	if relevance != model.ReportRelevanceOnTopic && relevance != model.ReportRelevanceOffTopic {
		relevance = ""
	}
	outcome := reportAssuranceEvalOutcome{
		Verdict: verdict, Relevance: relevance,
		Rationale: strings.TrimSpace(parsed.Rationale),
	}
	if parsed.Confidence != nil {
		confidence := *parsed.Confidence
		if confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}
		outcome.Confidence = &confidence
	}
	// The model's quote is *not* trusted as a citation: it is only used to check
	// whether the reviewer can point at anything at all. Layer ① already decided
	// traceability from the stored snippet (§5.1.1).
	outcome.EvidenceQuote = strings.TrimSpace(parsed.Quote)
	return outcome
}

// reportAssuranceVerdictFromModel is the subset a model may return. `outdated` is
// absent on purpose: deciding that evidence is stale needs the document's validity
// window, which is a rule-layer question, not a reading question.
func reportAssuranceVerdictFromModel(verdict string) bool {
	switch verdict {
	case model.ReportVerdictSupported, model.ReportVerdictPartiallySupported,
		model.ReportVerdictUnsupported, model.ReportVerdictConflicting,
		model.ReportVerdictInsufficient:
		return true
	}
	return false
}

// extractAssuranceJSON finds the outermost JSON object in the answer.
func extractAssuranceJSON(answer string) string {
	start := strings.Index(answer, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(answer); index++ {
		character := answer[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return answer[start : index+1]
			}
		}
	}
	return ""
}

// notifyReportAssurance publishes the run's terminal event.
//
// It goes to the notify stream rather than the report job event stream: an
// assurance run is a *separate* subject, and appending to
// `rgx_report_job_event` would make a report's replay show an audit that never
// ran as part of that job (doc/301 §7.4).
func notifyReportAssurance(
	ctx context.Context, run *model.ReportAssuranceRun, status, eligibility string,
	metrics reportAssuranceMetrics, severity string,
) {
	notify.Emit(ctx, notify.Event{
		Title: "report assurance " + status, Severity: severity, Type: "report.assurance." + status,
		TenantID: run.TenantID, Resource: "report", ResourceID: run.ReportID,
		ObjectType: "report", Action: "assurance_" + status,
		Detail: run.ID + "|" + eligibility, OccurredAt: time.Now().UTC(),
		Fields: map[string]string{
			"run_id": run.ID, "revision": itoa(int(run.Revision)),
			"release_eligibility": eligibility, "status": status,
			"coverage": ratioText(metrics.Coverage), "denominator": itoa(metrics.Denominator),
			"abstained": itoa(metrics.Verdicts[model.ReportVerdictAbstained]),
		},
	})
}

// reportAssuranceReconcileInterval is how often the sweep re-arms and closes.
// One minute, matching the report finalize reconciler: the sweep is a two-query
// read when there is nothing to do.
const reportAssuranceReconcileInterval = time.Minute

// ScheduleReportAssuranceReconciler enqueues the next sweep, at most one at a time
// (doc/301 §6.6). The `CountActiveJobsExcept` guard is what keeps a fast sweep from
// stacking jobs behind itself.
func (s *Service) ScheduleReportAssuranceReconciler(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindReportAssuranceReconcile, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	runAfter := time.Now().UTC().Add(reportAssuranceReconcileInterval)
	return s.Runner.Enqueue(ctx, model.JobKindReportAssuranceReconcile,
		"report_assurance_reconcile:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0)
}

// reportAssuranceReconcileWorker runs one sweep and schedules the next, the same
// posture as `reportFinalizeReconcileWorker`.
type reportAssuranceReconcileWorker struct{ svc *Service }

func (w *reportAssuranceReconcileWorker) Kind() string {
	return model.JobKindReportAssuranceReconcile
}

func (w *reportAssuranceReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	if err := w.svc.reconcileReportAssuranceRuns(ctx); err != nil {
		logger.Warn("report assurance reconcile failed", "job_id", job.ID, "error", err)
		return err
	}
	if _, err := w.svc.ScheduleReportAssuranceReconciler(ctx, job.ID); err != nil {
		logger.Warn("report assurance reconcile reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}

// reconcileReportAssuranceRuns closes runs whose worker died and re-arms runs that
// never got a job (doc/301 §6.6).
//
// Two failure shapes, two different answers:
//
//   - `queued` with no runtime job → re-enqueue. The run was created inside a
//     transaction that also enqueued; if the enqueue lost, nothing will ever start
//     it, and a re-arm is idempotent because the job key is `assurance:<run_id>`.
//   - `running` past the lease → close as `failed`. Re-arming a running run would
//     let two workers write evaluations to one run id, and the append-only table
//     would then hold two verdicts for the same claim — the exact ambiguity the
//     unique `(run_id, claim_id, attempt)` index exists to prevent.
func (s *Service) reconcileReportAssuranceRuns(ctx context.Context) error {
	now := time.Now().UTC()
	// The monthly allowance reconciliation rides this sweep (doc/301 T25) because it is
	// the only periodic assurance loop the product runs; it throttles itself, so a
	// per-minute sweep does not turn into a per-minute full-tenant book comparison.
	s.reconcileAssuranceSettlementThrottled(ctx, now)

	stale, err := s.Store.ListStaleAssuranceRuns(ctx, now.Add(-reportAssuranceWorkerLease))
	if err != nil {
		return err
	}
	for index := range stale {
		run := &stale[index]
		if run.Status == model.ReportAssuranceStatusQueued {
			if _, err := s.Store.EnqueueJob(ctx, reportAssuranceJob(run)); err != nil {
				// Re-arming is best effort; the next sweep retries. `EnqueueJob` is
				// idempotent on the key, so a duplicate attempt is a no-op.
				continue
			}
			continue
		}
		if err := s.failReportAssurance(ctx, run, "lease_expired",
			assuranceLeaseError("assurance run "+run.ID+" exceeded its worker lease")); err != nil {
			continue
		}
	}
	return nil
}

// assuranceLeaseError is a plain error value: it only ever reaches the log and
// `failure_reason`, never an HTTP response, so it does not need an error code.
type assuranceLeaseError string

func (e assuranceLeaseError) Error() string { return string(e) }

// runeCountOf is the structure layer's length measure.
func runeCountOf(text string) int { return utf8.RuneCountInString(text) }

// ratioText renders a nullable rate for a log/alert field. `nil` prints as "n/a"
// rather than "0.000": an empty denominator is not a zero rate (§4.3).
func ratioText(ratio *float64) string {
	if ratio == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*ratio, 'f', 3, 64)
}
