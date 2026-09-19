package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestRoleSingleSourceConsistent verifies that rgx_user.role (the derived
// marker) and rgx_user_role (the authoritative source) never diverge across
// user creation and role reassignment.
func TestRoleSingleSourceConsistent(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "r.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}

	u, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "alice", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "tenant_admin" {
		t.Fatalf("user role marker mismatch: %s", u.Role)
	}
	roles, _ := svc.Store.ListRolesByUser(ctx, u.ID)
	if len(roles) != 1 || roles[0].ID != "tenant_admin" {
		t.Fatalf("expected tenant_admin association, got %+v", roles)
	}

	// Reassign; both the marker and the association must move together.
	// Add a second tenant admin so demoting alice does not remove the last
	// admin, which is blocked by the self-admin protection.
	if _, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "bob", Password: "secret123", Role: "tenant_admin"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignRole(ctx, ta.ID, "tenant_admin", u.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	re, _ := svc.Store.GetUser(ctx, u.ID)
	if re.Role != "operator" {
		t.Fatalf("user role marker not updated: %s", re.Role)
	}
	roles2, _ := svc.Store.ListRolesByUser(ctx, u.ID)
	if len(roles2) != 1 || roles2[0].ID != "operator" {
		t.Fatalf("expected operator association, got %+v", roles2)
	}
}
