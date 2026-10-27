package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_CredentialRotationVerifiesBeforeActivation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Rotation Connection",
		BaseURL: server.URL + "/v1", CredentialRef: "vault://rotation/openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RotateEnterpriseConnectionCredential(ctx, connection.Connection.ID, admin.ID, RotateEnterpriseConnectionCredentialRequest{
		CredentialVersion: "v2",
	}); err == nil {
		t.Fatal("expected failed rotation health validation")
	}
	failed, err := svc.Store.GetEnterpriseConnection(ctx, connection.Connection.ID)
	if err != nil || failed == nil {
		t.Fatalf("reload failed rotation: %v", err)
	}
	if failed.CurrentConnectionVersion != 1 || failed.RuntimeHealth != model.RuntimeHealthUnavailable {
		t.Fatalf("failed rotation changed runtime state: %+v", failed)
	}
	current, err := svc.Store.GetEnterpriseConnectionVersion(ctx, connection.Connection.ID, 1)
	if err != nil || current == nil || current.CredentialVersion != "v1" {
		t.Fatalf("failed rotation changed current credential: %+v", current)
	}

	retried, err := svc.RotateEnterpriseConnectionCredential(ctx, connection.Connection.ID, admin.ID, RotateEnterpriseConnectionCredentialRequest{
		CredentialVersion: "v3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retried.Connection.CurrentConnectionVersion != 3 || retried.Version.CredentialVersion != "v3" ||
		retried.Connection.RuntimeHealth != model.RuntimeHealthHealthy {
		t.Fatalf("unexpected successful rotation: %+v", retried)
	}
	health, err := svc.Store.GetLatestEnterpriseConnectionHealthCheck(ctx, connection.Connection.ID)
	if err != nil || health == nil || health.ConnectionVersion != 3 || health.Health != model.RuntimeHealthHealthy {
		t.Fatalf("rotation health evidence missing: %+v", health)
	}
}

func TestEnterpriseConnectionRetireRequiresCleanDependencies(t *testing.T) {
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	workspace, err := svc.CreateTenant(ctx, "Retirement Workspace")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetireEnterpriseConnection(ctx, admin.ID, connection.Connection.ID); err == nil {
		t.Fatal("active binding must block retirement")
	}
	if _, err := svc.RevokeEnterpriseBinding(ctx, admin.ID, binding.Binding.BindingID); err != nil {
		t.Fatal(err)
	}
	retired, err := svc.RetireEnterpriseConnection(ctx, admin.ID, connection.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Connection.LifecycleStatus != model.EnterpriseLifecycleRetired {
		t.Fatalf("unexpected lifecycle: %+v", retired.Connection)
	}
}

func TestEnterpriseConnectionDeprecateLifecycle(t *testing.T) {
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	deprecated, err := svc.DeprecateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deprecated.Connection.LifecycleStatus != model.EnterpriseLifecycleDeprecated {
		t.Fatalf("unexpected deprecated lifecycle: %+v", deprecated.Connection)
	}
	if _, err := svc.DeprecateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID); err == nil {
		t.Fatal("repeated deprecation must be rejected")
	}
}

func newEnterpriseBindingApproval(t *testing.T, svc *Service, admin *model.User, connection *EnterpriseConnectionView, workspaceID string) *model.Approval {
	t.Helper()
	svc.SetApprovalConfig(config.Approval{
		Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 3,
		ExpireScanIntervalSec: 3600, RetentionDays: 365, PolicyCacheTTLSec: 0,
	})
	steps, err := json.Marshal([]model.ApprovalStepSpec{{
		StepNo: 1, Name: "Platform Governance", ApproverType: model.ApprovalApproverRole,
		ApproverValue: "platform_admin", RequiredApprovals: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "enterprise-binding-bind-policy", TenantID: admin.TenantID,
		ObjectType: model.ApprovalObjectEnterpriseBinding, Action: model.ApprovalActionBind,
		Enabled: true, Priority: 1, ConditionsJSON: "{}", StepsJSON: string(steps),
		ExpireHours: 72, Version: 1, CreatedBy: admin.ID,
	}
	if err := svc.Store.UpsertApprovalPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	approval, err := svc.SubmitApproval(context.Background(), admin.ID, ApprovalSubmitRequest{
		ObjectType: model.ApprovalObjectEnterpriseBinding, Action: model.ApprovalActionBind,
		ObjectID: connection.Connection.ID, Title: "Bind shared connection",
		Payload: map[string]any{
			"connection_id": connection.Connection.ID, "tenant_id": workspaceID,
			"allowed_model_refs": []string{"gpt-4o-mini"},
		},
		IdempotencyKey: "enterprise-binding-bind-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval == nil || approval.Status != model.ApprovalStatusPendingApproval {
		t.Fatalf("expected approval hold: %+v", approval)
	}
	return approval
}

func TestEnterpriseBindingApprovalExecutorCreatesApprovedBinding(t *testing.T) {
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	workspace, err := svc.CreateTenant(ctx, "Approved Binding Workspace")
	if err != nil {
		t.Fatal(err)
	}
	approval := newEnterpriseBindingApproval(t, svc, admin, connection, workspace.ID)
	claimed, err := svc.Store.TransitionApproval(ctx, approval.TenantID, approval.ID, model.ApprovalStatusPendingApproval, model.ApprovalStatusApproved, nil)
	if err != nil || !claimed {
		t.Fatalf("approve approval: claimed=%v err=%v", claimed, err)
	}
	executor, ok := svc.approvalExecutorRegistry().Get(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseBinding, model.ApprovalActionBind))
	if !ok {
		t.Fatal("enterprise binding approval executor is missing")
	}
	if err := executor.Validate(ctx, approval); err != nil {
		t.Fatal(err)
	}
	stale := *approval
	stale.SnapshotJSON = `{"connection_id":"` + connection.Connection.ID + `","connection_version":999}`
	if err := executor.Validate(ctx, &stale); err == nil {
		t.Fatal("changed connection version must invalidate approval")
	}
	result, err := executor.Execute(ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	if result["binding_id"] == "" || result["binding_version"] != int64(1) {
		t.Fatalf("unexpected executor result: %+v", result)
	}
}
