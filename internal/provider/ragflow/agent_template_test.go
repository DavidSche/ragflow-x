package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestGetAgentTemplateFallsBackToTheCatalogue is the live-drill regression: on
// 4.151 (RAGFlow v1.0.0) `GET /api/v1/agents/templates/:id` answers 404 for every
// id while the catalogue itself lists the templates *with* their dsl. Skeleton
// import must keep working, so a not-found is answered from the catalogue.
func TestGetAgentTemplateFallsBackToTheCatalogue(t *testing.T) {
	var singleCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/templates":
			writeEnvelope(t, w, []map[string]interface{}{
				{"id": "9", "canvas_category": "dataflow", "dsl": map[string]interface{}{"components": map[string]interface{}{}}},
				{"id": "16", "canvas_category": "dataflow", "dsl": map[string]interface{}{"components": map[string]interface{}{}}},
			})
		case "/api/v1/agents/templates/9":
			singleCalls++
			http.Error(w, `{"code":404,"message":"Not Found"}`, http.StatusNotFound)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	template, err := client.GetAgentTemplate(context.Background(), "9")
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if template.ID != "9" || len(template.DSL) == 0 {
		t.Fatalf("fallback lost the row or its dsl: %+v", template)
	}
	if singleCalls != 1 {
		t.Fatalf("expected the single-row route to be tried once before the fallback, got %d", singleCalls)
	}
}

// TestGetAgentTemplateUnknownIDFailsLoudly keeps the fallback honest: an id the
// catalogue does not list must not become a zero-value template that import
// would silently materialise.
func TestGetAgentTemplateUnknownIDFailsLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agents/templates" {
			writeEnvelope(t, w, []map[string]interface{}{{"id": "9"}})
			return
		}
		http.Error(w, `{"code":404,"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	if _, err := client.GetAgentTemplate(context.Background(), "missing"); err == nil {
		t.Fatal("expected an error for an id the catalogue does not list")
	}
}

// TestGetAgentTemplatePrefersTheSingleRow pins the fast path: when upstream does
// implement the single-row read, no catalogue call happens.
func TestGetAgentTemplatePrefersTheSingleRow(t *testing.T) {
	var listCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agents/templates" {
			listCalls++
			writeEnvelope(t, w, []map[string]interface{}{})
			return
		}
		writeEnvelope(t, w, map[string]interface{}{
			"id": "9", "canvas_category": "dataflow", "dsl": map[string]interface{}{"components": map[string]interface{}{}},
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	template, err := client.GetAgentTemplate(context.Background(), "9")
	if err != nil {
		t.Fatal(err)
	}
	if template.ID != "9" || listCalls != 0 {
		t.Fatalf("single-row path should not touch the catalogue: id=%s listCalls=%d", template.ID, listCalls)
	}
}

// TestGetAgentTemplateRejectsTransportErrors keeps a real outage distinguishable
// from a missing route: only a 404 triggers the catalogue fallback.
func TestGetAgentTemplateRejectsTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 0)

	if _, err := client.GetAgentTemplate(context.Background(), "9"); err == nil {
		t.Fatal("expected an error")
	} else if isNotFoundError(err) {
		t.Fatalf("500 must not be treated as not-found: %v", err)
	}
}
