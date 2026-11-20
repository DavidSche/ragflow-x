package router

// GW-3 route contract (doc/301 §5.10 / T21).
//
// The billing reconciliation reads a tenant's own ledgers (`read` / `usage`) but the
// on-demand run walks *every* tenant's ledgers, so it is `manage` / `system`. The split
// is pinned here because the tempting simplification — "it is a usage feature, put it
// under usage with manage" — would be a new permission row the plan forbids (301 D10)
// and would let a tenant admin reconcile another tenant's day.

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

type gatewayBillingRouteSpec struct {
	method   string
	path     string
	action   string
	resource string
}

func gatewayBillingRouteSpecs() []gatewayBillingRouteSpec {
	return []gatewayBillingRouteSpec{
		{"GET", "/api/v1/usage/reconcile", "read", "usage"},
		{"GET", "/api/v1/usage/reconcile/:runId/findings", "read", "usage"},
		{"POST", "/api/v1/system/usage/reconcile", "manage", "system"},
	}
}

func TestGatewayBillingRoutesHaveExactlyOneRule(t *testing.T) {
	counts := map[string]int{}
	actions := map[string]string{}
	resources := map[string]string{}
	for _, rule := range accessRules(nil) {
		key := rule.Method + " " + rule.Path
		counts[key]++
		actions[key] = rule.Action
		resources[key] = rule.Resource
	}
	for _, spec := range gatewayBillingRouteSpecs() {
		key := spec.method + " " + spec.path
		if counts[key] != 1 {
			t.Errorf("%s has %d access rules, want exactly 1", key, counts[key])
		}
		if actions[key] != spec.action {
			t.Errorf("%s action = %q, want %q", key, actions[key], spec.action)
		}
		if resources[key] != spec.resource {
			t.Errorf("%s resource = %q, want %q", key, resources[key], spec.resource)
		}
	}
	// A fourth reconciliation route added without a decision about its action fails
	// here. The set is counted by *distinct path*, not by call site: a path that
	// somehow gained two rules is already caught above.
	distinct := map[string]struct{}{}
	for _, spec := range gatewayBillingRouteSpecs() {
		distinct[spec.path] = struct{}{}
	}
	if len(distinct) != 3 {
		t.Fatalf("distinct reconciliation paths = %d, want 3", len(distinct))
	}
	matched := 0
	for _, rule := range accessRules(nil) {
		if _, ok := distinct[rule.Path]; ok {
			matched++
		}
	}
	if matched != 3 {
		t.Fatalf("reconciliation access rules = %d, want 3", matched)
	}
}

func TestGatewayBillingGrantsMatchTheRoleLattice(t *testing.T) {
	grants := map[string]map[string]map[string]bool{}
	for _, grant := range db.BuiltinPermissionMatrix() {
		if grant.Resource != "usage" && grant.Resource != "system" {
			continue
		}
		if grants[grant.Resource] == nil {
			grants[grant.Resource] = map[string]map[string]bool{}
		}
		if grants[grant.Resource][grant.Role] == nil {
			grants[grant.Resource][grant.Role] = map[string]bool{}
		}
		grants[grant.Resource][grant.Role][grant.Action] = true
	}

	// `usage` admits read only in the route registry; this test is what would fail
	// first if someone added a `manage` row to make the trigger fit under `usage`.
	if grants["usage"][model.RoleTenantAdmin]["manage"] {
		t.Error("usage must stay read-only: the reconciliation trigger belongs to system")
	}
	for _, role := range []string{model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator} {
		if !grants["usage"][role]["read"] {
			t.Errorf("%s must hold read on usage, got %v", role, grants["usage"][role])
		}
	}
	if grants["usage"][model.RoleViewer]["read"] {
		// Reading usage is business-user-denied on purpose (db/permission_intent.go);
		// if that changes, this assertion is the place to change it deliberately.
		t.Error("viewer must not read usage")
	}
	if !grants["system"][model.RolePlatformAdmin]["manage"] {
		t.Error("platform admin must hold manage on system to run the reconciliation")
	}
	if grants["system"][model.RoleTenantAdmin]["manage"] {
		// A tenant admin reconciling is not wrong per se, but the endpoint walks every
		// tenant: if the lattice ever grants it, the endpoint must be split first.
		t.Error("tenant admin must not hold manage on system")
	}
}
