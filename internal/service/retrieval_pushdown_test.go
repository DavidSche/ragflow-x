package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func pushdownTestService(t *testing.T) (*Service, *model.Tenant) {
	t.Helper()
	return newPushdownTestService(t)
}

// TestBuildPushdownConditionAllVisible confirms the zero-overhead path: no
// sensitive datasets means no condition (doc/123 §3.3 step 2).
func TestBuildPushdownConditionAllVisible(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	ds := svc.mustCreatePushdownDataset(t, tenant.ID, "internal", false)

	condition, err := svc.buildPushdownCondition(ctx, tenant.ID, []string{ds.ID})
	if err != nil {
		t.Fatal(err)
	}
	if condition != nil && !condition.Empty() {
		t.Fatalf("all-visible datasets must not produce a condition: %+v", condition)
	}
}

// TestBuildPushdownConditionExclusionForm pins the minority-sensitive form:
// and(not restricted, not confidential).
func TestBuildPushdownConditionExclusionForm(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	restricted := svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", true)
	confidential := svc.mustCreatePushdownDataset(t, tenant.ID, "confidential", true)
	visible := svc.mustCreatePushdownDataset(t, tenant.ID, "internal", true)

	condition, err := svc.buildPushdownCondition(ctx, tenant.ID, []string{visible.ID, restricted.ID, confidential.ID})
	if err != nil {
		t.Fatal(err)
	}
	if condition == nil || condition.Empty() {
		t.Fatal("sensitive datasets must produce a condition")
	}
	if condition.Logic != "and" || len(condition.Conditions) != 2 {
		t.Fatalf("exclusion form mismatch: %+v", condition)
	}
	for _, op := range condition.Conditions {
		if op.Name != "rgx_sensitivity" || op.ComparisonOperator != "!=" {
			t.Fatalf("condition operator mismatch: %+v", op)
		}
		if op.Value != "restricted" && op.Value != "confidential" {
			t.Fatalf("unexpected excluded level: %+v", op)
		}
	}
}

// TestBuildPushdownConditionWhitelistForm pins the majority-sensitive form:
// or(internal, public).
func TestBuildPushdownConditionWhitelistForm(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	ids := []string{
		svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", true).ID,
		svc.mustCreatePushdownDataset(t, tenant.ID, "confidential", true).ID,
		svc.mustCreatePushdownDataset(t, tenant.ID, "top-secret-unknown-level", true).ID,
		svc.mustCreatePushdownDataset(t, tenant.ID, "internal", true).ID,
	}

	condition, err := svc.buildPushdownCondition(ctx, tenant.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	if condition == nil || condition.Logic != "or" || len(condition.Conditions) != 2 {
		t.Fatalf("whitelist form mismatch: %+v", condition)
	}
	seen := map[string]bool{}
	for _, op := range condition.Conditions {
		if op.ComparisonOperator != "=" {
			t.Fatalf("whitelist must use equals: %+v", op)
		}
		seen[op.Value] = true
	}
	if !seen["internal"] || !seen["public"] {
		t.Fatalf("whitelist must contain internal+public: %+v", condition.Conditions)
	}
}

// TestBuildPushdownConditionFailsClosedOnUnknown pins that unknown sensitivity
// levels and missing governance records are treated as sensitive (doc/123
// §3.2, fail-closed parity with citationRedactionReasons).
func TestBuildPushdownConditionFailsClosedOnUnknown(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	unknown := svc.mustCreatePushdownDataset(t, tenant.ID, "top-secret-unknown-level", true)

	condition, err := svc.buildPushdownCondition(ctx, tenant.ID, []string{unknown.ID})
	if err != nil {
		t.Fatal(err)
	}
	if condition == nil || condition.Empty() {
		t.Fatal("unknown sensitivity must fail closed and produce a condition")
	}

	// Missing governance record fails closed with a synthetic "unknown"
	// exclusion (doc/123 §3.3 step 2); GetDatasetLink returns (nil, nil).
	missing, err := svc.buildPushdownCondition(ctx, tenant.ID, []string{"missing-dataset"})
	if err != nil {
		t.Fatal(err)
	}
	if missing == nil || missing.Empty() || missing.Conditions[0].Value != "unknown" {
		t.Fatalf("missing dataset link must fail closed: %+v", missing)
	}
}

// TestBuildPushdownConditionSkipsUnenrolledDatasets pins the per-dataset gate:
// PushdownEnabled=false removes the dataset from condition derivation.
func TestBuildPushdownConditionSkipsUnenrolledDatasets(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	restricted := svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", false)

	condition, err := svc.buildPushdownCondition(ctx, tenant.ID, []string{restricted.ID})
	if err != nil {
		t.Fatal(err)
	}
	if condition != nil && !condition.Empty() {
		t.Fatalf("unenrolled dataset must not be filtered: %+v", condition)
	}
}

// TestApplyChatPushdownEvidenceMatrix covers the evidence statuses the
// projection records (doc/123 §7): applied / absent / bypassed.
func TestApplyChatPushdownEvidenceMatrix(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()

	// Applied: sensitive dataset enrolled.
	sensitive := svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", true)
	req := &ragflow.CompletionRequest{}
	evidence := svc.applyChatPushdown(ctx, tenant.ID, []string{sensitive.ID}, req)
	if evidence.Status != model.RetrievalPushdownApplied || req.ExtraBody == nil {
		t.Fatalf("sensitive enrolled dataset must apply pushdown: %+v", evidence)
	}

	// Absent: all visible.
	visible := svc.mustCreatePushdownDataset(t, tenant.ID, "internal", true)
	req2 := &ragflow.CompletionRequest{}
	evidence = svc.applyChatPushdown(ctx, tenant.ID, []string{visible.ID}, req2)
	if evidence.Status != model.RetrievalPushdownAbsent || req2.ExtraBody != nil {
		t.Fatalf("all-visible must be absent: %+v", evidence)
	}

	// Absent: no datasets at all.
	req3 := &ragflow.CompletionRequest{}
	evidence = svc.applyChatPushdown(ctx, tenant.ID, nil, req3)
	if evidence.Status != model.RetrievalPushdownAbsent || evidence.Reason != "no_datasets" {
		t.Fatalf("no datasets must be absent: %+v", evidence)
	}
	if evidence.Policy != model.RetrievalPushdownPolicyVersion {
		t.Fatalf("evidence must carry the policy version: %+v", evidence)
	}
}

// TestApplyChatPushdownForChatMissingShadow pins the chat-shadow degradation.
func TestApplyChatPushdownForChatMissingShadow(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	req := &ragflow.CompletionRequest{}
	evidence := svc.applyChatPushdownForChat(context.Background(), tenant.ID, "no-such-chat", req)
	if evidence.Status != model.RetrievalPushdownBypassed || evidence.Reason != "chat_shadow_unavailable" {
		t.Fatalf("missing chat shadow must bypass: %+v", evidence)
	}
	if req.ExtraBody != nil {
		t.Fatalf("bypassed request must not carry a condition: %+v", req.ExtraBody)
	}
}

// TestPushdownEvidenceJSONAndPolicyInput pins the projection artifacts:
// evidence JSON shape and stable policy-input keys (doc/123 §7).
func TestPushdownEvidenceJSONAndPolicyInput(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	sensitive := svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", true)
	req := &ragflow.CompletionRequest{}
	evidence := svc.applyChatPushdown(ctx, tenant.ID, []string{sensitive.ID}, req)

	payload := pushdownEvidenceJSON(evidence)
	if !strings.Contains(payload, `"status":"applied"`) || !strings.Contains(payload, `"policy_version":"`+model.RetrievalPushdownPolicyVersion+`"`) {
		t.Fatalf("evidence json missing required fields: %s", payload)
	}

	policyInput := map[string]string{"tenant_id": tenant.ID}
	pushdownPolicyInputHashInputs(policyInput, evidence)
	if policyInput["pushdown_policy_version"] != model.RetrievalPushdownPolicyVersion {
		t.Fatalf("policy input missing version: %+v", policyInput)
	}
	if len(policyInput["pushdown_condition_hash"]) != 64 {
		t.Fatalf("policy input condition hash must be sha256 hex: %q", policyInput["pushdown_condition_hash"])
	}

	// Nil evidence is stable: version recorded, empty hash.
	nilInput := map[string]string{}
	pushdownPolicyInputHashInputs(nilInput, nil)
	if nilInput["pushdown_policy_version"] != model.RetrievalPushdownPolicyVersion || nilInput["pushdown_condition_hash"] == "" {
		t.Fatalf("nil evidence must still produce stable keys: %+v", nilInput)
	}
}

// TestSyncDatasetPushdownMetadata pins the backfill: every document receives
// rgx_sensitivity via BatchUpdateDatasetMetadata (doc/123 §5.2).
func TestSyncDatasetPushdownMetadata(t *testing.T) {
	svc, tenant := pushdownTestService(t)
	ctx := context.Background()
	ds := svc.mustCreatePushdownDataset(t, tenant.ID, "restricted", true)
	svc.mustUploadDocuments(t, tenant.ID, ds.ID, 3)

	if err := svc.SyncDatasetPushdownMetadata(ctx, tenant.ID, ds.ID); err != nil {
		t.Fatal(err)
	}
	docs, err := svc.RAGFlow.ListDocuments(ctx, ds.RAGFlowDatasetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		value, ok := doc.Metadata["rgx_sensitivity"]
		if !ok || value != "restricted" {
			t.Fatalf("document %s missing synced sensitivity: %+v", doc.ID, doc.Metadata)
		}
	}
}
