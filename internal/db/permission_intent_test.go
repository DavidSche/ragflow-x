package db

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestBusinessUserReadIntentCoversMatrix is the anti-omission guard from
// doc/242: every resource in BuiltinPermissionMatrix() must carry an explicit
// business_user read intent, and every rule must point at a real resource.
// Adding a governance resource without deciding the consumer's read access now
// fails here instead of silently shipping another viewer-only row.
func TestBusinessUserReadIntentCoversMatrix(t *testing.T) {
	inMatrix := map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		inMatrix[grant.Resource] = true
	}

	rules := businessUserReadRuleMap()
	if len(rules) != len(businessUserReadRules) {
		t.Fatalf("duplicate resource rules: %d rules collapse to %d entries", len(businessUserReadRules), len(rules))
	}

	var missing, unknown []string
	for resource := range inMatrix {
		if _, ok := rules[resource]; !ok {
			missing = append(missing, resource)
		}
	}
	for resource := range rules {
		if !inMatrix[resource] {
			unknown = append(unknown, resource)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	if len(missing) > 0 {
		t.Errorf("resources without a business_user read intent (add a rule in permission_intent.go): %v", missing)
	}
	if len(unknown) > 0 {
		t.Errorf("intent rules for resources that are not in the permission matrix: %v", unknown)
	}
}

// TestBusinessUserReadIntentMatchesMatrix keeps the declaration and the grants
// in sync in both directions, so an intent cannot be "documented but not
// granted" nor "granted but not intended".
func TestBusinessUserReadIntentMatchesMatrix(t *testing.T) {
	granted := map[string]map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		if grant.Role != model.RoleBusinessUser {
			continue
		}
		if granted[grant.Resource] == nil {
			granted[grant.Resource] = map[string]bool{}
		}
		granted[grant.Resource][grant.Action] = true
	}

	for _, rule := range businessUserReadRules {
		hasRead := granted[rule.Resource]["read"]
		switch rule.Intent {
		case BusinessUserReadAllowed:
			if !hasRead {
				t.Errorf("resource %q: intent is read but the matrix grants no business_user read", rule.Resource)
			}
		case BusinessUserReadDenied:
			if hasRead {
				t.Errorf("resource %q: intent is none but the matrix grants business_user read", rule.Resource)
			}
		default:
			t.Errorf("resource %q: unknown intent %q", rule.Resource, rule.Intent)
		}
	}
}

// TestViewerOnlyReadsAreGone is the v0.5.0 convergence guard (doc/245). It is
// stricter than the rationale escape hatch below: after doc/245 removed the 19
// viewer-only reads, *any* read the viewer holds that business_user does not is
// a regression, full stop. Rationale is no longer an acceptable substitute for
// convergence — a future resource must be readable by the working role first.
func TestViewerOnlyReadsAreGone(t *testing.T) {
	viewerReads := map[string]bool{}
	businessReads := map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		if grant.Action != "read" {
			continue
		}
		switch grant.Role {
		case model.RoleViewer:
			viewerReads[grant.Resource] = true
		case model.RoleBusinessUser:
			businessReads[grant.Resource] = true
		}
	}
	if len(viewerReads) != 14 {
		t.Fatalf("viewer must read exactly 14 resources (doc/245), got %d: %v", len(viewerReads), sortedKeys(viewerReads))
	}
	for resource := range viewerReads {
		if !businessReads[resource] {
			t.Errorf("viewer reads %q but business_user does not: read-only roles must not out-read working roles (doc/242/doc/245)", resource)
		}
	}
}

// TestViewerOnlyReadRequiresRationale is the older inversion guard, kept as a
// second net: *if* a viewer-only read ever returns, the denial must at least be
// a written decision (doc/242 §3). TestViewerOnlyReadsAreGone above should fail
// first.
func TestViewerOnlyReadRequiresRationale(t *testing.T) {
	readers := map[string]map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		if grant.Action != "read" {
			continue
		}
		if readers[grant.Resource] == nil {
			readers[grant.Resource] = map[string]bool{}
		}
		readers[grant.Resource][grant.Role] = true
	}

	rules := businessUserReadRuleMap()
	for resource, roles := range readers {
		if !roles[model.RoleViewer] || roles[model.RoleBusinessUser] {
			continue
		}
		rule, ok := rules[resource]
		if !ok {
			t.Errorf("resource %q: viewer reads it, business_user does not, and no intent rule exists", resource)
			continue
		}
		if rule.Intent != BusinessUserReadDenied {
			t.Errorf("resource %q: viewer-only read must be declared none, got %q", resource, rule.Intent)
		}
		if len(strings.TrimSpace(rule.Rationale)) < 20 {
			t.Errorf("resource %q: viewer-only read needs a substantive rationale, got %q", resource, rule.Rationale)
		}
	}
}

// TestBusinessUserReadRationalePresent requires a rationale wherever the
// decision is non-obvious, so a reviewer never has to guess why a grant exists.
func TestBusinessUserReadRationalePresent(t *testing.T) {
	for _, rule := range businessUserReadRules {
		if rule.Journey == "" {
			t.Errorf("resource %q: journey must be set", rule.Resource)
		}
		if len(strings.TrimSpace(rule.Rationale)) < 20 {
			t.Errorf("resource %q: rationale too thin (%q)", rule.Resource, rule.Rationale)
		}
	}
}

// TestBuiltinPermissionMatrixHasNoDuplicateGrants removes a whole class of
// silent drift: the flat literal had two duplicated platform rows, which made
// every "how many grants do we have" answer wrong.
func TestBuiltinPermissionMatrixHasNoDuplicateGrants(t *testing.T) {
	seen := map[string]int{}
	for _, grant := range BuiltinPermissionMatrix() {
		key := fmt.Sprintf("%s|%s|%s", grant.Role, grant.Action, grant.Resource)
		seen[key]++
	}
	var dups []string
	for key, count := range seen {
		if count > 1 {
			dups = append(dups, fmt.Sprintf("%s x%d", key, count))
		}
	}
	sort.Strings(dups)
	if len(dups) > 0 {
		t.Errorf("duplicate built-in grants: %v", dups)
	}
}

// TestTenantAdminSupersetOfBusinessUser encodes the only role hierarchy the
// matrix actually promises: a workspace admin can do everything a business user
// can. It is deliberately not extended to operator/viewer — those roles are
// orthogonal to the consumer role, which TestViewerOnlyReadRequiresRationale
// handles instead of a false containment claim.
func TestTenantAdminSupersetOfBusinessUser(t *testing.T) {
	byRole := map[string]map[string]map[string]bool{}
	for _, grant := range BuiltinPermissionMatrix() {
		if byRole[grant.Role] == nil {
			byRole[grant.Role] = map[string]map[string]bool{}
		}
		if byRole[grant.Role][grant.Resource] == nil {
			byRole[grant.Role][grant.Resource] = map[string]bool{}
		}
		byRole[grant.Role][grant.Resource][grant.Action] = true
	}

	admin := byRole[model.RoleTenantAdmin]
	for resource, actions := range byRole[model.RoleBusinessUser] {
		for action := range actions {
			if !admin[resource][action] {
				t.Errorf("tenant_admin is missing %s:%s which business_user holds", action, resource)
			}
		}
	}
}

// TestBusinessUserReadIntentJourneys keeps the journey vocabulary closed, so
// the doc table in doc/242 cannot drift into free-form labels.
func TestBusinessUserReadIntentJourneys(t *testing.T) {
	allowed := map[PermissionJourney]bool{
		JourneyWorkspace: true, JourneyKnowledge: true, JourneyAssistant: true,
		JourneyInteraction: true, JourneyOperations: true, JourneyGovernance: true,
		JourneyPlatform: true,
	}
	for _, rule := range businessUserReadRules {
		if !allowed[rule.Journey] {
			t.Errorf("resource %q: unknown journey %q", rule.Resource, rule.Journey)
		}
	}
}
