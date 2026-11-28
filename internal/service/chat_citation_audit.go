package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Weekly chat citation audit — doc/301 §5.10 QA-2 / T19.
//
// What it is: every week, per tenant, a bounded sample of the chat turns that
// carried citations is re-read and each citation is checked against the answer it
// was attached to. Turns whose citations do not support the answer become badcase
// candidates in the existing knowledge-ops queue.
//
// What it is NOT:
//
//   - it is not the report assurance pipeline. `rgx_report_*` is never written: a
//     chat turn and a report section are two populations, and the assurance metrics
//     count the second one (T19 DoD).
//   - it is not a second queue. The disposition is written back onto
//     `rgx_knowledge_ops_event`, so the board's existing filters keep working.
//   - it is not a model call by default. The mechanical layer (does the answer
//     actually use the cited text?) costs nothing; the semantic arm is opt-in and
//     capped by `knowledge_audit.max_tokens`.
const (
	// chatCitationAuditMethodVersion names the judgement rules. A later rule change
	// bumps this instead of silently reinterpreting past weekly counters.
	chatCitationAuditMethodVersion = "qa2.v1"

	// chatCitationAuditDefaultSample mirrors the settings default.
	chatCitationAuditDefaultSample = 50

	// chatCitationAuditMaxCitations bounds the per-turn work: a turn citing 40
	// chunks would otherwise dominate one week's budget. The remainder is not
	// "supported", it is simply not judged — and the turn still reports the
	// findings it has.
	chatCitationAuditMaxCitations = 8

	// chatCitationAuditAnswerPromptLimit bounds how much answer text enters a
	// semantic prompt. The judgement is about a sentence, not a document.
	chatCitationAuditAnswerPromptLimit = 1200

	// Support thresholds for the mechanical layer. `supportedAt` is deliberately
	// generous (the answer may legitimately paraphrase) and `unsupportedBelow` is
	// deliberately strict: between the two the row says "unverifiable" instead of
	// guessing, which is the same discipline the quote layer uses.
	chatCitationSupportedAt      = 0.5
	chatCitationUnsupportedBelow = 0.15

	// chatCitationVerbatimMinRunes is the shortest excerpt for which "the answer
	// contains the quote" is a claim the shortcut may make. It is counted in **runes**:
	// the number was always meant as characters (8 of them), but it was compared against
	// `len()` — bytes — and three Chinese characters are nine bytes, so a three-rune
	// fragment qualified and the board labelled it `answer_contains_quote` (doc/319
	// R-05). In a product whose documents are Chinese, the byte reading lowered the bar
	// to roughly three characters.
	chatCitationVerbatimMinRunes = 8
)

// ChatCitationAuditSettings is the `knowledge_audit.*` slice of the system
// settings snapshot.
type ChatCitationAuditSettings struct {
	Enabled bool
	// SamplePerWeek is how many turns one week's run may judge.
	SamplePerWeek int
	// MaxTokens caps the semantic arm. 0 means "mechanical only", which is the
	// default: the audit must be affordable on day one.
	MaxTokens int64
	// Semantic arms the model arm. It is only effective together with a model and
	// a positive MaxTokens, and the run freezes the *effective* value.
	Semantic bool
	Model    string
	// SweepIntervalHours is the cadence of the weekly sweep.
	SweepIntervalHours int
}

// chatCitationAuditSettingsFromSnapshot is the pure half of the resolver, so a
// test can state the mapping without a database.
func chatCitationAuditSettingsFromSnapshot(snapshot map[string]interface{}) ChatCitationAuditSettings {
	settings := ChatCitationAuditSettings{
		SamplePerWeek:      chatCitationAuditDefaultSample,
		SweepIntervalHours: 6,
	}
	group := settingGroup(snapshot, "knowledge_audit")
	if group == nil {
		return settings
	}
	if value, ok := group["enabled"].(bool); ok {
		settings.Enabled = value
	}
	if value, ok := settingNumberOf(group, "sample_per_week"); ok && value >= 1 {
		settings.SamplePerWeek = int(value)
	}
	if value, ok := settingNumberOf(group, "max_tokens"); ok && value >= 0 {
		settings.MaxTokens = int64(value)
	}
	if value, ok := group["semantic"].(bool); ok {
		settings.Semantic = value
	}
	if value, ok := settingNumberOf(group, "sweep_interval_hours"); ok && value >= 1 {
		settings.SweepIntervalHours = int(value)
	}
	settings.Model = settingStringOf(group, "model")
	return settings
}

func (s *Service) chatCitationAuditSettingsOf(ctx context.Context) (ChatCitationAuditSettings, error) {
	revision, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return ChatCitationAuditSettings{}, err
	}
	var snapshot map[string]interface{}
	if revision == nil {
		snapshot = defaultSystemSettingsSnapshot()
	} else if err := json.Unmarshal([]byte(revision.SnapshotJSON), &snapshot); err != nil {
		return ChatCitationAuditSettings{}, httperr.Internal("invalid system settings snapshot")
	}
	return chatCitationAuditSettingsFromSnapshot(withDefaultSettingGroups(snapshot)), nil
}

// chatCitationAuditWindow returns the *previous completed* ISO week. It never
// returns the week that is still running: a partial week would be counted as a
// week, and the next run for that period is blocked by the idempotency guard, so
// a Monday trigger would permanently hide the rest of that week.
func chatCitationAuditWindow(now time.Time) (periodStart string, from, to time.Time) {
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(day.Weekday()) + 6) % 7 // Monday-based week start
	weekStart := day.AddDate(0, 0, -offset)
	from = weekStart.AddDate(0, 0, -7)
	to = weekStart
	return from.Format("2006-01-02"), from, to
}

func chatCitationAuditKey(tenantID, periodStart string) string {
	return "chat_citation_audit:" + tenantID + ":" + periodStart
}

// RunChatCitationAudit judges one tenant's previous completed week.
//
// It is idempotent per (tenant, week): the second caller — a replayed job, an
// impatient operator, two schedulers — receives the existing run with created =
// false and nothing is judged twice. Concurrency is handled by the unique index,
// not by a process lock, so a restart mid-week cannot double-count.
func (s *Service) RunChatCitationAudit(ctx context.Context, tenantID, triggeredBy string) (*model.ChatCitationAuditRun, bool, error) {
	if tenantID == "" {
		return nil, false, httperr.BadRequest(40000, "tenant is required for a citation audit")
	}
	settings, err := s.chatCitationAuditSettingsOf(ctx)
	if err != nil {
		return nil, false, err
	}
	if !settings.Enabled {
		// The toggle is the whitelist, exactly like `report.assurance_enabled`: a
		// tenant that never opted in must not spend tokens on a model call, and must
		// not accumulate audit state it never asked for.
		return nil, false, httperr.New(400, 40000, "citation audit is not enabled for this tenant")
	}
	periodStart, from, to := chatCitationAuditWindow(time.Now().UTC())
	run := &model.ChatCitationAuditRun{
		ID: id.New(), TenantID: tenantID, PeriodStart: periodStart,
		WindowFrom: from, WindowTo: to,
		MethodVersion:  chatCitationAuditMethodVersion,
		SampleTarget:   settings.SamplePerWeek,
		BudgetTokens:   settings.MaxTokens,
		Semantic:       settings.Semantic && settings.Model != "" && settings.MaxTokens > 0,
		Status:         model.ChatCitationAuditRunRunning,
		IdempotencyKey: chatCitationAuditKey(tenantID, periodStart),
		TriggeredBy:    triggeredBy,
		StartedAt:      time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}
	created, err := s.Store.CreateChatCitationAuditRunIfAbsent(ctx, run)
	if err != nil {
		return nil, false, err
	}
	if !created {
		existing, err := s.Store.GetChatCitationAuditRunByPeriod(ctx, tenantID, periodStart)
		if err != nil {
			return nil, false, err
		}
		if existing == nil {
			// The insert lost but the row is gone: not a state this code can explain,
			// and returning nil would look like "no run this week" to the caller.
			return nil, false, httperr.Internal("citation audit run not found after conflict")
		}
		return existing, false, nil
	}
	return s.executeChatCitationAudit(ctx, run, settings)
}

// executeChatCitationAudit is the body of one run. It never returns an error for a
// judgement problem: a turn that cannot be judged is a `skipped` disposition with
// a reason, because "the audit crashed" and "the audit found nothing" must not
// look the same in the run list.
func (s *Service) executeChatCitationAudit(
	ctx context.Context, run *model.ChatCitationAuditRun, settings ChatCitationAuditSettings,
) (*model.ChatCitationAuditRun, bool, error) {
	turns, err := s.Store.ListChatCitationAuditTurns(ctx, run.TenantID, run.WindowFrom, run.WindowTo, settings.SamplePerWeek)
	if err != nil {
		return s.failChatCitationAudit(ctx, run, err)
	}
	budget := int64(0)
	if run.Semantic {
		budget = settings.MaxTokens
	}
	now := time.Now().UTC()
	findings := make([]model.ChatCitationAuditFinding, 0, len(turns)*2)
	// One bucket per (status, reason): the write-back is one UPDATE per bucket, so a
	// week's turns cost three to five statements rather than one per turn.
	type bucket struct {
		status  string
		reason  string
		eventID []string
	}
	buckets := map[string]*bucket{}
	addBucket := func(status, reason, eventID string) {
		key := status + "\x00" + reason
		entry, ok := buckets[key]
		if !ok {
			entry = &bucket{status: status, reason: reason}
			buckets[key] = entry
		}
		entry.eventID = append(entry.eventID, eventID)
	}
	run.Sampled = len(turns)
	budgetExhausted := false
	for _, turn := range turns {
		outcome := s.judgeChatCitationTurn(ctx, run, turn, settings, &budget)
		if outcome.BudgetExhausted {
			budgetExhausted = true
		}
		run.TokensIn += outcome.TokensIn
		run.TokensOut += outcome.TokensOut
		run.Findings += len(outcome.Findings)
		findings = append(findings, outcome.Findings...)
		switch outcome.Status {
		case model.ChatCitationAuditPassed:
			run.Passed++
			run.Judged++
		case model.ChatCitationAuditFailed:
			run.Failed++
			run.Judged++
		default:
			run.Skipped++
		}
		addBucket(outcome.Status, outcome.Reason, turn.EventID)
	}
	if err := s.Store.InsertChatCitationAuditFindings(ctx, findings); err != nil {
		return s.failChatCitationAudit(ctx, run, err)
	}
	for _, entry := range buckets {
		if err := s.Store.MarkChatCitationAudit(ctx, run.TenantID, run.ID, entry.status, entry.reason, entry.eventID, now); err != nil {
			return s.failChatCitationAudit(ctx, run, err)
		}
	}
	run.Status = model.ChatCitationAuditRunCompleted
	if budgetExhausted {
		run.Status = model.ChatCitationAuditRunBudgetExhausted
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if err := s.Store.FinishChatCitationAuditRun(ctx, run); err != nil {
		return nil, true, err
	}
	return run, true, nil
}

func (s *Service) failChatCitationAudit(
	ctx context.Context, run *model.ChatCitationAuditRun, cause error,
) (*model.ChatCitationAuditRun, bool, error) {
	run.Status = model.ChatCitationAuditRunFailed
	run.FailureReason = truncateKnowledgeText(cause.Error(), 512)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if err := s.Store.FinishChatCitationAuditRun(ctx, run); err != nil {
		logger.Warn("citation audit failure write failed", "run_id", run.ID, "error", err)
	}
	return run, true, nil
}

// chatCitationTurnOutcome is one turn's disposition plus the findings behind it.
type chatCitationTurnOutcome struct {
	Status          string
	Reason          string
	Findings        []model.ChatCitationAuditFinding
	TokensIn        int64
	TokensOut       int64
	BudgetExhausted bool
}

// judgeChatCitationTurn is the per-turn judgement. The turn's status summarises
// its citations:
//
//   - any unsupported citation ⇒ `failed` (a badcase candidate);
//   - every citation supported ⇒ `passed`;
//   - otherwise `skipped`, with a reason. `skipped` is never `passed`.
func (s *Service) judgeChatCitationTurn(
	ctx context.Context, run *model.ChatCitationAuditRun, turn model.ChatCitationAuditTurn,
	settings ChatCitationAuditSettings, budget *int64,
) chatCitationTurnOutcome {
	answer := trimSpace(turn.Answer)
	if answer == "" {
		return chatCitationTurnOutcome{Status: model.ChatCitationAuditSkipped, Reason: model.ChatCitationReasonNoAnswerText}
	}
	var citations []model.AnswerCitation
	if trimSpace(turn.CitationsJSON) == "" {
		return chatCitationTurnOutcome{Status: model.ChatCitationAuditSkipped, Reason: model.ChatCitationReasonNoCitations}
	}
	if err := json.Unmarshal([]byte(turn.CitationsJSON), &citations); err != nil {
		return chatCitationTurnOutcome{Status: model.ChatCitationAuditSkipped, Reason: model.ChatCitationReasonEvidenceMissing}
	}
	if len(citations) == 0 {
		return chatCitationTurnOutcome{Status: model.ChatCitationAuditSkipped, Reason: model.ChatCitationReasonNoCitations}
	}
	if len(citations) > chatCitationAuditMaxCitations {
		citations = citations[:chatCitationAuditMaxCitations]
	}
	outcome := chatCitationTurnOutcome{Status: model.ChatCitationAuditPassed}
	unsupported := 0
	unverifiable := 0
	semanticBudgetHit := false
	for _, citation := range citations {
		finding := model.ChatCitationAuditFinding{
			TenantID: run.TenantID, RunID: run.ID, EventID: turn.EventID,
			RequestID: turn.RequestID, CitationID: citation.ID,
			DocumentID: citation.DocumentID, ChunkID: citation.ChunkID,
			Method: model.ChatCitationMethodDeterministic,
		}
		excerpt := trimSpace(citation.CitedContentExcerpt)
		if excerpt == "" {
			finding.Verdict = model.ChatCitationVerdictUnverifiable
			finding.Reason = model.ChatCitationReasonExcerptEmpty
			unverifiable++
			outcome.Findings = append(outcome.Findings, finding)
			continue
		}
		// Integrity first: the delivery layer froze `sha256(excerpt)` with the
		// citation. A mismatch means the excerpt text changed after delivery, so the
		// row is not evidence any more and the mechanical layer must not reward it.
		finding.ExcerptHash = sha256Hex(excerpt)
		finding.ExcerptHashOK = citation.CitationContentHash == "" || citation.CitationContentHash == finding.ExcerptHash
		if !finding.ExcerptHashOK {
			finding.Verdict = model.ChatCitationVerdictUnsupported
			finding.Reason = model.ChatCitationReasonExcerptHashMismatch
			finding.Score = 0
			unsupported++
			outcome.Findings = append(outcome.Findings, finding)
			continue
		}
		score, verbatim := chatCitationSupport(answer, excerpt)
		finding.Score = score
		switch {
		case verbatim:
			finding.Verdict = model.ChatCitationVerdictSupported
			finding.Reason = model.ChatCitationReasonAnswerContainsQuote
		case score >= chatCitationSupportedAt:
			finding.Verdict = model.ChatCitationVerdictSupported
			finding.Reason = model.ChatCitationReasonAnswerOverlaps
		case score < chatCitationUnsupportedBelow:
			finding.Verdict = model.ChatCitationVerdictUnsupported
			finding.Reason = model.ChatCitationReasonExcerptAbsent
			unsupported++
		default:
			// Between the two thresholds the mechanical layer refuses to decide.
			verdict, reason, tokensIn, tokensOut, hit := s.judgeChatCitationSemantically(
				ctx, run, answer, excerpt, settings, budget)
			finding.Verdict, finding.Reason = verdict, reason
			// The method names the layer that *decided*, not the layer that ran: a
			// semantic call that could not be afforded leaves the verdict to the
			// mechanical layer, and recording it as `semantic` would overstate what
			// the model actually contributed.
			if reason == model.ChatCitationReasonSemanticSupported ||
				reason == model.ChatCitationReasonSemanticUnsupported ||
				reason == model.ChatCitationReasonSemanticInconclusive {
				finding.Method = model.ChatCitationMethodSemantic
			}
			outcome.TokensIn += tokensIn
			outcome.TokensOut += tokensOut
			if hit {
				semanticBudgetHit = true
			}
			if verdict == model.ChatCitationVerdictUnsupported {
				unsupported++
			} else if verdict == model.ChatCitationVerdictUnverifiable {
				unverifiable++
			}
		}
		outcome.Findings = append(outcome.Findings, finding)
	}
	switch {
	case unsupported > 0:
		outcome.Status = model.ChatCitationAuditFailed
		outcome.Reason = model.ChatCitationReasonExcerptAbsent
		for _, finding := range outcome.Findings {
			if finding.Verdict == model.ChatCitationVerdictUnsupported {
				outcome.Reason = finding.Reason
				break
			}
		}
	case unverifiable > 0:
		outcome.Status = model.ChatCitationAuditSkipped
		outcome.Reason = model.ChatCitationReasonWeakOverlap
		if semanticBudgetHit {
			outcome.Reason = model.ChatCitationReasonBudgetExhausted
		}
	default:
		outcome.Status = model.ChatCitationAuditPassed
		outcome.Reason = model.ChatCitationReasonAnswerOverlaps
		for _, finding := range outcome.Findings {
			if finding.Reason == model.ChatCitationReasonAnswerContainsQuote {
				outcome.Reason = model.ChatCitationReasonAnswerContainsQuote
				break
			}
		}
	}
	outcome.BudgetExhausted = semanticBudgetHit
	return outcome
}

// judgeChatCitationSemantically is the opt-in model arm. Every path that cannot
// decide returns `unverifiable` — never `supported`: an unaffordable or failed
// call must not become a pass.
func (s *Service) judgeChatCitationSemantically(
	ctx context.Context, run *model.ChatCitationAuditRun, answer, excerpt string,
	settings ChatCitationAuditSettings, budget *int64,
) (verdict, reason string, tokensIn, tokensOut int64, budgetHit bool) {
	if !run.Semantic || settings.Model == "" {
		return model.ChatCitationVerdictUnverifiable, model.ChatCitationReasonWeakOverlap, 0, 0, false
	}
	prompt := buildChatCitationAuditPrompt(answer, excerpt)
	estimate := estimateAssuranceTokens(prompt)
	if *budget <= 0 || estimate >= *budget {
		return model.ChatCitationVerdictUnverifiable, model.ChatCitationReasonBudgetExhausted, 0, 0, true
	}
	*budget -= estimate
	reply, err := s.RAGFlow.ChatToModel(ctx, "", "", settings.Model, prompt, false, false)
	if err != nil {
		// A provider failure is not a wrong verdict. The estimate stays charged
		// because the request may have been billed upstream; under-reporting spend
		// would make the cap unenforceable.
		logger.Warn("citation audit semantic call failed", "run_id", run.ID, "error", err)
		return model.ChatCitationVerdictUnverifiable, model.ChatCitationReasonSemanticInconclusive, estimate, 0, false
	}
	outTokens := estimateAssuranceTokens(reply)
	tokensOut = outTokens
	tokensIn = estimate
	parsed, ok := parseChatCitationAuditAnswer(reply)
	if !ok {
		return model.ChatCitationVerdictUnverifiable, model.ChatCitationReasonSemanticInconclusive, tokensIn, tokensOut, false
	}
	switch parsed {
	case model.ChatCitationVerdictSupported:
		return model.ChatCitationVerdictSupported, model.ChatCitationReasonSemanticSupported, tokensIn, tokensOut, false
	case model.ChatCitationVerdictUnsupported:
		return model.ChatCitationVerdictUnsupported, model.ChatCitationReasonSemanticUnsupported, tokensIn, tokensOut, false
	default:
		return model.ChatCitationVerdictUnverifiable, model.ChatCitationReasonSemanticInconclusive, tokensIn, tokensOut, false
	}
}

// chatCitationAuditSystemPrompt asks one narrow question. It is a different prompt
// from the report reviewer's on purpose: a chat answer is short and may paraphrase,
// so "is the cited text the basis of this answer" is not the same question as
// "does this evidence support this report claim".
const chatCitationAuditSystemPrompt = `你是企业知识库问答的引用检查员。你的唯一任务是判断：给出的「引用片段」是不是「答案」所依据的内容。
严格输出一行 JSON，不要输出任何其他内容：
{"verdict":"supported|unsupported|inconclusive","confidence":0.0,"rationale":"不超过60字的中文理由"}
规则：
1. 只依据给出的引用片段与答案判断，不得使用任何外部知识。
2. 答案中的结论能在引用片段中找到依据时用 supported；引用片段与答案内容无关时用 unsupported。
3. 无法确定时用 inconclusive，不要猜测。`

func buildChatCitationAuditPrompt(answer, excerpt string) string {
	return chatCitationAuditSystemPrompt + "\n\n【引用片段】\n" + truncateKnowledgeText(excerpt, 240) +
		"\n\n【答案】\n" + truncateKnowledgeText(answer, chatCitationAuditAnswerPromptLimit)
}

// parseChatCitationAuditAnswer reads the strict answer. Anything unparsable is
// "no verdict" rather than a guess.
func parseChatCitationAuditAnswer(reply string) (string, bool) {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return "", false
	}
	var parsed struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &parsed); err != nil {
		return "", false
	}
	switch parsed.Verdict {
	case model.ChatCitationVerdictSupported, model.ChatCitationVerdictUnsupported:
		return parsed.Verdict, true
	case "inconclusive", model.ChatCitationVerdictUnverifiable:
		return model.ChatCitationVerdictUnverifiable, true
	default:
		return "", false
	}
}

// SweepChatCitationAudit runs the weekly sample for every tenant that asked
// questions in the window.
//
// Throttling is process-local and deliberate: the sample is cheap but not free,
// and two schedulers on one process must not run two sweeps. A restart may repeat
// one sweep, which is harmless because the run row is the idempotency guard.
func (s *Service) SweepChatCitationAudit(ctx context.Context) error {
	settings, err := s.chatCitationAuditSettingsOf(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	interval := time.Duration(settings.SweepIntervalHours) * time.Hour
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	now := time.Now().UTC()
	s.chatCitationAuditSweepMu.Lock()
	if !s.chatCitationAuditSweptAt.IsZero() && now.Sub(s.chatCitationAuditSweptAt) < interval {
		s.chatCitationAuditSweepMu.Unlock()
		return nil
	}
	s.chatCitationAuditSweptAt = now
	s.chatCitationAuditSweepMu.Unlock()

	_, from, to := chatCitationAuditWindow(now)
	tenants, err := s.Store.ListChatCitationAuditTenants(ctx, from, to)
	if err != nil {
		return err
	}
	for _, tenantID := range tenants {
		run, created, err := s.RunChatCitationAudit(ctx, tenantID, SystemTenantID)
		if err != nil {
			logger.Warn("citation audit sweep failed for tenant", "tenant_id", tenantID, "error", err)
			continue
		}
		if created && run != nil && run.Failed > 0 {
			logger.Info("citation audit found unsupported citations",
				"tenant_id", tenantID, "run_id", run.ID, "failed_turns", run.Failed)
		}
	}
	return nil
}

// ChatCitationAuditRuns returns the recent weekly runs for the board.
func (s *Service) ChatCitationAuditRuns(ctx context.Context, tenantID string, limit int) ([]model.ChatCitationAuditRun, error) {
	if tenantID == "" {
		return nil, httperr.BadRequest(40000, "tenant is required")
	}
	return s.Store.ListChatCitationAuditRuns(ctx, tenantID, limit)
}

// ChatCitationAuditFindings returns one page of a run's citation verdicts.
func (s *Service) ChatCitationAuditFindings(ctx context.Context, tenantID, runID string, page, pageSize int) ([]model.ChatCitationAuditFinding, int64, error) {
	if tenantID == "" || runID == "" {
		return nil, 0, httperr.BadRequest(40000, "tenant and run are required")
	}
	return s.Store.ListChatCitationAuditFindings(ctx, tenantID, runID, page, pageSize)
}

// chatCitationSupport is the mechanical layer: does `answer` actually use the
// cited excerpt?
//
// The measure is bigram coverage of the excerpt over the answer (CJK text has no
// whitespace, so character bigrams are the portable token), plus a verbatim
// shortcut. It answers exactly one question — "is the cited content present in
// the answer" — and deliberately not "is the answer correct": a wrong answer built
// from a real excerpt is a different badcase family, judged by the evaluation arm.
func chatCitationSupport(answer, excerpt string) (score float64, verbatim bool) {
	normAnswer, _ := normalizeWithMap(answer)
	normExcerpt, _ := normalizeWithMap(excerpt)
	if normAnswer == "" || normExcerpt == "" {
		return 0, false
	}
	// The verbatim shortcut is a stronger claim than "these bigrams overlap": it says
	// the answer reproduces the cited text. That claim needs an excerpt long enough for
	// "contains" to be meaningful, and "long enough" is measured in runes, not bytes
	// (doc/319 R-05) — the excerpt is Chinese, and `len()` counts UTF-8 bytes.
	if utf8RuneCount(normExcerpt) >= chatCitationVerbatimMinRunes && strings.Contains(normAnswer, normExcerpt) {
		return 1, true
	}
	answerGrams := map[string]struct{}{}
	runes := []rune(normAnswer)
	for index := 0; index+1 < len(runes); index++ {
		answerGrams[string(runes[index:index+2])] = struct{}{}
	}
	excerptRunes := []rune(normExcerpt)
	if len(excerptRunes) < 2 {
		if strings.Contains(normAnswer, normExcerpt) {
			return 1, true
		}
		return 0, false
	}
	hits := 0
	total := 0
	for index := 0; index+1 < len(excerptRunes); index++ {
		total++
		if _, ok := answerGrams[string(excerptRunes[index:index+2])]; ok {
			hits++
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(hits) / float64(total), false
}

// ScheduleChatCitationAudit enqueues the next sweep, at most one at a time. The
// cadence comes from `knowledge_audit.sweep_interval_hours`, read at schedule time:
// a tenant that slows the sweep down must not wait for a restart to see it, and a
// sweep that is disabled stops rescheduling instead of waking up forever.
func (s *Service) ScheduleChatCitationAudit(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	settings, err := s.chatCitationAuditSettingsOf(ctx)
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, nil
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindChatCitationAudit, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	interval := time.Duration(settings.SweepIntervalHours) * time.Hour
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	runAfter := time.Now().UTC().Add(interval)
	return s.Runner.Enqueue(ctx, model.JobKindChatCitationAudit,
		"chat_citation_audit:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0)
}

// chatCitationAuditWorker runs one sweep and schedules the next, the same posture
// as `reportAssuranceReconcileWorker`.
type chatCitationAuditWorker struct{ svc *Service }

func (w *chatCitationAuditWorker) Kind() string { return model.JobKindChatCitationAudit }

func (w *chatCitationAuditWorker) Run(ctx context.Context, job *model.Job) error {
	if err := w.svc.SweepChatCitationAudit(ctx); err != nil {
		logger.Warn("citation audit sweep failed", "job_id", job.ID, "error", err)
		return err
	}
	if _, err := w.svc.ScheduleChatCitationAudit(ctx, job.ID); err != nil {
		logger.Warn("citation audit reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}
