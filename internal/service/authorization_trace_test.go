package service

import "testing"

func TestSQLAuthorizationTraceDenyMarksLaterLayersNotChecked(t *testing.T) {
	trace := newSQLAuthorizationTrace("rule-1", "sql_query")
	trace.deny(authorizationLayerToolRegistry)
	if trace.Authorization != authorizationDenied {
		t.Fatalf("unexpected authorization: %s", trace.Authorization)
	}
	expected := map[string]string{
		authorizationLayerRoutingRule:   authorizationAllowed,
		authorizationLayerToolRegistry:  authorizationDenied,
		authorizationLayerQueryTemplate: authorizationNotChecked,
		authorizationLayerDBConnection:  authorizationNotChecked,
	}
	for layer, status := range expected {
		if trace.Layers[layer] != status {
			t.Fatalf("layer %s: expected %s, got %s", layer, status, trace.Layers[layer])
		}
	}
}
