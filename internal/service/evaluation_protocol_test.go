package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const scopedPreregistrationV3Protocol = `{
	"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":1,
	"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
	"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
	"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":1,
	"answer_correctness_method":"expected_evidence_match",
	"require_citation_precision":true
}`

func TestEvaluationProtocolRegistrationCanonicalizesKeyOrder(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	first, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContractVersion != "scoped-paired-ab-v3" || first.Status != model.EvaluationProtocolRegistrationActive {
		t.Fatalf("unexpected registration: %+v", first)
	}
	// Same protocol with reordered keys and an explicitly-set default must be
	// recognized as the identical preregistered protocol.
	reordered := `{
		"require_citation_precision":true,"top_k":8,"min_samples":1,
		"answer_correctness_method":"expected_evidence_match",
		"min_compiled_artifact_effect_pairs":1,"min_hit_lift_pp":0,
		"contract_version":"scoped-paired-ab-v3","max_no_answer_lift_pp":0,
		"max_p95_latency_delta_ms":300,"min_citation_precision_lift_pp":20,
		"primary_metric":"citation_precision"
	}`
	second, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(reordered),
	})
	if err != nil {
		t.Fatalf("reordered protocol must register identically: %v", err)
	}
	if second.ProtocolHash != first.ProtocolHash {
		t.Fatalf("semantically identical protocols must share one hash: %s vs %s", first.ProtocolHash, second.ProtocolHash)
	}
	// A threshold change must produce a different hash.
	hardened := strings.Replace(scopedPreregistrationV3Protocol, `"min_citation_precision_lift_pp":20`, `"min_citation_precision_lift_pp":25`, 1)
	third, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(hardened),
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.ProtocolHash == first.ProtocolHash {
		t.Fatal("threshold change must change the protocol hash")
	}
	_ = ctx
}

func TestEvaluationProtocolRegistrationValidatesInput(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "", Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
	}); err == nil {
		t.Fatal("expected empty capability rejection")
	}
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(`{"contract_version":"wrong"}`),
	}); err == nil {
		t.Fatal("expected invalid protocol rejection")
	}
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", SampleSetHash: "not-a-hash", Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
	}); err == nil {
		t.Fatal("expected invalid sample set hash rejection")
	}
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, "missing-user", EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
	}); err == nil {
		t.Fatal("expected missing registrar rejection")
	}
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: strings.ToUpper("Wiki"), Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
	}); err != nil {
		t.Fatalf("capability normalization should accept case variants: %v", err)
	}
}

func TestScopedEvaluationRunFailsClosedWithoutPreregistration(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-1", "content": "X is 1"}}},
		true:  {Chunks: []map[string]interface{}{{"id": "hit", "document_id": "doc-1", "content": "X is 1"}}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationV3(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	// The fixture registered the protocol; revoke it so execution must fail closed.
	if _, err := svc.RevokeEvaluationProtocolRegistration(ctx, tenant.ID, user.ID, latestProtocolRegistrationID(t, svc, ctx, tenant.ID), EvaluationProtocolRegistrationRevokeInput{
		Reason: "drill revocation",
	}); err != nil {
		t.Fatal(err)
	}
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err == nil {
		t.Fatal("expected preregistration failure")
	}
	if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Code != 40077 {
		t.Fatalf("unexpected error: %+v", err)
	}
	if completed != nil {
		t.Fatalf("failed run must not be returned: %+v", completed)
	}
	stored, err := svc.Store.GetEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.EvaluationRunFailed || stored.GateStatus != model.EvaluationGateFail ||
		!strings.Contains(stored.GateReason, "pre-registered") {
		t.Fatalf("run must fail closed with a preregistration reason: %+v", stored)
	}
	if len(stub.searchRequests) != 0 {
		t.Fatalf("retrieval must not start for an unregistered protocol: %d", len(stub.searchRequests))
	}
}

func TestScopedEvaluationRunRejectsTamperedThresholdsAndSampleSet(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		false: {Chunks: []map[string]interface{}{{"id": "base", "document_id": "doc-1", "content": "X is 1"}}},
		true:  {Chunks: []map[string]interface{}{{"id": "hit", "document_id": "doc-1", "content": "X is 1"}}},
	}}
	svc.RAGFlow = stub
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenant.ID, user.ID, EvaluationSetVersionInput{
		EvalSetID: strategy.ID, Version: 1,
		Cases: json.RawMessage(`[{"case_id":"pair-1","paired_sample_id":"sample-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEvaluationCaseVersion(ctx, tenant.ID, user.ID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "pair-1", CaseVersion: 1,
		Question: "What is X?", ExpectedAnswer: "X is 1", ExpectedCitationDocIDs: "doc-1",
		Metadata: json.RawMessage(`{"capability":"page_index"}`), PairedSampleID: "sample-1",
	}); err != nil {
		t.Fatal(err)
	}
	// Register a protocol with a pinned sample set hash that does not match
	// the evaluation set the run was created against.
	if _, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability:    model.EvaluationCapabilityPageIndex,
		Protocol:      json.RawMessage(scopedPreregistrationV3Protocol),
		SampleSetHash: strings.Repeat("ab", 32),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "pageindex-live-v3", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "pageindex-live-v3", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(scopedPreregistrationV3Protocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err == nil {
		t.Fatal("expected sample set mismatch failure")
	}
	if !strings.Contains(err.Error(), "sample set does not match its preregistration") {
		t.Fatalf("unexpected error: %v", err)
	}
	if completed != nil {
		t.Fatalf("failed run must not be returned: %+v", completed)
	}
}

func TestEvaluationProtocolRegistrationRevocationLifecycle(t *testing.T) {
	svc, ctx, tenant, user := newGovernanceFixture(t)
	registration, err := svc.RegisterEvaluationProtocol(ctx, tenant.ID, user.ID, EvaluationProtocolRegistrationInput{
		Capability: "wiki", Protocol: json.RawMessage(scopedPreregistrationV3Protocol),
		Note: "drill protocol",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevokeEvaluationProtocolRegistration(ctx, tenant.ID, user.ID, registration.ID, EvaluationProtocolRegistrationRevokeInput{
		Reason: "",
	}); err == nil {
		t.Fatal("expected empty revoke reason rejection")
	}
	revoked, err := svc.RevokeEvaluationProtocolRegistration(ctx, tenant.ID, user.ID, registration.ID, EvaluationProtocolRegistrationRevokeInput{
		Reason: "thresholds superseded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != model.EvaluationProtocolRegistrationRevoked || revoked.RevokedAt == nil ||
		revoked.RevokedBy != user.ID || revoked.RevokeReason != "thresholds superseded" {
		t.Fatalf("unexpected revocation: %+v", revoked)
	}
	if _, err := svc.RevokeEvaluationProtocolRegistration(ctx, tenant.ID, user.ID, registration.ID, EvaluationProtocolRegistrationRevokeInput{
		Reason: "double revoke",
	}); err == nil {
		t.Fatal("expected double revocation rejection")
	}
	// A tenant must not see another tenant's registrations.
	otherTenant, err := svc.CreateTenant(ctx, "Other Tenant "+registration.ID[:8])
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := svc.ListEvaluationProtocolRegistrations(ctx, otherTenant.ID, repository.EvaluationProtocolRegistrationFilter{}, 1, 50)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("registrations leaked across tenants: total=%d items=%d err=%v", total, len(items), err)
	}
	items, total, err = svc.ListEvaluationProtocolRegistrations(ctx, tenant.ID, repository.EvaluationProtocolRegistrationFilter{}, 1, 50)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("unexpected registration list: total=%d items=%d err=%v", total, len(items), err)
	}
}

func latestProtocolRegistrationID(t *testing.T, svc *Service, ctx context.Context, tenantID string) string {
	t.Helper()
	items, _, err := svc.ListEvaluationProtocolRegistrations(ctx, tenantID, repository.EvaluationProtocolRegistrationFilter{}, 1, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("expected one registration: %v err=%v", items, err)
	}
	return items[0].ID
}
