package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// delegatedUser creates a fresh workspace, a custom role with exactly the given
// grants, a user bound to that role, and returns the user's API token. It models
// the delegation paths from doc/248: platform auditors and platform operators get
// narrow grants instead of the 140-permission platform_admin role.
func delegatedUser(t *testing.T, app *testApp, name, roleScope string, allow []service.PermissionSpec) (userID, tenantID, token string) {
	t.Helper()
	ctx := context.Background()
	admin, err := app.svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin lookup: %v", err)
	}
	workspace, err := app.svc.CreateTenant(ctx, name+"-ws")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	role, err := app.svc.CreateRole(ctx, admin.ID, model.RolePlatformAdmin, service.CreateRoleRequest{
		Name: name, Scope: roleScope,
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	specs := make([]service.PermissionSpec, 0, len(allow))
	for _, spec := range allow {
		spec.Effect = model.PermissionEffectAllow
		specs = append(specs, spec)
	}
	if err := app.svc.SetRolePermissions(ctx, admin.ID, model.RolePlatformAdmin, role.ID, specs); err != nil {
		t.Fatalf("set permissions: %v", err)
	}
	user, err := app.svc.CreateUser(ctx, workspace.ID, model.RolePlatformAdmin, service.CreateUserRequest{
		Username: name, Password: "secret123", Role: role.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"username": name, "password": "secret123"})
	resp, err := http.Post(app.ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %d %s", name, resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" {
		t.Fatalf("empty token for %s", name)
	}
	return user.ID, workspace.ID, out.Data.Token
}

// TestE2E_DelegatedAuditorReadsAuditsAcrossWorkspaces is the HTTP-level proof for
// R-26: the RBAC middleware must accept the same fine-grained permission the
// handler accepts, otherwise tenant.audit.read would be a declared-but-unreachable
// grant. It also pins the audit attribution to the permission that was used.
func TestE2E_DelegatedAuditorReadsAuditsAcrossWorkspaces(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	_, auditorTenant, auditorToken := delegatedUser(t, app, "platform-auditor", model.RoleScopePlatform, []service.PermissionSpec{
		{Action: "read", Resource: "audit"},
		{Action: "read", Resource: "audit-anchor"},
		{Action: service.PermTenantAuditRead, Resource: "tenant"},
	})
	_, _, workspaceAuditorToken := delegatedUser(t, app, "workspace-auditor", model.RoleScopePlatform, []service.PermissionSpec{
		{Action: "read", Resource: "audit"},
		{Action: "read", Resource: "audit-anchor"},
	})

	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/audit?scope=all", auditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delegated auditor scope=all should be 200, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/audit?scope=all", workspaceAuditorToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("audit read without tenant.audit.read should be 403, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/audit", workspaceAuditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace-scoped audit read should stay 200, got %d %s", resp.StatusCode, body)
	}
	// The audit evidence surfaces are one capability: the same delegated role must
	// reach anchors and export, not just the log itself.
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/audit/anchors?scope=all", auditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delegated auditor should read audit anchors, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/audit/export?scope=all", auditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delegated auditor should export the audit log, got %d %s", resp.StatusCode, body)
	}

	audits, _, err := app.svc.Store.ListAudits(ctx, auditorTenant, 1, 100, repository.AuditFilter{Action: "audit.governance.read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 {
		t.Fatal("expected a governance-read audit entry for the delegated audit read")
	}
	if audits[0].AuthorizationPermission != "tenant.audit.read:tenant" {
		t.Fatalf("audit should name the fine-grained permission, got %q", audits[0].AuthorizationPermission)
	}
}

// TestE2E_WorkspaceWriteCapabilitiesAreSeparate pins the operator split over HTTP:
// editing a workspace needs tenant.resource.manage, deleting needs
// tenant.resource.delete, and neither implies the other (doc/77 §4.6).
func TestE2E_WorkspaceWriteCapabilitiesAreSeparate(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	// The admin surface (manage tenant) is the route-level gate; the fine-grained
	// action decides what the delegated operator can actually do.
	surface := service.PermissionSpec{Action: "manage", Resource: "tenant"}
	_, _, editorToken := delegatedUser(t, app, "ws-editor", model.RoleScopePlatform, []service.PermissionSpec{
		surface,
		{Action: service.PermTenantResourceManage, Resource: "tenant"},
	})
	_, _, deleterToken := delegatedUser(t, app, "ws-deleter", model.RoleScopePlatform, []service.PermissionSpec{
		surface,
		{Action: service.PermTenantResourceDelete, Resource: "tenant"},
	})

	editable, err := app.svc.CreateTenant(ctx, "editable-ws")
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := app.svc.CreateTenant(ctx, "doomed-ws")
	if err != nil {
		t.Fatal(err)
	}

	resp, body := app.doAuth(t, http.MethodPut, "/api/v1/tenants/"+editable.ID, editorToken, []byte(`{"name":"renamed-ws"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant.resource.manage should allow editing, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/tenants/"+editable.ID, editorToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant.resource.manage must not allow deleting, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPut, "/api/v1/tenants/"+doomed.ID, deleterToken, []byte(`{"name":"renamed-by-deleter"}`))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant.resource.delete must not allow editing, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/tenants", deleterToken, []byte(`{"name":"created-by-deleter"}`))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant.resource.delete must not allow creating workspaces, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/tenants", editorToken, []byte(`{"name":"created-by-editor"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant.resource.manage should allow creating workspaces, got %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/tenants/"+doomed.ID, deleterToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant.resource.delete should allow deleting, got %d %s", resp.StatusCode, body)
	}
}
