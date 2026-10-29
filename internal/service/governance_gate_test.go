package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// delegatedPlatformUser creates a platform-scoped custom role holding exactly the
// given grants (plus optional denies), and one user bound to it in a fresh
// workspace. Platform scope is what the shipped design uses for these
// capabilities (doc/77 §4.7); the gate itself is permission-driven, not
// role-name-driven.
func delegatedPlatformUser(t *testing.T, svc *Service, name string, allow, deny []PermissionSpec) (string, string) {
	t.Helper()
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin lookup: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, name+"-ws")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	role, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{Name: name, Scope: model.RoleScopePlatform})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	specs := make([]PermissionSpec, 0, len(allow)+len(deny))
	for _, spec := range allow {
		spec.Effect = model.PermissionEffectAllow
		specs = append(specs, spec)
	}
	for _, spec := range deny {
		spec.Effect = model.PermissionEffectDeny
		specs = append(specs, spec)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, admin.Role, role.ID, specs); err != nil {
		t.Fatalf("set permissions: %v", err)
	}
	// actorRole is the third argument; a platform-scoped role may only be
	// assigned by a platform admin, which is exactly the delegation path we want
	// to model.
	user, err := svc.CreateUser(ctx, workspace.ID, model.RolePlatformAdmin, CreateUserRequest{
		Username: name, Password: "secret123", Role: role.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.ID, workspace.ID
}

// TestTenantAuditReadIsWiredAsFineGrainedAlternative covers R-26 for
// tenant.audit.read: a delegated auditor can read audit data across workspaces
// with that permission alone, and the capability does not leak into other
// governance reads.
func TestTenantAuditReadIsWiredAsFineGrainedAlternative(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	userID, tenantID := delegatedPlatformUser(t, svc, "auditor", []PermissionSpec{
		{Action: "tenant.audit.read", Resource: "tenant"},
	}, nil)

	scope, err := svc.ResolveTenantScopeFor(ctx, userID, tenantID, "all", "", "audit")
	if err != nil {
		t.Fatalf("auditor scope=all for audit should be allowed: %v", err)
	}
	if !scope.ScopeAll() {
		t.Fatalf("expected ALL_AUTHORIZED, got %s", scope.Kind)
	}
	if scope.PermittedBy != "tenant.audit.read:tenant" {
		t.Fatalf("audit attribution should name the fine-grained permission, got %q", scope.PermittedBy)
	}

	// The capability is narrow: it must not unlock usage or generic governance.
	if _, err := svc.ResolveTenantScopeFor(ctx, userID, tenantID, "all", "", "usage"); err == nil {
		t.Fatal("tenant.audit.read must not grant usage reads")
	}
	if _, err := svc.ResolveTenantScope(ctx, userID, tenantID, "all", ""); err == nil {
		t.Fatal("tenant.audit.read must not grant generic governance reads")
	}
	if _, err := svc.ResolveTenantScope(ctx, userID, tenantID, "specific", "some-other-tenant"); err == nil {
		t.Fatal("tenant.audit.read must not grant generic SPECIFIC scope")
	}

	ref, err := svc.AuthorizeAny(ctx, userID, GovernanceReadRefs("audit")...)
	if err != nil || ref.Action != PermTenantAuditRead {
		t.Fatalf("AuthorizeAny should match tenant.audit.read, got %v / %v", ref, err)
	}
}

// TestTenantUsageReadIsWiredAsFineGrainedAlternative covers R-26 for
// tenant.usage.read, including the SPECIFIC scope path.
func TestTenantUsageReadIsWiredAsFineGrainedAlternative(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	userID, tenantID := delegatedPlatformUser(t, svc, "cost-auditor", []PermissionSpec{
		{Action: "tenant.usage.read", Resource: "tenant"},
	}, nil)

	scope, err := svc.ResolveTenantScopeFor(ctx, userID, tenantID, "all", "", "usage")
	if err != nil {
		t.Fatalf("scope=all for usage should be allowed: %v", err)
	}
	if scope.PermittedBy != "tenant.usage.read:tenant" {
		t.Fatalf("usage attribution should name the fine-grained permission, got %q", scope.PermittedBy)
	}

	target, err := svc.CreateTenant(ctx, "usage-target")
	if err != nil {
		t.Fatal(err)
	}
	specific, err := svc.ResolveTenantScopeFor(ctx, userID, tenantID, "specific", target.ID, "usage")
	if err != nil {
		t.Fatalf("scope=specific for usage should be allowed: %v", err)
	}
	if specific.CurrentTenantID() != target.ID || specific.PermittedBy != "tenant.usage.read:tenant" {
		t.Fatalf("unexpected specific scope: %+v", specific)
	}
	if _, err := svc.ResolveTenantScopeFor(ctx, userID, tenantID, "all", "", "audit"); err == nil {
		t.Fatal("tenant.usage.read must not grant audit reads")
	}
}

// TestTenantResourceDeleteStaysSeparateFromEdit covers R-26 for
// tenant.resource.manage / tenant.resource.delete: editing another workspace does
// not imply deleting it (doc/77 §4.6), and governance.manage keeps working as the
// broad compat branch.
func TestTenantResourceDeleteStaysSeparateFromEdit(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	editorID, tenantID := delegatedPlatformUser(t, svc, "ws-editor", []PermissionSpec{
		{Action: PermTenantResourceManage, Resource: "tenant"},
	}, nil)
	target, err := svc.CreateTenant(ctx, "edit-target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTenant(ctx, editorID, tenantID, target.ID, UpdateTenantRequest{Name: "renamed-target"}); err != nil {
		t.Fatalf("tenant.resource.manage should allow editing another workspace: %v", err)
	}
	if err := svc.DeleteTenant(ctx, editorID, tenantID, target.ID, false); err == nil {
		t.Fatal("tenant.resource.manage must not allow deleting a workspace")
	}

	deleterID, deleterTenantID := delegatedPlatformUser(t, svc, "ws-deleter", []PermissionSpec{
		{Action: PermTenantResourceDelete, Resource: "tenant"},
	}, nil)
	doomed, err := svc.CreateTenant(ctx, "delete-target")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTenant(ctx, deleterID, deleterTenantID, doomed.ID, false); err != nil {
		t.Fatalf("tenant.resource.delete should allow deleting a workspace: %v", err)
	}
}

// TestGovernanceBroadPermissionKeepsAttribution pins the compatibility branch:
// platform_admin (which holds both permissions) keeps recording the historical
// "governance.read:tenant" label so audit history stays comparable, and a deny on
// the fine-grained action does not take the broad permission away.
func TestGovernanceBroadPermissionKeepsAttribution(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin lookup: %v", err)
	}
	scope, err := svc.ResolveTenantScopeFor(ctx, admin.ID, admin.TenantID, "all", "", "audit")
	if err != nil {
		t.Fatalf("platform admin should keep scope=all: %v", err)
	}
	if scope.PermittedBy != "governance.read:tenant" {
		t.Fatalf("broad permission should stay first for attribution, got %q", scope.PermittedBy)
	}

	// deny stays literal: denying the fine-grained action does not remove the
	// broad permission, and denying the broad one does not remove the fine one.
	broadOnlyID, broadOnlyTenant := delegatedPlatformUser(t, svc, "broad-only", []PermissionSpec{
		{Action: "governance.read", Resource: "tenant"},
	}, []PermissionSpec{{Action: PermTenantAuditRead, Resource: "tenant"}})
	if _, err := svc.ResolveTenantScopeFor(ctx, broadOnlyID, broadOnlyTenant, "all", "", "audit"); err != nil {
		t.Fatalf("deny tenant.audit.read must not remove governance.read: %v", err)
	}

	fineOnlyID, fineOnlyTenant := delegatedPlatformUser(t, svc, "fine-only", []PermissionSpec{
		{Action: PermTenantAuditRead, Resource: "tenant"},
	}, []PermissionSpec{{Action: "governance.read", Resource: "tenant"}})
	if _, err := svc.ResolveTenantScopeFor(ctx, fineOnlyID, fineOnlyTenant, "all", "", "audit"); err != nil {
		t.Fatalf("deny governance.read must not remove tenant.audit.read: %v", err)
	}
}
