package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func approvalIDFromResponse(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Data struct {
			ApprovalID string `json:"approval_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode approval response: %v", err)
	}
	if envelope.Data.ApprovalID == "" {
		t.Fatalf("approval response missing approval_id: %s", body)
	}
	return envelope.Data.ApprovalID
}

func TestCrossTenantProviderCreateRequiresExactTargetAndApproval(t *testing.T) {
	env := setupTestEnv(t)
	env.svc.SetProviderURLPolicy(true)
	platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)

	body := map[string]any{"provider_name": "openai-api-compatible"}
	resp := env.doRequest(t, http.MethodPost, "/api/v1/model-providers", body, platformToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("platform create own resource: got %d body %s", resp.Code, resp.Body.String())
	}
	var created struct {
		Data struct {
			TenantID string `json:"tenant_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.TenantID != model.PlatformTenantID {
		t.Fatalf("platform current create used tenant %q", created.Data.TenantID)
	}

	resp = env.doRequest(t, http.MethodPost, "/api/v1/model-providers?scope=all", body, platformToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("platform create with scope=all: got %d body %s", resp.Code, resp.Body.String())
	}

	resp = env.doRequest(
		t, http.MethodPost,
		"/api/v1/model-providers?scope=specific&tenant_id="+id.New(), body, platformToken,
	)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("platform create with unknown target: got %d body %s", resp.Code, resp.Body.String())
	}

	resp = env.doRequest(
		t, http.MethodPost,
		"/api/v1/model-providers?scope=specific&tenant_id="+env.tenantID, body, platformToken,
	)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("cross-tenant create without policy must fail closed: got %d body %s", resp.Code, resp.Body.String())
	}

	env.createPolicy(t, tenantToken, model.ApprovalObjectModelProvider, model.ApprovalActionCreate)
	resp = env.doRequest(
		t, http.MethodPost,
		"/api/v1/model-providers?scope=specific&tenant_id="+env.tenantID, body, platformToken,
	)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("cross-tenant create should submit approval: got %d body %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "api_key") {
		t.Fatalf("approval response leaked credential field: %s", resp.Body.String())
	}
}

func TestCrossTenantProviderInstanceUpdateUsesTargetApproval(t *testing.T) {
	env := setupTestEnv(t)
	env.svc.SetProviderURLPolicy(true)
	platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
	providerID := id.New()
	instanceID := id.New()
	if err := env.svc.Store.CreateModelProvider(t.Context(), &model.ModelProvider{
		ID: providerID, TenantID: env.tenantID, ProviderType: "openai-api-compatible",
		Name: "target provider", Enabled: true, Status: model.ProviderStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.CreateModelProviderInstance(t.Context(), &model.ModelProviderInstance{
		ID: instanceID, TenantID: env.tenantID, ProviderID: providerID, InstanceName: "target",
		BaseURL: "https://before.example.com/v1", Status: model.ProviderStatusActive, ExtraJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	env.createPolicy(t, tenantToken, model.ApprovalObjectModelInstance, model.ApprovalActionUpdate)

	body := map[string]any{"base_url": "http://127.0.0.1:8080/v1", "api_key": "sk-target-secret"}
	resp := env.doRequest(
		t, http.MethodPut,
		"/api/v1/model-providers/"+providerID+"/instances/"+instanceID+"?scope=specific&tenant_id="+env.tenantID,
		body, platformToken,
	)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("cross-tenant instance update should submit approval: got %d body %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "sk-target-secret") {
		t.Fatalf("approval response leaked API key")
	}
	approval, err := env.svc.Store.GetApproval(t.Context(), env.tenantID, approvalIDFromResponse(t, resp.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if approval.TargetTenantID != env.tenantID || approval.TenantID != env.tenantID {
		t.Fatalf("approval target mismatch: tenant=%s target=%s", approval.TenantID, approval.TargetTenantID)
	}
	if strings.Contains(approval.PayloadJSON, "sk-target-secret") || strings.Contains(approval.PayloadJSON, `"api_key"`) {
		t.Fatalf("approval payload leaked API key: %s", approval.PayloadJSON)
	}
}
