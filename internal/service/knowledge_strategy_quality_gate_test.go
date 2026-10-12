package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func createKnowledgeStrategyQualityFixture(t *testing.T) (*Service, context.Context, model.Tenant, *model.User, *model.KnowledgeStrategy) {
	t.Helper()
	svc, ctx, tenant, user := newGovernanceFixture(t)
	strategy := &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenant.ID, DatasetID: "quality-dataset",
		StrategyType:       model.KnowledgeStrategyCompiled,
		RAGFlowCapability:  model.RAGFlowCapabilityKnowledgeCompilation,
		Config:             `{"compilation_template":"quality-template"}`,
		FallbackStrategy:   model.KnowledgeStrategyHybrid,
		AuthorizationScope: `{"projects":[]}`, Active: true, CreatedBy: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeStrategy(ctx, strategy); err != nil {
		t.Fatal(err)
	}
	return svc, ctx, tenant, user, strategy
}

func TestKnowledgeStrategyQualityGateSummarizesLatestRunAndEvidence(t *testing.T) {
	svc, ctx, tenant, user, strategy := createKnowledgeStrategyQualityFixture(t)
	scopedCases := json.RawMessage(`[{"case_id":"strategy-quality-case"}]`)
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1, Cases: scopedCases,
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetEvaluationGate(
		ctx, tenant.ID, latest.ID, model.EvaluationRunRunning,
		model.EvaluationGatePass, "pre-registered paired strategy quality gate passed",
	); err != nil {
		t.Fatal(err)
	}
	latest, err = svc.Store.GetEvaluationRun(ctx, tenant.ID, latest.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &model.EvidenceBundle{
		ID: id.New(), TenantID: tenant.ID, EvaluationScope: latest.EvaluationScope,
		XStrategyID: latest.XStrategyID, EvaluationRunID: latest.ID,
		EvalSetID: latest.EvalSetID, EvalSetVersion: latest.EvalSetVersion,
		EvalSetHash: latest.EvalSetHash, CreatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateEvidenceBundle(ctx, evidence); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.GetKnowledgeStrategyQualityGate(ctx, tenant.ID, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.StrategyID != strategy.ID || summary.GateStatus != model.EvaluationGatePass ||
		summary.RunID != latest.ID || summary.RunStatus != model.EvaluationRunRunning ||
		summary.EvalSetID != latest.EvalSetID ||
		summary.EvalSetVersion != 1 || summary.Evidence == nil || summary.Evidence.ID != evidence.ID {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Evidence.EvaluationRunID != latest.ID ||
		summary.Evidence.EvaluationScope != model.EvaluationScopeCompilationStrategy {
		t.Fatalf("evidence is not bound to the latest strategy run: %+v", summary.Evidence)
	}
	if summary.EvidenceURL == "" {
		t.Fatal("expected evidence URL")
	}
}

func TestKnowledgeStrategyQualityGateIsTenantIsolatedAndNotEvaluated(t *testing.T) {
	svc, ctx, tenant, _, strategy := createKnowledgeStrategyQualityFixture(t)

	summary, err := svc.GetKnowledgeStrategyQualityGate(ctx, tenant.ID, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.GateStatus != KnowledgeStrategyQualityGateNotEvaluated ||
		summary.RunID != "" || summary.Evidence != nil {
		t.Fatalf("unexpected empty summary: %+v", summary)
	}

	other, otherCtx, otherTenant, _, _ := createKnowledgeStrategyQualityFixture(t)
	if _, err := other.GetKnowledgeStrategyQualityGate(otherCtx, otherTenant.ID, strategy.ID); err == nil {
		t.Fatal("expected strategy isolation across tenants")
	}
}
