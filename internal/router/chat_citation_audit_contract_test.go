package router

// QA-2 route contract (doc/301 §5.10 / T19).
//
// The sample is a *mutation of tenant audit state* — it spends the tenant's review
// allowance and writes a disposition onto the badcase queue — so triggering it is
// `manage`, while reading the runs and their findings is `read`. Pinning the split
// here is what stops a later change from quietly letting every viewer re-run the
// sample and spend tokens.

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

type chatCitationAuditRouteSpec struct {
	method string
	path   string
	action string
}

func chatCitationAuditRouteSpecs() []chatCitationAuditRouteSpec {
	return []chatCitationAuditRouteSpec{
		{"POST", "/api/v1/knowledge-ops/citation-audit", "manage"},
		{"GET", "/api/v1/knowledge-ops/citation-audit/runs", "read"},
		{"GET", "/api/v1/knowledge-ops/citation-audit/runs/:runId/findings", "read"},
	}
}

func TestChatCitationAuditRoutesHaveExactlyOneRule(t *testing.T) {
	counts := map[string]int{}
	actions := map[string]string{}
	for _, rule := range accessRules(nil) {
		key := rule.Method + " " + rule.Path
		counts[key]++
		actions[key] = rule.Action
	}
	for _, spec := range chatCitationAuditRouteSpecs() {
		key := spec.method + " " + spec.path
		if counts[key] != 1 {
			t.Errorf("%s has %d access rules, want exactly 1", key, counts[key])
		}
		if actions[key] != spec.action {
			t.Errorf("%s action = %q, want %q", key, actions[key], spec.action)
		}
	}
	// The closed set is pinned by count: a fourth citation-audit route added without
	// a rule here (and therefore without a decision about its action) fails.
	citationRules := 0
	for _, rule := range accessRules(nil) {
		if len(rule.Path) >= len("/api/v1/knowledge-ops/citation-audit") &&
			rule.Path[:len("/api/v1/knowledge-ops/citation-audit")] == "/api/v1/knowledge-ops/citation-audit" {
			citationRules++
		}
	}
	if citationRules != 3 {
		t.Fatalf("citation-audit rules = %d, want 3", citationRules)
	}
}

func TestChatCitationAuditGrantsMatchTheRoleLattice(t *testing.T) {
	grants := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "knowledge-ops" {
			continue
		}
		if grants[grant.Role] == nil {
			grants[grant.Role] = map[string]bool{}
		}
		grants[grant.Role][grant.Action] = true
	}
	allowed := []string{model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator}
	for _, role := range allowed {
		if !grants[role]["manage"] || !grants[role]["read"] {
			t.Errorf("%s must hold read+manage on knowledge-ops, got %v", role, grants[role])
		}
	}
	if grants[model.RoleViewer]["manage"] {
		t.Error("viewer must not be able to trigger the audit")
	}
	if grants[model.RoleViewer]["read"] {
		// Reading the board is intentionally business-user-denied (db/permission_intent.go);
		// if that ever changes this test is the place that must be updated deliberately.
		t.Error("viewer must not read the knowledge-ops board")
	}
}
