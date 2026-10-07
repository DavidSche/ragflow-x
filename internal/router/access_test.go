package router

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// protectedRoutes mirrors the routes registered inside the authenticated group
// in router.go. The RBAC middleware denies any unregistered protected route, so
// this list must stay in sync: every entry here must exist in accessRules and
// every accessRule must reference an actual protected route.
func protectedRoutes() []string {
	return []string{
		"GET /api/v1/auth/me",
		"GET /api/v1/branding",
		"PUT /api/v1/branding",
		"POST /api/v1/branding/logo",
		"GET /api/v1/tenants",
		"GET /api/v1/tenants/export",
		"POST /api/v1/tenants",
		"PUT /api/v1/tenants/status",
		"PUT /api/v1/tenants/:id",
		"DELETE /api/v1/tenants/:id",
		"GET /api/v1/teams",
		"GET /api/v1/teams/:id",
		"POST /api/v1/teams",
		"PUT /api/v1/teams/:id",
		"DELETE /api/v1/teams/:id",
		"GET /api/v1/teams/:id/users",
		"POST /api/v1/teams/:id/users",
		"DELETE /api/v1/teams/:id/users/:userId",
		"GET /api/v1/projects",
		"GET /api/v1/teams/:id/projects",
		"PUT /api/v1/teams/:id/projects",
		"POST /api/v1/projects",
		"DELETE /api/v1/projects/:id",
		"GET /api/v1/projects/:id/users",
		"POST /api/v1/projects/:id/users",
		"DELETE /api/v1/projects/:id/users/:userId",
		"GET /api/v1/datasets",
		"GET /api/v1/datasets/:id",
		"DELETE /api/v1/datasets/:id",
		"PUT /api/v1/datasets/:id",
		"GET /api/v1/datasets/export",
		"DELETE /api/v1/datasets",
		"PUT /api/v1/datasets/:id/project",
		"GET /api/v1/datasets/:id/config",
		"PUT /api/v1/datasets/:id/config",
		"POST /api/v1/datasets",
		"GET /api/v1/datasets/:id/documents",
		"POST /api/v1/datasets/:id/documents",
		"POST /api/v1/datasets/:id/parse",
		"POST /api/v1/datasets/:id/documents/stop",
		"DELETE /api/v1/datasets/:id/documents",
		"POST /api/v1/datasets/:id/documents/status",
		"PUT /api/v1/datasets/:id/documents/:docId/metadata",
		"GET /api/v1/datasets/:id/documents/:docId/chunks",
		"GET /api/v1/datasets/:id/documents/:docId/preview",
		"DELETE /api/v1/datasets/:id/documents/:docId/chunks",
		"PATCH /api/v1/datasets/:id/documents/:docId/chunks",
		"GET /api/v1/dashboard",
		"GET /api/v1/tasks",
		"DELETE /api/v1/tasks",
		"POST /api/v1/tasks/sync",
		"GET /api/v1/usage",
		"GET /api/v1/usage/details",
		"GET /api/v1/usage/attribution",
		"GET /api/v1/usage/export",
		"GET /api/v1/audit",
		"GET /api/v1/audit/export",
		"GET /api/v1/audit/verify",
		"GET /api/v1/system/health",
		"GET /api/v1/users",
		"POST /api/v1/users",
		"PUT /api/v1/users/:id/status",
		"PUT /api/v1/users/:id",
		"PUT /api/v1/users/:id/role",
		"DELETE /api/v1/users/:id",
		"GET /api/v1/roles",
		"GET /api/v1/roles/permission-catalog",
		"POST /api/v1/roles",
		"PUT /api/v1/roles/:id",
		"DELETE /api/v1/roles/:id",
		"GET /api/v1/roles/:id/permissions",
		"PUT /api/v1/roles/:id/permissions",
		"GET /api/v1/chats",
		"POST /api/v1/chat/completions",
		"GET /api/v1/chat/reference",
		"POST /api/v1/chat/feedback",
		"POST /api/v1/knowledge-ops/trace-runs",
		"GET /api/v1/knowledge-ops/trace-runs",
		"GET /api/v1/knowledge-ops/trace-runs/:id",
		"GET /api/v1/knowledge-tasks",
		"POST /api/v1/knowledge-tasks",
		"GET /api/v1/knowledge-tasks/summary",
		"GET /api/v1/knowledge-tasks/:id",
		"PATCH /api/v1/knowledge-tasks/:id",
		"GET /api/v1/answer-snapshots/:snapshotId",
		"GET /api/v1/answer-snapshots/by-request/:requestId",
		"POST /api/v1/answer-snapshots/:snapshotId/export",
		"GET /api/v1/answer-snapshots/:snapshotId/audit-export",
		"GET /api/v1/answer-snapshots/export-jobs/:jobId",
		"POST /api/v1/answer-snapshots/export-jobs/:jobId/retry",
		"GET /api/v1/answer-snapshots/artifacts/:artifactId/download",
		"POST /api/v1/knowledge-impact-reports",
		"GET /api/v1/knowledge-impact-reports",
		"POST /api/v1/knowledge-duplicate-candidates",
		"GET /api/v1/knowledge-duplicate-candidates",
		"POST /api/v1/knowledge-duplicate-candidates/:id/decision",
		"POST /api/v1/chats",
		"POST /api/v1/chats/batch-status",
		"DELETE /api/v1/chats",
		"GET /api/v1/chats/:id",
		"GET /api/v1/chats/:id/usage",
		"PUT /api/v1/chats/:id",
		"DELETE /api/v1/chats/:id",
		"GET /api/v1/chats/:id/sessions",
		"POST /api/v1/chats/:id/sessions",
		"GET /api/v1/chats/:id/sessions/:sessionId",
		"PATCH /api/v1/chats/:id/sessions/:sessionId",
		"DELETE /api/v1/chats/:id/sessions/:sessionId",
		"DELETE /api/v1/chats/:id/sessions",
		"GET /api/v1/model-providers",
		"POST /api/v1/model-providers",
		"DELETE /api/v1/model-providers/:id",
		"GET /api/v1/model-providers/:id",
		"GET /api/v1/model-providers/catalog",
		"GET /api/v1/model-providers/:id/instances",
		"POST /api/v1/model-providers/:id/instances",
		"PUT /api/v1/model-providers/:id/instances/:instanceId",
		"DELETE /api/v1/model-providers/:id/instances",
		"POST /api/v1/model-providers/:id/verify",
		"GET /api/v1/model-providers/:id/instances/:instanceId/models",
		"POST /api/v1/model-providers/:id/instances/:instanceId/models",
		"PATCH /api/v1/model-providers/:id/instances/:instanceId/models/:modelId",
		"DELETE /api/v1/model-providers/:id/instances/:instanceId/models",
		"POST /api/v1/model-providers/:id/instances/:instanceId/models/:modelId/test",
		"GET /api/v1/model-routes",
		"POST /api/v1/model-routes",
		"PUT /api/v1/model-routes/:id",
		"DELETE /api/v1/model-routes/:id",
		"GET /api/v1/keys",
		"POST /api/v1/keys",
		"POST /api/v1/keys/:id/revoke",
		// Template instance API family (doc/107 §3.1.5, doc/124 §2).
		"POST /api/v1/template-instances",
		"GET /api/v1/template-instances",
		"GET /api/v1/template-instances/:id",
		"POST /api/v1/template-instances/:id/dry-run",
		"POST /api/v1/template-instances/:id/evaluate",
		"POST /api/v1/template-instances/:id/release",
		"POST /api/v1/template-instances/:id/releases/:releaseId/activate",
		"POST /api/v1/template-instances/:id/rollback",
		"GET /api/v1/template-instances/:id/health",
		"GET /api/v1/template-instances/:id/releases",
		"POST /api/v1/template-instances/:id/releases/:releaseId/reconcile",
		// Rollout policy management (doc/124 §3).
		"GET /api/v1/assistants/:id/rollout-policies",
		"POST /api/v1/assistants/:id/rollout-policies",
		"PUT /api/v1/assistants/:id/rollout-policies/:policyId",
		// Parser policy and quality profile routes (doc/129 P0-A Slice 1).
		"GET /api/v1/parser-policies",
		"POST /api/v1/parser-policies",
		"GET /api/v1/parser-policies/:id",
		"PUT /api/v1/parser-policies/:id",
		"PATCH /api/v1/parser-policies/:id",
		"DELETE /api/v1/parser-policies/:id",
		"GET /api/v1/quality-profiles",
		"POST /api/v1/quality-profiles",
		"GET /api/v1/quality-profiles/:id",
		"PUT /api/v1/quality-profiles/:id",
		"PATCH /api/v1/quality-profiles/:id",
		"DELETE /api/v1/quality-profiles/:id",
		// Logical document lifecycle routes (doc/129 P0-B Slice 2).
		"GET /api/v1/logical-documents",
		"POST /api/v1/logical-documents",
		"GET /api/v1/logical-documents/:id",
		"PUT /api/v1/logical-documents/:id",
		"PATCH /api/v1/logical-documents/:id",
		"DELETE /api/v1/logical-documents/:id",
		"GET /api/v1/logical-documents/:id/versions",
		"POST /api/v1/logical-documents/:id/supersede",
		"POST /api/v1/logical-documents/:id/restore",
		"GET /api/v1/logical-documents/:id/publish-attempts",
		// Deterministic tool routing routes (doc/129 P0-C Slice 4).
		"GET /api/v1/tool-registry",
		"POST /api/v1/tool-registry",
		"GET /api/v1/tool-registry/:id",
		"PUT /api/v1/tool-registry/:id",
		"PATCH /api/v1/tool-registry/:id",
		"DELETE /api/v1/tool-registry/:id",
		"POST /api/v1/tool-registry/:id/execute",
		"POST /api/v1/tool-routing/routed-sql-answer",
		"POST /api/v1/tool-routing/routed-sql-answers",
		"POST /api/v1/tool-routing/routed-mixed-tool-answers",
		"POST /api/v1/tool-routing/planned-mixed-tool-answers",
		"POST /api/v1/tool-routing/answer-runs",
		"POST /api/v1/tool-routing/answer-runs/:answerRunId/evidence-facts",
		"GET /api/v1/tool-routing/answer-runs/:answerRunId/evidence-facts",
		"GET /api/v1/tool-routing/answer-runs/:answerRunId/fact-guard",
		"GET /api/v1/source-routing-rules",
		"POST /api/v1/source-routing-rules",
		"GET /api/v1/source-routing-rules/:id",
		"PUT /api/v1/source-routing-rules/:id",
		"PATCH /api/v1/source-routing-rules/:id",
		"DELETE /api/v1/source-routing-rules/:id",
		// SQL query template governance routes (doc/129 P1-B Slice 3).
		"GET /api/v1/query-templates",
		"POST /api/v1/query-templates",
		"GET /api/v1/query-templates/:id",
		"PUT /api/v1/query-templates/:id",
		"PATCH /api/v1/query-templates/:id",
		"DELETE /api/v1/query-templates/:id",
		"GET /api/v1/db-connections",
		"POST /api/v1/db-connections",
		"GET /api/v1/db-connections/:id",
		"PUT /api/v1/db-connections/:id",
		"PATCH /api/v1/db-connections/:id",
		"DELETE /api/v1/db-connections/:id",
		"POST /api/v1/db-connections/:id/test",
		"GET /api/v1/knowledge-strategies",
		"POST /api/v1/knowledge-strategies",
		"GET /api/v1/knowledge-strategies/:id",
		"PUT /api/v1/knowledge-strategies/:id",
		"PATCH /api/v1/knowledge-strategies/:id",
		"DELETE /api/v1/knowledge-strategies/:id",
		"POST /api/v1/knowledge-strategies/:id/probe",
		"POST /api/v1/knowledge-strategies/:id/retrieve",
		"GET /api/v1/datasets/:id/documents/:docId/parse-attempts",
		"GET /api/v1/datasets/:id/documents/:docId/parse-quality-report",
		"GET /api/v1/datasets/:id/documents/:docId/parse-quality-reports",
		"GET /api/v1/datasets/:id/parse-quality-reports",
	}
}

func TestProtectedRoutesAllRegistered(t *testing.T) {
	// accessRules requires a *service.Service only for the dataset scope
	// resolver factory, which it never invokes at construction time, so nil is
	// safe here.
	rules := accessRules(nil)
	byKey := map[string]bool{}
	for _, r := range rules {
		byKey[r.Method+" "+r.Path] = true
	}
	missing := []string{}
	for _, p := range protectedRoutes() {
		if !byKey[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("protected routes missing from accessRules (would be denied by default): %v", missing)
	}
}

// TestAnswerDeliveryRoutesCoveredByAccessRules pins the 11 answer delivery
// contract routes (doc/118 F-03): if any of them is removed from accessRules
// the RBAC middleware would silently 403 PDF/DOCX exports again.
func TestAnswerDeliveryRoutesCoveredByAccessRules(t *testing.T) {
	answerDeliveryRoutes := []string{
		"GET /api/v1/answer-snapshots/:snapshotId",
		"GET /api/v1/answer-snapshots/by-request/:requestId",
		"POST /api/v1/answer-snapshots/:snapshotId/export",
		"GET /api/v1/answer-snapshots/:snapshotId/audit-export",
		"GET /api/v1/answer-snapshots/export-jobs/:jobId",
		"POST /api/v1/answer-snapshots/export-jobs/:jobId/retry",
		"GET /api/v1/answer-snapshots/artifacts/:artifactId/download",
		"POST /api/v1/knowledge-impact-reports",
		"GET /api/v1/knowledge-impact-reports",
		"POST /api/v1/knowledge-duplicate-candidates",
		"GET /api/v1/knowledge-duplicate-candidates",
		"POST /api/v1/knowledge-duplicate-candidates/:id/decision",
	}
	rules := map[string]bool{}
	for _, r := range accessRules(nil) {
		rules[r.Method+" "+r.Path] = true
	}
	for _, route := range answerDeliveryRoutes {
		if !rules[route] {
			t.Errorf("answer delivery route %q is not covered by accessRules (default-deny would 403 it)", route)
		}
	}
}

func TestAccessRulesReferenceKnownResources(t *testing.T) {
	resources := map[string]bool{}
	for _, g := range db.BuiltinPermissionMatrix() {
		resources[g.Resource] = true
	}
	for _, r := range accessRules(nil) {
		if !resources[r.Resource] {
			t.Errorf("accessRule %s %s references unknown resource %q with no builtin grant", r.Method, r.Path, r.Resource)
		}
	}
}

func TestLogicalDocumentPermissionsMatchMinimumContract(t *testing.T) {
	registry := ResourceTypeRegistry()["logical-document"]
	if registry.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("logical-document scope = %q, want workspace", registry.ResourceScope)
	}
	if len(registry.AllowedActions) != 3 || registry.AllowedActions[0] != "read" || registry.AllowedActions[1] != "manage" || registry.AllowedActions[2] != "execute" {
		t.Fatalf("logical-document allowed actions = %v, want [read manage execute]", registry.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "logical-document" {
			continue
		}
		grants[grant.Role] = map[string]bool{}
	}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource == "logical-document" {
			grants[grant.Role][grant.Action] = true
		}
	}
	expected := map[string][]string{
		model.RolePlatformAdmin: {"read", "manage", "execute"},
		model.RoleTenantAdmin:   {"read", "manage", "execute"},
		model.RoleOperator:      {"read"},
		model.RoleBusinessUser:  {"read"},
		model.RoleViewer:        {"read"},
		model.RoleTeamAdmin:     {"read", "manage", "execute"},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("role %s grants = %v, want %v", role, grants[role], actions)
		}
		for _, action := range actions {
			if !grants[role][action] {
				t.Fatalf("role %s is missing %s", role, action)
			}
		}
	}
	if len(grants[model.RoleBusinessUser]) != 1 || !grants[model.RoleBusinessUser]["read"] {
		t.Fatalf("business_user must only receive logical-document read, got %v", grants[model.RoleBusinessUser])
	}
}

func TestP0CRoutingPermissionsMatchMinimumContract(t *testing.T) {
	registry := ResourceTypeRegistry()
	expected := map[string]map[string][]string{
		"tool-registry": {
			model.RolePlatformAdmin: {"read", "manage", "execute"},
			model.RoleTenantAdmin:   {"read", "manage", "execute"},
		},
		"source-routing-rule": {
			model.RolePlatformAdmin: {"read", "manage"},
			model.RoleTenantAdmin:   {"read", "manage"},
			model.RoleOperator:      {"read"},
			model.RoleViewer:        {"read"},
		},
	}
	for resource, roles := range expected {
		definition := registry[resource]
		if definition.ResourceScope != ResourceScopeWorkspace {
			t.Fatalf("%s scope = %q, want workspace", resource, definition.ResourceScope)
		}
		if resource == "tool-registry" {
			if len(definition.AllowedActions) != 3 || definition.AllowedActions[0] != "read" || definition.AllowedActions[1] != "manage" || definition.AllowedActions[2] != "execute" {
				t.Fatalf("%s allowed actions = %v, want [read manage execute]", resource, definition.AllowedActions)
			}
		} else if len(definition.AllowedActions) != 2 || definition.AllowedActions[0] != "read" || definition.AllowedActions[1] != "manage" {
			t.Fatalf("%s allowed actions = %v, want [read manage]", resource, definition.AllowedActions)
		}
		grants := map[string]map[string]bool{}
		for _, grant := range db.BuiltinPermissionMatrix() {
			if grant.Resource == resource {
				if grants[grant.Role] == nil {
					grants[grant.Role] = map[string]bool{}
				}
				grants[grant.Role][grant.Action] = true
			}
		}
		for role, actions := range roles {
			if len(grants[role]) != len(actions) {
				t.Fatalf("%s role %s grants = %v, want %v", resource, role, grants[role], actions)
			}
			for _, action := range actions {
				if !grants[role][action] {
					t.Fatalf("%s role %s is missing %s", resource, role, action)
				}
			}
		}
	}
}

func TestDBConnectionPermissionsMatchMinimumContract(t *testing.T) {
	definition := ResourceTypeRegistry()["db-connection"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("db-connection scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 3 || definition.AllowedActions[0] != "read" || definition.AllowedActions[1] != "manage" || definition.AllowedActions[2] != "test" {
		t.Fatalf("db-connection allowed actions = %v, want [read manage test]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "db-connection" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "manage": true, "test": true},
		model.RoleTenantAdmin:   {"read": true, "manage": true, "test": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s db-connection grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing db-connection %s", role, action)
			}
		}
	}
	for _, role := range []string{model.RoleOperator, model.RoleBusinessUser, model.RoleViewer, model.RoleTeamAdmin} {
		if len(grants[role]) != 0 {
			t.Fatalf("%s must not receive db-connection permissions, got %v", role, grants[role])
		}
	}
}

func TestQueryTemplatePermissionsMatchMinimumContract(t *testing.T) {
	definition := ResourceTypeRegistry()["query-template"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("query-template scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 2 || definition.AllowedActions[0] != "read" || definition.AllowedActions[1] != "manage" {
		t.Fatalf("query-template allowed actions = %v, want [read manage]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "query-template" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "manage": true},
		model.RoleTenantAdmin:   {"read": true, "manage": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s query-template grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing query-template %s", role, action)
			}
		}
	}
	for _, role := range []string{model.RoleOperator, model.RoleBusinessUser, model.RoleViewer, model.RoleTeamAdmin} {
		if len(grants[role]) != 0 {
			t.Fatalf("%s must not receive query-template permissions, got %v", role, grants[role])
		}
	}
}

func TestKnowledgeStrategyPermissionsMatchMinimumContract(t *testing.T) {
	definition := ResourceTypeRegistry()["knowledge-strategy"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("knowledge-strategy scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 3 || definition.AllowedActions[0] != "read" ||
		definition.AllowedActions[1] != "manage" || definition.AllowedActions[2] != "execute" {
		t.Fatalf("knowledge-strategy allowed actions = %v, want [read manage execute]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "knowledge-strategy" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "manage": true, "execute": true},
		model.RoleTenantAdmin:   {"read": true, "manage": true, "execute": true},
		model.RoleTeamAdmin:     {"read": true, "manage": true, "execute": true},
		model.RoleOperator:      {"read": true, "execute": true},
		model.RoleViewer:        {"read": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s knowledge-strategy grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing knowledge-strategy %s", role, action)
			}
		}
	}
	for _, role := range []string{model.RoleBusinessUser} {
		if len(grants[role]) != 0 {
			t.Fatalf("%s must not receive knowledge-strategy permissions in this slice, got %v", role, grants[role])
		}
	}
}

func TestEvidenceSnapshotPermissionsMatchReadOnlyContract(t *testing.T) {
	definition := ResourceTypeRegistry()["evidence-snapshot"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("evidence-snapshot scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 1 || definition.AllowedActions[0] != "read" {
		t.Fatalf("evidence-snapshot allowed actions = %v, want [read]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "evidence-snapshot" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true},
		model.RoleTenantAdmin:   {"read": true},
		model.RoleOperator:      {"read": true},
		model.RoleViewer:        {"read": true},
		model.RoleTeamAdmin:     {"read": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s evidence-snapshot grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing evidence-snapshot %s", role, action)
			}
		}
	}
	for _, role := range []string{model.RoleBusinessUser} {
		if len(grants[role]) != 0 {
			t.Fatalf("%s must not receive evidence-snapshot permissions, got %v", role, grants[role])
		}
	}
}

func TestOutboxEventPermissionsMatchOperationalContract(t *testing.T) {
	definition := ResourceTypeRegistry()["outbox-event"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("outbox-event scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 2 || definition.AllowedActions[0] != "read" ||
		definition.AllowedActions[1] != "execute" {
		t.Fatalf("outbox-event allowed actions = %v, want [read execute]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "outbox-event" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "execute": true},
		model.RoleTenantAdmin:   {"read": true, "execute": true},
		model.RoleOperator:      {"read": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s outbox-event grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing outbox-event %s", role, action)
			}
		}
	}
	for _, role := range []string{model.RoleBusinessUser, model.RoleViewer, model.RoleTeamAdmin} {
		if len(grants[role]) != 0 {
			t.Fatalf("%s must not receive outbox-event permissions, got %v", role, grants[role])
		}
	}
}

func TestEvalSetExecutePermissionsMatchRevalidationContract(t *testing.T) {
	definition := ResourceTypeRegistry()["eval-set"]
	if definition.ResourceScope != ResourceScopeWorkspace {
		t.Fatalf("eval-set scope = %q, want workspace", definition.ResourceScope)
	}
	if len(definition.AllowedActions) != 3 || definition.AllowedActions[0] != "read" ||
		definition.AllowedActions[1] != "manage" || definition.AllowedActions[2] != "execute" {
		t.Fatalf("eval-set allowed actions = %v, want [read manage execute]", definition.AllowedActions)
	}
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "eval-set" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	expected := map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "manage": true, "execute": true},
		model.RoleTenantAdmin:   {"read": true, "manage": true, "execute": true},
		model.RoleOperator:      {"read": true, "manage": true, "execute": true},
		model.RoleTeamAdmin:     {"read": true, "manage": true},
		model.RoleViewer:        {"read": true},
	}
	for role, actions := range expected {
		if len(grants[role]) != len(actions) {
			t.Fatalf("%s eval-set grants = %v, want %v", role, grants[role], actions)
		}
		for action := range actions {
			if !grants[role][action] {
				t.Fatalf("%s is missing eval-set %s", role, action)
			}
		}
	}
	if len(grants[model.RoleBusinessUser]) != 0 {
		t.Fatalf("business_user must not receive eval-set permissions, got %v", grants[model.RoleBusinessUser])
	}
}
