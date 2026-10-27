package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
)

func TestUpdateUserProfilePersists(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "oldname", Password: "secret123", Role: model.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}

	newPass := "brand-new-pass-123"
	if err := svc.UpdateUserProfile(ctx, ta.ID, model.RolePlatformAdmin, u.ID, "renamed", "renamed@example.com", model.RoleOperator, newPass); err != nil {
		t.Fatalf("update user profile: %v", err)
	}

	got, err := svc.Store.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "renamed" {
		t.Fatalf("username not persisted: %q", got.Username)
	}
	if got.Email != "renamed@example.com" {
		t.Fatalf("email not persisted: %q", got.Email)
	}
	if !password.Verify(got.PasswordHash, newPass) {
		t.Fatal("password not persisted (hash mismatch)")
	}
}
