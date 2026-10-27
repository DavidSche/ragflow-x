package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestMePermissions(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("platform admin missing: %v", err)
	}
	mp, err := svc.MePermissions(ctx, admin.ID)
	if err != nil {
		t.Fatalf("me permissions admin: %v", err)
	}
	if !mp.Platform {
		t.Fatal("platform admin must report platform=true")
	}
	adminHas := func(resource, action string) bool {
		for _, p := range mp.Permissions {
			if p.Resource == resource && p.Action == action && p.Effect == model.PermissionEffectAllow {
				return true
			}
		}
		return false
	}
	for _, action := range []string{"append", "delete:own", "execute"} {
		if !adminHas("document", action) {
			t.Fatalf("platform admin document permission %q missing", action)
		}
	}

	ta, _ := svc.CreateTenant(ctx, "TenantA")
	op, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "op", Password: "secret123", Role: model.RoleOperator})
	opPerms, err := svc.MePermissions(ctx, op.ID)
	if err != nil {
		t.Fatalf("me permissions operator: %v", err)
	}
	if opPerms.Platform {
		t.Fatal("operator must not be platform")
	}
	has := func(resource, action, effect string) bool {
		for _, p := range opPerms.Permissions {
			if p.Resource == resource && p.Action == action && p.Effect == effect {
				return true
			}
		}
		return false
	}
	// Least-privilege operator: can execute chat and read usage (usage nav),
	if !has("chat", "execute", model.PermissionEffectAllow) {
		t.Fatalf("operator should execute chat: %+v", opPerms.Permissions)
	}
	if !has("usage", "read", model.PermissionEffectAllow) {
		t.Fatal("operator should have read usage (usage nav)")
	}
	// but must NOT see governance surfaces (audit / gateway / brand / role).
	if has("audit", "read", model.PermissionEffectAllow) {
		t.Fatal("operator must not read audit")
	}
	if has("api-key", "read", model.PermissionEffectAllow) {
		t.Fatal("operator must not read api-key")
	}
	if has("model-provider", "read", model.PermissionEffectAllow) {
		t.Fatal("operator must not read model-provider")
	}
	if has("branding", "read", model.PermissionEffectAllow) {
		t.Fatal("operator must not read branding")
	}
	if has("role", "read", model.PermissionEffectAllow) {
		t.Fatal("operator must not read role")
	}
}
