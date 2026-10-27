package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientListDatasetsPaginationAndDelete(t *testing.T) {
	var deletedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/datasets":
			page := r.URL.Query().Get("page")
			pageSize := r.URL.Query().Get("page_size")
			if pageSize != "100" {
				t.Fatalf("dataset page size = %q, want 100", pageSize)
			}
			var datasets []Dataset
			if page == "1" {
				for i := 0; i < 100; i++ {
					datasets = append(datasets, Dataset{ID: "ds-" + strings.Repeat("a", 8) + json.Number(page).String()})
				}
			} else if page == "2" {
				datasets = []Dataset{{ID: "ds-last", Name: "last"}}
			} else {
				t.Fatalf("unexpected page %q", page)
			}
			writeEnvelope(t, w, datasets)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/datasets":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				IDs []string `json:"ids"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			deletedIDs = payload.IDs
			writeEnvelope(t, w, nil)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	datasets, err := client.ListDatasets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(datasets) != 101 || datasets[len(datasets)-1].ID != "ds-last" {
		t.Fatalf("expected all pages, got %d datasets", len(datasets))
	}

	if err := client.DeleteDataset(context.Background(), "ds-42"); err != nil {
		t.Fatal(err)
	}
	if len(deletedIDs) != 1 || deletedIDs[0] != "ds-42" {
		t.Fatalf("unexpected delete payload: %v", deletedIDs)
	}
}

func TestHTTPClientChatSessionLifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1":
			writeEnvelope(t, w, Chat{ID: "chat/1", Name: "updated", DatasetIDs: []string{"ds-1"}})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1":
			writeEnvelope(t, w, nil)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1/sessions":
			if query := r.URL.Query(); query.Get("page") != "2" || query.Get("page_size") != "20" || query.Get("name") != "support" {
				t.Fatalf("unexpected session query: %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, []Session{{ID: "s-old", ChatID: "chat/1", Name: "support"}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1/sessions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"name":"created"`) {
				t.Fatalf("unexpected create body: %s", body)
			}
			writeEnvelope(t, w, Session{ID: "s-new", ChatID: "chat/1", Name: "created"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1/sessions/s1":
			writeEnvelope(t, w, Session{
				ID: "s1", ChatID: "chat/1",
				Messages: []Message{
					{Role: "assistant", Content: "prologue"},
					{Role: "user", Content: "first"},
					{Role: "assistant", Content: "second [ID:0]"},
					{Role: "user", Content: "third"},
					{Role: "assistant", Content: "fourth [ID:0]"},
				},
				Reference: []map[string]interface{}{
					{"chunks": []interface{}{map[string]interface{}{"content": "first source", "document_id": "doc-1", "dataset_id": "dataset-1", "id": "chunk-1"}}},
					{"chunks": []interface{}{map[string]interface{}{"content": "second source", "document_id": "doc-2", "dataset_id": "dataset-1", "id": "chunk-2"}}},
				},
			})
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1/sessions/s1":
			writeEnvelope(t, w, Session{ID: "s1", ChatID: "chat/1", Name: "renamed"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/chats/chat%2F1/sessions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"ids":["s1","s2"]`) {
				t.Fatalf("unexpected delete body: %s", body)
			}
			writeEnvelope(t, w, nil)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()
	sessions, err := client.ListChatSessions(ctx, "chat/1", SessionListOptions{Page: 2, Name: "support"})
	if err != nil || len(sessions) != 1 || sessions[0].ID != "s-old" {
		t.Fatalf("list sessions: %+v err=%v", sessions, err)
	}
	created, err := client.CreateChatSession(ctx, "chat/1", "created")
	if err != nil || created.ID != "s-new" {
		t.Fatalf("create session: %+v err=%v", created, err)
	}
	session, err := client.GetChatSession(ctx, "chat/1", "s1")
	if err != nil || len(session.Messages) != 5 {
		t.Fatalf("get session: %+v err=%v", session, err)
	}
	if len(session.Messages[0].Citations) != 0 {
		t.Fatalf("prologue should not consume session references: %+v", session.Messages[0].Citations)
	}
	if len(session.Messages[2].Citations) != 1 || session.Messages[2].Citations[0]["content"] != "first source" {
		t.Fatalf("session references were not attached: %+v", session.Messages[2].Citations)
	}
	updated, err := client.UpdateChatSession(ctx, "chat/1", "s1", "renamed")
	if err != nil || updated.Name != "renamed" {
		t.Fatalf("update session: %+v err=%v", updated, err)
	}
	messages, next, err := client.ListChatSessionMessages(ctx, "chat/1", "s1", SessionMessagePageOptions{Limit: 2})
	if err != nil || len(messages) != 2 || messages[0].Content != "third" || !strings.Contains(messages[1].Content, "fourth") || next == "" {
		t.Fatalf("paged messages: %+v next=%q err=%v", messages, next, err)
	}
	if len(messages[1].Citations) != 1 || messages[1].Citations[0]["document_id"] != "doc-2" {
		t.Fatalf("paged message citations were not attached: %+v", messages[1].Citations)
	}
	chat, err := client.UpdateChat(ctx, "chat/1", UpdateChatRequest{Name: "updated"})
	if err != nil || chat.Name != "updated" {
		t.Fatalf("update chat: %+v err=%v", chat, err)
	}
	if err := client.DeleteChatSessions(ctx, "chat/1", []string{"s1", "s2"}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteChat(ctx, "chat/1"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientAgentVersionRollbackAndPagedMessages(t *testing.T) {
	var update UpdateAgentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/agents/ag%2F1/versions/v1":
			writeEnvelope(t, w, AgentVersion{ID: "v1", Title: "stable", Dsl: map[string]interface{}{"step": 1}})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/agents/ag%2F1":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &update); err != nil {
				t.Fatal(err)
			}
			writeEnvelope(t, w, nil)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/agents/ag%2F1/sessions/s1":
			writeEnvelope(t, w, AgentSession{ID: "s1", Messages: []Message{
				{Role: "user", Content: "one"},
				{Role: "assistant", Content: "two"},
				{Role: "user", Content: "three"},
			}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()
	if got := client.Name(); got != "http" {
		t.Fatalf("client name = %q", got)
	}
	if err := client.RollbackAgentVersion(ctx, "ag/1", "v1"); err != nil {
		t.Fatal(err)
	}
	if update.Dsl["step"] != float64(1) {
		t.Fatalf("rollback DSL was not applied: %+v", update)
	}
	messages, next, err := client.ListAgentSessionMessages(ctx, "ag/1", "s1", SessionMessagePageOptions{Limit: 2, Cursor: "3"})
	if err != nil || len(messages) != 2 || messages[0].Content != "two" || messages[1].Content != "three" || next != "1" {
		t.Fatalf("paged agent messages: %+v next=%q err=%v", messages, next, err)
	}
}
