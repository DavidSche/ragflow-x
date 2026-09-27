package router

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
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
