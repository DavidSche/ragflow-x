package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type failingModelCreateStore struct {
	repository.Store
}

func (s *failingModelCreateStore) CreateModelProviderInstanceWithModels(ctx context.Context, _ *model.ModelProviderInstance, _ []*model.ModelProviderModel) error {
	return context.DeadlineExceeded
}

type failingProviderCreateStore struct {
	repository.Store
	audits  []model.AuditLog
	created *model.ModelProvider
}

func (s *failingProviderCreateStore) CreateModelProvider(_ context.Context, provider *model.ModelProvider) error {
	s.created = provider
	return context.DeadlineExceeded
}

func (s *failingProviderCreateStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	s.audits = append(s.audits, *entry)
	return s.Store.CreateAudit(ctx, entry)
}

type failingProviderDeleteClient struct {
	ragflow.Client
}

func (c *failingProviderDeleteClient) DeleteProvider(context.Context, string) error {
	return context.DeadlineExceeded
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_AddModelProviderAuditsFailedRemoteRollback(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	store := &failingProviderCreateStore{Store: svc.Store}
	svc.Store = store
	svc.RAGFlow = &failingProviderDeleteClient{Client: svc.RAGFlow}

	_, err := svc.AddModelProvider(ctx, "tenant-1", "OpenAI-API-Compatible")
	if err == nil {
		t.Fatal("expected local provider create failure")
	}
	if len(store.audits) != 1 {
		t.Fatalf("compensation audit count = %d, want 1", len(store.audits))
	}
	audit := store.audits[0]
	if audit.TenantID != "tenant-1" || audit.Action != "provider.compensation.failed" || audit.Resource != "model-provider" || audit.ResourceID != store.created.ID || audit.Result != "FAILURE" {
		t.Fatalf("unexpected compensation audit: %+v", audit)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
		t.Fatalf("decode compensation audit detail: %v", err)
	}
	if detail["operation"] != "add-model-provider" || detail["stage"] != "delete-upstream-provider" {
		t.Fatalf("unexpected compensation detail: %+v", detail)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_CreateProviderInstanceRollsBackRemoteWhenLocalCreateFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	svc.Store = &failingModelCreateStore{Store: svc.Store}

	_, err = svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "api-key",
		BaseURL:      "https://api.example.com/v1",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err == nil {
		t.Fatal("expected local create failure")
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 0 {
		t.Fatalf("remote rollback left %+v", remoteInstances)
	}
	localInstances, err := svc.Store.ListModelProviderInstances(ctx, "tenant-1", provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(localInstances) != 0 {
		t.Fatalf("local rollback left %+v", localInstances)
	}
}

type failingModelUpdateStore struct {
	repository.Store
}

func (s *failingModelUpdateStore) UpdateModelProviderInstanceWithModels(context.Context, *model.ModelProvider, *model.ModelProviderInstance, []*model.ModelProviderModel, []string) error {
	return context.DeadlineExceeded
}

type failingProviderInstanceListStore struct {
	repository.Store
	audits []model.AuditLog
}

func (s *failingProviderInstanceListStore) ListModelProviderInstances(context.Context, string, string) ([]model.ModelProviderInstance, error) {
	return nil, context.DeadlineExceeded
}

func (s *failingProviderInstanceListStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	s.audits = append(s.audits, *entry)
	return s.Store.CreateAudit(ctx, entry)
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UpdateProviderInstanceRestoresRemoteWhenLocalUpdateFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "old-key",
		BaseURL:      "https://old.example.com/v1",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	svc.Store = &failingModelUpdateStore{Store: svc.Store}

	_, err = svc.UpdateProviderInstance(ctx, "tenant-1", provider.ID, instance.ID, CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "new-key",
		BaseURL:      "https://new.example.com/v1",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 64000}},
	})
	if err == nil {
		t.Fatal("expected local update failure")
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 || remoteInstances[0].BaseURL != "https://old.example.com/v1" || remoteInstances[0].APIKey != "old-key" {
		t.Fatalf("remote instance was not restored: %+v", remoteInstances)
	}
	remoteModels, err := svc.RAGFlow.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteModels) != 1 || remoteModels[0].MaxTokens != 8192 {
		t.Fatalf("remote model was not restored: %+v", remoteModels)
	}
	localInstance, err := svc.Store.GetModelProviderInstance(ctx, "tenant-1", provider.ID, instance.ID)
	if err != nil || localInstance == nil || localInstance.BaseURL != "https://old.example.com/v1" {
		t.Fatalf("local instance changed: %+v err=%v", localInstance, err)
	}
	localModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil || len(localModels) != 1 || localModels[0].MaxTokens != 8192 {
		t.Fatalf("local models changed: %+v err=%v", localModels, err)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UpdateProviderInstanceAuditsFailedRollbackWhenListFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "old-key", BaseURL: "https://old.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	store := &failingProviderInstanceListStore{Store: svc.Store}
	svc.Store = store
	svc.RAGFlow = &failingRemoteUpdateClient{Client: svc.RAGFlow}

	if _, err := svc.UpdateProviderInstance(ctx, "tenant-1", provider.ID, instance.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "new-key", BaseURL: "https://new.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 64000}},
	}); err == nil {
		t.Fatal("expected instance list failure")
	}
	if len(store.audits) != 1 || store.audits[0].Action != AuditActionProviderCompensationFailed {
		t.Fatalf("compensation audit = %+v", store.audits)
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 {
		t.Fatalf("unexpected remote instances: %+v", remoteInstances)
	}
	if remoteInstances[0].BaseURL != "https://new.example.com/v1" || remoteInstances[0].APIKey != "new-key" {
		t.Fatalf("failed rollback unexpectedly restored remote: %+v", remoteInstances[0])
	}
}

type failingModelPersistStore struct {
	repository.Store
}

func (s *failingModelPersistStore) CreateModelProviderModel(context.Context, *model.ModelProviderModel) error {
	return context.DeadlineExceeded
}

func (s *failingModelPersistStore) UpdateModelProviderModel(context.Context, *model.ModelProviderModel) error {
	return context.DeadlineExceeded
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_AddProviderModelRestoresRemoteWhenLocalCreateFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "api-key",
		BaseURL:      "https://api.example.com/v1",
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	svc.Store = &failingModelPersistStore{Store: svc.Store}

	_, err = svc.AddProviderModel(ctx, "tenant-1", provider.ID, instance.ID, ModelInfoInput{
		ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192,
	})
	if err == nil {
		t.Fatal("expected local model create failure")
	}
	remoteModels, err := svc.RAGFlow.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteModels) != 0 {
		t.Fatalf("remote model rollback left %+v", remoteModels)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UpdateProviderModelRestoresRemoteWhenLocalUpdateFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "api-key",
		BaseURL:      "https://api.example.com/v1",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	svc.Store = &failingModelPersistStore{Store: svc.Store}
	localModel, err := svc.Store.GetModelProviderModelByName(ctx, "tenant-1", provider.ID, instance.ID, "gpt-test")
	if err != nil || localModel == nil {
		t.Fatalf("load local model: %+v err=%v", localModel, err)
	}

	_, err = svc.UpdateProviderModel(ctx, "tenant-1", provider.ID, instance.ID, localModel.ID, ModelUpdateDTO{
		Status: "disabled", MaxTokens: 64000,
	})
	if err == nil {
		t.Fatal("expected local model update failure")
	}
	remoteModels, err := svc.RAGFlow.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteModels) != 1 || remoteModels[0].Status != "active" || remoteModels[0].MaxTokens != 8192 {
		t.Fatalf("remote model was not restored: %+v", remoteModels)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteProviderInstancesUsesUpstreamIDsAndAtomicallyDeletesShadow(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	first, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "primary", APIKey: "first-key", BaseURL: "https://first.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-first", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "secondary", APIKey: "second-key", BaseURL: "https://second.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-second", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{first.ID}); err != nil {
		t.Fatalf("delete first instance: %v", err)
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 || remoteInstances[0].InstanceName != "secondary" {
		t.Fatalf("unexpected remaining remote instances: %+v", remoteInstances)
	}
	remoteModels, err := svc.RAGFlow.ListInstanceModels(ctx, "OpenAI-API-Compatible", "secondary", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteModels) != 1 || remoteModels[0].Name != "gpt-second" {
		t.Fatalf("unexpected remaining remote models: %+v", remoteModels)
	}
	if got, _ := svc.Store.GetModelProviderInstance(ctx, "tenant-1", provider.ID, first.ID); got != nil {
		t.Fatalf("deleted local instance remains: %+v", got)
	}
	localModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", first.ID)
	if err != nil || len(localModels) != 0 {
		t.Fatalf("deleted local models remain: %+v err=%v", localModels, err)
	}
	updatedProvider, err := svc.Store.GetModelProvider(ctx, "tenant-1", provider.ID)
	if err != nil || updatedProvider == nil || updatedProvider.BaseURL != "https://second.example.com/v1" || updatedProvider.APIKeyEnc == "" {
		t.Fatalf("provider default not reconciled: %+v err=%v", updatedProvider, err)
	}

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{second.ID}); err != nil {
		t.Fatalf("delete second instance: %v", err)
	}
	remoteInstances, err = svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 0 {
		t.Fatalf("remote instances were not deleted: %+v", remoteInstances)
	}
	updatedProvider, err = svc.Store.GetModelProvider(ctx, "tenant-1", provider.ID)
	if err != nil || updatedProvider == nil || updatedProvider.BaseURL != "" || updatedProvider.APIKeyEnc != "" {
		t.Fatalf("provider default not cleared: %+v err=%v", updatedProvider, err)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteProviderInstancesRestoresShadowWhenRemoteDeleteFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "api-key", BaseURL: "https://api.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	svc.RAGFlow = &failingRemoteDeleteClient{Client: svc.RAGFlow}

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{instance.ID}); err == nil {
		t.Fatal("expected remote delete failure")
	}
	localInstance, err := svc.Store.GetModelProviderInstance(ctx, "tenant-1", provider.ID, instance.ID)
	if err != nil || localInstance == nil || localInstance.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("local instance not restored: %+v err=%v", localInstance, err)
	}
	localModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil || len(localModels) != 1 || localModels[0].ModelName != "gpt-test" {
		t.Fatalf("local models not restored: %+v err=%v", localModels, err)
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 || remoteInstances[0].InstanceName != "production" {
		t.Fatalf("remote instance changed: %+v", remoteInstances)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteProviderInstancesCompensatesOnlyFailedBatchItems(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	first, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "first-key", BaseURL: "https://first.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-first", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create first instance: %v", err)
	}
	second, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "secondary", APIKey: "second-key", BaseURL: "https://second.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-second", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create second instance: %v", err)
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	remoteProductionID := ""
	for _, remote := range remoteInstances {
		if remote.InstanceName == "production" {
			remoteProductionID = remote.ID
		}
	}
	if remoteProductionID == "" {
		t.Fatalf("production remote instance not found: %+v", remoteInstances)
	}
	client := &partialRemoteDeleteClient{Client: svc.RAGFlow, failInstanceID: remoteProductionID}
	svc.RAGFlow = client

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{first.ID, second.ID}); err == nil {
		t.Fatal("expected one batch item to fail")
	}
	if len(client.calls) != 2 {
		t.Fatalf("remote delete calls = %+v, want one call per upstream instance", client.calls)
	}
	remoteInstances, err = svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 || remoteInstances[0].InstanceName != "production" {
		t.Fatalf("successful item was incorrectly restored: %+v", remoteInstances)
	}
	localInstance, err := svc.Store.GetModelProviderInstance(ctx, "tenant-1", provider.ID, second.ID)
	if err != nil || localInstance != nil {
		t.Fatalf("successful item local state = %+v err=%v, want deleted", localInstance, err)
	}
	restored, err := svc.Store.GetModelProviderInstance(ctx, "tenant-1", provider.ID, first.ID)
	if err != nil || restored == nil || restored.BaseURL != "https://first.example.com/v1" {
		t.Fatalf("failed item was not restored: %+v err=%v", restored, err)
	}
	restoredModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", first.ID)
	if err != nil || len(restoredModels) != 1 || restoredModels[0].ModelName != "gpt-first" {
		t.Fatalf("failed item models were not restored: %+v err=%v", restoredModels, err)
	}
	updatedProvider, err := svc.Store.GetModelProvider(ctx, "tenant-1", provider.ID)
	if err != nil || updatedProvider == nil || updatedProvider.BaseURL != "https://first.example.com/v1" {
		t.Fatalf("provider default was not reconciled: %+v err=%v", updatedProvider, err)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteProviderInstancesDoesNotCallRemoteWhenLocalDeleteFails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "api-key", BaseURL: "https://api.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	svc.Store = &failingProviderDeleteStore{Store: svc.Store}

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{instance.ID}); err == nil {
		t.Fatal("expected local delete failure")
	}
	remoteInstances, err := svc.RAGFlow.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil {
		t.Fatal(err)
	}
	if len(remoteInstances) != 1 {
		t.Fatalf("remote deletion called before local commit: %+v", remoteInstances)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteCompensationFailureIsAudited(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "api-key", BaseURL: "https://api.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	store := &failingProviderRestoreStore{Store: svc.Store}
	svc.Store = store
	svc.RAGFlow = &failingRemoteDeleteClient{Client: svc.RAGFlow}

	if err := svc.DeleteProviderInstances(ctx, "tenant-1", provider.ID, []string{instance.ID}); err == nil {
		t.Fatal("expected delete failure")
	}
	if len(store.audits) != 1 {
		t.Fatalf("compensation audit count = %d, want 1", len(store.audits))
	}
	audit := store.audits[0]
	if audit.TenantID != "tenant-1" || audit.Action != "provider.compensation.failed" || audit.Resource != "model-provider" || audit.ResourceID != provider.ID || audit.Result != "FAILURE" {
		t.Fatalf("unexpected audit: %+v", audit)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	if detail["operation"] != "delete-provider-instance" || detail["stage"] != "restore-local-shadow" {
		t.Fatalf("unexpected audit detail: %+v", detail)
	}
}

type failingRemoteModelDeleteClient struct {
	ragflow.Client
	calls [][]string
}

func (c *failingRemoteModelDeleteClient) DeleteModelsFromInstance(_ context.Context, _, _ string, modelNames []string) error {
	c.calls = append(c.calls, append([]string(nil), modelNames...))
	return context.DeadlineExceeded
}

type failingModelShadowDeleteStore struct {
	repository.Store
	audits []model.AuditLog
}

func (s *failingModelShadowDeleteStore) DeleteModelProviderModels(context.Context, string, string, []string) error {
	return context.DeadlineExceeded
}

func (s *failingModelShadowDeleteStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	s.audits = append(s.audits, *entry)
	return s.Store.CreateAudit(ctx, entry)
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_RemoteModelDeleteFailurePreservesShadowModels(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "api-key", BaseURL: "https://api.example.com/v1",
		Models: []ModelInfoInput{
			{ModelName: "gpt-first", ModelTypes: []string{"chat"}, MaxTokens: 8192},
			{ModelName: "gpt-second", ModelTypes: []string{"chat"}, MaxTokens: 8192},
		},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	localModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	modelIDs := []string{localModels[0].ID, localModels[1].ID}
	remote := &failingRemoteModelDeleteClient{Client: svc.RAGFlow}
	svc.RAGFlow = remote

	if err := svc.DeleteProviderModels(ctx, "tenant-1", provider.ID, instance.ID, modelIDs); err == nil {
		t.Fatal("expected remote model delete failure")
	}
	remaining, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("shadow model count = %d, want 2", len(remaining))
	}
	if len(remote.calls) != 1 || len(remote.calls[0]) != 2 {
		t.Fatalf("unexpected remote delete calls: %+v", remote.calls)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_ModelShadowDeleteFailureIsAudited(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "api-key", BaseURL: "https://api.example.com/v1",
		Models: []ModelInfoInput{
			{ModelName: "gpt-first", ModelTypes: []string{"chat"}, MaxTokens: 8192},
			{ModelName: "gpt-second", ModelTypes: []string{"chat"}, MaxTokens: 8192},
		},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	localModels, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	modelIDs := []string{localModels[0].ID, localModels[1].ID}
	store := &failingModelShadowDeleteStore{Store: svc.Store}
	svc.Store = store

	if err := svc.DeleteProviderModels(ctx, "tenant-1", provider.ID, instance.ID, modelIDs); err == nil {
		t.Fatal("expected local model delete failure")
	}
	if len(store.audits) != 1 {
		t.Fatalf("compensation audit count = %d, want 1", len(store.audits))
	}
	audit := store.audits[0]
	if audit.TenantID != "tenant-1" || audit.Action != "provider.compensation.failed" || audit.Resource != "model-provider" || audit.ResourceID != provider.ID || audit.Result != "FAILURE" {
		t.Fatalf("unexpected compensation audit: %+v", audit)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
		t.Fatalf("decode compensation audit detail: %v", err)
	}
	if detail["operation"] != "delete-provider-model" || detail["stage"] != "delete-local-shadow" {
		t.Fatalf("unexpected compensation detail: %+v", detail)
	}
	remaining, err := store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("shadow model count = %d, want 2", len(remaining))
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_DeleteModelProviderAuditsFailedRemoteCleanup(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	store := &failingProviderCreateStore{Store: svc.Store}
	svc.Store = store
	svc.RAGFlow = &failingProviderDeleteClient{Client: svc.RAGFlow}

	if err := svc.DeleteModelProvider(ctx, "tenant-1", provider.ID); err != nil {
		t.Fatalf("local deletion should remain best-effort after remote cleanup failure: %v", err)
	}
	if len(store.audits) != 1 {
		t.Fatalf("compensation audit count = %d, want 1", len(store.audits))
	}
	audit := store.audits[0]
	if audit.TenantID != "tenant-1" || audit.Action != "provider.compensation.failed" || audit.Resource != "model-provider" || audit.ResourceID != provider.ID || audit.Result != "FAILURE" {
		t.Fatalf("unexpected compensation audit: %+v", audit)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit.DetailJSON), &detail); err != nil {
		t.Fatalf("decode compensation audit detail: %v", err)
	}
	if detail["operation"] != "delete-model-provider" || detail["stage"] != "delete-upstream-provider" {
		t.Fatalf("unexpected compensation detail: %+v", detail)
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_UpdateCompensationFailureIsAudited(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)
	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "old-key", BaseURL: "https://old.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	store := &failingProviderRestoreStore{Store: svc.Store}
	svc.Store = store
	svc.RAGFlow = &failingRemoteUpdateClient{Client: svc.RAGFlow}

	if _, err := svc.UpdateProviderInstance(ctx, "tenant-1", provider.ID, instance.ID, CreateProviderInstanceRequest{
		InstanceName: "production", APIKey: "new-key", BaseURL: "https://new.example.com/v1",
		Models: []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 64000}},
	}); err == nil {
		t.Fatal("expected local update failure")
	}
	if len(store.audits) != 1 {
		t.Fatalf("compensation audit count = %d, want 1", len(store.audits))
	}
	if store.audits[0].Action != "provider.compensation.failed" || store.audits[0].ResourceID != provider.ID {
		t.Fatalf("unexpected audit: %+v", store.audits[0])
	}
}

type failingRemoteDeleteClient struct {
	ragflow.Client
}

type partialRemoteDeleteClient struct {
	ragflow.Client
	calls          [][]string
	failInstanceID string
}

func (c *partialRemoteDeleteClient) DeleteProviderInstances(_ context.Context, _ string, instanceIDs []string) error {
	ids := append([]string(nil), instanceIDs...)
	c.calls = append(c.calls, ids)
	for _, instanceID := range ids {
		if instanceID == c.failInstanceID {
			return context.DeadlineExceeded
		}
	}
	return c.Client.DeleteProviderInstances(context.Background(), "OpenAI-API-Compatible", ids)
}

func (c *failingRemoteDeleteClient) DeleteProviderInstances(context.Context, string, []string) error {
	return context.DeadlineExceeded
}

type failingRemoteUpdateClient struct {
	ragflow.Client
	updateCalls int
}

func (c *failingRemoteUpdateClient) UpdateProviderInstance(ctx context.Context, providerName, instanceName string, req ragflow.RegisterModelProviderRequest) error {
	c.updateCalls++
	if c.updateCalls > 1 {
		return context.DeadlineExceeded
	}
	return c.Client.UpdateProviderInstance(ctx, providerName, instanceName, req)
}

type failingProviderDeleteStore struct {
	repository.Store
}

func (s *failingProviderDeleteStore) DeleteModelProviderInstancesWithModels(context.Context, *model.ModelProvider, []string) error {
	return context.DeadlineExceeded
}

type failingProviderRestoreStore struct {
	repository.Store
	audits []model.AuditLog
}

func (s *failingProviderRestoreStore) DeleteModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, ids []string) error {
	return s.Store.DeleteModelProviderInstancesWithModels(ctx, provider, ids)
}

func (s *failingProviderRestoreStore) RestoreModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, instances []*model.ModelProviderInstance, models []*model.ModelProviderModel) error {
	return context.DeadlineExceeded
}

func (s *failingProviderRestoreStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	s.audits = append(s.audits, *entry)
	return s.Store.CreateAudit(ctx, entry)
}

func (s *failingProviderRestoreStore) UpdateModelProviderInstanceWithModels(context.Context, *model.ModelProvider, *model.ModelProviderInstance, []*model.ModelProviderModel, []string) error {
	return context.DeadlineExceeded
}
