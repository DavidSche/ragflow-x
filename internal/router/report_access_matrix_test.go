package router

// A19 (complete) — the six-role × report-route access matrix (doc/288 §7.6 / §14.3,
// K57 "六角色 × 16 路由"). The handler-level sibling
// (internal/handler/report_role_matrix_test.go) proves the object gate answers 404
// for a permitted caller; this file proves the *role* gate in front of it, on the
// real rule table the API actually installs.
//
// Two properties are pinned here:
//   - coverage: every report route has exactly one access rule, and the rule names
//     the action the surface intends. A route added without a rule is a deny-by-
//     default 403 (silently broken); a rule added here without a route breaks
//     TestAccessRulesMatchRegisteredRoutes elsewhere. Both fail loudly.
//   - granting: the builtin permission matrix grants exactly the roles written in
//     `reportMatrixGrants`. That table is spelled out rather than derived, because
//     a derived expectation would rubber-stamp a widened grant — and "viewer can
//     read reports" is precisely the kind of change this test exists to stop.

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// reportMatrixRouteSpec is one row of the report surface's contract: the address and
// the action that decides who may call it.
type reportMatrixRouteSpec struct {
	method string
	path   string
	action string
}

// reportMatrixRouteSpecs is the authoritative route→action table (§11.4 + T13
// retry + T20b retention). Adding a report endpoint means adding a row here.
func reportMatrixRouteSpecs() []reportMatrixRouteSpec {
	return []reportMatrixRouteSpec{
		{"POST", "/api/v1/reports", "execute"},
		{"GET", "/api/v1/reports", "read"},
		{"GET", "/api/v1/reports/:id", "read"},
		{"DELETE", "/api/v1/reports/:id", "delete:own"},
		{"POST", "/api/v1/reports/:id/restore", "delete:own"},
		{"POST", "/api/v1/reports/:id/cancel", "execute"},
		{"POST", "/api/v1/reports/:id/revisions", "execute"},
		{"GET", "/api/v1/reports/:id/revisions", "read"},
		{"GET", "/api/v1/reports/:id/revisions/:revision", "read"},
		{"GET", "/api/v1/reports/:id/revisions/:revision/sections", "read"},
		{"POST", "/api/v1/reports/:id/revisions/:revision/outline/confirm", "execute"},
		{"POST", "/api/v1/reports/:id/revisions/:revision/artifacts", "execute"},
		{"GET", "/api/v1/reports/:id/revisions/:revision/artifacts/:artifactId/download", "read"},
		{"GET", "/api/v1/report-templates", "read"},
		{"POST", "/api/v1/report-templates/:key/capability-check", "read"},
		{"POST", "/api/v1/report-templates/:key/clone", "manage"},
		{"GET", "/api/v1/report-jobs/:jobId", "read"},
		{"GET", "/api/v1/report-jobs/:jobId/events", "read"},
		{"POST", "/api/v1/report-jobs/:jobId/sections/:ord/retry", "execute"},
		{"GET", "/api/v1/report-retention", "read"},
		{"PUT", "/api/v1/report-retention", "manage"},
		{"GET", "/api/v1/report-metrics/trend", "read"},
	}
}

// reportMatrixGrants is the 6 roles × 4 actions table the report surface requires.
// `viewer` holds nothing on the report surface at all: a report contains financial
// and compliance evidence assembled from datasets the viewer may not even read
// (§13 "P0-A 只写 owner_only"), so even the read-only role gets no foothold.
func reportMatrixGrants() map[string]map[string]bool {
	return map[string]map[string]bool{
		model.RolePlatformAdmin: {"read": true, "execute": true, "manage": true, "delete:own": true},
		model.RoleTenantAdmin:   {"read": true, "execute": true, "manage": true, "delete:own": true},
		model.RoleOperator:      {"read": true, "execute": true, "delete:own": true},
		model.RoleTeamAdmin:     {"read": true, "execute": true, "delete:own": true},
		model.RoleBusinessUser:  {"read": true, "execute": true, "delete:own": true},
		model.RoleViewer:        {},
	}
}

func reportMatrixRoles() []string {
	return []string{
		model.RolePlatformAdmin,
		model.RoleTenantAdmin,
		model.RoleOperator,
		model.RoleTeamAdmin,
		model.RoleBusinessUser,
		model.RoleViewer,
	}
}

// TestReportAccessRulesCoverEveryRouteWithOneRule asserts the rule table and the
// route contract are the same set — one rule per route, no orphan rules.
func TestReportAccessRulesCoverEveryRouteWithOneRule(t *testing.T) {
	rules := map[string]string{}
	for _, rule := range accessRules(nil) {
		if !reportPath(rule.Path) {
			continue
		}
		key := rule.Method + " " + rule.Path
		if _, seen := rules[key]; seen {
			t.Errorf("report route %s has more than one access rule", key)
		}
		rules[key] = rule.Action
	}
	specs := reportMatrixRouteSpecs()
	if len(rules) != len(specs) {
		t.Fatalf("access rules cover %d report routes, the contract table has %d", len(rules), len(specs))
	}
	for _, spec := range specs {
		key := spec.method + " " + spec.path
		action, ok := rules[key]
		if !ok {
			t.Errorf("report route %s has no access rule (deny-by-default 403)", key)
			continue
		}
		if action != spec.action {
			t.Errorf("report route %s is gated by %q, contract says %q", key, action, spec.action)
		}
	}
}

// TestReportAccessMatrixMatchesBuiltinGrants walks the 6 × 18 matrix: for every role
// and every route, the builtin matrix either grants the route's action or does not,
// and the answer must equal `reportMatrixGrants`.
func TestReportAccessMatrixMatchesBuiltinGrants(t *testing.T) {
	granted := map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "report" && grant.Resource != "report-template" {
			continue
		}
		if granted[grant.Role] == nil {
			granted[grant.Role] = map[string]bool{}
		}
		granted[grant.Role][grant.Action] = true
	}
	expected := reportMatrixGrants()
	for _, role := range reportMatrixRoles() {
		for action := range expected[role] {
			if !granted[role][action] {
				t.Errorf("%s is expected to hold %s on the report surface but the builtin matrix does not grant it",
					role, action)
			}
		}
		for action := range granted[role] {
			if !expected[role][action] {
				t.Errorf("%s holds unexpected %s on the report surface", role, action)
			}
		}
	}
	// The matrix must also be *usable*: every action named by a route is granted to
	// at least one role, otherwise the route is dead surface nobody can call.
	for _, spec := range reportMatrixRouteSpecs() {
		callable := false
		for _, role := range reportMatrixRoles() {
			if granted[role][spec.action] {
				callable = true
				break
			}
		}
		if !callable {
			t.Errorf("report route %s %s requires %q, which no role holds",
				spec.method, spec.path, spec.action)
		}
	}
}

// reportPath reports whether a path belongs to the report surface. It is prefix-based
// on purpose: a new sub-resource under /reports or /report-* must show up in the
// matrix, not slip past it.
func reportPath(path string) bool {
	for _, prefix := range []string{
		"/api/v1/reports", "/api/v1/report-templates",
		"/api/v1/report-jobs", "/api/v1/report-retention",
		"/api/v1/report-metrics",
	} {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
