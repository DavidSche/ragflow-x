package ragflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ScenarioID: SC-PROVIDER-00X (doc/269 §4.1) — tenant default-model write contract.
// Set-default, clear-default (all-empty body) and alias normalisation share one
// implementation path; RAGFlow business errors (code != 0) surface as
// ErrorTypeBusiness *Error.
func TestP0_PROVIDER_SetDefaultModelContract(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/models/default" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			writeTestEnvelope(w, map[string]interface{}{})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		// v1.0.0 rejects decision with a business error before doing anything.
		if strings.Contains(string(raw), `"decision"`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":101,"message":"model type decision is invalid","data":null}`))
			return
		}
		writeTestEnvelope(w, map[string]interface{}{})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "provider-key", 2*time.Second, 1)
	ctx := context.Background()

	// set default chat model
	if err := client.SetDefaultModel(ctx, SetDefaultModelRequest{ModelType: "chat", ModelID: "m-1", ModelName: "gpt-4o"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	// clear default chat model (all-empty body)
	if err := client.SetDefaultModel(ctx, SetDefaultModelRequest{ModelType: "chat"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// alias normalisation: asr -> speech2text
	if err := client.SetDefaultModel(ctx, SetDefaultModelRequest{ModelType: "asr", ModelID: "m-2"}); err != nil {
		t.Fatalf("alias: %v", err)
	}
	// alias normalisation: vision -> image2text
	if err := client.SetDefaultModel(ctx, SetDefaultModelRequest{ModelType: "vision", ModelID: "m-3"}); err != nil {
		t.Fatalf("alias vision: %v", err)
	}
	// v1.0.0 has no decision type -> upstream business error
	err := client.SetDefaultModel(ctx, SetDefaultModelRequest{ModelType: "decision", ModelID: "m-4"})
	var ragErr *Error
	if !errors.As(err, &ragErr) || ragErr.Type != ErrorTypeBusiness || !strings.Contains(ragErr.Message, "invalid") {
		t.Fatalf("decision error = %v, want *Error{Type:Business, msg contains invalid}", err)
	}

	if len(bodies) != 5 {
		t.Fatalf("request count = %d, want 5 (4 writes + 1 rejected decision)", len(bodies))
	}
	if bodies[0] == "{}" || !strings.Contains(bodies[0], `"model_id":"m-1"`) || !strings.Contains(bodies[0], `"model_type":"chat"`) {
		t.Fatalf("set body: %s", bodies[0])
	}
	// clear = all-empty except model_type
	if strings.Contains(bodies[1], "model_id") {
		t.Fatalf("clear body must omit model_id: %s", bodies[1])
	}
	if !strings.Contains(bodies[1], `"model_type":"chat"`) {
		t.Fatalf("clear body missing model_type: %s", bodies[1])
	}
	// asr normalised to speech2text, vision normalised to image2text
	if !strings.Contains(bodies[2], `"model_type":"speech2text"`) || strings.Contains(bodies[2], "asr") {
		t.Fatalf("asr alias body: %s", bodies[2])
	}
	if !strings.Contains(bodies[3], `"model_type":"image2text"`) {
		t.Fatalf("vision alias body: %s", bodies[3])
	}
}
