package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UnauthorizedWritesDoNotAuditOrCreateApprovals(t *testing.T) {
	tests := []struct {
		name         string
		objectType   string
		policyAction string
		body         any
		params       gin.Params
		invoke       func(h *Handler, c *gin.Context)
	}{
		{
			name: "provider create", objectType: model.ApprovalObjectModelProvider, policyAction: model.ApprovalActionCreate,
			body:   map[string]any{"provider_name": "openai-api-compatible"},
			invoke: func(h *Handler, c *gin.Context) { h.CreateModelProvider(c) },
		},
		{
			name: "provider delete", objectType: model.ApprovalObjectModelProvider, policyAction: model.ApprovalActionDelete,
			params: gin.Params{{Key: "id", Value: "provider-1"}},
			invoke: func(h *Handler, c *gin.Context) { h.DeleteModelProvider(c) },
		},
		{
			name: "instance create", objectType: model.ApprovalObjectModelInstance, policyAction: model.ApprovalActionCreate,
			body:   map[string]any{"instance_name": "new-instance", "api_key": "sk-instance-secret"},
			params: gin.Params{{Key: "id", Value: "provider-1"}},
			invoke: func(h *Handler, c *gin.Context) { h.CreateProviderInstance(c) },
		},
		{
			name: "instance update", objectType: model.ApprovalObjectModelInstance, policyAction: model.ApprovalActionUpdate,
			body: map[string]any{"instance_name": "updated-instance", "base_url": "https://updated.example.com/v1"},
			params: gin.Params{
				{Key: "id", Value: "provider-1"}, {Key: "instanceId", Value: "instance-1"},
			},
			invoke: func(h *Handler, c *gin.Context) { h.UpdateProviderInstance(c) },
		},
		{
			name: "instance delete", objectType: model.ApprovalObjectModelInstance, policyAction: model.ApprovalActionDelete,
			body:   map[string]any{"instances": []string{"instance-1"}},
			params: gin.Params{{Key: "id", Value: "provider-1"}},
			invoke: func(h *Handler, c *gin.Context) { h.DeleteProviderInstances(c) },
		},
		{
			name: "provider verify", objectType: "", policyAction: "",
			body:   map[string]any{"base_url": "https://verify.example.com/v1", "api_key": "sk-verify-secret"},
			params: gin.Params{{Key: "id", Value: "provider-1"}},
			invoke: func(h *Handler, c *gin.Context) { h.VerifyProviderConnection(c) },
		},
		{
			name: "model add", objectType: model.ApprovalObjectModelModel, policyAction: model.ApprovalActionCreate,
			body: map[string]any{"model_name": "new-model", "model_type": []string{"chat"}},
			params: gin.Params{
				{Key: "id", Value: "provider-1"}, {Key: "instanceId", Value: "instance-1"},
			},
			invoke: func(h *Handler, c *gin.Context) { h.AddProviderModel(c) },
		},
		{
			name: "model update", objectType: model.ApprovalObjectModelModel, policyAction: model.ApprovalActionUpdate,
			body: map[string]any{"status": "disabled", "max_tokens": 4096},
			params: gin.Params{
				{Key: "id", Value: "provider-1"}, {Key: "instanceId", Value: "instance-1"},
				{Key: "modelId", Value: "model-1"},
			},
			invoke: func(h *Handler, c *gin.Context) { h.UpdateProviderModel(c) },
		},
		{
			name: "model delete", objectType: model.ApprovalObjectModelModel, policyAction: model.ApprovalActionDelete,
			body: map[string]any{"model_ids": []string{"model-1"}},
			params: gin.Params{
				{Key: "id", Value: "provider-1"}, {Key: "instanceId", Value: "instance-1"},
			},
			invoke: func(h *Handler, c *gin.Context) { h.DeleteProviderModels(c) },
		},
		{
			name: "model test", objectType: model.ApprovalObjectModelModel, policyAction: model.ApprovalActionTest,
			body: map[string]any{"message": "ping"},
			params: gin.Params{
				{Key: "id", Value: "provider-1"}, {Key: "instanceId", Value: "instance-1"},
				{Key: "modelId", Value: "model-1"},
			},
			invoke: func(h *Handler, c *gin.Context) { h.TestProviderModel(c) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			env.svc.SetProviderURLPolicy(true)
			unauthorized, err := env.svc.CreateUser(t.Context(), env.tenantID, env.adminID, service.CreateUserRequest{
				Username: "provider-operator-" + test.name, Password: "password123", Role: model.RoleOperator,
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.objectType != "" {
				env.createPolicy(t, env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin), test.objectType, test.policyAction)
			}

			c := modelProviderHandlerContext(t, test.body, test.params)
			c.Set(middleware.ContextUserID, unauthorized.ID)
			c.Set(middleware.ContextTenantID, env.tenantID)
			test.invoke(env.h, c)

			if c.Writer.Status() != http.StatusForbidden {
				t.Fatalf("unauthorized write: got %d", c.Writer.Status())
			}
			audits, _, err := env.svc.Store.ListAudits(t.Context(), env.tenantID, 1, 100, repository.AuditFilter{
				Action: "model-provider.governance.write",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(audits) != 0 {
				t.Fatalf("unauthorized write created %d governance audits", len(audits))
			}
			_, approvals, err := env.svc.Store.ListApprovals(t.Context(), env.tenantID, repository.ApprovalFilter{}, repository.ApprovalListQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if approvals != 0 {
				t.Fatalf("unauthorized write created %d approvals", approvals)
			}
		})
	}
}

func modelProviderHandlerContext(t *testing.T, body any, params gin.Params) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Writer = &providerCaptureWriter{ResponseWriter: context.Writer}
	var request []byte
	if body != nil {
		request, _ = json.Marshal(body)
	}
	context.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(request))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = params
	return context
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_GovernanceReadScopeDoesNotLeakForeignProviderData(t *testing.T) {
	env := setupTestEnv(t)
	env.svc.SetProviderURLPolicy(true)
	providerID, _, _ := seedModelProviderFixture(t, env)
	foreignTenant, err := env.svc.CreateTenant(t.Context(), "foreign-provider-tenant")
	if err != nil {
		t.Fatal(err)
	}
	foreignProvider, foreignInstance, foreignModel := id.New(), id.New(), id.New()
	if err := env.svc.Store.CreateModelProvider(t.Context(), &model.ModelProvider{
		ID: foreignProvider, TenantID: foreignTenant.ID, ProviderType: "openai-api-compatible",
		Name: "foreign provider", Enabled: true, Status: model.ProviderStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.CreateModelProviderInstance(t.Context(), &model.ModelProviderInstance{
		ID: foreignInstance, TenantID: foreignTenant.ID, ProviderID: foreignProvider,
		InstanceName: "foreign instance", BaseURL: "https://foreign.example.com/v1",
		Status: model.ProviderStatusActive, ExtraJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.Store.CreateModelProviderModel(t.Context(), &model.ModelProviderModel{
		ID: foreignModel, TenantID: foreignTenant.ID, ProviderID: foreignProvider,
		InstanceID: foreignInstance, ModelName: "foreign-model", ModelType: 1,
		Status: model.ProviderStatusActive, ExtraJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	target := "?scope=specific&tenant_id=" + env.tenantID
	c := modelProviderHandlerContext(t, nil, nil)
	c.Set(middleware.ContextUserID, env.platformID)
	c.Set(middleware.ContextTenantID, model.PlatformTenantID)
	c.Request.URL.RawQuery = strings.TrimPrefix(target, "?")
	env.h.ListModelProviders(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("scoped provider list: got %d", c.Writer.Status())
	}
	var providers struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(c.Writer.(*providerCaptureWriter).body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers.Data) != 1 || providers.Data[0]["id"] != providerID {
		t.Fatalf("scoped provider list leaked foreign data: %#v", providers.Data)
	}

	c = modelProviderHandlerContext(t, nil, gin.Params{{Key: "id", Value: foreignProvider}})
	c.Set(middleware.ContextUserID, env.platformID)
	c.Set(middleware.ContextTenantID, model.PlatformTenantID)
	c.Request.URL.RawQuery = strings.TrimPrefix(target, "?")
	env.h.ListProviderInstances(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("foreign provider instance list: got %d", c.Writer.Status())
	}
	if !strings.Contains(c.Writer.(*providerCaptureWriter).body.String(), `"data":[]`) {
		t.Fatalf("foreign provider instances leaked: %s", c.Writer.(*providerCaptureWriter).body.String())
	}

	c = modelProviderHandlerContext(t, nil, gin.Params{{Key: "instanceId", Value: foreignInstance}})
	c.Set(middleware.ContextUserID, env.platformID)
	c.Set(middleware.ContextTenantID, model.PlatformTenantID)
	c.Request.URL.RawQuery = strings.TrimPrefix(target, "?")
	env.h.ListProviderInstanceModels(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("foreign instance model list: got %d", c.Writer.Status())
	}
	if !strings.Contains(c.Writer.(*providerCaptureWriter).body.String(), `"data":[]`) {
		t.Fatalf("foreign instance models leaked: %s", c.Writer.(*providerCaptureWriter).body.String())
	}
	if strings.Contains(c.Writer.(*providerCaptureWriter).body.String(), "foreign-model") {
		t.Fatalf("foreign model name leaked")
	}

	audits, _, err := env.svc.Store.ListAudits(t.Context(), model.PlatformTenantID, 1, 100, repository.AuditFilter{
		Action: "model-provider.governance.read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 3 {
		t.Fatalf("expected one audit per governed read, got %d", len(audits))
	}
	for _, audit := range audits {
		if audit.Scope != string(service.TenantScopeSpecific) || audit.TargetTenantID != env.tenantID ||
			audit.AuthorizationPermission != "governance.read:tenant" {
			t.Fatalf("governance read audit missing scope metadata: %+v", audit)
		}
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_MiddlewareGovernanceScopeIsReusedWithoutDuplicateAudit(t *testing.T) {
	env := setupTestEnv(t)
	providerID, _, _ := seedModelProviderFixture(t, env)
	scope := service.TenantScope{
		Kind: service.TenantScopeSpecific, ActorTenantID: model.PlatformTenantID,
		TargetTenantID: env.tenantID, AllowedTenantIDs: []string{env.tenantID},
	}
	if err := env.svc.RecordAudit(t.Context(), &model.AuditLog{
		TenantID: model.PlatformTenantID, UserID: env.platformID, Action: "model-provider.governance.read",
		Resource: "model-provider", ResourceID: env.tenantID, Scope: string(scope.Kind),
		TargetTenantID: env.tenantID, AuthorizationPermission: "governance.read:tenant",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}); err != nil {
		t.Fatal(err)
	}

	c := modelProviderHandlerContext(t, nil, nil)
	c.Set(middleware.ContextUserID, env.platformID)
	c.Set(middleware.ContextTenantID, model.PlatformTenantID)
	c.Set(middleware.ContextTenantScope, scope)
	c.Request.URL.RawQuery = "scope=specific&tenant_id=" + env.tenantID
	env.h.ListModelProviders(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("cached governance read: got %d", c.Writer.Status())
	}
	var providers struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(c.Writer.(*providerCaptureWriter).body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers.Data) != 1 || providers.Data[0]["id"] != providerID {
		t.Fatalf("cached scope read returned wrong data: %#v", providers.Data)
	}
	audits, _, err := env.svc.Store.ListAudits(t.Context(), model.PlatformTenantID, 1, 100, repository.AuditFilter{
		Action: "model-provider.governance.read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("cached governance scope created %d duplicate audits", len(audits)-1)
	}
}

type providerCaptureWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (writer *providerCaptureWriter) Write(payload []byte) (int, error) {
	writer.body.Write(payload)
	return writer.ResponseWriter.Write(payload)
}
