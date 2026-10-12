package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func createMemoryPolicyQualityRun(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID, policyID string, version int64,
	gateStatus string, pass *bool,
) *model.EvaluationRun {
	t.Helper()
	cases := json.RawMessage(`[{"case_id":"quality-case"}]`)
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenantID, userID, EvaluationSetVersionInput{
		EvalSetID: policyID, Version: version, Cases: cases,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenantID, userID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeMemoryPolicy,
		XMemoryPolicyID: policyID,
		EvalSetID:       evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		Capability: model.EvaluationCapabilityMemory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateStatus == "" {
		return run
	}
	if err := svc.Store.CompleteEvaluationRunWithGate(
		ctx, tenantID, run.ID, model.EvaluationRunCompleted,
		`{"contract_version":"scoped-paired-ab-v1","pair_count":20,"hit_lift_pp":85}`,
		pass, gateStatus, "pre-registered paired quality gate passed",
	); err != nil {
		t.Fatal(err)
	}
	completed, err := svc.Store.GetEvaluationRun(ctx, tenantID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func createMemoryPolicyQualityFixture(t *testing.T) (*Service, context.Context, model.Tenant, *model.User, *model.MemoryPolicy) {
	t.Helper()
	svc, ctx, tenant, user := newGovernanceFixture(t)
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: "project", AssistantID: "assistant",
		State: model.MemoryPolicyPaused, ReadPolicy: "deny", WritePolicy: "deny",
		PolicyGeneration: 1, Config: `{"purpose":"evaluation","read_policy":"deny","write_policy":"deny","retention_days":1}`,
		CreatedBy: user.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return svc, ctx, tenant, user, policy
}

func TestMemoryPolicyQualityGateSummarizesLatestRunAndEvidence(t *testing.T) {
	svc, ctx, tenant, user, policy := createMemoryPolicyQualityFixture(t)
	pass := true
	createMemoryPolicyQualityRun(t, svc, ctx, tenant.ID, user.ID, policy.ID, 1, model.EvaluationGateWait, nil)
	time.Sleep(2 * time.Millisecond)
	latest := createMemoryPolicyQualityRun(
		t, svc, ctx, tenant.ID, user.ID, policy.ID, 2, model.EvaluationGatePass, &pass,
	)
	evidence := createScopedEvaluationEvidence(t, svc, ctx, tenant.ID, user.ID, latest)

	summary, err := svc.GetMemoryPolicyQualityGate(ctx, tenant.ID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.GateStatus != model.EvaluationGatePass || summary.RunID != latest.ID ||
		summary.EvalSetID != latest.EvalSetID || summary.EvalSetVersion != 2 ||
		summary.Evidence == nil || summary.Evidence.ID != evidence.ID {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Evidence.EvaluationRunID != latest.ID || summary.Evidence.EvaluationScope != model.EvaluationScopeMemoryPolicy {
		t.Fatalf("evidence is not bound to the latest policy run: %+v", summary.Evidence)
	}
	if summary.EvidenceURL == "" {
		t.Fatal("expected evidence URL")
	}
	metrics, ok := summary.Metrics["pair_count"].(float64)
	if !ok || metrics != 20 {
		t.Fatalf("metrics were not decoded: %+v", summary.Metrics)
	}
}

func TestMemoryPolicyQualityGateIsTenantIsolatedAndNotEvaluated(t *testing.T) {
	svc, ctx, tenant, _, policy := createMemoryPolicyQualityFixture(t)

	summary, err := svc.GetMemoryPolicyQualityGate(ctx, tenant.ID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.GateStatus != MemoryPolicyQualityGateNotEvaluated || summary.RunID != "" || summary.Evidence != nil {
		t.Fatalf("unexpected empty summary: %+v", summary)
	}

	other, otherCtx, otherTenant, _, _ := createMemoryPolicyQualityFixture(t)
	if _, err := other.GetMemoryPolicyQualityGate(otherCtx, otherTenant.ID, policy.ID); err == nil {
		t.Fatal("expected policy isolation across tenants")
	}
}
