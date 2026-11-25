package docs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/docs"
)

// TestEmbeddedSpecIsASpec reads the two embedded artifacts through the same values
// the router serves. It is not the freshness check (that is s25, on bytes); it is the
// cheap guard against the embed pointing at an empty or truncated file — a binary that
// serves `{}` or 0 bytes would still compile, still pass every route test, and only
// fail in front of an integrator.
func TestEmbeddedSpecIsASpec(t *testing.T) {
	var spec struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
	}
	if err := json.Unmarshal(docs.OpenAPISpecJSON, &spec); err != nil {
		t.Fatalf("embedded swagger.json does not parse: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		t.Errorf("embedded spec version = %q, want 3.x", spec.OpenAPI)
	}
	if len(spec.Paths) == 0 {
		t.Error("embedded spec has no paths")
	}
	if strings.TrimSpace(spec.Info.Title) == "" {
		t.Error("embedded spec has no info.title")
	}
	if len(docs.OpenAPISpecYAML) == 0 {
		t.Fatal("embedded swagger.yaml is empty")
	}
	if !strings.Contains(string(docs.OpenAPISpecYAML), "openapi: "+spec.OpenAPI) {
		t.Error("embedded swagger.yaml and swagger.json disagree about the spec version")
	}
}
