package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type trialFixture struct {
	svc      *Service
	ctx      context.Context
	tenant   model.Tenant
	template *model.ScenarioTemplateAsset
	chat     *model.ChatShadow
}

// newTrialFixture instantiates a builtin template into a real assistant with
// its profile authoring, exactly like the wizard does.
func newTrialFixture(t *testing.T) *trialFixture {
	t.Helper()
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Trial Tenant")
	if err != nil {
		t.Fatal(err)
	}
	assets, _, err := svc.Store.ListScenarioTemplates(ctx, tenant.ID, 1, 100, repository.GovernanceFilter{Key: "enterprise-qa"})
	if err != nil || len(assets) != 1 {
		t.Fatalf("seeded template missing: %v", err)
	}
	inst, err := svc.CreateChatFromScenarioTemplate(ctx, tenant.ID, "owner", assets[0].ID,
		ScenarioTemplateInstantiationInput{CreateMissingDatasets: true})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Chat.ScenarioTemplateID != assets[0].ID || inst.Chat.ScenarioTemplateVersion != assets[0].LatestVersion {
		t.Fatalf("instantiation must record provenance: %+v", inst.Chat)
	}
	return &trialFixture{svc: svc, ctx: ctx, tenant: tenant, template: &assets[0], chat: inst.Chat}
}

func (f *trialFixture) waitForRun(t *testing.T, runID string) *TrialRunView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.svc.GetTrialRun(f.ctx, f.tenant.ID, runID)
		if err != nil {
			t.Fatal(err)
		}
		switch view.Status {
		case model.TrialRunCompleted, model.TrialRunFailed, model.TrialRunCancelled:
			return view
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("trial run %s did not reach a terminal state", runID)
	return nil
}

// TestTrialRunCompletesRetrievalOnly covers the happy path and the two rules
// that keep a trial from impersonating release evidence: no gate eligibility
// and no fabricated hit rate when the suite has no expected citations.
func TestTrialRunCompletesRetrievalOnly(t *testing.T) {
	f := newTrialFixture(t)
	view, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", TrialRunInput{
		ScenarioTemplateID: f.template.ID, AssistantID: f.chat.ID, IdempotencyKey: "run-once",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Mode != model.TrialModeRetrievalOnly || view.GateEligible {
		t.Fatalf("trial run must be retrieval_only and never gate eligible: %+v", view)
	}
	final := f.waitForRun(t, view.ID)
	if final.Status != model.TrialRunCompleted {
		t.Fatalf("unexpected status %s (%s)", final.Status, final.FailureReason)
	}
	if final.Summary.Cases != len(builtinScenarioTemplates[0].Payload.EvaluationQuestions) {
		t.Fatalf("expected the template suite to run, got %+v", final.Summary)
	}
	if final.Summary.HitAtK != nil || final.Summary.HitDenominator != 0 {
		t.Fatalf("hit_at_k must stay undefined when no case defines expected citations: %+v", final.Summary)
	}
	if final.Summary.UndefinedCases != final.Summary.Cases {
		t.Fatalf("every case must be reported as undefined: %+v", final.Summary)
	}
	if len(final.Cases) != final.Summary.Cases {
		t.Fatalf("case rows must be persisted: %+v", final.Cases)
	}
	// Snapshot ≡ assistant configuration (diff = 0).
	shadow, err := f.svc.Store.GetChatShadow(f.ctx, f.tenant.ID, f.chat.ID, false)
	if err != nil || shadow == nil {
		t.Fatal(err)
	}
	authoring := ChatAuthoring{}
	if err := json.Unmarshal([]byte(shadow.ConfigJSON), &authoring); err != nil {
		t.Fatal(err)
	}
	if final.Snapshot.TopN != authoring.TopN ||
		final.Snapshot.SimilarityThreshold != authoring.SimilarityThreshold ||
		final.Snapshot.VectorSimilarityWeight != authoring.VectorSimilarityWeight ||
		final.Snapshot.PromptPresetID != f.template.Key {
		t.Fatalf("snapshot drifted from assistant config: snapshot=%+v authoring=%+v", final.Snapshot, authoring)
	}
	if final.Snapshot.TemplateVersion != f.template.LatestVersion {
		t.Fatalf("snapshot must pin the template version it ran: %+v", final.Snapshot)
	}
}

// TestTrialRunIdempotencyKeyReplays protects against double-submits: the same
// key returns the original run instead of launching a second one.
func TestTrialRunIdempotencyKeyReplays(t *testing.T) {
	f := newTrialFixture(t)
	in := TrialRunInput{ScenarioTemplateID: f.template.ID, AssistantID: f.chat.ID, IdempotencyKey: "same-key"}
	first, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent replay must return the same run: %s vs %s", first.ID, second.ID)
	}
	other := in
	other.AssistantID = "chat-other"
	if _, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", other); err == nil {
		t.Fatal("reusing a key for a different assistant must conflict")
	} else if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Code != 40941 {
		t.Fatalf("expected 409/40941, got %v", err)
	}
}

// TestTrialRunRejectsConfigDrift asserts the run refuses to proceed when the
// assistant no longer matches the template parameter profile.
func TestTrialRunRejectsConfigDrift(t *testing.T) {
	f := newTrialFixture(t)
	shadow, err := f.svc.Store.GetChatShadow(f.ctx, f.tenant.ID, f.chat.ID, false)
	if err != nil || shadow == nil {
		t.Fatal(err)
	}
	authoring := ChatAuthoring{}
	if err := json.Unmarshal([]byte(shadow.ConfigJSON), &authoring); err != nil {
		t.Fatal(err)
	}
	authoring.TopN++
	drifted, _ := json.Marshal(authoring)
	shadow.ConfigJSON = string(drifted)
	if err := f.svc.Store.UpsertChatShadow(f.ctx, shadow); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", TrialRunInput{
		ScenarioTemplateID: f.template.ID, AssistantID: f.chat.ID, IdempotencyKey: "drift",
	})
	if err == nil {
		t.Fatal("config drift must be rejected")
	}
	httpErr, ok := err.(*httperr.Error)
	if !ok || httpErr.Code != 40940 {
		t.Fatalf("expected 409/40940, got %v", err)
	}
}

// TestTrialRunRejectsUnlinkedAssistant: a bare assistant created outside the
// wizard has no template provenance and cannot be trial run.
func TestTrialRunRejectsUnlinkedAssistant(t *testing.T) {
	f := newTrialFixture(t)
	chat, err := f.svc.CreateChat(f.ctx, f.tenant.ID, "bare assistant", splitCSV(f.chat.DatasetIDs))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", TrialRunInput{
		ScenarioTemplateID: f.template.ID, AssistantID: chat.ID, IdempotencyKey: "unlinked",
	})
	if err == nil {
		t.Fatal("assistant without provenance must be rejected")
	}
	if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Code != 40942 {
		t.Fatalf("expected 409/40942, got %v", err)
	}
}

// TestOnboardingReadinessDerivesMilestones walks the four milestones without
// any stored readiness state.
func TestOnboardingReadinessDerivesMilestones(t *testing.T) {
	f := newTrialFixture(t)
	readiness, err := f.svc.OnboardingReadiness(f.ctx, f.tenant.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Completed != 1 || readiness.Next != MilestoneDatasetReady {
		t.Fatalf("before parsing and trial: %+v", readiness)
	}
	// ① mark one bound dataset as parsed.
	datasets := splitCSV(f.chat.DatasetIDs)
	if err := f.svc.CreateTask(f.ctx, &model.Task{
		TenantID: f.tenant.ID, DatasetID: datasets[0], DocID: "doc-1", DocName: "doc-1",
		TaskType: model.TaskTypeParse, Status: model.TaskStatusDone,
	}); err != nil {
		t.Fatal(err)
	}
	readiness, err = f.svc.OnboardingReadiness(f.ctx, f.tenant.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Milestones[0].Done || readiness.Completed != 2 || readiness.Next != MilestoneTrialCompleted {
		t.Fatalf("dataset milestone not derived: %+v", readiness)
	}
	// ③ run a trial to completion.
	run, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", TrialRunInput{
		ScenarioTemplateID: f.template.ID, AssistantID: f.chat.ID, IdempotencyKey: "readiness",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.waitForRun(t, run.ID)
	readiness, err = f.svc.OnboardingReadiness(f.ctx, f.tenant.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Milestones[2].Done || readiness.Next != MilestoneFirstCitedAnswer {
		t.Fatalf("trial milestone not derived: %+v", readiness)
	}
	// ④ record one cited answer.
	now := time.Now().UTC()
	if err := f.svc.Store.UpsertKnowledgeOpsEvent(f.ctx, &model.KnowledgeOpsEvent{
		ID: id.New(), RequestID: id.New(), TenantID: f.tenant.ID, UserID: "owner",
		AppType: "chat", AppID: f.chat.ID, Question: "q", QuestionHash: "h",
		Status: model.KnowledgeOpsCompleted, CitationsCount: 2, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	readiness, err = f.svc.OnboardingReadiness(f.ctx, f.tenant.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Completed != 4 || readiness.Next != "" {
		t.Fatalf("all milestones must be done: %+v", readiness)
	}
}

// TestTrialHitAtKUsesNilForUndefined keeps the "never show 0%" rule at the
// pure-function level.
func TestTrialHitAtKUsesNilForUndefined(t *testing.T) {
	if got := trialHitAtK(nil, []string{"doc-1"}); got != nil {
		t.Fatalf("no expected citations must be undefined, got %v", *got)
	}
	if got := trialHitAtK([]string{"doc-1"}, nil); got != nil {
		t.Fatalf("no returned evidence must be undefined, got %v", *got)
	}
	if got := trialHitAtK([]string{"doc-1"}, []string{"doc-2"}); got == nil || *got != 0 {
		t.Fatalf("a judged miss must be 0, got %v", got)
	}
	if got := trialHitAtK([]string{"doc-1"}, []string{"doc-2", "doc-1"}); got == nil || *got != 1 {
		t.Fatalf("a judged hit must be 1, got %v", got)
	}
	summary := buildTrialSummary([]model.TrialCaseResult{
		{Returned: true, HitAtK: nil, TopScore: 0.5},
		{Returned: true, HitAtK: nil, TopScore: 0.7},
	})
	if summary.HitAtK != nil || summary.HitDenominator != 0 {
		t.Fatalf("all-undefined suite must not report a rate: %+v", summary)
	}
}
