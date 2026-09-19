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

func manifestJSON() string {
	sections := map[string]interface{}{}
	for _, name := range []string{
		"target", "prompt", "knowledge", "modelRoute", "policy", "catalog", "router", "tools",
		"retrievalConfig", "executionConfig",
	} {
		sections[name] = map[string]string{"version": "v1", "hash": "hash-" + name}
	}
	data, _ := json.Marshal(sections)
	return string(data)
}

func newCompletedGovernanceChain(t *testing.T, pass bool) (*Service, context.Context, model.Tenant, *model.User, *model.ReleaseCandidate, *model.ExecutionSnapshot, *model.EvidenceBundle) {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Release Failure Tenant "+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin: %+v err=%v", user, err)
	}
	candidateID := id.New()[:12]
	candidate, err := svc.CreateReleaseCandidate(ctx, tenant.ID, user.ID, ReleaseCandidateInput{
		TargetType: "assistant", TargetID: candidateID, TargetVersion: "v2",
		CandidateID: candidateID, CandidateVersion: 1, ChangeSummary: "negative case",
		Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkReleaseCandidateReady(ctx, tenant.ID, candidate.CandidateID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.CreateExecutionSnapshot(ctx, tenant.ID, user.ID, ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{"mode":"read_only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: candidateID + "-set", Version: 1, Cases: json.RawMessage(`[{"case_id":"case-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseVersion, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "case-1",
		CaseVersion: 1, Question: "What is X?", ExpectedAnswer: "X is 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		ExecutionSnapshotID: snapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	casePass := pass
	if _, err := svc.AddEvaluationCaseResult(ctx, tenant.ID, user.ID, EvaluationCaseResultInput{
		RunID: run.ID, CaseID: caseVersion.CaseID, CaseVersionID: caseVersion.ID,
		CaseVersionHash: caseVersion.Hash, ActualAnswer: "X is 1", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{}`), Pass: &casePass,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteEvaluationRun(ctx, tenant.ID, run.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.CreateEvidenceBundle(ctx, tenant.ID, user.ID, EvidenceBundleInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvaluationRunID: run.ID, SecurityEvidence: json.RawMessage(`{}`),
		PolicyEvidence: json.RawMessage(`{}`), RiskEvidence: json.RawMessage(`{}`),
		PermissionEvidence: json.RawMessage(`{}`), ConfigurationEvidence: json.RawMessage(`{}`),
		ApprovalEvidence: json.RawMessage(`{}`), EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, ctx, tenant, user, candidate, snapshot, bundle
}

func newGovernanceFixture(t *testing.T) (*Service, context.Context, model.Tenant, *model.User) {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Governance Fixture "+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin: %+v err=%v", user, err)
	}
	return svc, ctx, tenant, user
}

func TestCreateScenarioTemplateReleaseCandidate(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, user.ID, ScenarioTemplateInput{
		Key: "support-faq", Name: "Support FAQ", AppTypes: []string{"chat"},
		Description: "Customer support", Status: model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{
			PromptPresetID: "support", ParameterProfileID: "balanced",
			DatasetSuggestions: []string{"faq"}, EvaluationQuestions: []string{"What is the return policy?"},
		},
		ChangeNote: "initial support template",
	})
	if err != nil {
		t.Fatalf("create scenario template: %v", err)
	}
	result, err := svc.CreateScenarioTemplateReleaseCandidate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateReleaseCandidateInput{})
	if err != nil {
		t.Fatalf("create template release candidate: %v", err)
	}
	candidate := result.Candidate
	if !result.EvalSetCreated || result.EvalSet == nil || result.EvalSet.ScenarioTemplateID != asset.ID {
		t.Fatalf("template candidate must create a bound eval set: %+v", result)
	}
	if result.EvalSetVersion == nil || !result.EvalSetVersionCreated || result.EvalSetVersion.Version != result.EvalSet.Version {
		t.Fatalf("template candidate must publish an immutable eval set version: %+v", result)
	}
	caseVersions, err := svc.Store.ListEvaluationCaseVersions(ctx, tenant.ID, result.EvalSet.ID, result.EvalSetVersion.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(caseVersions) != 1 || caseVersions[0].Question != "What is the return policy?" {
		t.Fatalf("immutable eval set must snapshot actual cases: %+v", caseVersions)
	}
	if result.ExecutionSnapshot == nil || result.ExecutionSnapshot.SnapshotHash == "" {
		t.Fatalf("template candidate must create an execution snapshot: %+v", result.ExecutionSnapshot)
	}
	if result.Candidate.Status != model.CandidateReadyForEvaluation {
		t.Fatalf("template candidate with eval and snapshot artifacts must be ready: %s", result.Candidate.Status)
	}
	var snapshotConfig map[string]any
	if err := json.Unmarshal([]byte(result.ExecutionSnapshot.ExecutionConfig), &snapshotConfig); err != nil {
		t.Fatal(err)
	}
	if snapshotConfig["mode"] != "read_only" || snapshotConfig["candidate_hash"] != candidate.CandidateHash {
		t.Fatalf("execution snapshot must bind read-only candidate config: %+v", snapshotConfig)
	}
	if candidate.TargetType != "template" || candidate.TargetID != asset.ID ||
		candidate.TargetVersion != "v1" || candidate.CandidateVersion != 1 {
		t.Fatalf("unexpected template candidate: %+v", candidate)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate.CandidateManifest), &manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest) != len(candidateManifestSections) {
		t.Fatalf("manifest sections = %d, want %d", len(manifest), len(candidateManifestSections))
	}
	if _, err := svc.CreateScenarioTemplateReleaseCandidate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateReleaseCandidateInput{}); err == nil {
		t.Fatal("duplicate template version candidate must be rejected")
	}
	existing, created, err := svc.EnsureScenarioTemplateEvalSet(ctx, tenant.ID, user.ID, asset.ID, asset.Name+"评测集")
	if err != nil {
		t.Fatalf("ensure template eval set: %v", err)
	}
	if created || existing.ID != result.EvalSet.ID {
		t.Fatalf("ensure template eval set must reuse the bound suite: created=%v id=%s", created, existing.ID)
	}
	updated, err := svc.UpdateScenarioTemplate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateInput{
		Name: asset.Name, AppTypes: []string{"chat"}, Status: model.AssetStatusPublished,
		Payload:    ScenarioTemplatePayload{EvaluationQuestions: []string{"What is the exchange policy?"}},
		ChangeNote: "add exchange question",
	})
	if err != nil {
		t.Fatalf("update scenario template: %v", err)
	}
	second, err := svc.CreateScenarioTemplateReleaseCandidate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateReleaseCandidateInput{})
	if err != nil {
		t.Fatalf("create second template release candidate: %v", err)
	}
	if second.Candidate.CandidateVersion != 2 || !second.EvalSetVersionCreated ||
		second.EvalSet.Version != 2 || second.EvalSetVersion.Version != 2 ||
		second.EvalSetVersion.ID == result.EvalSetVersion.ID {
		t.Fatalf("new template version must publish a fresh eval set version: %+v", second)
	}
	if second.EvalSet.ItemCount != 1 || updated.LatestVersion != 2 {
		t.Fatalf("template eval set sync mismatch: set=%+v latest=%d", second.EvalSet, updated.LatestVersion)
	}
}

func TestCreateScenarioTemplateReleaseCandidateRequiresEvaluationQuestions(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, user.ID, ScenarioTemplateInput{
		Key: "no-eval-template", Name: "No Eval Template", AppTypes: []string{"chat"},
		Status:  model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{PromptPresetID: "support"},
	})
	if err != nil {
		t.Fatalf("create scenario template: %v", err)
	}
	if _, err := svc.CreateScenarioTemplateReleaseCandidate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateReleaseCandidateInput{}); err == nil {
		t.Fatal("candidate without evaluation questions must be rejected")
	}
	evalSets, total, err := svc.ListEvalSets(ctx, tenant.ID, false, 1, 10, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(evalSets) != 0 {
		t.Fatalf("failed candidate must not create an eval set: total=%d sets=%d", total, len(evalSets))
	}
}

func passingSubGates(state string) string {
	data, _ := json.Marshal(map[string]string{
		"security": state, "policy": "PASS", "risk": "PASS", "permission": "PASS",
		"configuration": "PASS", "approval": "PASS",
	})
	return string(data)
}

func TestReleaseGateRejectsIncompleteWaiverEvidence(t *testing.T) {
	svc, ctx, tenant, user, candidate, _, bundle := newCompletedGovernanceChain(t, true)
	_, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "development", SubGateStates: json.RawMessage(passingSubGates("WAIVED")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash", Waiver: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expected incomplete waiver evidence to be rejected")
	}
}

func TestEvaluationBusinessFailBlocksProductionRelease(t *testing.T) {
	svc, ctx, tenant, user, candidate, _, bundle := newCompletedGovernanceChain(t, false)
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "production", SubGateStates: json.RawMessage(passingSubGates("PASS")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gate.Decision != model.GateDecisionBlock {
		t.Fatalf("gate decision = %s, want BLOCK", gate.Decision)
	}
	_, err = svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: bundle.SnapshotID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err == nil {
		t.Fatal("expected business-fail production release to be rejected")
	}
}

func TestReleaseRejectsGateEnvironmentMismatch(t *testing.T) {
	svc, ctx, tenant, user, candidate, snapshot, bundle := newCompletedGovernanceChain(t, true)
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "development", SubGateStates: json.RawMessage(passingSubGates("PASS")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: snapshot.ID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err == nil {
		t.Fatal("expected cross-environment release to be rejected")
	}
}

func TestQualityToReleaseSuccessChain(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Release Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin: %+v err=%v", user, err)
	}
	candidate, err := svc.CreateReleaseCandidate(ctx, tenant.ID, user.ID, ReleaseCandidateInput{
		TargetType: "assistant", TargetID: "chat-1", TargetVersion: "v2",
		CandidateID: "cand-1", CandidateVersion: 1, ChangeSummary: "prompt fix",
		Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkReleaseCandidateReady(ctx, tenant.ID, candidate.CandidateID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.CreateExecutionSnapshot(ctx, tenant.ID, user.ID, ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{"mode":"read_only"}`),
		PromptVersion: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: "set-1", Version: 1, Cases: json.RawMessage(`[{"case_id":"case-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseVersion, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "case-1",
		CaseVersion: 1, Question: "What is X?", ExpectedAnswer: "X is 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		ExecutionSnapshotID: snapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	pass := true
	if _, err := svc.AddEvaluationCaseResult(ctx, tenant.ID, user.ID, EvaluationCaseResultInput{
		RunID: run.ID, CaseID: caseVersion.CaseID, CaseVersionID: caseVersion.ID,
		CaseVersionHash: caseVersion.Hash, ActualAnswer: "X is 1", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{"retrieval_hit":true}`), Pass: &pass,
	}); err != nil {
		t.Fatal(err)
	}
	run, err = svc.CompleteEvaluationRun(ctx, tenant.ID, run.ID, false, "")
	if err != nil || run.Pass == nil || !*run.Pass {
		t.Fatalf("complete run: run=%+v err=%v", run, err)
	}
	bundle, err := svc.CreateEvidenceBundle(ctx, tenant.ID, user.ID, EvidenceBundleInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvaluationRunID: run.ID, SecurityEvidence: json.RawMessage(`{}`),
		PolicyEvidence: json.RawMessage(`{}`), RiskEvidence: json.RawMessage(`{}`),
		PermissionEvidence: json.RawMessage(`{}`), ConfigurationEvidence: json.RawMessage(`{}`),
		ApprovalEvidence: json.RawMessage(`{}`), EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	subGates, _ := json.Marshal(map[string]string{
		"security": "PASS", "policy": "PASS", "risk": "PASS", "permission": "PASS",
		"configuration": "PASS", "approval": "PASS",
	})
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "production", SubGateStates: subGates,
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash", Activate: true,
	})
	if err != nil || gate.Decision != model.GateDecisionPass {
		t.Fatalf("gate: %+v err=%v", gate, err)
	}
	release, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: snapshot.ID, GateDecisionID: gate.ID, Environment: "production",
		RollbackBaseline: "v1",
	})
	if err != nil || release.Status != model.ReleaseStatusCreated {
		t.Fatalf("release: %+v err=%v", release, err)
	}
	release, err = svc.CompleteRelease(ctx, tenant.ID, release.ID, false)
	if err != nil || release.Status != model.ReleaseStatusReleased {
		t.Fatalf("complete release: %+v err=%v", release, err)
	}
	baseline := &model.Release{
		ID: id.New(), TenantID: tenant.ID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: candidate.CandidateVersion, SnapshotID: snapshot.ID,
		GateDecisionID: id.New(), Environment: "production", Status: model.ReleaseStatusReleased,
		ReleasedBy: user.ID, RollbackBaseline: "v0",
	}
	baseline.CreatedAt = time.Now().UTC().Add(-time.Minute)
	if err := svc.Store.CreateRelease(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	baseline, err = svc.RollbackRelease(ctx, tenant.ID, user.ID, release.ID, "regression found")
	if err != nil {
		t.Fatal(err)
	}
	current, err := svc.Store.GetRelease(ctx, tenant.ID, release.ID)
	if err != nil || current.Status != model.ReleaseStatusRolledBack {
		t.Fatalf("rollback current: %+v err=%v", current, err)
	}
	if baseline.ID == release.ID || baseline.Status != model.ReleaseStatusReleased {
		t.Fatalf("rollback baseline = %+v", baseline)
	}
}

func TestRollbackRequiresPreviousReleaseAndReason(t *testing.T) {
	svc, ctx, tenant, user, candidate, snapshot, bundle := newCompletedGovernanceChain(t, true)
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "production", SubGateStates: json.RawMessage(passingSubGates("PASS")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash", Activate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: snapshot.ID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteRelease(ctx, tenant.ID, release.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RollbackRelease(ctx, tenant.ID, user.ID, release.ID, ""); err == nil {
		t.Fatal("expected rollback reason to be required")
	}
	if _, err := svc.RollbackRelease(ctx, tenant.ID, user.ID, release.ID, "no baseline"); err == nil {
		t.Fatal("expected rollback without a prior release to be rejected")
	}
}
