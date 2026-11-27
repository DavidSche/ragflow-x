package docs_test

import (
	"encoding/json"
	"os"
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

// TestOfflineDeliverableIsTheSpecAndTheRenderedPage pins the decision doc/310 §22
// T310-08 asks for. Two ways to publish an API reference with no server: ship the
// interactive UI, or ship bytes a browser can open. This project ships the bytes —
// `doc/specification/api/api-reference.{md,html}` are tracked and s28 proves they are
// a fresh render of the served spec — while the UI stays a convenience of the gated
// `/docs` surface, whose assets live in gin-swagger's own embedded copy, not here.
//
// The test therefore guards the shape of that sentence: this package may contain the
// two spec artifacts and Go code, nothing else. Dropping a swagger-ui bundle next to
// them would be changing the delivery decision, and the failure message says so
// instead of letting a new file ride along unnoticed.
func TestOfflineDeliverableIsTheSpecAndTheRenderedPage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	allowed := map[string]bool{"swagger.json": true, "swagger.yaml": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".md") {
			continue
		}
		if !allowed[name] {
			t.Errorf("internal/docs carries %q: the offline deliverable is the two spec artifacts "+
				"plus the rendered page under doc/specification/api (doc/310 §22 T310-08). "+
				"Shipping an interactive UI asset is a different decision — argue it there first", name)
		}
	}
	for _, name := range []string{"swagger.json", "swagger.yaml"} {
		if !allowed[name] {
			continue
		}
		if _, err := os.Stat(name); err != nil {
			t.Errorf("the embedded artifact %s is missing: the binary would serve an empty spec", name)
		}
	}
}
