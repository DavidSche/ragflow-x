package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func seedModelProviderFixture(t *testing.T, env *testEnv) (providerID, instanceID, modelID string) {
	t.Helper()
	providerID = id.New()
	instanceID = id.New()
	modelID = id.New()
	if err := env.svc.Store.CreateModelProvider(t.Context(), &model.ModelProvider{
		ID: providerID, TenantID: env.tenantID, ProviderType: "openai-api-compatible",
		Name: "target provider", Enabled: true, Status: model.ProviderStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.CreateModelProviderInstance(t.Context(), &model.ModelProviderInstance{
		ID: instanceID, TenantID: env.tenantID, ProviderID: providerID, InstanceName: "target instance",
		BaseURL: "https://target.example.com/v1", Region: "default",
		Status: model.ProviderStatusActive, ExtraJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.CreateModelProviderModel(t.Context(), &model.ModelProviderModel{
		ID: modelID, TenantID: env.tenantID, ProviderID: providerID, InstanceID: instanceID,
		ModelName: "target-model", ModelType: 1, Status: model.ProviderStatusActive,
		MaxTokens: 8192, ExtraJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	return providerID, instanceID, modelID
}

func normalizeModelTestValue(value any, instanceID, modelID string) any {
	switch typed := value.(type) {
	case string:
		typed = strings.ReplaceAll(typed, "@@instance@@", instanceID)
		return strings.ReplaceAll(typed, "@@model@@", modelID)
	case []string:
		normalized := make([]string, len(typed))
		for index, item := range typed {
			normalized[index] = strings.ReplaceAll(item, "@@instance@@", instanceID)
			normalized[index] = strings.ReplaceAll(normalized[index], "@@model@@", modelID)
		}
		return normalized
	default:
		return value
	}
}

func TestCrossTenantModelProviderManagementSubmitsExactTargetApprovals(t *testing.T) {
	providerObject := model.ApprovalObjectModelProvider
	instanceObject := model.ApprovalObjectModelInstance
	modelObject := model.ApprovalObjectModelModel
	tests := []struct {
		name         string
		objectType   string
		policyAction string
		action       string
		method       string
		path         func(providerID, instanceID, modelID string) string
		body         map[string]any
		snapshotKeys []string
	}{
		{
			name: "provider delete", objectType: providerObject, policyAction: model.ApprovalActionDelete,
			action: model.ApprovalActionDelete, method: http.MethodDelete,
			path:         func(providerID, _, _ string) string { return "/api/v1/model-providers/" + providerID },
			snapshotKeys: []string{"name", "provider_type", "status"},
		},
		{
			name: "instance create", objectType: instanceObject, policyAction: model.ApprovalActionCreate,
			action: model.ApprovalActionCreate, method: http.MethodPost,
			path: func(providerID, _, _ string) string { return "/api/v1/model-providers/" + providerID + "/instances" },
			body: map[string]any{"instance_name": "new instance", "api_key": "sk-instance-secret", "base_url": "https://new.example.com/v1"},
		},
		{
			name: "instance delete", objectType: instanceObject, policyAction: model.ApprovalActionDelete,
			action: model.ApprovalActionDelete, method: http.MethodDelete,
			path: func(providerID, instanceID, _ string) string {
				return "/api/v1/model-providers/" + providerID + "/instances"
			},
			body:         map[string]any{"instances": []string{"@@instance@@"}},
			snapshotKeys: []string{"instance_name", "base_url", "status"},
		},
		{
			name: "model create", objectType: modelObject, policyAction: model.ApprovalActionCreate,
			action: model.ApprovalActionCreate, method: http.MethodPost,
			path: func(providerID, instanceID, _ string) string {
				return "/api/v1/model-providers/" + providerID + "/instances/" + instanceID + "/models"
			},
			body: map[string]any{"model_name": "new-model", "model_type": []string{"chat"}},
		},
		{
			name: "model update", objectType: modelObject, policyAction: model.ApprovalActionUpdate,
			action: model.ApprovalActionUpdate, method: http.MethodPatch,
			path: func(providerID, instanceID, modelID string) string {
				return "/api/v1/model-providers/" + providerID + "/instances/" + instanceID + "/models/" + modelID
			},
			body:         map[string]any{"status": "disabled", "max_tokens": 4096},
			snapshotKeys: []string{"model_name", "model_status", "model_max_tokens"},
		},
		{
			name: "model delete", objectType: modelObject, policyAction: model.ApprovalActionDelete,
			action: model.ApprovalActionDelete, method: http.MethodDelete,
			path: func(providerID, instanceID, modelID string) string {
				return "/api/v1/model-providers/" + providerID + "/instances/" + instanceID + "/models"
			},
			body:         map[string]any{"model_ids": []string{"@@model@@"}},
			snapshotKeys: []string{"model_name", "model_status", "model_max_tokens"},
		},
		{
			name: "model test", objectType: modelObject, policyAction: model.ApprovalActionTest,
			action: model.ApprovalActionTest, method: http.MethodPost,
			path: func(providerID, instanceID, modelID string) string {
				return "/api/v1/model-providers/" + providerID + "/instances/" + instanceID + "/models/" + modelID + "/test"
			},
			body:         map[string]any{"message": "ping"},
			snapshotKeys: []string{"model_name", "model_status", "model_max_tokens"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			env.svc.SetProviderURLPolicy(true)
			platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
			tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
			providerID, instanceID, modelID := seedModelProviderFixture(t, env)
			target := env.tenantID
			scope := "?scope=specific&tenant_id=" + target
			requestBody := map[string]any{}
			for key, value := range test.body {
				requestBody[key] = normalizeModelTestValue(value, instanceID, modelID)
			}

			for _, scopeQuery := range []string{"?scope=all", "?scope=specific&tenant_id=" + id.New()} {
				resp := env.doRequest(t, test.method, test.path(providerID, instanceID, modelID)+scopeQuery, requestBody, platformToken)
				if resp.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid scope should fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
				}
			}

			resp := env.doRequest(t, test.method, test.path(providerID, instanceID, modelID)+scope, requestBody, platformToken)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s without approval policy must fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
			}

			env.createPolicy(t, tenantToken, test.objectType, test.policyAction)
			resp = env.doRequest(t, test.method, test.path(providerID, instanceID, modelID)+scope, requestBody, platformToken)
			if resp.Code != http.StatusAccepted {
				t.Fatalf("%s should submit approval: got %d body %s", test.name, resp.Code, resp.Body.String())
			}
			if strings.Contains(resp.Body.String(), "sk-instance-secret") {
				t.Fatalf("%s approval response leaked API key", test.name)
			}

			approval, err := env.svc.Store.GetApproval(t.Context(), target, approvalIDFromResponse(t, resp.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if approval.TenantID != target || approval.TargetTenantID != target {
				t.Fatalf("%s approval target mismatch: tenant=%s target=%s", test.name, approval.TenantID, approval.TargetTenantID)
			}
			if approval.ResourceVersion == "" || approval.ApprovalActionHash == "" {
				t.Fatalf("%s approval missing fingerprint/version: %+v", test.name, approval)
			}
			if strings.Contains(approval.PayloadJSON, "sk-instance-secret") || strings.Contains(approval.PayloadJSON, `"api_key"`) {
				t.Fatalf("%s approval payload leaked API key: %s", test.name, approval.PayloadJSON)
			}
			snapshot := map[string]any{}
			if err := json.Unmarshal([]byte(approval.SnapshotJSON), &snapshot); err != nil {
				t.Fatal(err)
			}
			for _, key := range test.snapshotKeys {
				if _, ok := snapshot[key]; !ok {
					t.Fatalf("%s snapshot missing %s: %s", test.name, key, approval.SnapshotJSON)
				}
			}
			audits, _, err := env.svc.Store.ListAudits(t.Context(), target, 1, 100, repository.AuditFilter{Action: "approval.submitted"})
			if err != nil {
				t.Fatal(err)
			}
			if len(audits) == 0 || audits[0].ResourceID != approval.ID {
				t.Fatalf("%s approval submission audit missing: %d", test.name, len(audits))
			}
		})
	}
}
