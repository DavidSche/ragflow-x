package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestPlatformConnectionOperationsUseSeparatedPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	ctx := context.Background()

	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v err=%v", admin, err)
	}
	role, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{
		Name: "connection-rotator", Scope: model.RoleScopePlatform,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, []PermissionSpec{
		{Action: "governance.manage", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "enterprise-connection", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	actor, err := svc.CreateUser(ctx, admin.TenantID, admin.Role, CreateUserRequest{
		Username: "connection-rotator", Password: "secret123", Role: role.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Separated Permissions",
		BaseURL: server.URL + "/v1", CredentialRef: "vault://permissions/openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RotateEnterpriseConnectionCredential(ctx, connection.Connection.ID, actor.ID, RotateEnterpriseConnectionCredentialRequest{
		CredentialVersion: "v2",
	}); err == nil {
		t.Fatal("connection management permission must not imply credential rotation")
	}

	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, []PermissionSpec{
		{Action: "governance.manage", Resource: "tenant", Effect: model.PermissionEffectAllow},
		{Action: "manage", Resource: "enterprise-connection", Effect: model.PermissionEffectAllow},
		{Action: "tenant.connection.rotate_secret", Resource: "tenant", Effect: model.PermissionEffectAllow},
	}); err != nil {
		t.Fatal(err)
	}
	rotated, err := svc.RotateEnterpriseConnectionCredential(ctx, connection.Connection.ID, actor.ID, RotateEnterpriseConnectionCredentialRequest{
		CredentialVersion: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Version.CredentialVersion != "v2" {
		t.Fatalf("unexpected rotated credential version: %+v", rotated.Version)
	}
}
