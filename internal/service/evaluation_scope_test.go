package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func completeScopedEvaluation(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID, scope, targetID string, evalSetVersion int64,
) *model.EvaluationRun {
	t.Helper()
	scopedCases := json.RawMessage("[{\"case_id\":\"scoped-case\"}]")
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenantID, userID, EvaluationSetVersionInput{
		EvalSetID: targetID, Version: evalSetVersion, Cases: scopedCases,
	})
	if err != nil {
		t.Fatal(err)
	}
	strategyID, memoryPolicyID := targetID, targetID
	if scope == model.EvaluationScopeCompilationStrategy {
		memoryPolicyID = ""
	}
	if scope == model.EvaluationScopeMemoryPolicy {
		strategyID = ""
	}
	run, err := svc.CreateEvaluationRun(ctx, tenantID, userID, EvaluationRunInput{
		EvaluationScope: scope,
		XStrategyID:     strategyID,
		XMemoryPolicyID: memoryPolicyID,
		EvalSetID:       targetID, EvalSetVersion: evalSetVersion, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	resultPass := true
	caseVersion, err := svc.CreateEvaluationCaseVersion(ctx, tenantID, userID, EvaluationCaseVersionInput{
		EvalSetID: targetID, EvalSetVersion: evalSetVersion, CaseID: "scoped-case",
		CaseVersion: 1, Question: "What is scoped?", ExpectedAnswer: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddEvaluationCaseResult(ctx, tenantID, userID, EvaluationCaseResultInput{
		RunID: run.ID, CaseID: "scoped-case", CaseVersionID: caseVersion.ID,
		CaseVersionHash: caseVersion.Hash, ActualAnswer: "ok", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{}`), Pass: &resultPass,
	}); err != nil {
		t.Fatal(err)
	}
	completed, err := svc.CompleteEvaluationRun(ctx, tenantID, run.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func createScopedEvaluationEvidence(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID string, run *model.EvaluationRun,
) *model.EvidenceBundle {
	t.Helper()
	bundle, err := svc.CreateEvidenceBundle(ctx, tenantID, userID, EvidenceBundleInput{
		EvaluationRunID: run.ID, SecurityEvidence: json.RawMessage(`{}`),
		PolicyEvidence: json.RawMessage(`{}`), RiskEvidence: json.RawMessage(`{}`),
		PermissionEvidence: json.RawMessage(`{}`), ConfigurationEvidence: json.RawMessage(`{}`),
		ApprovalEvidence: json.RawMessage(`{}`), EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestEvaluationScopeIsolatesStrategyAndMemoryPolicyFromRelease(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: "dataset", StrategyType: model.KnowledgeStrategyCompiled,
		RAGFlowCapability: model.RAGFlowCapabilityKnowledgeCompilation,
		Config:            `{"compilation_template":"tpl"}`, FallbackStrategy: model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: "project", AssistantID: "assistant",
		State: model.MemoryPolicyPaused, ReadPolicy: "deny", WritePolicy: "deny",
		PolicyGeneration: 1, Config: `{"purpose":"evaluation","read_policy":"deny","write_policy":"deny","retention_days":1}`,
		CreatedBy: user.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	strategyRun := completeScopedEvaluation(t, svc, ctx, tenant.ID, user.ID, model.EvaluationScopeCompilationStrategy, strategy.ID, 1)
	if strategyRun.EvaluationScope != model.EvaluationScopeCompilationStrategy ||
		strategyRun.XStrategyID == nil || *strategyRun.XStrategyID != strategy.ID || strategyRun.ReleaseCandidateID != nil ||
		strategyRun.CandidateVersion != nil {
		t.Fatalf("invalid strategy run: %+v", strategyRun)
	}
	strategyBundle := createScopedEvaluationEvidence(t, svc, ctx, tenant.ID, user.ID, strategyRun)
	if strategyBundle.EvaluationScope != model.EvaluationScopeCompilationStrategy || strategyBundle.ReleaseCandidateID != nil {
		t.Fatalf("invalid strategy bundle: %+v", strategyBundle)
	}

	policyRun := completeScopedEvaluation(t, svc, ctx, tenant.ID, user.ID, model.EvaluationScopeMemoryPolicy, policy.ID, 1)
	if policyRun.EvaluationScope != model.EvaluationScopeMemoryPolicy ||
		policyRun.XMemoryPolicyID == nil || *policyRun.XMemoryPolicyID != policy.ID || policyRun.ReleaseCandidateID != nil {
		t.Fatalf("invalid policy run: %+v", policyRun)
	}
	policyBundle := createScopedEvaluationEvidence(t, svc, ctx, tenant.ID, user.ID, policyRun)
	if policyBundle.EvaluationScope != model.EvaluationScopeMemoryPolicy || policyBundle.ReleaseCandidateID != nil {
		t.Fatalf("invalid policy bundle: %+v", policyBundle)
	}

	releaseSvc, releaseCtx, releaseTenant, releaseUser, candidate, _, _ := newCompletedGovernanceChain(t, true)
	releaseRuns, _, err := releaseSvc.ListEvaluationRuns(releaseCtx, releaseTenant.ID, repository.EvaluationScopeFilter{
		CandidateID: candidate.CandidateID,
	}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(releaseRuns) != 1 || releaseRuns[0].EvaluationScope != model.EvaluationScopeRelease {
		t.Fatalf("release run list mixed scoped results: %+v", releaseRuns)
	}
	releaseBundles, _, err := releaseSvc.ListEvidenceBundles(releaseCtx, releaseTenant.ID, repository.EvaluationScopeFilter{
		CandidateID: candidate.CandidateID,
	}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(releaseBundles) != 1 || releaseBundles[0].EvaluationScope != model.EvaluationScopeRelease {
		t.Fatalf("release evidence list mixed scoped results: %+v", releaseBundles)
	}

	strategyRuns, _, err := svc.ListEvaluationRuns(ctx, tenant.ID, repository.EvaluationScopeFilter{
		Scope: model.EvaluationScopeCompilationStrategy, StrategyID: strategy.ID,
	}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(strategyRuns) != 1 || strategyRuns[0].XStrategyID == nil || *strategyRuns[0].XStrategyID != strategy.ID {
		t.Fatalf("unexpected strategy runs: %+v", strategyRuns)
	}
	policyRuns, _, err := svc.ListEvaluationRuns(ctx, tenant.ID, repository.EvaluationScopeFilter{
		Scope: model.EvaluationScopeMemoryPolicy, MemoryPolicyID: policy.ID,
	}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(policyRuns) != 1 || policyRuns[0].XMemoryPolicyID == nil || *policyRuns[0].XMemoryPolicyID != policy.ID {
		t.Fatalf("unexpected policy runs: %+v", policyRuns)
	}

	if _, err := svc.CreateEvidenceBundle(ctx, tenant.ID, user.ID, EvidenceBundleInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvaluationRunID: strategyRun.ID, SecurityEvidence: json.RawMessage(`{}`),
		PolicyEvidence: json.RawMessage(`{}`), RiskEvidence: json.RawMessage(`{}`),
		PermissionEvidence: json.RawMessage(`{}`), ConfigurationEvidence: json.RawMessage(`{}`),
		ApprovalEvidence: json.RawMessage(`{}`), EvidenceItems: json.RawMessage(`[]`),
	}); err == nil {
		t.Fatal("expected scoped evidence to reject release attribution")
	}
	if _, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: strategyBundle.ID,
	}); err == nil {
		t.Fatal("expected scoped evidence to be rejected by release gate")
	}
	if _, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: id.New(),
		EvalSetID: strategy.ID, EvalSetVersion: 1, EvalSetHash: "missing",
	}); err == nil {
		t.Fatal("expected foreign strategy to be rejected")
	}
	if _, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeMemoryPolicy, XMemoryPolicyID: id.New(),
		EvalSetID: policy.ID, EvalSetVersion: 1, EvalSetHash: "missing",
	}); err == nil {
		t.Fatal("expected foreign policy to be rejected")
	}
	if _, err := releaseSvc.CreateEvaluationRun(releaseCtx, releaseTenant.ID, releaseUser.ID, EvaluationRunInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		XStrategyID: strategy.ID, EvalSetID: strategy.ID, EvalSetVersion: 1, EvalSetHash: "missing",
	}); err == nil {
		t.Fatal("expected release evaluation to reject scoped attribution")
	}
}
