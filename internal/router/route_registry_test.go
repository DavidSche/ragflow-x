package router

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/db"
)

func TestAccessRulesHaveRegistryContracts(t *testing.T) {
	registry := ResourceTypeRegistry()
	for _, rule := range accessRules(nil) {
		definition, ok := registry[rule.Resource]
		if !ok {
			t.Errorf("%s %s references unregistered resource %q", rule.Method, rule.Path, rule.Resource)
			continue
		}
		if rule.ResourceScope != definition.ResourceScope {
			t.Errorf("%s %s scope %q does not match registry %q", rule.Method, rule.Path, rule.ResourceScope, definition.ResourceScope)
		}
		if rule.RouteClass != RouteClassBusiness && rule.RouteClass != RouteClassGovernance {
			t.Errorf("%s %s has invalid route class %q", rule.Method, rule.Path, rule.RouteClass)
		}
		if len(rule.AllowedScope) == 0 {
			t.Errorf("%s %s has no allowed scope", rule.Method, rule.Path)
		}
		if rule.RouteClass == RouteClassGovernance && !rule.AuditRequired {
			t.Errorf("%s %s governance read must require audit", rule.Method, rule.Path)
		}
		if definition.SupportsCrossTenantWrite && rule.Method != "GET" && !rule.ActingContextRequired {
			t.Errorf("%s %s cross-tenant write must require Acting Context", rule.Method, rule.Path)
		}
		if !rule.SupportsCrossTenantWrite && definition.SupportsCrossTenantWrite {
			t.Errorf("%s %s does not expose cross-tenant write metadata", rule.Method, rule.Path)
		}
		if definition.RequiresApproval && rule.Method != "GET" && !rule.RequiresApproval {
			t.Errorf("%s %s must expose approval requirement", rule.Method, rule.Path)
		}
	}
}

func TestResourceRegistryActionsMatchRouteContracts(t *testing.T) {
	for _, rule := range accessRules(nil) {
		definition := ResourceTypeRegistry()[rule.Resource]
		allowed := false
		for _, action := range definition.AllowedActions {
			if action == rule.Action {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("registry for %q does not allow %s required by %s %s", rule.Resource, rule.Action, rule.Method, rule.Path)
		}
	}
}

// ScenarioID: SC-AUTHZ-001
func TestP0_AUTHZ_001_RouteContractsMatchPermissionRegistry(t *testing.T) {
	grants := make(map[string]bool)
	for _, grant := range db.BuiltinPermissionMatrix() {
		grants[grant.Action+"|"+grant.Resource] = true
	}
	for _, rule := range accessRules(nil) {
		if !grants[rule.Action+"|"+rule.Resource] && !grants["*|*"] {
			t.Errorf("permission registry is missing %s on %q required by %s %s", rule.Action, rule.Resource, rule.Method, rule.Path)
		}
	}
}

func TestGovernanceReadContractsAllowAuthorizedScopes(t *testing.T) {
	for _, rule := range accessRules(nil) {
		definition := ResourceTypeRegistry()[rule.Resource]
		if rule.Method != "GET" || !definition.SupportsGovernanceRead {
			continue
		}
		found := map[string]bool{}
		for _, scope := range rule.AllowedScope {
			found[scope] = true
		}
		for _, scope := range []string{AllowedScopeCurrent, AllowedScopeSpecific, AllowedScopeAll} {
			if !found[scope] {
				t.Errorf("%s %s governance read is missing allowed scope %s", rule.Method, rule.Path, scope)
			}
		}
	}
}
