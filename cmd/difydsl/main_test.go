package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/difydsl"
)

func TestImportResultRequiresRAGFlowConfiguration(t *testing.T) {
	result := difydsl.Result{DSL: map[string]any{}, Report: difydsl.Report{Title: "Agent"}}
	if err := importResult("", "key", time.Second, result, importOptions{}); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("importResult() error = %v, want base URL error", err)
	}
	if err := importResult("http://127.0.0.1:1", "", time.Second, result, importOptions{}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("importResult() error = %v, want API key error", err)
	}
}

func TestImportResultRefusesUnmappedResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	defer server.Close()
	result := difydsl.Result{
		DSL: map[string]any{},
		Report: difydsl.Report{
			Title:            "Agent",
			UnmappedDatasets: []string{"dify-dataset"},
			UnmappedTools:    []string{"workflow"},
		},
	}
	err := importResult(server.URL, "key", time.Second, result, importOptions{})
	if err == nil || !strings.Contains(err.Error(), "unmapped datasets") {
		t.Fatalf("importResult() error = %v, want unmapped resource refusal", err)
	}
}

func TestImportResultCreatesAgent(t *testing.T) {
	var received struct {
		Title          string         `json:"title"`
		Release        bool           `json:"release"`
		CanvasCategory string         `json:"canvas_category"`
		Dsl            map[string]any `json:"dsl"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"id": "agent-id", "title": received.Title, "release": received.Release},
		})
	}))
	defer server.Close()

	result := difydsl.Result{DSL: map[string]any{"version": "0.1.0"}, Report: difydsl.Report{Title: "Converted"}}
	if err := importResult(server.URL, "test-key", time.Second, result, importOptions{Release: true}); err != nil {
		t.Fatalf("importResult() error = %v", err)
	}
	if received.Title != "Converted" || !received.Release || received.CanvasCategory != "agent_canvas" || received.Dsl["version"] != "0.1.0" {
		t.Fatalf("received = %#v", received)
	}
}
