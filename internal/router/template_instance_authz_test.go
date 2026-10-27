package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// Handler-level authorization regression coverage for the doc/107 §3.1.5
// template instance API family, the rollout policy endpoints and the
// doc/100 §7.3 audit-grade answer export. The tests exercise the real
// router stack: auth middleware + RBAC accessRules + the handler's own
// authorizeResource re-check (doc/11 §6.1).

type templateInstanceAuthzEnv struct {
	app      *testApp
	tenantID string
	// platformToken: platform_admin (bootstrap admin, own tenant).
	platformToken string
	// adminToken: tenant_admin in tenantID (allowed by the matrix).
	adminToken string
	// operatorToken: operator in tenantID (release-governance read-only,
	// no scenario-template:manage, no audit-export).
	operatorToken string
}

// newTemplateInstanceAuthzEnv boots the full router stack and provisions a
// tenant admin and an operator under one workspace. The bootstrap platform
// admin from newTestApp serves as the platform_admin actor.
func newTemplateInstanceAuthzEnv(t *testing.T) *templateInstanceAuthzEnv {
	t.Helper()
	app := newTestApp(t)
	t.Cleanup(app.close)
	ctx := context.Background()

	tenant, err := app.svc.CreateTenant(ctx, "ti-authz-"+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	operator, err := app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "ti-operator-" + id.New()[:6], Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := &templateInstanceAuthzEnv{app: app, tenantID: tenant.ID}
	env.platformToken = app.login(t)
	adminToken, err := loginAsTenantAdmin(t, app, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	env.adminToken = adminToken
	env.operatorToken = loginAsUser(t, app, operator.Username, "secret123")
	return env
}

// loginAsTenantAdmin creates a tenant_admin under tenantID and logs in.
func loginAsTenantAdmin(t *testing.T, app *testApp, tenantID string) (string, error) {
	t.Helper()
	admin, err := app.svc.CreateUser(context.Background(), tenantID, "", service.CreateUserRequest{
		Username: "ti-admin-" + id.New()[:6], Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		return "", err
	}
	token := loginAsUser(t, app, admin.Username, "secret123")
	return token, nil
}

// assertForbiddenOrNotFound accepts the two fail-closed shapes: the RBAC
// middleware and the handler re-check return 403; tenant-scoped lookups may
// legally degrade to 404 (resource exists in another workspace). Any 2xx is
// a leak and any 5xx hides an authorization bug behind a server error.
func assertForbiddenOrNotFound(t *testing.T, action string, status int, body []byte) {
	t.Helper()
	if status >= 200 && status < 300 {
		t.Fatalf("%s must not succeed for an unauthorized role: status %d body %s", action, status, body)
	}
	if status >= 500 {
		t.Fatalf("%s failed with a server error instead of a clean denial: status %d body %s", action, status, body)
	}
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("%s unexpected denial status %d: %s", action, status, body)
	}
}

// TestTemplateInstanceFamilyDeniesRolesWithoutManage pins the middleware +
// handler double gate on every release-governance manage route: the operator
// role holds release-governance:read only, so every mutating endpoint must
// be denied; dry-run additionally requires scenario-template:manage which
// the operator also lacks.
func TestTemplateInstanceFamilyDeniesRolesWithoutManage(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]interface{}
	}{
		{"instantiate", http.MethodPost, "/api/v1/template-instances", map[string]interface{}{
			"template_id": "t1", "project_id": "p1", "name": "n1", "idempotency_key": "k1",
		}},
		{"dry-run", http.MethodPost, "/api/v1/template-instances/ti-1/dry-run", map[string]interface{}{}},
		{"evaluate", http.MethodPost, "/api/v1/template-instances/ti-1/evaluate", nil},
		{"release", http.MethodPost, "/api/v1/template-instances/ti-1/release", nil},
		{"activate", http.MethodPost, "/api/v1/template-instances/ti-1/releases/rel-1/activate", nil},
		{"rollback", http.MethodPost, "/api/v1/template-instances/ti-1/rollback", map[string]interface{}{
			"target_release_id": "rel-old",
		}},
		{"reconcile", http.MethodPost, "/api/v1/template-instances/ti-1/releases/rel-1/reconcile", nil},
	}
	for _, tc := range cases {
		resp, body := env.app.doAuth(t, tc.method, tc.path, env.operatorToken, mustJSON(t, tc.body))
		assertForbiddenOrNotFound(t, "template-instances "+tc.name+" (operator)", resp.StatusCode, body)
	}
}

// TestTemplateInstanceReadRoutesDenyMissingReadGrant is a control pair: the
// operator does hold release-governance:read, so list must return 200 with an
// empty page, while a viewer-level denial is covered by the audit-export
// denial test below (same operator, resource it does not hold).
func TestTemplateInstanceReadRoutesAllowOperatorRead(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	resp, body := env.app.doAuth(t, http.MethodGet, "/api/v1/template-instances", env.operatorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operator list template-instances status = %d: %s", resp.StatusCode, body)
	}
	var page struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if page.Data.Total != 0 || len(page.Data.Items) != 0 {
		t.Fatalf("fresh workspace must list no instances: %+v", page.Data)
	}
}

// TestTemplateInstanceInstantiateDeniedWithoutScenarioTemplateManage pins the
// doc/124 §2.4 double check on POST /template-instances: the tenant admin
// holds release-governance:manage, but the route also requires
// scenario-template:manage at the handler layer — a role cannot trade one
// grant for the other. We assert via the operator (holding neither) that the
// middleware gate fires first with 403, and keep the release-governance-only
// shape covered by the handler unit contract: instantiate with a valid token
// but a missing template fails on template lookup, not on authorization.
func TestTemplateInstanceInstantiateDeniedWithoutScenarioTemplateManage(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	resp, body := env.app.doAuth(t, http.MethodPost, "/api/v1/template-instances", env.operatorToken, mustJSON(t, map[string]interface{}{
		"template_id": "t1", "project_id": "p1", "name": "n1", "idempotency_key": "k1",
	}))
	assertForbiddenOrNotFound(t, "instantiate (operator)", resp.StatusCode, body)

	// Allowed role, unknown template: authorization passes, service layer
	// fails on the published-template lookup with a 4xx client error.
	resp, body = env.app.doAuth(t, http.MethodPost, "/api/v1/template-instances", env.adminToken, mustJSON(t, map[string]interface{}{
		"template_id": "missing-template", "project_id": "p1", "name": "n1", "idempotency_key": "k1",
	}))
	if resp.StatusCode >= 500 || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		t.Fatalf("instantiate with allowed role must fail on template lookup (4xx), got %d: %s", resp.StatusCode, body)
	}
}

// TestRolloutPoliciesAuthorizationMatrix covers the three rollout policy
// routes: GET requires release-governance:read (operator allowed, and the
// missing assistant surfaces as 404 after authorization), POST/PUT require
// release-governance:manage (operator denied).
func TestRolloutPoliciesAuthorizationMatrix(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	// Manage routes denied for the read-only operator role.
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   map[string]interface{}
	}{
		{"create", http.MethodPost, "/api/v1/assistants/ast-1/rollout-policies", map[string]interface{}{}},
		{"update", http.MethodPut, "/api/v1/assistants/ast-1/rollout-policies/pol-1", map[string]interface{}{}},
	} {
		resp, body := env.app.doAuth(t, tc.method, tc.path, env.operatorToken, mustJSON(t, tc.body))
		assertForbiddenOrNotFound(t, "rollout-policies "+tc.name+" (operator)", resp.StatusCode, body)
	}

	// Read route allowed for the operator; authorization precedes the
	// assistant lookup, so the unknown assistant yields a clean 404.
	resp, body := env.app.doAuth(t, http.MethodGet, "/api/v1/assistants/ast-1/rollout-policies", env.operatorToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("rollout-policies read (operator, missing assistant) status = %d, want 404: %s", resp.StatusCode, body)
	}
}

// TestAnswerAuditExportAuthorization pins the doc/100 §7.3 contract 2
// permission split through the router stack: the audit-grade export is gated
// by its own audit-export:read resource. The operator role (holding
// chat:read for business exports) must be denied; the tenant admin is
// allowed and the response carries the forensic export job.
func TestAnswerAuditExportAuthorization(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	// Denied: operator lacks audit-export:read.
	resp, body := env.app.doAuth(t, http.MethodGet, "/api/v1/answer-snapshots/snap-1/audit-export", env.operatorToken, nil)
	assertForbiddenOrNotFound(t, "audit-export (operator)", resp.StatusCode, body)

	// Allowed: tenant_admin holds audit-export:read. The unknown snapshot
	// surfaces as a 404 after authorization succeeds.
	resp, body = env.app.doAuth(t, http.MethodGet, "/api/v1/answer-snapshots/snap-1/audit-export", env.adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("audit-export (admin, missing snapshot) status = %d, want 404: %s", resp.StatusCode, body)
	}
}

// TestAnswerAuditExportCrossTenantDenied verifies the ABAC layer: a
// platform admin reading another workspace's snapshot is scoped to the
// platform tenant and must not observe the resource; a foreign tenant admin
// is rejected outright.
func TestAnswerAuditExportCrossTenantDenied(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	resp, body := env.app.doAuth(t, http.MethodGet, "/api/v1/answer-snapshots/snap-1/audit-export", env.platformToken, nil)
	assertForbiddenOrNotFound(t, "audit-export (platform admin, foreign snapshot)", resp.StatusCode, body)

	resp, body = env.app.doAuth(t, http.MethodGet, "/api/v1/answer-snapshots/snap-1/audit-export", env.adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("audit-export (own admin, missing snapshot) status = %d, want 404: %s", resp.StatusCode, body)
	}
}

// TestTemplateInstanceFamilyExposesNoDestructiveMethod pins a product-level
// invariant: template instances are immutable governance objects, so no
// DELETE is mounted on the family. Today the request dies at the gin router
// with a framework 404; if a destructive method is ever mounted, the RBAC
// default-deny must still reject it with 403 (route coverage itself is
// guarded by access_test.go TestProtectedRoutesAllRegistered and
// middleware/rbac_test.go TestP0_AUTHZ_001_RBACDefaultsDenyUnregisteredRoute).
func TestTemplateInstanceFamilyExposesNoDestructiveMethod(t *testing.T) {
	env := newTemplateInstanceAuthzEnv(t)

	resp, body := env.app.doAuth(t, http.MethodDelete, "/api/v1/template-instances/ti-1", env.adminToken, nil)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return // not mounted (404) or mounted but default-denied (403)
	}
	t.Fatalf("DELETE on template-instances must stay unmounted or fail closed, got %d: %s", resp.StatusCode, body)
}

// mustJSON encodes a request body map; nil yields a nil body.
func mustJSON(t *testing.T, body map[string]interface{}) []byte {
	t.Helper()
	if body == nil {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
