package router

import (
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/gateway/contract"
)

// TestGatewayRoutesAreCoveredByContract enforces doc/227 WS3 C3.1: every
// customer-facing gateway route carries a stability label, and the contract
// registry holds no stale entries. Adding a /v1 route without a contract entry
// fails here instead of shipping an unclassified API.
func TestGatewayRoutesAreCoveredByContract(t *testing.T) {
	specs := gatewayRouteSpecs(nil)
	if len(specs) == 0 {
		t.Fatal("gatewayRouteSpecs must not be empty")
	}

	registered := map[string]bool{}
	for _, spec := range specs {
		api, ok := contract.APIByPath(spec.Method, spec.Path)
		if !ok {
			t.Errorf("gateway route %s %s has no contract entry (doc/227 C3.1)", spec.Method, spec.Path)
			continue
		}
		if api.Stability == "" {
			t.Errorf("gateway route %s %s has an empty stability label", spec.Method, spec.Path)
		}
		registered[spec.Method+" "+spec.Path] = true
	}

	for _, api := range contract.APIs() {
		if !registered[api.Method+" "+api.Path] {
			t.Errorf("contract entry %s (%s %s) is not registered by the router: stale contract", api.ID, api.Method, api.Path)
		}
	}
}

// TestGatewayRouteSpecsArePOSTWithHandlers pins the shape the API-key rate-limit
// loop depends on: a non-POST or handler-less spec would either bypass the
// per-key bucket or panic at registration.
func TestGatewayRouteSpecsArePOSTWithHandlers(t *testing.T) {
	for _, spec := range gatewayRouteSpecs(nil) {
		if spec.Method != "POST" {
			t.Errorf("gateway route %s must be POST, got %s", spec.Path, spec.Method)
		}
		if spec.Handler == nil {
			t.Errorf("gateway route %s has no handler", spec.Path)
		}
	}
}
