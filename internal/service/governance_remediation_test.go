package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/keygen"
)

func TestExplicitPlatformGovernancePermissionsControlCrossTenantObjects(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("platform admin missing: %v err=%v", admin, err)
	}
	target, err := svc.CreateTenant(ctx, "governance target")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := svc.CreateUser(ctx, model.PlatformTenantID, model.RolePlatformAdmin, CreateUserRequest{
		Username: "governance-operator", Password: "secret123", Role: model.RolePlatformAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, CreateRoleRequest{
		Name: "enterprise governor", Scope: model.RoleScopePlatform,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, role.ID, []PermissionSpec{
		{Action: "governance.read", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "governance.manage", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "dataset", Effect: model.PermissionEffectAllow},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AssignUserRole(ctx, operator.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.RemoveUserRole(ctx, operator.ID, model.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeObject(ctx, operator.ID, "manage", "dataset", target.ID, ""); err != nil {
		t.Fatalf("explicit cross-tenant manage should be allowed: %v", err)
	}
	err = svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, role.ID, []PermissionSpec{
		{Action: "governance.read", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "dataset", Effect: model.PermissionEffectAllow},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeObject(ctx, operator.ID, "manage", "dataset", target.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removing governance.manage must reject cross-tenant manage, got %v", err)
	}
	if err := svc.AuthorizeObject(ctx, operator.ID, "read", "dataset", target.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("governance.read must not imply cross-tenant resource read without resource permission, got %v", err)
	}
}

func TestDisabledWorkspaceBlocksAuthScopeAndAPIKeys(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "paused workspace")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "admin", CreateUserRequest{
		Username: "paused-user", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	rawKey, apiKey, err := svc.CreateAPIKey(ctx, tenant.ID, user.ID, "paused key", nil, 0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rawKey == "" || apiKey == nil {
		t.Fatal("expected raw key and credential")
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("platform admin missing: %v err=%v", admin, err)
	}
	if err := svc.BatchUpdateTenantStatus(ctx, admin.ID, []string{tenant.ID}, model.TenantStatusDisabled); err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, user.ID, "read", "dataset"); err == nil {
		t.Fatal("disabled workspace must reject authorization")
	}
	if _, err := svc.ResolveTenantScope(ctx, user.ID, tenant.ID, "current", ""); err == nil {
		t.Fatal("disabled workspace must reject governance scope resolution")
	}
	if _, err := svc.ResolveAPIKey(ctx, rawKey); err == nil {
		t.Fatal("disabled workspace must reject API key")
	}
	if _, err := svc.RefreshAccessToken(ctx, "not-a-token"); err == nil {
		t.Fatal("invalid token should fail")
	}
}

func TestDisabledWorkspaceRejectsFreshLogin(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "fresh disabled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "admin", CreateUserRequest{
		Username: "fresh-user", Password: "secret123", Role: model.RoleTenantAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("platform admin missing: %v err=%v", admin, err)
	}
	active, err := svc.Login(ctx, "fresh-user", "secret123", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BatchUpdateTenantStatus(ctx, admin.ID, []string{tenant.ID}, model.TenantStatusDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "fresh-user", "secret123", "127.0.0.1"); err == nil {
		t.Fatal("disabled workspace login must fail")
	} else if authzErr, ok := err.(*httperr.Error); !ok || authzErr.Status != 403 || authzErr.Code != 40301 {
		t.Fatalf("disabled workspace login error: %+v", err)
	}
	if _, err := svc.RefreshAccessToken(ctx, active.Refresh); err == nil {
		t.Fatal("disabled workspace refresh must fail")
	} else if authzErr, ok := err.(*httperr.Error); !ok || authzErr.Status != 403 || authzErr.Code != 40301 {
		t.Fatalf("disabled workspace refresh error: %+v", err)
	}
}

func TestEnterpriseBindingCapabilityIsEnforcedForExactPinVersion(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	tenant, err := svc.CreateTenant(ctx, "capability workspace")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, tenant.ID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Private LLM",
		BaseURL: "https://enterprise.example.com", CredentialRef: "vault://private-llm",
		Visibility: model.EnterpriseConnectionVisibilityPrivate,
		ManagedBy:  model.EnterpriseConnectionManagedByWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: tenant.ID,
		AllowedCapabilities: []string{"embedding"}, AllowedModelRefs: []string{"text-embedding-3-small"},
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID := id.New()
	route := &model.ModelRoute{ID: routeID, TenantID: tenant.ID, Scenario: "embedding", TargetModel: "text-embedding-3-small", Enabled: true}
	if err := svc.Store.CreateModelRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, "admin", tenant.ID, routeID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: connection.Connection.CurrentConnectionVersion,
		BindingID: binding.Binding.BindingID, BindingVersion: binding.Binding.CurrentBindingVersion,
		ModelRef: "text-embedding-3-small",
	})
	if err != nil {
		t.Fatal(err)
	}
	route.CurrentPinID = pin.PinID
	route.CurrentPinVersion = pin.Version
	route.Scenario = "chat"
	if _, err := svc.ValidateEnterpriseModelRoutePin(ctx, route); err == nil {
		t.Fatal("chat route must not use embedding-only binding")
	}
	route.Scenario = "embedding"
	pinned, err := svc.ValidateEnterpriseModelRoutePin(ctx, route)
	if err != nil || pinned == nil {
		t.Fatalf("embedding route should pass: pin=%+v err=%v", pinned, err)
	}
	if keygen.Hash("") == keygen.Hash("x") {
		t.Fatal("key hash changed unexpectedly")
	}
	_ = time.Now
}
