package ragflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mustDecodeDatasetConfigUpdate(t *testing.T, r *http.Request) DatasetConfigUpdate {
	t.Helper()
	request := decodeCapturedJSON(t, r)
	value, err := json.Marshal(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var update DatasetConfigUpdate
	if err := json.Unmarshal(value, &update); err != nil {
		t.Fatal(err)
	}
	return update
}

func TestHTTPClientDocumentChunksAndDatasetConfig(t *testing.T) {
	var update DatasetConfigUpdate
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1/documents/doc%3F1/chunks":
			if query := r.URL.Query(); query.Get("page") != "2" || query.Get("page_size") != "100" {
				t.Fatalf("unexpected chunk query: %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, map[string]interface{}{
				"chunks": []Chunk{{ID: "chunk-1", Content: "parsed text", DocumentID: "doc?1", Available: true}},
				"total":  1,
			})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1/documents/doc%3F1/chunks/chunk%231":
			writeEnvelope(t, w, Chunk{ID: "chunk#1", Content: "single chunk", DocumentID: "doc?1"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1":
			writeEnvelope(t, w, DatasetConfig{
				ID: "ds/1", Name: "Knowledge", ChunkMethod: "naive",
				EmbeddingModel: "text-embedding-3-small", Permission: "team",
				ParserConfig: map[string]interface{}{"chunk_token_num": float64(512)},
			})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1":
			update = mustDecodeDatasetConfigUpdate(t, r)
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1/documents/doc%3F1/chunks":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == "/api/v1/datasets/ds%2F1/documents/doc%3F1/chunks":
			writeTestEnvelope(w, nil)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()
	chunks, total, err := client.ListDocumentChunks(ctx, "ds/1", "doc?1", 2, 200)
	if err != nil || total != 1 || len(chunks) != 1 || chunks[0].Content != "parsed text" {
		t.Fatalf("list chunks: chunks=%+v total=%d err=%v", chunks, total, err)
	}
	chunk, err := client.GetChunk(ctx, "ds/1", "doc?1", "chunk#1")
	if err != nil || chunk.ID != "chunk#1" || chunk.Content != "single chunk" {
		t.Fatalf("get chunk: %+v err=%v", chunk, err)
	}
	config, err := client.GetDatasetConfig(ctx, "ds/1")
	if err != nil || config.Name != "Knowledge" || config.ParserConfig["chunk_token_num"] != float64(512) {
		t.Fatalf("dataset config: %+v err=%v", config, err)
	}
	name := "Renamed Knowledge"
	err = client.UpdateDatasetConfig(ctx, "ds/1", DatasetConfigUpdate{Name: &name, ParserConfig: map[string]interface{}{"chunk_token_num": 256}})
	if err != nil {
		t.Fatal(err)
	}
	if update.Name == nil || *update.Name != "Renamed Knowledge" || update.ParserConfig["chunk_token_num"] != float64(256) {
		t.Fatalf("update was not forwarded: %+v", update)
	}
	if err := client.DeleteChunks(ctx, "ds/1", "doc?1", []string{"chunk#1"}); err != nil {
		t.Fatal(err)
	}
	if err := client.SetChunksAvailable(ctx, "ds/1", "doc?1", []string{"chunk#1"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientProviderCatalogAndInvocationContract(t *testing.T) {
	var requests []capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, decodeCapturedJSON(t, r))
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/providers":
			if r.URL.Query().Get("available") != "true" {
				t.Fatalf("unexpected provider query: %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, []ProviderInfo{{
				Name:        "OpenAI-API-Compatible",
				URL:         ProviderURLs{Default: "https://api.example.com/v1"},
				ModelTypes:  []string{"chat", "embedding"},
				HasInstance: true,
			}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/models":
			if r.URL.Query().Get("api_key") == "" || r.URL.Query().Get("base_url") == "" {
				t.Fatalf("catalog lookup omitted credentials: %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, []ProviderModel{{
				Name: "qwen-plus", MaxTokens: 32768,
				ModelTypes: []string{"chat"}, Features: []string{"tools"},
			}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/instances":
			writeEnvelope(t, w, []ProviderInstance{{
				ID: "instance-1", InstanceName: "production",
				ProviderID: "factory-1", BaseURL: "https://api.example.com/v1", Status: "ready",
			}})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/providers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":102,"message":"The provider has already exists.","data":null}`))
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/models":
			if r.URL.Query().Get("type") != "embedding" {
				t.Fatalf("unexpected model type query: %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, []AddedModel{{
				ModelID: "model-1", Name: "text-embedding", Type: []string{"embedding"},
				Provider: "OpenAI-API-Compatible", Instance: "production",
			}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/models/default":
			writeEnvelope(t, w, map[string]interface{}{"models": []interface{}{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/chat/completions":
			writeEnvelope(t, w, map[string]interface{}{
				"id": "resp-1", "answer": "Knowledge is ready.", "model": "qwen-plus",
				"usage": map[string]interface{}{"prompt_tokens": 8, "completion_tokens": 5},
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "catalog-key", 2*time.Second, 1)
	ctx := context.Background()
	providers, err := client.ListProviders(ctx, true)
	if err != nil || len(providers) != 1 || !providers[0].HasInstance {
		t.Fatalf("list providers: %+v err=%v", providers, err)
	}
	models, err := client.ListProviderModels(ctx, "OpenAI-API-Compatible", "secret", "https://api.example.com/v1")
	if err != nil || len(models) != 1 || models[0].Name != "qwen-plus" || models[0].MaxTokens != 32768 {
		t.Fatalf("provider models: %+v err=%v", models, err)
	}
	instances, err := client.ListProviderInstances(ctx, "OpenAI-API-Compatible")
	if err != nil || len(instances) != 1 || instances[0].ID != "instance-1" {
		t.Fatalf("provider instances: %+v err=%v", instances, err)
	}
	if err := client.AddProvider(ctx, "OpenAI-API-Compatible"); err != nil {
		t.Fatalf("AddProvider must tolerate already-exists conflicts: err=%v", err)
	}
	if err := client.DeleteProvider(ctx, "OpenAI-API-Compatible"); err != nil {
		t.Fatal(err)
	}
	added, err := client.ListModels(ctx, "embedding")
	if err != nil || len(added) != 1 || added[0].ModelID != "model-1" {
		t.Fatalf("tenant models: %+v err=%v", added, err)
	}
	if defaults, err := client.ListDefaultModels(ctx); err != nil || len(defaults) != 0 {
		t.Fatalf("default models: %+v err=%v", defaults, err)
	}
	completion, err := client.ChatCompletion(ctx, "chat-1", CompletionRequest{
		SessionID: "session-1", Messages: []Message{{Role: "user", Content: "hi"}}, Stream: true,
	})
	if err != nil || completion.ID != "resp-1" || len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "Knowledge is ready." {
		t.Fatalf("chat completion: %+v err=%v", completion, err)
	}

	if len(requests) != 8 {
		t.Fatalf("request count = %d, want 8", len(requests))
	}
	assertCapturedRequest(t, requests[7], http.MethodPost, "/api/v1/chat/completions", map[string]interface{}{
		"chat_id":    "chat-1",
		"session_id": "session-1",
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		"stream":     false,
	})
}
