package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ScenarioID: SC-PROVIDER-001
func TestP0_PROVIDER_001_ModelContract(t *testing.T) {
	type request struct {
		method string
		path   string
		query  string
		auth   string
	}
	var requests []request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, request{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
		})
		switch r.URL.Path {
		case "/api/v1/models":
			writeTestEnvelope(w, []map[string]interface{}{
				{
					"model_id":      "model-1",
					"name":          "gpt-4o",
					"model_type":    []string{"chat"},
					"provider_name": "OpenAI",
					"instance_name": "production",
				},
			})
		case "/api/v1/models/default":
			writeTestEnvelope(w, map[string]interface{}{
				"models": []map[string]interface{}{
					{"model_id": "default-1", "name": "gpt-4o", "model_type": "chat"},
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			writeTestEnvelope(w, map[string]interface{}{})
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "provider-key", 2*time.Second, 1)
	ctx := context.Background()

	models, err := client.ListModels(ctx, "chat")
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := client.ListDefaultModels(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[0].method != http.MethodGet || requests[0].path != "/api/v1/models" || requests[0].query != "type=chat" || requests[0].auth != "provider-key" {
		t.Fatalf("models request: %+v", requests[0])
	}
	if requests[1].method != http.MethodGet || requests[1].path != "/api/v1/models/default" || requests[1].query != "" || requests[1].auth != "provider-key" {
		t.Fatalf("default models request: %+v", requests[1])
	}
	if len(models) != 1 || models[0].ModelID != "model-1" || models[0].Name != "gpt-4o" || len(models[0].Type) != 1 || models[0].Type[0] != "chat" || models[0].Provider != "OpenAI" || models[0].Instance != "production" {
		t.Fatalf("unexpected models: %+v", models)
	}
	if len(defaults) != 1 || defaults[0].ModelID != "default-1" || defaults[0].Name != "gpt-4o" || defaults[0].Type != "chat" {
		t.Fatalf("unexpected default models: %+v", defaults)
	}
}
