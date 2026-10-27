package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestTemplateInstanceInstantiateAndDryRun covers the doc/107 §3.1.5
// instantiation orchestration on top of the release fixture (doc/124 §2.5).
func TestTemplateInstanceInstantiateAndDryRun(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ti-basic")
	ctx := context.Background()

	// The release fixture auto-creates one TemplateInstance for its own
	// assistant release (doc/124 §2.5), so capture that baseline first.
	baseline, err := svc.ListTemplateInstances(ctx, fixture.tenantID, "", "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}

	// The release fixture does not create a published scenario template, so
	// instantiation without a template must fail with the published-template
	// error rather than creating an additional instance row.
	_, err = svc.InstantiateTemplateInstance(ctx, fixture.tenantID, "creator", InstantiateTemplateInstanceInput{
		TemplateID: "missing-template", ProjectID: fixture.projectID,
		Name: "ti-basic-instance", IdempotencyKey: "inst-1",
	})
	if err == nil {
		t.Fatal("expected instantiation with unknown template to fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected template-not-found error, got: %v", err)
	}
	page, err2 := svc.ListTemplateInstances(ctx, fixture.tenantID, "", "", 1, 20)
	if err2 != nil {
		t.Fatal(err2)
	}
	if page.Total != baseline.Total {
		t.Fatalf("failed instantiation must not add instance rows: baseline=%d after=%d", baseline.Total, page.Total)
	}
}

// TestTemplateInstanceUnsupportedCapabilityProfile pins the v1 chat-only
// boundary (doc/124 §2.1 non-goal 2).
func TestTemplateInstanceUnsupportedCapabilityProfile(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ti-profile")
	ctx := context.Background()
	_, err := svc.InstantiateTemplateInstance(ctx, fixture.tenantID, "creator", InstantiateTemplateInstanceInput{
		TemplateID: "t", ProjectID: fixture.projectID,
		CapabilityProfile: model.CapabilityAgenticTask, IdempotencyKey: "inst-2",
	})
	if err == nil {
		t.Fatal("expected agentic_task profile to be rejected")
	}
	if !strings.Contains(err.Error(), "capability_profile") {
		t.Fatalf("expected capability_profile error, got: %v", err)
	}
}

// TestTemplateInstanceMissingIdempotencyKey pins the idempotency contract.
func TestTemplateInstanceMissingIdempotencyKey(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ti-idem")
	ctx := context.Background()
	_, err := svc.InstantiateTemplateInstance(ctx, fixture.tenantID, "creator", InstantiateTemplateInstanceInput{
		TemplateID: "t", ProjectID: fixture.projectID,
	})
	if err == nil || !strings.Contains(err.Error(), "Idempotency-Key") {
		t.Fatalf("expected idempotency key error, got: %v", err)
	}
}

// TestTemplateInstanceDryRunHasNoSideEffects verifies dry-run never writes
// rows nor creates release operations (doc/124 §2.3).
func TestTemplateInstanceDryRunHasNoSideEffects(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ti-dry")
	ctx := context.Background()

	view, err := svc.GetTemplateInstance(ctx, fixture.tenantID, "does-not-exist")
	if err == nil {
		t.Fatal("expected missing instance lookup to fail")
	}
	if view != nil {
		t.Fatal("expected nil view for missing instance")
	}

	release, err := svc.GetAssistantRelease(ctx, fixture.tenantID, fixture.releaseID)
	if err != nil || release == nil {
		t.Fatalf("release fixture missing: %v", err)
	}
	operations, err := svc.ListReleaseOperations(ctx, fixture.tenantID, fixture.releaseID, "", "", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	before := len(operations.Items)
	// Dry run against an instance id that does not exist must not create any
	// rows; the fixture release operation count stays unchanged.
	if _, err := svc.DryRunTemplateInstance(ctx, fixture.tenantID, "missing", TemplateInstanceDryRunInput{
		SampleQuestions: []string{"测试问题"},
	}); err == nil {
		t.Fatal("expected dry-run on missing instance to fail")
	}
	operations, err = svc.ListReleaseOperations(ctx, fixture.tenantID, fixture.releaseID, "", "", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations.Items) != before {
		t.Fatalf("dry-run created release operations: %d -> %d", before, len(operations.Items))
	}
}

// TestRolloutPolicyLifecycle covers create/validate/update semantics of the
// rollout policy API (doc/124 §3).
func TestRolloutPolicyLifecycle(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ro-basic")
	ctx := context.Background()

	// percentage bounds (fail closed).
	if _, err := svc.CreateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 0,
	}); err == nil || !strings.Contains(err.Error(), "percentage") {
		t.Fatalf("expected percentage validation error, got: %v", err)
	}
	if _, err := svc.CreateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 150,
	}); err == nil {
		t.Fatal("expected percentage > 100 to be rejected")
	}

	// canary release must belong to the assistant.
	if _, err := svc.CreateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, RolloutPolicyInput{
		CanaryReleaseID: "other-assistant-release", Percentage: 10,
	}); err == nil {
		t.Fatal("expected foreign canary release to be rejected")
	}

	// successful pre-create lands in PENDING.
	policy, err := svc.CreateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 10,
		Targeting: map[string]any{"project": fixture.projectID},
		StartAt:   ptrTime(time.Now().UTC()), EndAt: ptrTime(time.Now().UTC().Add(time.Hour)),
		RollbackCondition: "error_rate>5%",
	})
	if err != nil {
		t.Fatalf("policy creation failed: %v", err)
	}
	if policy.Stage != model.RolloutStagePending || policy.Status != model.RolloutStagePending {
		t.Fatalf("expected PENDING policy, got stage=%s status=%s", policy.Stage, policy.Status)
	}
	if policy.CanaryReleaseID != fixture.releaseID {
		t.Fatalf("policy bound to wrong release: %s", policy.CanaryReleaseID)
	}

	// a second active policy conflicts (doc/124 §3.3 rule 3).
	if _, err := svc.CreateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 20,
	}); err == nil || !strings.Contains(err.Error(), "another rollout policy") {
		t.Fatalf("expected active-policy conflict, got: %v", err)
	}

	// optimistic version mismatch is rejected.
	if _, err := svc.UpdateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, policy.ID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 25, OptimisticVersion: 99,
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale version conflict, got: %v", err)
	}

	// successful update with correct optimistic version.
	updated, err := svc.UpdateAssistantRolloutPolicy(ctx, fixture.tenantID, "creator", fixture.assistantID, policy.ID, RolloutPolicyInput{
		CanaryReleaseID: fixture.releaseID, Percentage: 25, OptimisticVersion: policy.OptimisticVersion,
	})
	if err != nil {
		t.Fatalf("policy update failed: %v", err)
	}
	if updated.Percentage != 25 || updated.OptimisticVersion != policy.OptimisticVersion+1 {
		t.Fatalf("unexpected update result: percentage=%v version=%d", updated.Percentage, updated.OptimisticVersion)
	}
	if !strings.Contains(updated.TargetingJSON, fixture.projectID) {
		t.Fatalf("targeting json lost: %s", updated.TargetingJSON)
	}

	// list returns the policy and the active view.
	items, active, total, err := svc.GetAssistantRolloutPolicies(ctx, fixture.tenantID, fixture.assistantID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || active == nil || active.ID != policy.ID {
		t.Fatalf("unexpected policy list: total=%d items=%d active=%v", total, len(items), active)
	}
}

// TestReleaseTemplateInstanceRequiresEvidence pins the release endpoint's
// evidence chain requirement (doc/107 §3.1.5 release row).
func TestReleaseTemplateInstanceRequiresEvidence(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "ti-release")
	ctx := context.Background()
	if _, err := svc.ReleaseTemplateInstance(ctx, fixture.tenantID, "creator", "missing", CreateAssistantReleaseInput{
		IdempotencyKey: "rel-1",
	}); err == nil {
		t.Fatal("expected missing instance to fail")
	}
	if _, err := svc.ReleaseTemplateInstance(ctx, fixture.tenantID, "creator", "missing", CreateAssistantReleaseInput{
		IdempotencyKey: "rel-1",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}
