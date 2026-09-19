package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_CreateProviderInstanceRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	request := CreateProviderInstanceRequest{
		InstanceName: "production",
		APIKey:       "api-key",
		BaseURL:      "https://api.example.com/v1",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	}
	if _, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, request); err != nil {
		t.Fatalf("create first instance: %v", err)
	}
	request.InstanceName = strings.ToUpper(request.InstanceName)
	_, err = svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, request)
	if he, ok := err.(*httperr.Error); !ok || he.Status != 409 {
		t.Fatalf("expected duplicate instance conflict, got %v", err)
	}
	instances, err := svc.ListProviderInstances(ctx, "tenant-1", provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instance count = %d, want 1", len(instances))
	}
}

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_AddProviderModelRejectsDuplicateName(t *testing.T) {
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
	model := ModelInfoInput{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}
	if _, err := svc.AddProviderModel(ctx, "tenant-1", provider.ID, instance.ID, model); err != nil {
		t.Fatalf("add first model: %v", err)
	}
	model.ModelName = strings.ToUpper(model.ModelName)
	_, err = svc.AddProviderModel(ctx, "tenant-1", provider.ID, instance.ID, model)
	if he, ok := err.(*httperr.Error); !ok || he.Status != 409 {
		t.Fatalf("expected duplicate model conflict, got %v", err)
	}
	models, err := svc.Store.ListModelProviderModels(ctx, "tenant-1", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("model count = %d, want 1", len(models))
	}
}

func TestUpdateProviderInstanceKeepsStoredCredentialWhenAPIKeyOmitted(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance, err := svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "primary",
		APIKey:       "existing-key",
		BaseURL:      "https://api.example.com/v1",
		Region:       "default",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	updated, err := svc.UpdateProviderInstance(ctx, "tenant-1", provider.ID, instance.ID, CreateProviderInstanceRequest{
		InstanceName: "renamed",
		BaseURL:      "https://api2.example.com/v1",
		Region:       "default",
		Models:       []ModelInfoInput{{ModelName: "gpt-test", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	if err != nil {
		t.Fatalf("update instance: %v", err)
	}
	plain, err := crypto.Decrypt(svc.EncryptKey, updated.APIKeyEnc)
	if err != nil {
		t.Fatalf("decrypt updated credential: %v", err)
	}
	if plain != "existing-key" {
		t.Fatalf("expected stored credential to be preserved, got %q", plain)
	}
}
