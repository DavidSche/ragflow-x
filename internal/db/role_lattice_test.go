package db

import (
	"sort"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func actionsOf(index map[string]map[string]map[string]struct{}, resource, role string) map[string]struct{} {
	if index[resource] == nil {
		return nil
	}
	return index[resource][role]
}

// TestRoleLatticeIsFullyDeclared requires every ordered pair of built-in roles
// to be *decided*: either a superset relation or a declared incomparability.
// A new built-in role cannot ship without answering "what is it relative to the
// others", which is what prevents the "grafted role" class of defect.
func TestRoleLatticeIsFullyDeclared(t *testing.T) {
	direct := roleSupersetSet()
	closure := roleSupersetClosure()
	incomparables := roleIncomparableSet()

	for _, a := range BuiltinRoles {
		for _, b := range BuiltinRoles {
			if a == b {
				continue
			}
			_, directSuperset := direct[[2]string{a, b}]
			_, closureSuperset := closure[[2]string{a, b}]
			isSuperset := closureSuperset || directSuperset
			_, isIncomparable := incomparables[[2]string{a, b}]
			switch {
			case isSuperset && isIncomparable:
				t.Errorf("%s vs %s is declared both superset and incomparable", a, b)
			case !isSuperset && !isIncomparable:
				// Either the direction (this way or the reverse) must be known,
				// or the pair must be declared incomparable with a rationale.
				if closure[[2]string{b, a}] {
					continue
				}
				t.Errorf("role pair (%s, %s) is undecided: the lattice cannot tell which way it goes and it is not declared incomparable", a, b)
			}
		}
	}
}

// TestRoleSupersetRelationsHoldOnMatrix is the monotonicity guard: a declared
// superset must hold every action the subset holds, on every resource.
func TestRoleSupersetRelationsHoldOnMatrix(t *testing.T) {
	index := roleGrantIndex()
	for _, rel := range roleSupersetRelations {
		if strings.TrimSpace(rel.Rationale) == "" {
			t.Errorf("%s ⊇ %s: superset relation needs a rationale", rel.Superset, rel.Subset)
		}
		for _, resource := range sortedKeys(index) {
			supersetActs := actionsOf(index, resource, rel.Superset)
			for action := range actionsOf(index, resource, rel.Subset) {
				if _, ok := supersetActs[action]; ok {
					continue
				}
				t.Errorf("%s ⊇ %s violated: %s misses %q on resource %q (subset holds it)",
					rel.Superset, rel.Subset, rel.Superset, action, resource)
			}
		}
	}
}

// TestRoleIncomparableRelationsAreReal keeps the declaration honest: an
// incomparable pair that stopped diverging is stale documentation and must be
// promoted to a superset relation (or removed).
func TestRoleIncomparableRelationsAreReal(t *testing.T) {
	index := roleGrantIndex()
	for _, rel := range roleIncomparableRelations {
		if strings.TrimSpace(rel.Rationale) == "" {
			t.Errorf("%s vs %s: incomparability needs a rationale", rel.Left, rel.Right)
		}
		leftOnly, rightOnly := 0, 0
		for _, resource := range sortedKeys(index) {
			left := actionsOf(index, resource, rel.Left)
			right := actionsOf(index, resource, rel.Right)
			for action := range left {
				if _, ok := right[action]; !ok {
					leftOnly++
				}
			}
			for action := range right {
				if _, ok := left[action]; !ok {
					rightOnly++
				}
			}
		}
		if leftOnly == 0 || rightOnly == 0 {
			t.Errorf("%s vs %s no longer diverges in both directions (%s-only=%d, %s-only=%d): "+
				"one side now dominates the other, so declare a superset relation instead",
				rel.Left, rel.Right, rel.Left, leftOnly, rel.Right, rightOnly)
		}
	}
}

// TestActionImplicationRequiresRead enforces "if you may manage/execute it, you
// may read it". The exception list is explicit and justified, so a resource can
// only escape the rule on purpose.
func TestActionImplicationRequiresRead(t *testing.T) {
	index := roleGrantIndex()
	for _, resource := range sortedKeys(index) {
		if reason, ok := actionOnlyResources[resource]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("action-only resource %q needs a rationale", resource)
			}
			for _, role := range BuiltinRoles {
				if _, ok := actionsOf(index, resource, role)["read"]; ok {
					t.Errorf("action-only resource %q must not grant read to %q", resource, role)
				}
			}
			continue
		}
		for _, role := range BuiltinRoles {
			acts := actionsOf(index, resource, role)
			if acts == nil {
				continue
			}
			if _, canRead := acts["read"]; canRead {
				continue
			}
			for _, implied := range actionImplications {
				if _, ok := acts[implied]; ok {
					t.Errorf("%s holds %q on %q without read: add a read grant or declare the resource action-only",
						role, implied, resource)
				}
			}
		}
	}
}

// TestActionOnlyResourcesExist guards against typos in the exception list: an
// exception for a resource that is not in the matrix is dead weight.
func TestActionOnlyResourcesExist(t *testing.T) {
	index := roleGrantIndex()
	for resource := range actionOnlyResources {
		if _, ok := index[resource]; !ok {
			t.Errorf("actionOnlyResources refers to unknown resource %q", resource)
		}
	}
}

// TestRoleSupersetRelationsReferenceBuiltinRoles keeps the lattice finite.
func TestRoleSupersetRelationsReferenceBuiltinRoles(t *testing.T) {
	known := map[string]struct{}{}
	for _, role := range BuiltinRoles {
		known[role] = struct{}{}
	}
	for _, rel := range roleSupersetRelations {
		if _, ok := known[rel.Superset]; !ok {
			t.Errorf("superset relation names unknown role %q", rel.Superset)
		}
		if _, ok := known[rel.Subset]; !ok {
			t.Errorf("superset relation names unknown role %q", rel.Subset)
		}
		if rel.Superset == rel.Subset {
			t.Errorf("superset relation is reflexive for %q", rel.Superset)
		}
	}
	for _, rel := range roleIncomparableRelations {
		if _, ok := known[rel.Left]; !ok {
			t.Errorf("incomparable relation names unknown role %q", rel.Left)
		}
		if _, ok := known[rel.Right]; !ok {
			t.Errorf("incomparable relation names unknown role %q", rel.Right)
		}
		if rel.Left == rel.Right {
			t.Errorf("incomparable relation is reflexive for %q", rel.Left)
		}
	}
}

// TestTeamAdminCanUseTheProduct pins the doc/243 decision: team_admin is
// "business_user + team-scoped management", so it must reach every consumer
// surface a business user can reach (otherwise the role cannot be used to
// supervise its own team's assistants).
func TestTeamAdminCanUseTheProduct(t *testing.T) {
	index := roleGrantIndex()
	for _, resource := range []string{"chat", "search-app", "agent", "assistant", "memory"} {
		for _, action := range []string{"read", "execute"} {
			if _, ok := actionsOf(index, resource, model.RoleTeamAdmin)[action]; !ok {
				t.Errorf("team_admin must hold %q on %q to be usable at all (doc/243)", action, resource)
			}
		}
	}
	if _, ok := actionsOf(index, "agent", model.RoleTeamAdmin)["session:create"]; !ok {
		t.Errorf("team_admin must hold session:create on agent (creating a session is how the product is entered)")
	}
	// Trial runs need scenario-template:read + eval-set:execute. A team admin
	// gets the catalog read (that is how the product is entered) but
	// deliberately NOT eval-set:execute: doc/129 §1178 pins that action to
	// platform/tenant/operator because it also drives
	// POST /eval-cases/:id/revalidate, and
	// TestEvalSetExecutePermissionsMatchRevalidationContract enforces it.
	// Consequence, documented in doc/243 §3.2: team_admin cannot start a trial
	// run; that stays an operations duty.
	if _, ok := actionsOf(index, "scenario-template", model.RoleTeamAdmin)["read"]; !ok {
		t.Errorf("team_admin must read scenario-template to enter the template catalog")
	}
	if _, ok := actionsOf(index, "eval-set", model.RoleTeamAdmin)["execute"]; ok {
		t.Errorf("team_admin must NOT hold eval-set:execute (doc/129 §1178); it would also unlock POST /eval-cases/:id/revalidate")
	}
}

// TestViewerIsTheReadOnlySliceOfBusinessUser pins doc/245: `viewer` is the
// read-only slice of `business_user`, not a second read audience. Two things
// must hold forever:
//
//  1. viewer holds exactly one action (`read`) on exactly the declared 14
//     resources — no more, and never a write;
//  2. viewer's reads are a subset of business_user's reads, i.e. the
//     "read-only role can read more than the working role" inversion (doc/242)
//     is gone in both directions.
//
// The 19 removals are named explicitly so re-adding one is a test failure with
// a readable message instead of a silent privilege expansion.
func TestViewerIsTheReadOnlySliceOfBusinessUser(t *testing.T) {
	index := roleGrantIndex()

	got := map[string]bool{}
	for resource, byRole := range index {
		actions := byRole[model.RoleViewer]
		if len(actions) == 0 {
			continue
		}
		if len(actions) != 1 {
			t.Errorf("viewer must stay read-only on %q, got %v", resource, actions)
		}
		if _, ok := actions["read"]; !ok {
			t.Errorf("viewer only ever holds read, but %q grants %v", resource, actions)
		}
		got[resource] = true
	}

	want := map[string]bool{
		"agent": true, "assistant": true, "chat": true, "context-decision": true,
		"dataset": true, "document": true, "logical-document": true, "memory": true,
		"memory-operation": true, "memory-policy": true, "scenario-template": true,
		"search-app": true, "trial-run": true, "user": true,
	}
	for resource := range want {
		if !got[resource] {
			t.Errorf("viewer must read %q (doc/245: the read-only slice of business_user)", resource)
		}
	}
	for resource := range got {
		if want[resource] {
			continue
		}
		t.Errorf("viewer must not read %q: it is not part of the business read surface (doc/245)", resource)
	}

	for resource := range got {
		if _, ok := actionsOf(index, resource, model.RoleBusinessUser)["read"]; !ok {
			t.Errorf("viewer reads %q but business_user does not — that is the inversion doc/245 removed", resource)
		}
	}
}

// TestViewerConvergenceRemovedReadsIsCurrent keeps the documented removal list
// honest: every name in it must be a resource the matrix actually knows, so a
// renamed resource cannot silently turn the list into a no-op.
func TestViewerConvergenceRemovedReadsIsCurrent(t *testing.T) {
	known := map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		known[grant.Resource] = true
	}
	if len(viewerConvergenceRemovedReads) != 19 {
		t.Fatalf("doc/245 removed 19 viewer reads, list has %d", len(viewerConvergenceRemovedReads))
	}
	for _, resource := range viewerConvergenceRemovedReads {
		if !known[resource] {
			t.Errorf("%q is listed as a converged viewer read but is not a matrix resource (stale name?)", resource)
		}
	}
}

// TestReadImpliedByActionCoversWriteVerbs pins the runtime accessor to the same
// list the matrix guard uses. A verb that is added to actionImplications but not
// honoured at authorization time (or vice versa) is exactly the builtin-vs-custom
// role divergence doc/247 closes.
func TestReadImpliedByActionCoversWriteVerbs(t *testing.T) {
	for _, action := range []string{"manage", "execute", "append", "test", "session:create", "delete:own"} {
		if !ReadImpliedByAction(action) {
			t.Errorf("ReadImpliedByAction(%q) = false, want true", action)
		}
	}
	for _, action := range []string{"read", "governance.read", "governance.manage", "unknown"} {
		if ReadImpliedByAction(action) {
			t.Errorf("ReadImpliedByAction(%q) = true, want false", action)
		}
	}
}
