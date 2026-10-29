package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// platformAuditorRole creates a platform-scoped role holding exactly the given
// grants, plus the platform user bound to it. This models the delegated platform
// auditor from doc/248: audit read surfaces + tenant.audit.read, no
// governance.read and no governance.manage.
func platformAuditorRole(t *testing.T, env *testEnv, name string, allow []service.PermissionSpec) (string, string) {
	t.Helper()
	ctx := context.Background()
	role, err := env.svc.CreateRole(ctx, env.platformID, model.RolePlatformAdmin, service.CreateRoleRequest{
		Name: name, Scope: model.RoleScopePlatform,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	specs := make([]service.PermissionSpec, 0, len(allow))
	for _, spec := range allow {
		spec.Effect = model.PermissionEffectAllow
		specs = append(specs, spec)
	}
	if err := env.svc.SetRolePermissions(ctx, env.platformID, model.RolePlatformAdmin, role.ID, specs); err != nil {
		t.Fatalf("set permissions: %v", err)
	}
	user, err := env.svc.CreateUser(ctx, env.tenantID, model.RolePlatformAdmin, service.CreateUserRequest{
		Username: name, Password: "secret123", Role: role.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID, role.ID
}

func auditListContext(t *testing.T, userID, tenantID, query string) *gin.Context {
	t.Helper()
	c := modelProviderHandlerContext(t, nil, nil)
	c.Request.Method = http.MethodGet
	c.Request.URL.RawQuery = query
	c.Set(middleware.ContextUserID, userID)
	c.Set(middleware.ContextTenantID, tenantID)
	return c
}

// TestTenantAuditReadUnlocksCrossWorkspaceAuditList is the R-26 end-to-end
// wiring check: a delegated platform auditor that holds the audit read surfaces
// plus tenant.audit.read (but not governance.read) can list audits with
// scope=all, and the audit entry names the fine-grained permission that actually
// justified the cross-workspace read.
func TestTenantAuditReadUnlocksCrossWorkspaceAuditList(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	userID, _ := platformAuditorRole(t, env, "platform-auditor", []service.PermissionSpec{
		{Action: "read", Resource: "audit"},
		{Action: "read", Resource: "audit-anchor"},
		{Action: service.PermTenantAuditRead, Resource: "tenant"},
	})

	c := auditListContext(t, userID, env.tenantID, "scope=all")
	env.h.ListAudits(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("delegated auditor scope=all should be allowed, got %d: %s", c.Writer.Status(), c.Writer.(*providerCaptureWriter).body.String())
	}

	audits, _, err := env.svc.Store.ListAudits(ctx, env.tenantID, 1, 100, repository.AuditFilter{Action: "audit.governance.read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one governance-read audit entry, got %d", len(audits))
	}
	if audits[0].AuthorizationPermission != "tenant.audit.read:tenant" {
		t.Fatalf("audit should name the fine-grained permission, got %q", audits[0].AuthorizationPermission)
	}
	if audits[0].Scope != string(service.TenantScopeAllAuthorized) {
		t.Fatalf("audit scope should be ALL_AUTHORIZED, got %q", audits[0].Scope)
	}
}

// TestAuditReadWithoutTenantAuditReadStaysWorkspaceScoped pins that the narrow
// capability is what unlocks the cross-workspace scope: the same audit read
// surfaces without tenant.audit.read are still rejected for scope=all.
func TestAuditReadWithoutTenantAuditReadStaysWorkspaceScoped(t *testing.T) {
	env := setupTestEnv(t)
	userID, _ := platformAuditorRole(t, env, "workspace-auditor", []service.PermissionSpec{
		{Action: "read", Resource: "audit"},
		{Action: "read", Resource: "audit-anchor"},
	})

	c := auditListContext(t, userID, env.tenantID, "scope=all")
	env.h.ListAudits(c)
	if c.Writer.Status() != http.StatusForbidden {
		t.Fatalf("scope=all without tenant.audit.read should be 403, got %d", c.Writer.Status())
	}

	// The workspace-scoped default still works, so the role stays usable.
	c = auditListContext(t, userID, env.tenantID, "")
	env.h.ListAudits(c)
	if c.Writer.Status() != http.StatusOK {
		t.Fatalf("workspace-scoped audit read should stay allowed, got %d", c.Writer.Status())
	}
}
