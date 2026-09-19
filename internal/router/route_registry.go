package router

import (
	"github.com/ragflow-x/ragflow-x/internal/middleware"
)

const (
	RouteClassBusiness     = "BUSINESS"
	RouteClassGovernance   = middleware.RouteClassGovernance
	ResourceScopePlatform  = "PLATFORM"
	ResourceScopeWorkspace = "WORKSPACE"
	AllowedScopeCurrent    = "CURRENT"
	AllowedScopeSpecific   = "SPECIFIC"
	AllowedScopeAll        = "ALL_AUTHORIZED"
)

// ResourceTypeDefinition is the authoritative policy shape for every resource
// referenced by a route. Registry consistency is enforced by contract tests.
type ResourceTypeDefinition struct {
	ResourceScope            string
	AllowedActions           []string
	SupportsGovernanceRead   bool
	SupportsCrossTenantWrite bool
	RequiresApproval         bool
	RequiresActingContext    bool
}

func resourceActions(actions ...string) []string {
	return actions
}

// ResourceTypeRegistry is the centralized registry required by doc/77. It is
// intentionally code-resident so unknown or unregistered resources fail CI.
func ResourceTypeRegistry() map[string]ResourceTypeDefinition {
	return map[string]ResourceTypeDefinition{
		"agent":                 {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute", "session:create"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"alert":                 {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"api-key":               {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"approval":              {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "execute", "manage"), SupportsGovernanceRead: true},
		"approval-policy":       {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"assistant":             {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true},
		"audit":                 {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"audit-anchor":          {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"branding":              {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"chat":                  {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"dashboard":             {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"dataset":               {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"dataset-export":        {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"document":              {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "append", "delete:own", "execute"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"enterprise-connection": {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read", "test", "manage"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"eval-set":              {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"ragflow-sync":          {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"knowledge-lifecycle":   {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"knowledge-ops":         {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"memory":                {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true},
		"model-provider":        {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true, SupportsCrossTenantWrite: true, RequiresApproval: true, RequiresActingContext: true},
		"model-route":           {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"project":               {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"prompt-policy":         {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"release-governance":    {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"role":                  {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"scenario-template":     {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"search-app":            {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true},
		"system":                {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"system-health":         {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"task":                  {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage", "execute"), SupportsGovernanceRead: true},
		"team":                  {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"tenant":                {ResourceScope: ResourceScopePlatform, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
		"usage":                 {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"usage-export":          {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read"), SupportsGovernanceRead: true},
		"user":                  {ResourceScope: ResourceScopeWorkspace, AllowedActions: resourceActions("read", "manage"), SupportsGovernanceRead: true},
	}
}

func allowedScopesFor(rule middleware.Rule, definition ResourceTypeDefinition) []string {
	if rule.Method == "GET" && definition.SupportsGovernanceRead {
		return []string{AllowedScopeCurrent, AllowedScopeSpecific, AllowedScopeAll}
	}
	if rule.Method != "GET" && definition.SupportsCrossTenantWrite && definition.RequiresApproval &&
		definition.RequiresActingContext {
		return []string{AllowedScopeCurrent, AllowedScopeSpecific}
	}
	return []string{AllowedScopeCurrent}
}

func routeClassFor(rule middleware.Rule, definition ResourceTypeDefinition) string {
	if rule.Method == "GET" && definition.SupportsGovernanceRead {
		return RouteClassGovernance
	}
	return RouteClassBusiness
}

func withRouteContracts(rules []middleware.Rule) []middleware.Rule {
	registry := ResourceTypeRegistry()
	for index := range rules {
		definition, ok := registry[rules[index].Resource]
		if !ok {
			continue
		}
		rules[index].RouteClass = routeClassFor(rules[index], definition)
		rules[index].ResourceScope = definition.ResourceScope
		rules[index].AllowedScope = allowedScopesFor(rules[index], definition)
		rules[index].AuditRequired = rules[index].RouteClass == RouteClassGovernance
		rules[index].ActingContextRequired = definition.RequiresActingContext &&
			rules[index].Method != "GET"
		rules[index].SupportsCrossTenantWrite = definition.SupportsCrossTenantWrite
		rules[index].RequiresApproval = definition.RequiresApproval
	}
	return rules
}
