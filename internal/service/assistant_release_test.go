package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type releaseFixture struct {
	tenantID    string
	projectID   string
	assistantID string
	releaseID   string
	datasetID   string
	chatID      string
}

func newReleaseFixture(t *testing.T, name string) (*Service, releaseFixture) {
	t.Helper()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(context.Background(), tenant.ID, name+"-dataset")
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(context.Background(), tenant.ID, name+"-project", "canonical release fixture")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(context.Background(), tenant.ID, name+"-chat", []string{dataset.ID})
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, gateID, approvalID := createReleaseEvidenceFixture(t, svc, tenant.ID, name)
	input := CreateAssistantReleaseInput{
		ProjectID:            project.ID,
		ScenarioTemplateID:   "tmpl-" + name,
		TemplateVersionID:    "tmplver-" + name,
		Name:                 name,
		Description:          "canonical release fixture",
		OwnerID:              "owner-" + name,
		ScenarioPackSchema:   "1",
		ScenarioPackPayload:  "{}",
		ExecutionContract:    "{}",
		PolicyVersion:        "policy-v1",
		PolicyJSON:           "{}",
		RuntimeProfile:       "{}",
		DesiredState:         "{}",
		DesiredTargetID:      chat.ID,
		DesiredTargetType:    "chat",
		DesiredTargetVersion: "1",
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_chat", TargetType: "chat", TargetID: chat.ID,
			TargetVersion: "1", RGXResourceID: chat.ID, OwnershipVerified: true,
		}},
		DatasetBindings: []DatasetBindingInput{{
			BindingID: "dataset-main", DatasetID: dataset.ID, DatasetVersion: "1",
		}},
		ModelRouteBindings: []ModelRouteBindingInput{{
			BindingID: "model-main", ProviderID: "provider-main", ModelID: "model-main",
		}},
		EvidenceBundleID: evidenceID, GateDecisionID: gateID,
		GateResult: model.GateDecisionPass, ApprovalID: approvalID,
		IdempotencyKey: "create-" + name,
	}
	release, err := svc.CreateAssistantRelease(context.Background(), tenant.ID, "creator-"+name, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := approveReleaseFixture(svc, tenant.ID, "creator-"+name, release.ID); err != nil {
		t.Fatal(err)
	}
	return svc, releaseFixture{tenantID: tenant.ID, projectID: project.ID, assistantID: release.AssistantID, releaseID: release.ID, datasetID: dataset.ID, chatID: chat.ID}
}

func createReleaseEvidenceFixture(t *testing.T, svc *Service, tenantID, name string) (string, string, string) {
	t.Helper()
	now := time.Now().UTC()
	candidateID := "cand-" + name
	evidence := &model.EvidenceBundle{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidateID, CandidateVersion: 1,
		SnapshotID: "snapshot-" + name, SnapshotHash: "hash-" + name, ModelRouteVersion: "1",
		EvaluationRunID: "run-" + name, EvalSetID: "set-" + name, EvalSetVersion: 1,
		EvalSetHash: "set-hash-" + name, EvaluationPolicyVersion: "1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "1", AggregationPolicyHash: "aggregation-hash",
		SecurityEvidence: "{}", PolicyEvidence: "{}", RiskEvidence: "{}", PermissionEvidence: "{}",
		ConfigurationEvidence: "{}", ApprovalEvidence: "{}", EvidenceItems: "[]",
		HashAlgorithm: model.HashAlgorithmSHA256, BundleHash: "bundle-hash", CreatedBy: "tester", CreatedAt: now,
	}
	if err := svc.Store.CreateEvidenceBundle(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	gate := &model.ReleaseGateDecision{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidateID, CandidateVersion: 1,
		DecisionVersion: now.UnixNano(), EvidenceBundleID: evidence.ID, Environment: "production",
		EnvironmentPolicyVersion: "1", EnvironmentPolicyHash: "env-hash", SubGateStates: "{}",
		Decision: model.GateDecisionPass, ActiveGate: true, Actor: "tester",
		ApprovalID: id.New(), CreatedAt: now,
	}
	if _, err := svc.Store.CreateGateDecision(context.Background(), gate); err != nil {
		t.Fatal(err)
	}
	approval := &model.Approval{
		ID: gate.ApprovalID, TenantID: tenantID, RequestNo: "req-" + name, ObjectType: "assistant-release",
		ObjectID: "release-" + name, Action: "release", Title: "Assistant release", PayloadJSON: "{}",
		SnapshotJSON: "{}", Status: model.ApprovalStatusApproved, PolicyID: "policy", CurrentStep: 1,
		RequesterID: "tester", IdempotencyKey: "approval-" + name, ExpiresAt: now.Add(time.Hour),
		SubmittedAt: now, DecidedAt: now, ResultJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateApprovalWithAudit(context.Background(), approval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return evidence.ID, gate.ID, approval.ID
}

func approveReleaseFixture(svc *Service, tenantID, userID, releaseID string) error {
	states := []string{
		model.ReleaseDraft, model.ReleaseValidating, model.ReleaseSnapshotted,
		model.ReleaseEvaluating, model.ReleaseGated, model.ReleaseApproved,
	}
	for index := 0; index < len(states)-1; index++ {
		key := "transition-" + releaseID + "-" + states[index+1]
		if _, err := svc.TransitionAssistantRelease(context.Background(), tenantID, userID, releaseID, states[index], states[index+1], OperationInput{IdempotencyKey: key}); err != nil {
			return err
		}
	}
	return nil
}

func TestAssistantReleaseSnapshotLifecycleAndActivation(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "lifecycle")
	ctx := context.Background()
	release, err := svc.GetAssistantRelease(ctx, fixture.tenantID, fixture.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseState != model.ReleaseApproved {
		t.Fatalf("expected APPROVED fixture release, got %s", release.ReleaseState)
	}
	manifest, err := svc.GetSnapshotManifest(ctx, fixture.tenantID, release.SnapshotManifestID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Complete || manifest.SnapshotHash == "" {
		t.Fatal("manifest must be complete and hashed")
	}
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-lifecycle", OperationInput{
		IdempotencyKey: "apply-lifecycle", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	verified, err := svc.GetAssistantRelease(ctx, fixture.tenantID, fixture.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ReleaseState != model.ReleaseVerified || verified.ReconcileStatus != model.ReconcileConsistent {
		t.Fatalf("expected VERIFIED/CONSISTENT, got %s/%s", verified.ReleaseState, verified.ReconcileStatus)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-lifecycle", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-lifecycle", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	assistant, err := svc.GetAssistant(ctx, fixture.tenantID, fixture.assistantID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.CurrentAssistantReleaseID != fixture.releaseID {
		t.Fatalf("assistant current release mismatch: %s", assistant.CurrentAssistantReleaseID)
	}
	active, err := svc.ListAssistantReleases(ctx, fixture.tenantID, fixture.assistantID, model.ReleaseActive, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if active.Total != 1 || active.Items[0].ID != fixture.releaseID {
		t.Fatalf("expected exactly one ACTIVE release, got %+v", active.Items)
	}
}

func TestAssistantReleaseIdempotencyAndTenantIsolation(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "idempotent")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-idempotent", OperationInput{
		IdempotencyKey: "apply-idempotent", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-idempotent", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-key", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-idempotent", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-key", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.OperationState != model.OperationSucceeded {
		t.Fatalf("activation retry must return same operation, got %s and %s", first.ID, second.ID)
	}
	_, err = svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-idempotent", OperationInput{
		IdempotencyKey: "activate-key", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if !expectHTTPError(err, 409, 40930) {
		t.Fatalf("operation type reuse must conflict, got %v", err)
	}
	_, err = svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-idempotent", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-key", ExpectedCurrentReleaseID: fixture.releaseID,
		FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if !expectHTTPError(err, 409, 40930) {
		t.Fatalf("different activation payload reuse must conflict, got %v", err)
	}
	if _, err := svc.GetAssistantRelease(ctx, "other-tenant", fixture.releaseID); err == nil || !expectHTTPError(err, 404, 404) {
		t.Fatalf("cross-tenant read must be denied, got %v", err)
	}
}

func TestAssistantReleaseCreationBlocksUnverifiedCapability(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "capability-gate")
	ctx := context.Background()
	svc.RAGFlow = fixedVersionEngine{version: "0.28.0"}
	evidenceID, gateID, approvalID := createReleaseEvidenceFixture(t, svc, fixture.tenantID, "capability-block")
	_, err := svc.CreateAssistantRelease(ctx, fixture.tenantID, "creator", CreateAssistantReleaseInput{
		ProjectID: fixture.projectID, Name: "capability-gate-blocked", OwnerID: "owner",
		ScenarioPackSchema: "1", ScenarioPackPayload: "{}", TemplateVersionID: "template-v2",
		ExecutionContract: "{}", PolicyVersion: "policy-v1", PolicyJSON: "{}",
		RuntimeProfile: "{}", DesiredState: "{}", DesiredTargetID: fixture.chatID,
		DesiredTargetType: "chat", DesiredTargetVersion: "1",
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_chat", TargetType: "chat", TargetID: fixture.chatID,
			TargetVersion: "1", RGXResourceID: fixture.chatID, OwnershipVerified: true,
		}},
		EvidenceBundleID: evidenceID, GateDecisionID: gateID, GateResult: model.GateDecisionPass,
		ApprovalID: approvalID, IdempotencyKey: "create-capability-block",
	})
	if !expectHTTPError(err, 422, 42260) {
		t.Fatalf("unverified capability must block release creation, got %v", err)
	}
}

func TestAssistantReleaseProviderDriftFailsClosed(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "drift")
	ctx := context.Background()
	svc.Store = &driftChatStore{Store: svc.Store}
	_, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-drift", OperationInput{
		IdempotencyKey: "apply-drift", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if err == nil || !expectHTTPError(err, 422, 42230) {
		t.Fatalf("expected provider drift, got %v", err)
	}
	release, err := svc.GetAssistantRelease(ctx, fixture.tenantID, fixture.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReconcileStatus != model.ReconcileUnknown || release.ReleaseState != model.ReleaseApplyingFailed {
		t.Fatalf("failed reconcile must preserve release, got %s/%s", release.ReconcileStatus, release.ReleaseState)
	}
}

func TestAssistantReleaseFailureEmitsHubNotification(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "release-notify")
	ctx := context.Background()
	captured := &capturedNotifier{}
	hub := notify.NewHubWithNotifiers([]notify.Notifier{captured}, 0)
	notify.Set(hub)
	defer func() {
		hub.Shutdown()
		notify.Set(nil)
	}()
	svc.Store = &driftChatStore{Store: svc.Store}
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-notify", OperationInput{
		IdempotencyKey: "apply-notify", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err == nil || !expectHTTPError(err, 422, 42230) {
		t.Fatalf("expected provider drift, got %v", err)
	}
	hub.Shutdown()
	var found *notify.Event
	for index, event := range captured.snapshot() {
		if event.Type == "release.failed" {
			found = &captured.snapshot()[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("release failure notification missing: %+v", captured.snapshot())
	}
	if found.ResourceID != fixture.releaseID || found.Fields["step"] != "Apply" ||
		found.Fields["error_code"] != "PROVIDER_DRIFT" {
		t.Fatalf("unexpected release failure event: %+v", found)
	}
	audits, _, err := svc.ListAudits(ctx, fixture.tenantID, 1, 20, repository.AuditFilter{Action: "assistant_release.failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Result != "FAILED" || audits[0].ResourceID != fixture.releaseID {
		t.Fatalf("release failure audit missing: %+v", audits)
	}
}

type targetUpdateRecorder struct {
	ragflow.Mock
	agentRequests  []ragflow.UpdateAgentRequest
	searchRequests []ragflow.UpdateSearchAppRequest
}

func (r *targetUpdateRecorder) UpdateAgent(ctx context.Context, agentID string, req ragflow.UpdateAgentRequest) error {
	r.agentRequests = append(r.agentRequests, req)
	return r.Mock.UpdateAgent(ctx, agentID, req)
}

func (r *targetUpdateRecorder) UpdateSearchApp(ctx context.Context, searchAppID string, req ragflow.UpdateSearchAppRequest) (*ragflow.SearchApp, error) {
	r.searchRequests = append(r.searchRequests, req)
	return r.Mock.UpdateSearchApp(ctx, searchAppID, req)
}

func createTargetApplyRelease(t *testing.T, svc *Service, fixture releaseFixture, name, targetID, targetType, desired string) *model.AssistantRelease {
	t.Helper()
	evidenceID, gateID, approvalID := createReleaseEvidenceFixture(t, svc, fixture.tenantID, name)
	project, err := svc.CreateProject(context.Background(), fixture.tenantID, name+"-project", "provider target apply")
	if err != nil {
		t.Fatal(err)
	}
	release, err := svc.CreateAssistantRelease(context.Background(), fixture.tenantID, "creator", CreateAssistantReleaseInput{
		ProjectID: project.ID, Name: name + "-assistant", OwnerID: "owner",
		ScenarioPackSchema: "1", ScenarioPackPayload: "{}", TemplateVersionID: "template-v1", ExecutionContract: "{}", PolicyVersion: "policy-v1",
		PolicyJSON: "{}", RuntimeProfile: "{}", DesiredState: desired,
		DesiredTargetID: targetID, DesiredTargetType: targetType,
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_" + targetType, TargetType: targetType, TargetID: targetID,
			TargetVersion: "1", RGXResourceID: targetID, OwnershipVerified: true,
		}},
		DatasetBindings:    []DatasetBindingInput{{BindingID: "dataset-main", DatasetID: fixture.datasetID, DatasetVersion: "1"}},
		ModelRouteBindings: []ModelRouteBindingInput{{BindingID: "model-main", ProviderID: "provider-main", ModelID: "model-main"}},
		EvidenceBundleID:   evidenceID, GateDecisionID: gateID, GateResult: model.GateDecisionPass,
		ApprovalID: approvalID, IdempotencyKey: "create-" + name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := approveReleaseFixture(svc, fixture.tenantID, "creator", release.ID); err != nil {
		t.Fatal(err)
	}
	return release
}

func TestAssistantReleaseAppliesAgentTarget(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "agent-apply")
	ctx := context.Background()
	recorder := &targetUpdateRecorder{Mock: *ragflow.NewMock()}
	svc.RAGFlow = recorder
	agent, err := svc.CreateAgent(ctx, fixture.tenantID, "release-agent", map[string]any{"initial": true}, false, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	release := createTargetApplyRelease(t, svc, fixture, "agent-release", agent.ID, "agent", `{"dsl":{"workflow":"v2"},"release":true}`)
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, release.ID, "worker", OperationInput{
		IdempotencyKey: "apply-agent", FencingToken: svc.currentFencingToken(t, release.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.agentRequests) != 1 || recorder.agentRequests[0].Dsl["workflow"] != "v2" ||
		recorder.agentRequests[0].Release == nil || !*recorder.agentRequests[0].Release {
		t.Fatalf("agent apply request mismatch: %+v", recorder.agentRequests)
	}
	live, err := svc.RAGFlow.GetAgent(ctx, agent.ID)
	if err != nil || live == nil || !live.Release || live.Dsl["workflow"] != "v2" {
		t.Fatalf("agent target was not materialized: %+v/%v", live, err)
	}
	live.Dsl["workflow"] = "manual-edit"
	_, err = svc.ReconcileAssistantRelease(ctx, fixture.tenantID, "worker", release.ID, OperationInput{
		IdempotencyKey: "reconcile-agent-drift", FencingToken: svc.currentFencingToken(t, release.ID),
	})
	if err == nil || !expectHTTPError(err, 422, 42230) {
		t.Fatalf("manual agent edit must fail closed, got %v", err)
	}
	drifted, err := svc.GetAssistantRelease(ctx, fixture.tenantID, release.ID)
	if err != nil || drifted.ReconcileStatus != model.ReconcileDrifted {
		t.Fatalf("manual agent edit must persist DRIFTED, got %+v/%v", drifted, err)
	}
}

func TestAssistantReleaseAppliesSearchAppTarget(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "search-apply")
	ctx := context.Background()
	recorder := &targetUpdateRecorder{Mock: *ragflow.NewMock()}
	svc.RAGFlow = recorder
	searchApp, err := svc.CreateSearchApp(ctx, fixture.tenantID, "release-search", &ragflow.SearchConfig{TopK: 7})
	if err != nil {
		t.Fatal(err)
	}
	release := createTargetApplyRelease(t, svc, fixture, "search-release", searchApp.ID, "search_app", `{"search_config":{"top_k":11}}`)
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, release.ID, "worker", OperationInput{
		IdempotencyKey: "apply-search", FencingToken: svc.currentFencingToken(t, release.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.searchRequests) < 2 || recorder.searchRequests[len(recorder.searchRequests)-1].SearchConfig == nil || recorder.searchRequests[len(recorder.searchRequests)-1].SearchConfig.TopK != 11 {
		t.Fatalf("search app apply request mismatch: %+v", recorder.searchRequests)
	}
	live, err := svc.GetSearchAppConfig(ctx, fixture.tenantID, searchApp.ID, false)
	datasetLink, linkErr := svc.Store.GetDatasetLink(ctx, fixture.tenantID, fixture.datasetID)
	if err != nil || live.SearchConfig == nil || live.SearchConfig.TopK != 11 ||
		datasetLink == nil || !equalStringSets(live.SearchConfig.KbIDs, []string{datasetLink.RAGFlowDatasetID}) {
		if linkErr != nil {
			t.Fatal(linkErr)
		}
		t.Fatalf("search app target was not materialized: %+v/%v", live, err)
	}
	_, err = svc.RAGFlow.UpdateSearchApp(ctx, searchApp.ID, ragflow.UpdateSearchAppRequest{Name: "release-search", SearchConfig: &ragflow.SearchConfig{TopK: 99}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReconcileAssistantRelease(ctx, fixture.tenantID, "worker", release.ID, OperationInput{
		IdempotencyKey: "reconcile-search-drift", FencingToken: svc.currentFencingToken(t, release.ID),
	})
	if err == nil || !expectHTTPError(err, 422, 42230) {
		t.Fatalf("manual search app edit must fail closed, got %v", err)
	}
	drifted, err := svc.GetAssistantRelease(ctx, fixture.tenantID, release.ID)
	if err != nil || drifted.ReconcileStatus != model.ReconcileDrifted {
		t.Fatalf("manual search app edit must persist DRIFTED, got %+v/%v", drifted, err)
	}
}

func TestAssistantReleaseStaleWorkerCannotOverwrite(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "stale")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-stale", OperationInput{
		IdempotencyKey: "apply-stale", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-stale", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-first", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "stale-worker", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-stale", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	})
	if err == nil || !expectHTTPError(err, 409, 40930) {
		t.Fatalf("expected stale worker rejection, got %v", err)
	}
}

func TestAssistantReleaseConcurrentActivationKeepsOneActive(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "concurrent")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-concurrent", OperationInput{
		IdempotencyKey: "apply-concurrent", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var successes int
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "worker", fixture.releaseID, ActivationInput{
				IdempotencyKey: "activate-concurrent-" + string(rune('a'+index)),
				FencingToken:   svc.currentFencingToken(t, fixture.releaseID),
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected one successful concurrent activation, got %d", successes)
	}
	active, err := svc.ListAssistantReleases(ctx, fixture.tenantID, fixture.assistantID, model.ReleaseActive, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if active.Total != 1 {
		t.Fatalf("expected one ACTIVE release, got %d", active.Total)
	}
}

func TestAssistantRollbackRestoresImmutableSnapshot(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "rollback")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-rollback", OperationInput{
		IdempotencyKey: "apply-rollback", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-rollback", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-rollback", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	first := fixture.releaseID
	contract := map[string]interface{}{"revision": 2}
	contractJSON, _ := json.Marshal(contract)
	secondEvidence, secondGate, secondApproval := createReleaseEvidenceFixture(t, svc, fixture.tenantID, "rollback-v2")
	second, err := svc.CreateAssistantRelease(ctx, fixture.tenantID, "creator-rollback", CreateAssistantReleaseInput{
		AssistantID:          fixture.assistantID,
		ProjectID:            fixture.projectID,
		ScenarioTemplateID:   "tmpl-rollback",
		TemplateVersionID:    "tmplver-rollback-v2",
		Name:                 "rollback",
		OwnerID:              "owner-rollback",
		ScenarioPackSchema:   "1",
		ScenarioPackPayload:  "{}",
		ExecutionContract:    string(contractJSON),
		PolicyVersion:        "policy-v1",
		PolicyJSON:           "{}",
		RuntimeProfile:       "{}",
		DesiredState:         "{}",
		DesiredTargetID:      fixture.chatID,
		DesiredTargetType:    "chat",
		DesiredTargetVersion: "1",
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_chat", TargetType: "chat", TargetID: fixture.chatID,
			TargetVersion: "1", RGXResourceID: fixture.chatID, OwnershipVerified: true,
		}},
		DatasetBindings: []DatasetBindingInput{{BindingID: "dataset-main", DatasetID: fixture.datasetID, DatasetVersion: "1"}},
		ModelRouteBindings: []ModelRouteBindingInput{{
			BindingID: "model-main", ProviderID: "provider-main", ModelID: "model-main",
		}},
		EvidenceBundleID: secondEvidence, GateDecisionID: secondGate,
		GateResult: model.GateDecisionPass, ApprovalID: secondApproval,
		IdempotencyKey: "create-rollback-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := approveReleaseFixture(svc, fixture.tenantID, "creator-rollback", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, second.ID, "apply-rollback-v2", OperationInput{
		IdempotencyKey: "apply-rollback-v2", FencingToken: svc.currentFencingToken(t, second.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activator-rollback", second.ID, ActivationInput{
		IdempotencyKey: "activate-rollback-v2", FencingToken: svc.currentFencingToken(t, second.ID),
		ExpectedCurrentReleaseID: first,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RollbackAssistantRelease(ctx, fixture.tenantID, "rollback-owner", second.ID, first, RollbackInput{
		IdempotencyKey: "rollback-first", FencingToken: svc.currentFencingToken(t, second.ID),
	}); err != nil {
		t.Fatal(err)
	}
	assistant, err := svc.GetAssistant(ctx, fixture.tenantID, fixture.assistantID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.CurrentAssistantReleaseID != first {
		t.Fatalf("rollback must restore first immutable release, got %s", assistant.CurrentAssistantReleaseID)
	}
	active, err := svc.ListAssistantReleases(ctx, fixture.tenantID, fixture.assistantID, model.ReleaseActive, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if active.Total != 1 || active.Items[0].ID != first {
		t.Fatalf("rollback must leave exactly one ACTIVE first release, got %+v", active.Items)
	}
}

func TestAssistantReleaseBlocksUnsafeCanaryAndRollbackStates(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "unsafe-states")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-unsafe-first", OperationInput{
		IdempotencyKey: "apply-unsafe-first", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activate-unsafe-first", fixture.releaseID, ActivationInput{
		IdempotencyKey: "activate-unsafe-first", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	first := fixture.releaseID
	releases := make([]*model.AssistantRelease, 0, 2)
	for revision := 2; revision <= 3; revision++ {
		contractJSON, _ := json.Marshal(map[string]int{"revision": revision})
		key := fmt.Sprintf("unsafe-v%d", revision)
		evidenceID, gateID, approvalID := createReleaseEvidenceFixture(t, svc, fixture.tenantID, key)
		release, err := svc.CreateAssistantRelease(ctx, fixture.tenantID, "creator-unsafe", CreateAssistantReleaseInput{
			AssistantID:          fixture.assistantID,
			ProjectID:            fixture.projectID,
			ScenarioTemplateID:   "tmpl-unsafe",
			TemplateVersionID:    "tmplver-" + key,
			Name:                 key,
			OwnerID:              "owner-unsafe",
			ScenarioPackSchema:   "1",
			ScenarioPackPayload:  "{}",
			ExecutionContract:    string(contractJSON),
			PolicyVersion:        "policy-v1",
			PolicyJSON:           "{}",
			RuntimeProfile:       "{}",
			DesiredState:         "{}",
			DesiredTargetID:      fixture.chatID,
			DesiredTargetType:    "chat",
			DesiredTargetVersion: "1",
			CapabilityBindings: []CapabilityBindingInput{{
				BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
				Adapter: "ragflow_chat", TargetType: "chat", TargetID: fixture.chatID,
				TargetVersion: "1", RGXResourceID: fixture.chatID, OwnershipVerified: true,
			}},
			DatasetBindings: []DatasetBindingInput{{BindingID: "dataset-main", DatasetID: fixture.datasetID, DatasetVersion: "1"}},
			ModelRouteBindings: []ModelRouteBindingInput{{
				BindingID: "model-main", ProviderID: "provider-main", ModelID: "model-main",
			}},
			EvidenceBundleID: evidenceID, GateDecisionID: gateID,
			GateResult: model.GateDecisionPass, ApprovalID: approvalID,
			IdempotencyKey: "create-" + key,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := approveReleaseFixture(svc, fixture.tenantID, "creator-unsafe", release.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, release.ID, "apply-"+key, OperationInput{
			IdempotencyKey: "apply-" + key, FencingToken: svc.currentFencingToken(t, release.ID),
		}); err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activate-unsafe-canary", releases[0].ID, ActivationInput{
		IdempotencyKey: "activate-unsafe-canary", FencingToken: svc.currentFencingToken(t, releases[0].ID),
		Canary: true, RolloutPercentage: 5, ExpectedCurrentReleaseID: first,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ActivateAssistantRelease(ctx, fixture.tenantID, "activate-unsafe-stable", releases[1].ID, ActivationInput{
		IdempotencyKey: "activate-unsafe-stable", FencingToken: svc.currentFencingToken(t, releases[1].ID),
		ExpectedCurrentReleaseID: first,
	})
	if err == nil || !expectHTTPError(err, 409, 40930) {
		t.Fatalf("stable activation must be blocked while canary is active, got %v", err)
	}
	_, err = svc.RollbackAssistantRelease(ctx, fixture.tenantID, "rollback-unsafe", first, releases[0].ID, RollbackInput{
		IdempotencyKey: "rollback-unsafe", FencingToken: svc.currentFencingToken(t, first),
	})
	if err == nil || !expectHTTPError(err, 409, 40930) {
		t.Fatalf("rollback to CANARY_ACTIVE must be blocked, got %v", err)
	}
	assistant, err := svc.GetAssistant(ctx, fixture.tenantID, fixture.assistantID)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.CurrentAssistantReleaseID != first {
		t.Fatalf("unsafe attempts must not move stable pointer, got %s", assistant.CurrentAssistantReleaseID)
	}
}

func TestAssistantReleaseCrashRecoveryAndCompensation(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "recovery")
	ctx := context.Background()
	if _, err := svc.ApplyAssistantRelease(ctx, fixture.tenantID, fixture.releaseID, "apply-recovery", OperationInput{
		IdempotencyKey: "apply-recovery", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	operations, err := svc.ListReleaseOperations(ctx, fixture.tenantID, fixture.releaseID, model.OperationApply, model.OperationSucceeded, 1, 10)
	if err != nil || len(operations.Items) == 0 {
		t.Fatalf("expected succeeded apply operation, got %v/%v", len(operations.Items), err)
	}
	operation := operations.Items[0]
	operation.OperationState = model.OperationRunning
	if err := svc.Store.UpdateReleaseOperation(ctx, &operation); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.RecoverAssistantReleaseOperation(ctx, fixture.tenantID, "recovery-worker", operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.OperationState != model.OperationSucceeded || recovered.Attempt != 2 {
		t.Fatalf("expected recovered operation, got %+v", recovered)
	}

	if err := svc.Store.UpdateAssistantReleaseState(ctx, fixture.tenantID, fixture.releaseID, model.ReleaseVerified, model.ReleaseApplying, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpdateAssistantReleaseState(ctx, fixture.tenantID, fixture.releaseID, model.ReleaseApplying, model.ReleaseApplyingFailed, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompensateAssistantRelease(ctx, fixture.tenantID, "operator", fixture.releaseID, OperationInput{
		IdempotencyKey: "compensate-release", FencingToken: svc.currentFencingToken(t, fixture.releaseID),
	}); err != nil {
		t.Fatal(err)
	}
	release, err := svc.GetAssistantRelease(ctx, fixture.tenantID, fixture.releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseState != model.ReleaseCompensated {
		t.Fatalf("expected COMPENSATED release, got %s", release.ReleaseState)
	}
}

func (s *Service) currentFencingToken(t *testing.T, releaseID string) int64 {
	t.Helper()
	release, err := s.GetAssistantRelease(context.Background(), s.currentTenantID(t), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	return release.FencingToken
}

func (s *Service) currentTenantID(t *testing.T) string {
	t.Helper()
	tenants, _, err := s.Store.ListTenants(context.Background(), 1, 1, repository.TenantFilter{})
	if err != nil || len(tenants) == 0 {
		t.Fatalf("fixture tenant unavailable: %v", err)
	}
	return tenants[0].ID
}

type driftChatStore struct {
	repository.Store
}

func expectHTTPError(err error, status, code int) bool {
	var httpErr *httperr.Error
	return errors.As(err, &httpErr) && httpErr.Status == status && httpErr.Code == code
}

func (s *driftChatStore) GetChatShadow(ctx context.Context, tenantID, chatID string, scopeAll bool) (*model.ChatShadow, error) {
	return nil, httperr.New(422, 42230, "provider drift detected")
}
