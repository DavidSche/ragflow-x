package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ScenarioID: SC-ROUTE-001
func TestP0_ROUTE_001_UpdateModelRouteRejectsMissingAndForeignProviders(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	workspace, err := svc.CreateTenant(ctx, "Route Workspace")
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, err := svc.CreateTenant(ctx, "Foreign Route Workspace")
	if err != nil {
		t.Fatal(err)
	}
	providerID, foreignProviderID := id.New(), id.New()
	if err := svc.Store.CreateModelProvider(ctx, &model.ModelProvider{
		ID: providerID, TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "route provider", Enabled: true, Status: model.ProviderStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateModelProvider(ctx, &model.ModelProvider{
		ID: foreignProviderID, TenantID: foreignTenant.ID, ProviderType: "openai-api-compatible",
		Name: "foreign provider", Enabled: true, Status: model.ProviderStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: providerID, Scenario: "chat", ModelAlias: "route-model", TargetModel: "target-model",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		providerID string
	}{
		{name: "missing provider", providerID: id.New()},
		{name: "foreign provider", providerID: foreignProviderID},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.UpdateModelRoute(ctx, workspace.ID, route.ID, CreateModelRouteRequest{
				ProviderID: test.providerID, Scenario: "changed", ModelAlias: "changed-model", TargetModel: "changed-target",
			})
			if err == nil {
				t.Fatalf("%s must not be accepted", test.name)
			}
			stored, err := svc.Store.GetModelRoute(ctx, workspace.ID, route.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored == nil || stored.ProviderID != providerID || stored.ModelAlias != "route-model" ||
				stored.TargetModel != "target-model" || stored.Scenario != "chat" {
				t.Fatalf("rejected update changed route: %+v", stored)
			}
		})
	}
}

// ScenarioID: SC-ROUTE-001
func TestP0_ROUTE_001_PinnedModelRouteRejectsProviderChange(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Pinned Route Workspace")
	if err != nil {
		t.Fatal(err)
	}
	providerID, replacementProviderID := id.New(), id.New()
	for _, provider := range []*model.ModelProvider{
		{ID: providerID, TenantID: workspace.ID, ProviderType: "openai-api-compatible", Name: "pinned provider", Enabled: true, Status: model.ProviderStatusActive},
		{ID: replacementProviderID, TenantID: workspace.ID, ProviderType: "openai-api-compatible", Name: "replacement provider", Enabled: true, Status: model.ProviderStatusActive},
	} {
		if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: providerID, Scenario: "chat", ModelAlias: "pinned-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Pinned Route Connection",
		BaseURL: "https://pinned.example.com/v1", CredentialRef: "vault://pinned/route",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: []string{"chat"}, AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1,
		ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.UpdateModelRoute(ctx, workspace.ID, route.ID, CreateModelRouteRequest{
		ProviderID: replacementProviderID, Scenario: "chat", ModelAlias: "pinned-model", TargetModel: "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("pinned route provider change must be rejected")
	}
	stored, err := svc.Store.GetModelRoute(ctx, workspace.ID, route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.ProviderID != providerID || stored.CurrentPinID != pin.PinID || stored.CurrentPinVersion != pin.Version {
		t.Fatalf("pinned route state changed: %+v", stored)
	}
}
