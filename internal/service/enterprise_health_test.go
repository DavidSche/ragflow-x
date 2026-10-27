package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func enterpriseHealthScope(tenantID string) TenantScope {
	return TenantScope{
		Kind: TenantScopeCurrent, ActorTenantID: tenantID,
		TargetTenantID: tenantID, AllowedTenantIDs: []string{tenantID},
	}
}

func newProbedEnterpriseConnection(t *testing.T, handler http.HandlerFunc) (*Service, context.Context, *model.User, *EnterpriseConnectionView, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Health Connection",
		BaseURL: server.URL + "/v1", CredentialRef: "vault://health/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, ctx, admin, connection, server.URL
}

func TestEnterpriseConnectionHealthProbeAndLayeredAvailability(t *testing.T) {
	var sawAuthorization bool
	var sawPath string
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		sawAuthorization = r.Header.Get("Authorization") != ""
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	workspace, err := svc.CreateTenant(ctx, "Health Workspace")
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
	provider := &model.ModelProvider{
		ID: "health-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Health Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "health-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := TenantScope{Kind: TenantScopeAllAuthorized, ActorTenantID: admin.TenantID}
	health, err := svc.TestEnterpriseConnection(ctx, scope, connection.Connection.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sawAuthorization || sawPath != "/v1/models" {
		t.Fatalf("unsafe probe request: auth=%v path=%s", sawAuthorization, sawPath)
	}
	if health.Connection.RuntimeHealth != model.RuntimeHealthHealthy || health.Latest == nil ||
		health.Latest.Health != model.RuntimeHealthHealthy || health.Latest.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected health: %+v", health)
	}
	if health.Connection.LastHealthCheckAt == nil {
		t.Fatal("last health check time was not persisted")
	}

	availability, err := svc.GetEnterpriseConnectionAvailability(ctx, scope, connection.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(availability.Bindings) != 1 || len(availability.Routes) != 1 {
		t.Fatalf("availability layers: bindings=%d routes=%d", len(availability.Bindings), len(availability.Routes))
	}
	bindingAvailability := availability.Bindings[0]
	if bindingAvailability.Binding.BindingID != binding.Binding.BindingID ||
		bindingAvailability.Availability != "AVAILABLE" || len(bindingAvailability.ModelAvailability) != 1 ||
		bindingAvailability.ModelAvailability[0].Availability != "AVAILABLE" {
		t.Fatalf("unexpected binding availability: %+v", bindingAvailability)
	}
	routeAvailability := availability.Routes[0]
	if routeAvailability.Route.ID != route.ID || routeAvailability.Route.CurrentPinID != pin.PinID ||
		routeAvailability.Availability != "AVAILABLE" {
		t.Fatalf("unexpected route availability: %+v", routeAvailability)
	}
}

func TestEnterpriseConnectionHealthProbeClassifiesUpstreamFailure(t *testing.T) {
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	health, err := svc.TestEnterpriseConnection(ctx, enterpriseHealthScope(admin.TenantID), connection.Connection.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Connection.RuntimeHealth != model.RuntimeHealthDegraded || health.Latest.Health != model.RuntimeHealthDegraded ||
		health.Latest.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("unexpected degraded health: %+v", health)
	}
}

func TestEnterpriseConnectionAvailabilityRejectsSupersededBindingPin(t *testing.T) {
	svc, ctx, admin, connection, _ := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	workspace, err := svc.CreateTenant(ctx, "Superseded Binding Workspace")
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
	provider := &model.ModelProvider{ID: "binding-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible", Name: "Binding Provider", Enabled: true}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "binding-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateEnterpriseBinding(ctx, admin.ID, binding.Binding.BindingID, UpdateEnterpriseBindingRequest{
		AllowedModelRefs: []string{"gpt-4o-mini", "gpt-4.1-mini"},
	}); err != nil {
		t.Fatal(err)
	}
	scope := TenantScope{Kind: TenantScopeAllAuthorized, ActorTenantID: admin.TenantID}
	availability, err := svc.GetEnterpriseConnectionAvailability(ctx, scope, connection.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(availability.Routes) != 1 || availability.Routes[0].Availability != "UNAVAILABLE" ||
		availability.Routes[0].Reason != "route pin references a superseded binding version" {
		t.Fatalf("unexpected stale binding route availability: %+v", availability.Routes)
	}
}

func TestEnterpriseConnectionAvailabilityRejectsSupersededConnectionPin(t *testing.T) {
	svc, ctx, admin, connection, serverURL := newProbedEnterpriseConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	workspace, err := svc.CreateTenant(ctx, "Superseded Connection Workspace")
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
	provider := &model.ModelProvider{ID: "connection-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible", Name: "Connection Provider", Enabled: true}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "connection-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID, UpdateEnterpriseConnectionRequest{
		ProviderName:      "openai-api-compatible",
		DisplayName:       "Health Connection v2",
		BaseURL:           serverURL + "/v1",
		CredentialRef:     "vault://health/openai",
		CredentialVersion: "v2",
		Visibility:        model.EnterpriseConnectionVisibilityShared,
		UsableBy:          "AUTHORIZED_BINDINGS",
		ManagedBy:         model.EnterpriseConnectionManagedByPlatform,
		CredentialScope:   model.EnterpriseConnectionCredentialScopeConnection,
	}); err != nil {
		t.Fatal(err)
	}
	scope := TenantScope{Kind: TenantScopeAllAuthorized, ActorTenantID: admin.TenantID}
	availability, err := svc.GetEnterpriseConnectionAvailability(ctx, scope, connection.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(availability.Routes) != 1 || availability.Routes[0].Availability != "UNAVAILABLE" ||
		availability.Routes[0].Reason != "route pin references a superseded connection version" {
		t.Fatalf("unexpected stale connection route availability: %+v", availability.Routes)
	}
}
