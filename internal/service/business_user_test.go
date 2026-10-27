package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestBusinessUserRolePermissionBoundary(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Business User Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "business-user",
		Password: "secret123",
		Role:     model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatalf("create business user: %v", err)
	}

	allowed := []struct {
		action   string
		resource string
	}{
		{"append", "document"},
		{"execute", "assistant"},
		{"execute", "chat"},
		{"execute", "search-app"},
		{"execute", "agent"},
		{"session:create", "agent"},
	}
	for _, grant := range allowed {
		if err := svc.Authorize(ctx, user.ID, grant.action, grant.resource); err != nil {
			t.Fatalf("expected %s on %s: %v", grant.action, grant.resource, err)
		}
	}

	denied := []struct {
		action   string
		resource string
	}{
		{"manage", "dataset"},
		{"execute", "document"},
		{"manage", "prompt-policy"},
		{"manage", "agent"},
		{"manage", "chat"},
		{"manage", "user"},
		{"manage", "role"},
	}
	for _, grant := range denied {
		if err := svc.Authorize(ctx, user.ID, grant.action, grant.resource); err == nil {
			t.Fatalf("expected %s on %s to be denied", grant.action, grant.resource)
		}
	}
}
