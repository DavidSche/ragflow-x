package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientOperationalResourceContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/agents/ag1/versions/v1":
			writeEnvelope(t, w, AgentVersion{ID: "v1", Title: "baseline", Dsl: map[string]interface{}{"version": float64(1)}})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/agents/ag1":
			if _, err := io.ReadAll(r.Body); err != nil {
				t.Fatal(err)
			}
			writeEnvelope(t, w, map[string]interface{}{"update_time": 1})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/chats/c1":
			writeEnvelope(t, w, Chat{ID: "c1", Name: "renamed"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/chats/c1":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/chats/c1/sessions":
			writeEnvelope(t, w, Session{ID: "s1", ChatID: "c1", Name: "Support"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/chats/c1/sessions/s1":
			writeEnvelope(t, w, Session{ID: "s1", ChatID: "c1", Messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}})
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == "/api/v1/chats/c1/sessions/s1":
			writeEnvelope(t, w, Session{ID: "s1", ChatID: "c1", Name: "Renamed"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/chats/c1/sessions":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/providers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":102,"message":"The provider has already exists.","data":null}`))
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/instances":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/instances/production/models":
			writeEnvelope(t, w, []ProviderModel{{Name: "gpt-4o", ModelTypes: []string{"chat"}, Status: "active"}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/instances/production/models/gpt-4o":
			writeEnvelope(t, w, json.RawMessage(`{"answer":"model is healthy"}`))
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/providers/OpenAI-API-Compatible/connection":
			writeTestEnvelope(w, nil)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/datasets/ds1/documents/doc1/chunks/c1":
			writeEnvelope(t, w, Chunk{ID: "c1", DocumentID: "doc1", Content: "chunk text"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/documents/images/img1":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()
	version, err := client.GetAgentVersion(ctx, "ag1", "v1")
	if err != nil || version.ID != "v1" || version.Dsl["version"] != float64(1) {
		t.Fatalf("agent version: %+v err=%v", version, err)
	}
	if err := client.RollbackAgentVersion(ctx, "ag1", "v1"); err != nil {
		t.Fatal(err)
	}

	updated, err := client.UpdateChat(ctx, "c1", UpdateChatRequest{Name: "renamed"})
	if err != nil || updated.ID != "c1" || updated.Name != "renamed" {
		t.Fatalf("update chat: %+v err=%v", updated, err)
	}
	session, err := client.CreateChatSession(ctx, "c1", "Support")
	if err != nil || session.ID != "s1" {
		t.Fatalf("create session: %+v err=%v", session, err)
	}
	detail, err := client.GetChatSession(ctx, "c1", "s1")
	if err != nil || len(detail.Messages) != 2 {
		t.Fatalf("session detail: %+v err=%v", detail, err)
	}
	messages, next, err := client.ListChatSessionMessages(ctx, "c1", "s1", SessionMessagePageOptions{Limit: 1})
	if err != nil || next == "" || len(messages) != 1 || messages[0].Role != "assistant" {
		t.Fatalf("session page: messages=%+v next=%s err=%v", messages, next, err)
	}
	renamed, err := client.UpdateChatSession(ctx, "c1", "s1", "Renamed")
	if err != nil || renamed.Name != "Renamed" {
		t.Fatalf("rename session: %+v err=%v", renamed, err)
	}
	if err := client.DeleteChatSessions(ctx, "c1", []string{"s1"}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteChat(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	request := RegisterModelProviderRequest{
		FactoryName: "OpenAI-API-Compatible", InstanceName: "production",
		APIKey: "secret", BaseURL: "https://api.example.com/v1",
		ModelName: "gpt-4o", MaxTokens: 64000,
	}
	if err := client.UpsertModelProvider(ctx, request); err != nil {
		t.Fatal(err)
	}
	models, err := client.ListInstanceModels(ctx, "OpenAI-API-Compatible", "production", true)
	if err != nil || len(models) != 1 || models[0].Name != "gpt-4o" {
		t.Fatalf("instance models: %+v err=%v", models, err)
	}
	if result, err := client.VerifyConnection(ctx, "OpenAI-API-Compatible", "secret", "https://api.example.com/v1", "default", []ModelInfo{{ModelName: "gpt-4o"}}); err != nil || result["gpt-4o"] != modelVerifySuccessLabel {
		t.Fatalf("verify connection: %+v err=%v", result, err)
	}
	if response, err := client.ChatToModel(ctx, "OpenAI-API-Compatible", "production", "gpt-4o", "hello", false, true); err != nil || !strings.Contains(response, "model is healthy") {
		t.Fatalf("chat to model: %s err=%v", response, err)
	}
	chunk, err := client.GetChunk(ctx, "ds1", "doc1", "c1")
	if err != nil || chunk.Content != "chunk text" {
		t.Fatalf("chunk: %+v err=%v", chunk, err)
	}
	image, contentType, err := client.GetChunkImage(ctx, "img1")
	if err != nil || string(image) != "png-bytes" || contentType != "image/png" {
		t.Fatalf("chunk image: %s %s err=%v", image, contentType, err)
	}

	mock := NewMock()
	if added, err := mock.ListModels(ctx, "chat"); err != nil || len(added) != 0 {
		t.Fatalf("mock models: %+v err=%v", added, err)
	}
	if defaults, err := mock.ListDefaultModels(ctx); err != nil || len(defaults) != 0 {
		t.Fatalf("mock defaults: %+v err=%v", defaults, err)
	}
	if result, err := mock.VerifyConnection(ctx, "OpenAI-API-Compatible", "secret", "", "", []ModelInfo{{ModelName: "gpt-4o"}}); err != nil || result["gpt-4o"] != "success" {
		t.Fatalf("mock verify: %+v err=%v", result, err)
	}
	if response, err := mock.ChatToModel(ctx, "OpenAI-API-Compatible", "production", "gpt-4o", "hello", true, false); err != nil || !strings.Contains(response, "hello") {
		t.Fatalf("mock chat to model: %s err=%v", response, err)
	}
	uploaded, err := mock.UploadChatFile(ctx, "note.txt", []byte("content"))
	if err != nil || uploaded.Name != "note.txt" || uploaded.Size != 7 {
		t.Fatalf("mock upload: %+v err=%v", uploaded, err)
	}
	if image, contentType, err := mock.GetChunkImage(ctx, "img1"); err != nil || string(image) != "mock-image-img1" || contentType != "image/png" {
		t.Fatalf("mock image: %s %s err=%v", image, contentType, err)
	}
	var stream bytes.Buffer
	searchID, err := mock.CreateSearchApp(ctx, CreateSearchAppRequest{Name: "support"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.StreamSearchAppCompletion(ctx, searchID, SearchAppCompletionRequest{Question: "hi"}, &stream); err != nil || !bytes.Contains(stream.Bytes(), []byte("mock answer")) {
		t.Fatalf("mock search stream: %s err=%v", stream.String(), err)
	}
}
