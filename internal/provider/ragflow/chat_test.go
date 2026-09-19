package ragflow

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClient_ChatContract(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotMethod, gotBody = r.URL.Path, r.Method, string(b)
		switch r.URL.Path {
		case "/api/v1/chats":
			if r.Method == http.MethodGet && r.URL.Query().Get("page") == "1" {
				writeEnvelope(t, w, map[string]interface{}{
					"chats": []map[string]interface{}{{
						"id": "c1", "name": "kb-assistant", "dataset_ids": []string{"d1"},
					}},
					"total": 1,
				})
				return
			}
			if r.Method == http.MethodPost {
				writeEnvelope(t, w, map[string]interface{}{"id": "c1", "name": "kb-assistant", "dataset_ids": []string{"d1"}})
				return
			}
			writeEnvelope(t, w, map[string]interface{}{"chats": []map[string]interface{}{}, "total": 1})
		case "/api/v1/chats/c1":
			writeEnvelope(t, w, map[string]interface{}{"id": "c1", "name": "kb-assistant"})
		case "/api/v1/chats/c1/sessions":
			writeEnvelope(t, w, []map[string]interface{}{{"id": "s1", "chat_id": "c1", "name": "Session 1"}})
		case "/api/v1/chats/c1/sessions/s1":
			writeEnvelope(t, w, map[string]interface{}{
				"id": "s1", "chat_id": "c1", "name": "Session 1",
				"messages": []map[string]string{{"role": "user", "content": "hi"}},
			})
		case "/api/v1/chat/completions":
			writeEnvelope(t, w, map[string]interface{}{
				"id":     "cmpl-1",
				"answer": "hello",
				"usage":  map[string]int64{"prompt_tokens": 7, "completion_tokens": 3},
			})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	created, err := client.CreateChat(ctx, CreateChatRequest{Name: "kb-assistant", DatasetIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "c1" || created.Name != "kb-assistant" {
		t.Fatalf("unexpected created chat: %+v", created)
	}
	if gotPath != "/api/v1/chats" || gotMethod != http.MethodPost || !strings.Contains(gotBody, `"dataset_ids":["d1"]`) {
		t.Fatalf("create chat: path=%s method=%s body=%s", gotPath, gotMethod, gotBody)
	}

	got, err := client.GetChat(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "c1" || got.Name != "kb-assistant" {
		t.Fatalf("unexpected get chat: %+v", got)
	}
	if gotPath != "/api/v1/chats/c1" || gotMethod != http.MethodGet {
		t.Fatalf("get chat: path=%s method=%s", gotPath, gotMethod)
	}

	chats, err := client.ListChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != "c1" || chats[0].Name != "kb-assistant" {
		t.Fatalf("unexpected chats: %+v", chats)
	}

	sess, err := client.ListChatSessions(ctx, "c1", SessionListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sess) != 1 || sess[0].ID != "s1" {
		t.Fatalf("unexpected sessions: %+v", sess)
	}

	detail, err := client.GetChatSession(ctx, "c1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Content != "hi" {
		t.Fatalf("unexpected session detail: %+v", detail)
	}
	msgs, err := client.ListSessionMessages(ctx, "c1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	resp, err := client.ChatCompletion(ctx, "c1", CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("unexpected completion: %+v", resp)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 7 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if gotPath != "/api/v1/chat/completions" {
		t.Fatalf("completion path: %s", gotPath)
	}
	if !strings.Contains(gotBody, `"chat_id":"c1"`) || !strings.Contains(gotBody, `"stream":false`) {
		t.Fatalf("completion body must carry chat_id and stream:false, got %s", gotBody)
	}
}

func TestHTTPClient_StreamChatCompletion(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	var out bytes.Buffer
	err := client.StreamChatCompletion(context.Background(), "c1", CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok") && !strings.Contains(out.String(), "hello") {
		t.Fatalf("stream passthrough missing content: %q", out.String())
	}
	if !strings.Contains(out.String(), "[DONE]") {
		t.Fatalf("stream must terminate with [DONE]: %q", out.String())
	}
}

func TestHTTPClientCreateChatSessionDefaultsEmptyName(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		writeEnvelope(t, w, Session{ID: "s-new", ChatID: "c1"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	session, err := client.CreateChatSession(context.Background(), "c1", "")
	if err != nil || session.ID != "s-new" {
		t.Fatalf("create session: session=%+v err=%v", session, err)
	}
	if !strings.Contains(gotBody, `"name":"New session"`) {
		t.Fatalf("empty session name was not defaulted: %s", gotBody)
	}
}

func TestMock_ChatLifecycle(t *testing.T) {
	m := NewMock()
	ctx := context.Background()

	// CreateChat and ListChats/GetChat/UpdateChat.
	created, err := m.CreateChat(ctx, CreateChatRequest{Name: "kb", DatasetIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	chats, err := m.ListChats(ctx)
	if err != nil || len(chats) != 1 {
		t.Fatalf("list chats: %v %+v", err, chats)
	}
	got, err := m.GetChat(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "kb" {
		t.Fatalf("get chat name: %+v", got)
	}
	updated, err := m.UpdateChat(ctx, created.ID, UpdateChatRequest{Name: "kb-v2", DatasetIDs: []string{"d2"}})
	if err != nil || updated.Name != "kb-v2" {
		t.Fatalf("update chat: %v %+v", err, updated)
	}

	// Sessions and completion append messages.
	sess, err := m.CreateChatSession(ctx, created.ID, "Session 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChatCompletion(ctx, created.ID, CompletionRequest{SessionID: sess.ID, Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	msgs, err := m.ListSessionMessages(ctx, created.ID, sess.ID)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hi" {
		t.Fatalf("session messages: %v %+v", err, msgs)
	}

	// Streaming passthrough via mock.
	var buf bytes.Buffer
	if err := m.StreamChatCompletion(ctx, created.ID, CompletionRequest{SessionID: sess.ID}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "mock stream") || !strings.Contains(buf.String(), "[DONE]") {
		t.Fatalf("mock stream output: %q", buf.String())
	}

	// Delete chat clears its sessions.
	if err := m.DeleteChat(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetChat(ctx, created.ID); err == nil {
		t.Fatal("expected get deleted chat to fail")
	}
}
