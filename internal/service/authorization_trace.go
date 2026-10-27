package service

const (
	authorizationLayerRoutingRule   = "routing_rule"
	authorizationLayerToolRegistry  = "tool_registry"
	authorizationLayerQueryTemplate = "query_template"
	authorizationLayerDBConnection  = "db_connection"

	authorizationAllowed    = "allowed"
	authorizationDenied     = "denied"
	authorizationNotChecked = "not_checked"
)

// AuthorizationTrace records only policy identifiers and layer decisions.
// SQL, table names, column names, connection details, credentials, parameters,
// and result values must never be added to this structure.
type AuthorizationTrace struct {
	Source        string            `json:"source"`
	Resource      string            `json:"resource"`
	Authorization string            `json:"authorization"`
	PolicyID      string            `json:"policy_id"`
	PolicyVersion string            `json:"policy_version"`
	Layers        map[string]string `json:"layers"`
}

func newSQLAuthorizationTrace(sourceRoutingRuleID, toolID string) *AuthorizationTrace {
	return &AuthorizationTrace{
		Source:        "db",
		Resource:      toolID,
		Authorization: authorizationAllowed,
		PolicyID:      sourceRoutingRuleID,
		Layers: map[string]string{
			authorizationLayerRoutingRule:   authorizationAllowed,
			authorizationLayerToolRegistry:  authorizationAllowed,
			authorizationLayerQueryTemplate: authorizationAllowed,
			authorizationLayerDBConnection:  authorizationAllowed,
		},
	}
}

func newToolAuthorizationTrace(sourceRoutingRuleID, toolID string) *AuthorizationTrace {
	return &AuthorizationTrace{
		Source:        "tool",
		Resource:      toolID,
		Authorization: authorizationAllowed,
		PolicyID:      sourceRoutingRuleID,
		Layers: map[string]string{
			authorizationLayerRoutingRule:  authorizationAllowed,
			authorizationLayerToolRegistry: authorizationAllowed,
		},
	}
}

func (trace *AuthorizationTrace) deny(layer string) {
	trace.Authorization = authorizationDenied
	failed := false
	for _, name := range authorizationLayerOrder {
		switch {
		case name == layer:
			failed = true
			trace.Layers[name] = authorizationDenied
		case failed:
			trace.Layers[name] = authorizationNotChecked
		}
	}
}

var authorizationLayerOrder = []string{
	authorizationLayerRoutingRule,
	authorizationLayerToolRegistry,
	authorizationLayerQueryTemplate,
	authorizationLayerDBConnection,
}

func authorizationPolicyVersion(trace *AuthorizationTrace) string {
	if trace == nil || trace.PolicyVersion == "" {
		return "explicit-rbac-v1"
	}
	return trace.PolicyVersion
}
