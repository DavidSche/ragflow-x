package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_GovernedWritesAuthorizeBeforeApprovalHold(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.svc.SetProviderURLPolicy(true)
	connection, err := env.svc.CreateEnterpriseConnection(ctx, env.platformID, model.PlatformTenantID, service.CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Governance Connection",
		BaseURL: "https://enterprise.example.com/v1", CredentialRef: "vault://governance/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := env.svc.CreateTenant(ctx, "Governance Workspace")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := env.svc.CreateEnterpriseBinding(ctx, env.platformID, service.CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.svc.CreateUser(ctx, env.tenantID, model.RoleTenantAdmin, service.CreateUserRequest{
		Username: "unauthorized-" + id.New()[:8], Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := env.tokenFor(t, user.ID, env.tenantID, model.RoleOperator)
	for _, action := range []string{
		model.ApprovalActionCreate, model.ApprovalActionUpdate,
		model.ApprovalActionRotateCredential, model.ApprovalActionRetire,
		model.ApprovalActionDeprecate, model.ApprovalActionBind,
		model.ApprovalActionUpdate, model.ApprovalActionRevoke,
	} {
		env.createPolicy(t, env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin), model.ApprovalObjectEnterpriseConnection, action)
	}
	connectionID := connection.Connection.ID
	bindingID := binding.Binding.BindingID
	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"create connection", http.MethodPost, "/api/v1/enterprise-connections", service.CreateEnterpriseConnectionRequest{ProviderName: "openai-api-compatible", DisplayName: "New"}},
		{"update connection", http.MethodPut, "/api/v1/enterprise-connections/" + connectionID, service.UpdateEnterpriseConnectionRequest{DisplayName: "Changed"}},
		{"rotate credential", http.MethodPost, "/api/v1/enterprise-connections/" + connectionID + "/rotate-credential", service.RotateEnterpriseConnectionCredentialRequest{CredentialVersion: "v2"}},
		{"retire connection", http.MethodPost, "/api/v1/enterprise-connections/" + connectionID + "/retire", nil},
		{"deprecate connection", http.MethodPost, "/api/v1/enterprise-connections/" + connectionID + "/deprecate", nil},
		{"create binding", http.MethodPost, "/api/v1/enterprise-connections/" + connectionID + "/bindings", service.CreateEnterpriseBindingRequest{TenantID: workspace.ID, AllowedModelRefs: []string{"gpt-4o-mini"}}},
		{"update binding", http.MethodPut, "/api/v1/enterprise-connections/" + connectionID + "/bindings/" + bindingID, service.UpdateEnterpriseBindingRequest{AllowedModelRefs: []string{"gpt-4o"}}},
		{"revoke binding", http.MethodPost, "/api/v1/enterprise-connections/" + connectionID + "/bindings/" + bindingID + "/revoke", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.doRequest(t, tt.method, tt.path, tt.body, token)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("unauthorized governed write: got %d body %s", resp.Code, resp.Body.String())
			}
		})
	}
	_, approvals, err := env.svc.Store.ListApprovals(ctx, env.tenantID, repository.ApprovalFilter{}, repository.ApprovalListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if approvals != 0 {
		t.Fatalf("unauthorized governed writes created %d approvals", approvals)
	}
}

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_ReadOnlyEndpointsEnforceGovernanceScope(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.svc.SetProviderURLPolicy(true)
	connection, err := env.svc.CreateEnterpriseConnection(ctx, env.platformID, model.PlatformTenantID, service.CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Scoped Connection",
		BaseURL: "https://enterprise.example.com/v1", CredentialRef: "vault://scoped/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceA, err := env.svc.CreateTenant(ctx, "Scoped Workspace A")
	if err != nil {
		t.Fatal(err)
	}
	workspaceB, err := env.svc.CreateTenant(ctx, "Scoped Workspace B")
	if err != nil {
		t.Fatal(err)
	}
	bindingA, err := env.svc.CreateEnterpriseBinding(ctx, env.platformID, service.CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspaceA.ID,
		AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingB, err := env.svc.CreateEnterpriseBinding(ctx, env.platformID, service.CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspaceB.ID,
		AllowedModelRefs: []string{"gpt-4o"},
	})
	if err != nil {
		t.Fatal(err)
	}
	userA, err := env.svc.CreateUser(ctx, workspaceA.ID, model.RolePlatformAdmin, service.CreateUserRequest{
		Username: "workspace-a-" + id.New()[:8], Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenA := env.tokenFor(t, userA.ID, workspaceA.ID, model.RoleTenantAdmin)

	for _, tt := range []struct {
		name   string
		method string
		path   string
	}{
		{"connection", http.MethodGet, "/api/v1/enterprise-connections/" + connection.Connection.ID},
		{"connection versions", http.MethodGet, "/api/v1/enterprise-connections/" + connection.Connection.ID + "/versions"},
		{"foreign binding", http.MethodGet, "/api/v1/enterprise-connections/" + connection.Connection.ID + "/bindings/" + bindingB.Binding.BindingID},
		{"foreign binding versions", http.MethodGet, "/api/v1/enterprise-connections/" + connection.Connection.ID + "/bindings/" + bindingB.Binding.BindingID + "/versions"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.doRequest(t, tt.method, tt.path, nil, tokenA)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("cross-scope read: got %d body %s", resp.Code, resp.Body.String())
			}
		})
	}

	resp := env.doRequest(t, http.MethodGet, "/api/v1/enterprise-connections", nil, tokenA)
	if resp.Code != http.StatusOK {
		t.Fatalf("scoped list: got %d body %s", resp.Code, resp.Body.String())
	}
	var list struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Items) != 0 {
		t.Fatalf("platform connection leaked into workspace list: %s", resp.Body.String())
	}
	resp = env.doRequest(t, http.MethodGet, "/api/v1/enterprise-connections/"+connection.Connection.ID+"/bindings", nil, tokenA)
	if resp.Code != http.StatusOK {
		t.Fatalf("scoped binding list: got %d body %s", resp.Code, resp.Body.String())
	}
	var page struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	bindingItem, _ := page.Data.Items[0]["binding"].(map[string]any)
	if len(page.Data.Items) != 1 || bindingItem["binding_id"] != bindingA.Binding.BindingID {
		t.Fatalf("binding list leaked cross-tenant data: %s", resp.Body.String())
	}
}

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_BindingApprovalGateRejectsInvalidWorkspaceTarget(t *testing.T) {
	env := setupTestEnv(t)
	env.svc.SetProviderURLPolicy(true)
	env.createPolicy(t, env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin), model.ApprovalObjectEnterpriseBinding, model.ApprovalActionBind)
	connection, err := env.svc.CreateEnterpriseConnection(context.Background(), env.platformID, model.PlatformTenantID, service.CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Binding Gate",
		BaseURL: "https://enterprise.example.com/v1", CredentialRef: "vault://binding/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := service.CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: id.New(),
		AllowedModelRefs: []string{"gpt-4o-mini"},
	}
	resp := env.doRequest(t, http.MethodPost, "/api/v1/enterprise-connections/"+connection.Connection.ID+"/bindings", body, env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin))
	if resp.Code == http.StatusAccepted {
		t.Fatalf("invalid workspace target must be rejected before approval: %s", resp.Body.String())
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("invalid workspace target: got %d body %s", resp.Code, resp.Body.String())
	}
	_, approvals, err := env.svc.Store.ListApprovals(context.Background(), model.PlatformTenantID, repository.ApprovalFilter{}, repository.ApprovalListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if approvals != 0 {
		t.Fatalf("invalid workspace target created %d approvals", approvals)
	}
}

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_ApprovalAcceptedResponseHidesCredentialMaterial(t *testing.T) {
	env := setupTestEnv(t)
	env.svc.SetProviderURLPolicy(true)
	env.createPolicy(t, env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin), model.ApprovalObjectEnterpriseConnection, model.ApprovalActionRotateCredential)
	connection, err := env.svc.CreateEnterpriseConnection(context.Background(), env.platformID, model.PlatformTenantID, service.CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Credential Projection",
		BaseURL: "https://enterprise.example.com/v1", CredentialRef: "vault://projection/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := service.RotateEnterpriseConnectionCredentialRequest{
		CredentialRef: "vault://projection/rotated", CredentialVersion: "v2",
	}
	resp := env.doRequest(t, http.MethodPost, "/api/v1/enterprise-connections/"+connection.Connection.ID+"/rotate-credential", body, env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin))
	if resp.Code != http.StatusAccepted {
		t.Fatalf("credential rotation should hold for approval: got %d body %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "vault://") || strings.Contains(resp.Body.String(), "credential_ref") {
		t.Fatalf("approval response exposed credential material: %s", resp.Body.String())
	}
}
