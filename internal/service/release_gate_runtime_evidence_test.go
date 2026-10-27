package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func strictPermissionEvidence(candidate *model.ReleaseCandidate, passed bool) string {
	raw, _ := json.Marshal(map[string]any{
		"permission_gate": map[string]any{
			"candidate_id":                 candidate.CandidateID,
			"candidate_version":            candidate.CandidateVersion,
			"contract_version":             releaseGatePermissionContractVersion,
			"passed":                       passed,
			"resource_matrix":              true,
			"builtin_reconciliation":       true,
			"pre_model_authorization":      true,
			"membership_revocation":        true,
			"endpoint_abac_matrix":         true,
			"pii_template_tenant_boundary": true,
			"preview_redaction":            true,
		},
	})
	return string(raw)
}

func strictExperienceEvidence(candidate *model.ReleaseCandidate) string {
	raw, _ := json.Marshal(map[string]any{
		"experience_gate": map[string]any{
			"candidate_id":        candidate.CandidateID,
			"candidate_version":   candidate.CandidateVersion,
			"contract_version":    releaseGateExperienceContractVersion,
			"passed":              true,
			"dataset_details":     true,
			"knowledge_ops":       true,
			"memory_workbench":    true,
			"conversation_center": true,
			"assistant_release":   true,
			"loading_state":       true,
			"empty_state":         true,
			"wait_state":          true,
			"error_state":         true,
			"unauthorized_state":  true,
			"retry_state":         true,
			"i18n_en":             true,
			"i18n_zh":             true,
		},
	})
	return string(raw)
}

func strictReleaseEvidenceContracts(candidate *model.ReleaseCandidate) (string, string) {
	return strictPermissionEvidence(candidate, true), strictExperienceEvidence(candidate)
}

func strictReleaseEvidenceRaw(
	candidate *model.ReleaseCandidate,
) (json.RawMessage, json.RawMessage) {
	permission, configuration := strictReleaseEvidenceContracts(candidate)
	return json.RawMessage(permission), json.RawMessage(configuration)
}

func createCandidateEvidenceBundle(
	t *testing.T, svc *Service, ctx context.Context,
	bound *model.ReleaseCandidate, evidence *model.ReleaseCandidate,
	permission, configuration string,
) {
	t.Helper()
	candidateID := bound.CandidateID
	candidateVersion := bound.CandidateVersion
	if err := svc.Store.CreateEvidenceBundle(ctx, &model.EvidenceBundle{
		ID: id.New(), TenantID: bound.TenantID,
		ReleaseCandidateID: &candidateID, CandidateVersion: &candidateVersion,
		EvaluationScope: model.EvaluationScopeRelease,
		EvaluationRunID: id.New(), EvalSetID: id.New()[:8], EvalSetVersion: 1,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "hash",
		PermissionEvidence: permission, ConfigurationEvidence: configuration,
		HashAlgorithm: "sha256", BundleHash: "hash", CreatedBy: "tester",
		CreatedAt: time.Now().UTC().Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
}

func releaseEvidenceState(t *testing.T, report *ReleaseGateEvidenceReport, gate string) ReleaseGateEvidenceItem {
	t.Helper()
	for index := range report.Items {
		if report.Items[index].Gate == gate {
			return report.Items[index]
		}
	}
	t.Fatalf("gate %s is missing from report %+v", gate, report)
	return ReleaseGateEvidenceItem{}
}

func TestReleaseGateEvidenceDerivesCandidateRuntimeContracts(t *testing.T) {
	svc, ctx, _, _, candidate, _, _ := newCompletedGovernanceChain(t, true)
	releaseID := id.New()
	now := time.Now().UTC()
	if err := svc.Store.CreateAssistantRelease(ctx, &model.AssistantRelease{
		ID: releaseID, TenantID: candidate.TenantID,
		AssistantID: candidate.TargetID, AssistantVersionID: id.New()[:8],
		ReleaseVersion: 2, ReleaseState: model.ReleaseActive,
		DesiredStateJSON: "{}", DesiredStateHash: "hash", ActualStateJSON: "{}",
		ActualStateHash: "hash", ReconcileStatus: model.ReconcileConsistent,
		SnapshotManifestID: id.New()[:8], CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	requests := []string{"runtime-request-pass-1", "runtime-request-pass-2"}
	for index, requestID := range requests {
		if err := svc.Store.UpsertTraceRun(ctx, &model.TraceRun{
			ID: id.New(), TenantID: candidate.TenantID, TraceID: requestID,
			RequestID: requestID, AssistantID: candidate.TargetID,
			AssistantReleaseID: releaseID, AppType: "chat", AppID: candidate.TargetID,
			Status: model.TraceRunCompleted, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		decision := "memory_denied"
		if index == 0 {
			decision = "memory_allowed"
		}
		detail, _ := json.Marshal(map[string]any{
			"contract_version":   releaseGateConversationContractVersion,
			"search_side_effect": false, "memory_operation_created": false,
		})
		if err := svc.Store.CreateContextDecision(ctx, &model.ContextDecision{
			ID: id.New(), TenantID: candidate.TenantID, RequestID: requestID,
			RequestKind: "chat", AssistantID: candidate.TargetID, UserID: "user-" + requestID,
			Decision: decision, DecisionHash: "hash", Detail: string(detail), CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Store.RecordCostMetric(ctx, &model.CostMetric{
			ID: id.New(), RequestID: requestID, TenantID: candidate.TenantID,
			UserID: "user-" + requestID, Date: now.Format("2006-01-02"), Model: "test-model",
			Scenario: "chat", ChatID: candidate.TargetID, TokensIn: 10, TokensOut: 5,
			EstimatedCost: 0.1, Estimated: true, EstimationPolicyVersion: "cost-v1", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, &model.KnowledgeOpsEvent{
			ID: id.New(), RequestID: requestID, TenantID: candidate.TenantID,
			UserID: "user-" + requestID, AppType: "chat", AppID: candidate.TargetID,
			QuestionHash: "hash", Status: model.KnowledgeOpsCompleted,
			CitationsCount: 2, DurationMs: 100, TokensIn: 10, TokensOut: 5,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	permission, configuration := strictReleaseEvidenceContracts(candidate)
	createCandidateEvidenceBundle(t, svc, ctx, candidate, candidate, permission, configuration)
	report, err := svc.GetReleaseGateEvidenceReport(
		ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{
		model.ReleaseGatePermission, model.ReleaseGateCost,
		model.ReleaseGateConversation, model.ReleaseGateExperience,
	} {
		if item := releaseEvidenceState(t, report, gate); item.State != ReleaseGateEvidencePass {
			t.Fatalf("gate %s must PASS, got %+v", gate, item)
		}
	}
}

func TestReleaseGateEvidenceBlocksIncompleteCandidateRuntime(t *testing.T) {
	svc, ctx, _, _, candidate, _, _ := newCompletedGovernanceChain(t, true)
	releaseID := id.New()
	now := time.Now().UTC()
	if err := svc.Store.CreateAssistantRelease(ctx, &model.AssistantRelease{
		ID: releaseID, TenantID: candidate.TenantID,
		AssistantID: candidate.TargetID, AssistantVersionID: id.New()[:8],
		ReleaseVersion: 2, ReleaseState: model.ReleaseActive,
		DesiredStateJSON: "{}", DesiredStateHash: "hash", ActualStateJSON: "{}",
		ActualStateHash: "hash", ReconcileStatus: model.ReconcileConsistent,
		SnapshotManifestID: id.New()[:8], CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertTraceRun(ctx, &model.TraceRun{
		ID: id.New(), TenantID: candidate.TenantID, TraceID: "runtime-request-gap",
		RequestID: "runtime-request-gap", AssistantID: candidate.TargetID,
		AssistantReleaseID: releaseID, AppType: "search", AppID: candidate.TargetID,
		Status: model.TraceRunCompleted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	permission, configuration := strictReleaseEvidenceContracts(candidate)
	createCandidateEvidenceBundle(t, svc, ctx, candidate, candidate, permission, configuration)
	report, err := svc.GetReleaseGateEvidenceReport(
		ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{
		model.ReleaseGateCost, model.ReleaseGateConversation, model.ReleaseGateExperience,
	} {
		if item := releaseEvidenceState(t, report, gate); item.State != ReleaseGateEvidenceBlock {
			t.Fatalf("gate %s must BLOCK, got %+v", gate, item)
		}
	}
	if report.OverallState != ReleaseGateEvidenceBlock {
		t.Fatalf("incomplete runtime must BLOCK overall, got %s", report.OverallState)
	}
}

func TestReleaseGateEvidenceRejectsCandidateEvidenceIdentityMismatch(t *testing.T) {
	svc, ctx, _, _, candidate, _, _ := newCompletedGovernanceChain(t, true)
	other := *candidate
	other.CandidateID = "mismatched-candidate"
	createCandidateEvidenceBundle(
		t, svc, ctx, candidate, &other, strictPermissionEvidence(&other, true),
		strictExperienceEvidence(&other),
	)
	report, err := svc.GetReleaseGateEvidenceReport(
		ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{
		model.ReleaseGatePermission, model.ReleaseGateExperience,
	} {
		if item := releaseEvidenceState(t, report, gate); item.State != ReleaseGateEvidenceBlock {
			t.Fatalf("gate %s must BLOCK on identity mismatch, got %+v", gate, item)
		}
	}
}
