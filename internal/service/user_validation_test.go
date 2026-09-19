package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestCreateUserValidatesEmailAndTrimsFields(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "alice", Password: "secret123", Email: "not-an-email", Role: model.RoleOperator,
	}); err == nil {
		t.Fatal("invalid email must be rejected")
	}

	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: " alice ", Password: "secret123", Email: " alice@example.com ", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "alice" || user.Email != "alice@example.com" {
		t.Fatalf("fields were not normalized: username=%q email=%q", user.Username, user.Email)
	}

	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "Alice", Password: "secret123", Role: model.RoleOperator,
	}); err == nil {
		t.Fatal("duplicate username must be rejected")
	}
}
