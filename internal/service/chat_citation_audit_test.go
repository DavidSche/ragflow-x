package service

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// QA-2 weekly chat citation sample (doc/301 §5.10 / T19).
//
// The assertions that matter are cross-table facts, so the fixture is a real
// migrated schema, not a mock:
//
//   - the run row is the idempotency guard: the second call returns the same run
//     and writes no second finding;
//   - the disposition is written back onto `rgx_knowledge_ops_event`, which is the
//     existing badcase queue;
//   - the audit never writes `rgx_report_*` — the DoD's hard boundary, asserted by
//     counting the report tables before and after.

type chatAuditFixture struct {
	svc      *Service
	gdb      *gorm.DB
	tenantID string
}

func newChatAuditFixture(t *testing.T, name string) *chatAuditFixture {
	t.Helper()
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: testSQLiteDSN(t, "chat-audit-"+name+".db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	engine := &reportTestEngine{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), engine, jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	svc.RAGFlow = engine
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "引用审计工作区 "+name)
	if err != nil {
		t.Fatal(err)
	}
	return &chatAuditFixture{svc: svc, gdb: gdb, tenantID: tenant.ID}
}

// enableChatCitationAudit writes the settings through the real patch path, which
// also proves the six keys are registered (an unregistered key is a 40098).
func (fx *chatAuditFixture) enableChatCitationAudit(t *testing.T, changes map[string]interface{}) {
	t.Helper()
	ctx := context.Background()
	current, err := fx.svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatalf("current settings revision: %v", err)
	}
	expected := int64(0)
	if current != nil {
		expected = current.Revision
	}
	payload := map[string]interface{}{"knowledge_audit.enabled": true}
	for key, value := range changes {
		payload[key] = value
	}
	if _, err := fx.svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: expected,
		Changes:          payload,
		Note:             "citation audit test",
		Confirmed:        true,
	}); err != nil {
		t.Fatalf("enable citation audit: %v", err)
	}
}

// seedChatAuditTurn writes one delivered chat turn: the ledger row plus the answer
// snapshot the audit judges. It goes through `FinalizeAnswerDelivery` because the
// join is on `request_id` and only the production writer keeps those two in step.
func (fx *chatAuditFixture) seedChatAuditTurn(
	t *testing.T, requestID, answer string, citations []model.AnswerCitation, at time.Time,
) {
	t.Helper()
	ctx := context.Background()
	event := &model.KnowledgeOpsEvent{
		RequestID: requestID, TenantID: fx.tenantID, UserID: "user-" + requestID,
		AppType: "chat", AppID: "chat-app", SessionID: "session-" + requestID,
		Question: "差旅报销上限是多少？", QuestionHash: "hash-" + requestID,
		AnswerExcerpt: truncateKnowledgeText(answer, 1024), Status: model.KnowledgeOpsCompleted,
		CitationsCount: len(citations), CreatedAt: at, UpdatedAt: at,
	}
	if err := fx.svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	for index := range citations {
		if citations[index].CitationContentHash == "" && citations[index].CitedContentExcerpt != "" {
			citations[index].CitationContentHash = sha256Hex(citations[index].CitedContentExcerpt)
		}
	}
	if _, err := fx.svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: fx.tenantID, SessionID: "session-" + requestID, AssistantID: "chat-app",
		PrincipalID: "user-" + requestID, Question: "差旅报销上限是多少？",
		Model: "chat-chat-app", RequestID: requestID,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "ANSWER_DELIVERED", Content: answer, Citations: citations,
		Limitations: []string{}, Actions: []map[string]interface{}{},
	}); err != nil {
		t.Fatalf("seed answer delivery: %v", err)
	}
	// The ledger row was written before the snapshot; the delivery path does not
	// touch it, so the audit's window filter must see the seeded time.
	if err := fx.gdb.Model(&model.KnowledgeOpsEvent{}).
		Where("request_id = ?", requestID).
		Update("created_at", at).Error; err != nil {
		t.Fatalf("restore event clock: %v", err)
	}
}

func (fx *chatAuditFixture) countTable(t *testing.T, table string) int64 {
	t.Helper()
	var total int64
	if err := fx.gdb.Table(table).Count(&total).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}

// TestChatCitationAuditSettingsDefaultsAndOverrides pins the mapping. The defaults
// are the honest ones: off, and mechanical-only (max_tokens 0), because the sample
// must cost nothing until somebody turns the model arm on.
func TestChatCitationAuditSettingsDefaultsAndOverrides(t *testing.T) {
	defaults := chatCitationAuditSettingsFromSnapshot(defaultSystemSettingsSnapshot())
	if defaults.Enabled {
		t.Fatal("citation audit must default to disabled")
	}
	if defaults.SamplePerWeek != chatCitationAuditDefaultSample {
		t.Fatalf("default sample = %d, want %d", defaults.SamplePerWeek, chatCitationAuditDefaultSample)
	}
	if defaults.MaxTokens != 0 || defaults.Semantic || defaults.Model != "" {
		t.Fatalf("default semantic arm must be off: %+v", defaults)
	}
	if defaults.SweepIntervalHours != 6 {
		t.Fatalf("default sweep interval = %d, want 6", defaults.SweepIntervalHours)
	}

	snapshot := defaultSystemSettingsSnapshot()
	groups, _ := snapshot["groups"].(map[string]interface{})
	groups["knowledge_audit"] = map[string]interface{}{
		"enabled":              true,
		"sample_per_week":      120.0,
		"max_tokens":           40000.0,
		"semantic":             true,
		"model":                "Qwen3.8-27B@radeon-external",
		"sweep_interval_hours": 12.0,
	}
	settings := chatCitationAuditSettingsFromSnapshot(snapshot)
	if !settings.Enabled || settings.SamplePerWeek != 120 || settings.MaxTokens != 40000 ||
		!settings.Semantic || settings.SweepIntervalHours != 12 ||
		settings.Model != "Qwen3.8-27B@radeon-external" {
		t.Fatalf("override mapping = %+v", settings)
	}
}

// TestChatCitationAuditWindowIsThePreviousCompletedWeek pins the calendar. The
// running week is never sampled: a partial week counted as a week would be hidden
// from the next run by the idempotency guard.
func TestChatCitationAuditWindowIsThePreviousCompletedWeek(t *testing.T) {
	// Wednesday 2026-03-04 → the week of 2026-02-23..2026-03-02.
	period, from, to := chatCitationAuditWindow(time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC))
	if period != "2026-02-23" {
		t.Fatalf("period start = %q, want 2026-02-23", period)
	}
	if !from.Equal(time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window from = %s", from)
	}
	if !to.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window to = %s", to)
	}
	// Monday 2026-03-02 00:30 → still the week that just ended, never the new one.
	period, _, to = chatCitationAuditWindow(time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC))
	if period != "2026-02-23" {
		t.Fatalf("monday period start = %q, want 2026-02-23", period)
	}
	if !to.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("monday window to = %s", to)
	}
	now := time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC)
	if !to.Before(now) && !to.Equal(now.Truncate(24*time.Hour)) {
		t.Fatalf("window must not include the running week: to=%s now=%s", to, now)
	}
}

// TestChatCitationSupportSeparatesVerbatimOverlapAndAbsence pins the mechanical
// layer's three bands. The numbers are derived from character bigrams, so they are
// stated as exact fractions rather than as a "roughly" threshold.
func TestChatCitationSupportSeparatesVerbatimOverlapAndAbsence(t *testing.T) {
	excerpt := "甲乙丙丁戊己庚辛壬癸" // 9 bigrams
	cases := []struct {
		name     string
		answer   string
		want     float64
		verbatim bool
	}{
		{"verbatim", "按制度：" + excerpt + "，已核对。", 1, true},
		{"overlap above the support bar", "甲乙丙丁戊己庚", 6.0 / 9.0, false},
		{"weak overlap", "甲乙丙。天地玄黄，宇宙洪荒。", 2.0 / 9.0, false},
		{"absent", "本文件规定了供应商准入的资质要求。", 0, false},
	}
	for _, testCase := range cases {
		score, verbatim := chatCitationSupport(testCase.answer, excerpt)
		if math.Abs(score-testCase.want) > 1e-9 {
			t.Fatalf("%s: score = %v, want %v", testCase.name, score, testCase.want)
		}
		if verbatim != testCase.verbatim {
			t.Fatalf("%s: verbatim = %v, want %v", testCase.name, verbatim, testCase.verbatim)
		}
	}
	if _, verbatim := chatCitationSupport(excerpt, excerpt); !verbatim {
		t.Fatal("an answer that repeats the excerpt verbatim must be verbatim")
	}
	if score, _ := chatCitationSupport("", excerpt); score != 0 {
		t.Fatalf("empty answer score = %v, want 0", score)
	}
}

// TestChatCitationAuditJudgesWritesBackAndIsIdempotent is the whole feature in one
// test: a passing turn, a failing turn and a tampered citation, then a replay.
func TestChatCitationAuditJudgesWritesBackAndIsIdempotent(t *testing.T) {
	fx := newChatAuditFixture(t, "judge")
	ctx := context.Background()
	fx.enableChatCitationAudit(t, map[string]interface{}{"knowledge_audit.sample_per_week": 50.0})

	_, from, _ := chatCitationAuditWindow(time.Now().UTC())
	turnAt := from.Add(48 * time.Hour)
	supportedExcerpt := "差旅报销上限按城市等级执行，一线城市每晚六百元，二线城市每晚四百元。"
	fx.seedChatAuditTurn(t, "req-supported",
		"依据差旅制度第三章，一线城市每晚六百元，二线城市每晚四百元。",
		[]model.AnswerCitation{{
			ID: "c1", ChunkID: "chunk-1", DocumentID: "doc-1",
			CitedContentExcerpt: supportedExcerpt,
			CitationContentHash: sha256Hex(supportedExcerpt),
		}}, turnAt)
	absentExcerpt := "本文件规定了供应商准入的资质要求：注册资本不低于五百万元。"
	fx.seedChatAuditTurn(t, "req-absent",
		"差旅报销按城市等级执行，一线城市每晚六百元。",
		[]model.AnswerCitation{{
			ID: "c2", ChunkID: "chunk-2", DocumentID: "doc-2",
			CitedContentExcerpt: absentExcerpt,
			CitationContentHash: sha256Hex(absentExcerpt),
		}}, turnAt.Add(time.Hour))
	// Tampered citation: the recorded hash no longer describes the stored excerpt.
	fx.seedChatAuditTurn(t, "req-tampered",
		"差旅报销按城市等级执行，一线城市每晚六百元。",
		[]model.AnswerCitation{{
			ID: "c3", ChunkID: "chunk-3", DocumentID: "doc-3",
			CitedContentExcerpt: "差旅报销按城市等级执行，一线城市每晚六百元。",
			CitationContentHash: sha256Hex("this text was edited after delivery"),
		}}, turnAt.Add(2*time.Hour))

	// Every report table the sample could plausibly be confused with. The DoD's
	// boundary is "the audit never writes the report family", so the assertion counts
	// all of them, not just the assurance ones.
	reportTables := []string{
		"rgx_report", "rgx_report_revision", "rgx_report_claim", "rgx_report_citation",
		"rgx_report_assurance_run", "rgx_report_claim_evaluation",
		"rgx_report_review_finding", "rgx_report_evidence_snapshot",
	}
	before := map[string]int64{}
	for _, table := range reportTables {
		before[table] = fx.countTable(t, table)
	}

	run, created, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin")
	if err != nil {
		t.Fatalf("run citation audit: %v", err)
	}
	if !created {
		t.Fatal("first run must be created")
	}
	if run.Sampled != 3 || run.Judged != 3 || run.Passed != 1 || run.Failed != 2 {
		t.Fatalf("counters = %+v", run)
	}
	if run.Findings != 3 {
		t.Fatalf("findings = %d, want 3", run.Findings)
	}
	if run.Status != model.ChatCitationAuditRunCompleted {
		t.Fatalf("status = %q", run.Status)
	}
	if run.MethodVersion != chatCitationAuditMethodVersion {
		t.Fatalf("method version = %q", run.MethodVersion)
	}

	// Write-back: the existing queue can now filter on the disposition, and every
	// reason it carries is in the closed set.
	var events []model.KnowledgeOpsEvent
	if err := fx.gdb.Where("tenant_id = ?", fx.tenantID).Order("request_id ASC").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	byRequest := map[string]model.KnowledgeOpsEvent{}
	for _, event := range events {
		byRequest[event.RequestID] = event
		if event.CitationAuditStatus == "" {
			t.Fatalf("%s: audit left no disposition", event.RequestID)
		}
		if !model.IsChatCitationAuditTurnStatus(event.CitationAuditStatus) {
			t.Fatalf("%s: disposition %q outside the closed set", event.RequestID, event.CitationAuditStatus)
		}
		if !model.IsChatCitationAuditReason(event.CitationAuditReason) {
			t.Fatalf("%s: reason %q outside the closed set", event.RequestID, event.CitationAuditReason)
		}
		if event.CitationAuditRunID != run.ID {
			t.Fatalf("%s: run id = %q, want %q", event.RequestID, event.CitationAuditRunID, run.ID)
		}
		if event.CitationAuditedAt == nil {
			t.Fatalf("%s: audited_at not stamped", event.RequestID)
		}
	}
	if got := byRequest["req-supported"].CitationAuditStatus; got != model.ChatCitationAuditPassed {
		t.Fatalf("supported turn disposition = %q", got)
	}
	if got := byRequest["req-absent"].CitationAuditStatus; got != model.ChatCitationAuditFailed {
		t.Fatalf("absent turn disposition = %q", got)
	}
	if got := byRequest["req-absent"].CitationAuditReason; got != model.ChatCitationReasonExcerptAbsent {
		t.Fatalf("absent turn reason = %q", got)
	}
	if got := byRequest["req-tampered"].CitationAuditReason; got != model.ChatCitationReasonExcerptHashMismatch {
		t.Fatalf("tampered turn reason = %q", got)
	}

	var findings []model.ChatCitationAuditFinding
	if err := fx.gdb.Where("run_id = ?", run.ID).Order("request_id ASC").Find(&findings).Error; err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("finding rows = %d, want 3", len(findings))
	}
	for _, finding := range findings {
		if !model.IsChatCitationFindingVerdict(finding.Verdict) {
			t.Fatalf("verdict %q outside the closed set", finding.Verdict)
		}
		if !model.IsChatCitationAuditReason(finding.Reason) {
			t.Fatalf("finding reason %q outside the closed set", finding.Reason)
		}
		if finding.TenantID != fx.tenantID || finding.EventID == "" || finding.RequestID == "" {
			t.Fatalf("finding missing scope: %+v", finding)
		}
	}

	// Idempotency: the unique (tenant, week) row is the guard, so the replay returns
	// the same run and writes nothing.
	replay, replayCreated, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin")
	if err != nil {
		t.Fatalf("replay citation audit: %v", err)
	}
	if replayCreated {
		t.Fatal("replay must not create a second run")
	}
	if replay.ID != run.ID {
		t.Fatalf("replay run id = %q, want %q", replay.ID, run.ID)
	}
	if got := fx.countTable(t, "rgx_chat_citation_audit_run"); got != 1 {
		t.Fatalf("run rows = %d, want 1", got)
	}
	if got := fx.countTable(t, "rgx_chat_citation_audit_finding"); got != 3 {
		t.Fatalf("finding rows after replay = %d, want 3", got)
	}

	// Hard boundary: the audit never touches the report family.
	for _, table := range reportTables {
		if after := fx.countTable(t, table); after != before[table] {
			t.Fatalf("%s: citation audit wrote to the report family (%d → %d)", table, before[table], after)
		}
	}
}

// TestChatCitationAuditRefusesWhenDisabledAndLeavesNothing pins fail-closed: a
// tenant that never opted in gets a 400 and no rows at all.
func TestChatCitationAuditRefusesWhenDisabledAndLeavesNothing(t *testing.T) {
	fx := newChatAuditFixture(t, "disabled")
	ctx := context.Background()
	_, _, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin")
	if err == nil {
		t.Fatal("disabled citation audit must refuse")
	}
	httpErr, ok := err.(*httperr.Error)
	if !ok || httpErr.Status != 400 {
		t.Fatalf("error = %v, want a 400 business error", err)
	}
	if got := fx.countTable(t, "rgx_chat_citation_audit_run"); got != 0 {
		t.Fatalf("run rows = %d, want 0", got)
	}
}

// TestChatCitationAuditSampleIgnoresTurnsWithoutEvidence documents the join: a
// ledger row whose snapshot never landed cannot be judged, so it is not "passed" —
// it is simply not sampled, and the run reports 0 of its target.
func TestChatCitationAuditSampleIgnoresTurnsWithoutEvidence(t *testing.T) {
	fx := newChatAuditFixture(t, "no-evidence")
	ctx := context.Background()
	fx.enableChatCitationAudit(t, nil)
	_, from, _ := chatCitationAuditWindow(time.Now().UTC())
	if err := fx.svc.Store.UpsertKnowledgeOpsEvent(ctx, &model.KnowledgeOpsEvent{
		RequestID: "req-no-snapshot", TenantID: fx.tenantID, UserID: "u1",
		AppType: "chat", AppID: "chat-app", Question: "q", QuestionHash: "h",
		Status: model.KnowledgeOpsCompleted, CitationsCount: 2,
		CreatedAt: from.Add(time.Hour), UpdatedAt: from.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	run, created, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin")
	if err != nil || !created {
		t.Fatalf("run: %v created=%v", err, created)
	}
	if run.Sampled != 0 || run.Passed != 0 || run.Failed != 0 || run.Skipped != 0 {
		t.Fatalf("counters = %+v, want an empty sample", run)
	}
	if run.SampleTarget != chatCitationAuditDefaultSample {
		t.Fatalf("sample target = %d", run.SampleTarget)
	}
}

// TestChatCitationAuditSemanticArmIsCappedAndNeverGuesses pins the cost bound: with
// a positive budget that is already spent, the inconclusive citation stays
// `unverifiable` with `budget_exhausted`, and the run says so. A cap that silently
// produced `supported` would be worse than no cap.
func TestChatCitationAuditSemanticArmIsCappedAndNeverGuesses(t *testing.T) {
	svc := &Service{}
	run := &model.ChatCitationAuditRun{
		ID: "run-1", TenantID: "tenant-1", Semantic: true,
	}
	settings := ChatCitationAuditSettings{Model: "mock-model", MaxTokens: 1000}
	turn := model.ChatCitationAuditTurn{
		EventID: "event-1", RequestID: "req-1",
		Answer: "甲乙丙。天地玄黄，宇宙洪荒。",
		CitationsJSON: `[{"id":"c1","cited_content_excerpt":"甲乙丙丁戊己庚辛壬癸","citation_content_hash":"` +
			sha256Hex("甲乙丙丁戊己庚辛壬癸") + `"}]`,
	}
	budget := int64(0)
	outcome := svc.judgeChatCitationTurn(context.Background(), run, turn, settings, &budget)
	if outcome.Status != model.ChatCitationAuditSkipped {
		t.Fatalf("status = %q, want skipped", outcome.Status)
	}
	if outcome.Reason != model.ChatCitationReasonBudgetExhausted {
		t.Fatalf("reason = %q, want %q", outcome.Reason, model.ChatCitationReasonBudgetExhausted)
	}
	if !outcome.BudgetExhausted {
		t.Fatal("budget exhaustion must be reported to the run")
	}
	if len(outcome.Findings) != 1 {
		t.Fatalf("findings = %d", len(outcome.Findings))
	}
	finding := outcome.Findings[0]
	if finding.Verdict != model.ChatCitationVerdictUnverifiable {
		t.Fatalf("verdict = %q: an unaffordable call must not become a pass", finding.Verdict)
	}
	if finding.Method != model.ChatCitationMethodDeterministic {
		t.Fatalf("method = %q: the deciding layer is the mechanical one", finding.Method)
	}
	if outcome.TokensIn != 0 || outcome.TokensOut != 0 {
		t.Fatalf("a call that never happened spent tokens: %+v", outcome)
	}

	// Without a model the arm is open but unarmed: still `unverifiable`, and the run
	// must not claim the budget was the reason.
	budget = 1000
	outcome = svc.judgeChatCitationTurn(context.Background(), run, turn,
		ChatCitationAuditSettings{MaxTokens: 1000}, &budget)
	if outcome.Reason != model.ChatCitationReasonWeakOverlap || outcome.BudgetExhausted {
		t.Fatalf("unarmed arm: reason = %q budget_hit=%v", outcome.Reason, outcome.BudgetExhausted)
	}
}

// TestChatCitationAuditFrozenSemanticFlag pins what the run records about its own
// capability: `semantic` is the *effective* value, so a run that could not call a
// model must not advertise a semantic arm it never used.
func TestChatCitationAuditFrozenSemanticFlag(t *testing.T) {
	fx := newChatAuditFixture(t, "semantic-flag")
	ctx := context.Background()
	fx.enableChatCitationAudit(t, map[string]interface{}{
		"knowledge_audit.semantic":   true,
		"knowledge_audit.max_tokens": 1000.0,
		// deliberately no model
	})
	run, created, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin")
	if err != nil || !created {
		t.Fatalf("run: %v created=%v", err, created)
	}
	if run.Semantic {
		t.Fatal("run must not claim a semantic arm without a model")
	}
	if run.BudgetTokens != 1000 {
		t.Fatalf("frozen budget = %d, want 1000", run.BudgetTokens)
	}

	// A registered numeric cap whose name contains "token" must be settable (304 E52).
	fx.enableChatCitationAudit(t, map[string]interface{}{
		"knowledge_audit.model": "Qwen3.8-27B@radeon-external",
	})
	settings, err := fx.svc.chatCitationAuditSettingsOf(ctx)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !settings.Semantic || settings.Model == "" {
		t.Fatalf("settings = %+v, want the semantic arm armed", settings)
	}
	if fmt.Sprint(settings.MaxTokens) != "1000" {
		t.Fatalf("max tokens = %d, want 1000", settings.MaxTokens)
	}
}

// TestUnregisteredSettingStillRefusedAsSecret pins the other half of E52: the
// secret-name heuristic still refuses an *unregistered* key that looks like a
// credential, so the registry reorder did not open a hole.
func TestUnregisteredSettingStillRefusedAsSecret(t *testing.T) {
	fx := newChatAuditFixture(t, "secret-guard")
	ctx := context.Background()
	_, err := fx.svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: 0,
		Changes:          map[string]interface{}{"gateway.provider_token": "oops"},
		Note:             "secret guard",
		Confirmed:        true,
	})
	if err == nil {
		t.Fatal("an unregistered token-shaped key must be refused")
	}
	httpErr, ok := err.(*httperr.Error)
	if !ok || httpErr.Status != 400 {
		t.Fatalf("error = %v, want a 400", err)
	}
	if httpErr.Message != "system setting secrets must use the credential API" {
		t.Fatalf("message = %q", httpErr.Message)
	}
}

// TestChatCitationAuditSampleExcludesDisposedTurns pins the resume contract: a turn
// that already carries a disposition is not sampled again. Without it, a manual
// re-run after a failure would re-judge (and re-count) the same citations, and the
// two runs' counters would double-count one population.
func TestChatCitationAuditSampleExcludesDisposedTurns(t *testing.T) {
	fx := newChatAuditFixture(t, "resume")
	ctx := context.Background()
	fx.enableChatCitationAudit(t, nil)
	_, from, to := chatCitationAuditWindow(time.Now().UTC())
	excerpt := "差旅报销上限按城市等级执行，一线城市每晚六百元。"
	fx.seedChatAuditTurn(t, "req-resume",
		"依据差旅制度第三章，一线城市每晚六百元。",
		[]model.AnswerCitation{{
			ID: "c1", ChunkID: "chunk-1", DocumentID: "doc-1",
			CitedContentExcerpt: excerpt, CitationContentHash: sha256Hex(excerpt),
		}}, from.Add(24*time.Hour))

	turns, err := fx.svc.Store.ListChatCitationAuditTurns(ctx, fx.tenantID, from, to, 50)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns before the run = %d, want 1", len(turns))
	}
	if turns[0].Answer == "" {
		t.Fatal("the sample must carry the delivered answer text")
	}
	if _, _, err := fx.svc.RunChatCitationAudit(ctx, fx.tenantID, "admin"); err != nil {
		t.Fatalf("run: %v", err)
	}
	turns, err = fx.svc.Store.ListChatCitationAuditTurns(ctx, fx.tenantID, from, to, 50)
	if err != nil {
		t.Fatalf("list turns after the run: %v", err)
	}
	if len(turns) != 0 {
		t.Fatalf("disposed turn is still sampleable: %d", len(turns))
	}
}

// TestChatCitationAuditSettingBounds pins the two bounds that carry meaning: a
// sample of 0 is refused (a run that judges nothing is not a sample), while a token
// cap of 0 is *accepted* (mechanical-only is the honest default, not a missing
// value). The second half is the E52 regression guard at the validator level.
func TestChatCitationAuditSettingBounds(t *testing.T) {
	fx := newChatAuditFixture(t, "bounds")
	ctx := context.Background()
	if _, err := fx.svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: 0,
		Changes:          map[string]interface{}{"knowledge_audit.sample_per_week": 0.0},
		Note:             "zero sample",
		Confirmed:        true,
	}); err == nil {
		t.Fatal("sample_per_week = 0 must be refused")
	}
	fx.enableChatCitationAudit(t, map[string]interface{}{"knowledge_audit.max_tokens": 0.0})
	settings, err := fx.svc.chatCitationAuditSettingsOf(ctx)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if settings.MaxTokens != 0 {
		t.Fatalf("max tokens = %d, want 0", settings.MaxTokens)
	}
}
