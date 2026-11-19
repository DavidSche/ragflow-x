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

func TestHTTPClient_AgentContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		switch r.URL.Path {
		case "/api/v1/agents":
			if r.Method == http.MethodPost {
				if !strings.Contains(body, `"dsl"`) || !strings.Contains(body, `"title":"a"`) {
					http.Error(w, "bad create body "+body, http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"id": "ag1", "title": "a", "release": true})
				return
			}
			writeEnvelope(t, w, map[string]interface{}{"canvas": []map[string]interface{}{{"id": "ag1", "title": "a"}}, "total": 1})
		case "/api/v1/agents/ag1":
			switch r.Method {
			case http.MethodGet:
				writeEnvelope(t, w, map[string]interface{}{"id": "ag1", "title": "a", "release": true})
			case http.MethodPut:
				if !strings.Contains(body, `"release"`) {
					http.Error(w, "bad update body "+body, http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"update_time": 1})
			case http.MethodDelete:
				writeEnvelope(t, w, true)
			}
		case "/api/v1/agents/ag1/versions":
			writeEnvelope(t, w, []map[string]interface{}{{"id": "v1", "title": "a", "release": true}})
		case "/api/v1/agents/ag1/sessions":
			if r.Method == http.MethodGet {
				// RAGFlow returns: {code: 0, data: [...sessions], total: N}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]interface{}{
					"code":    0,
					"message": "success",
					"data":    []map[string]interface{}{{"id": "s1", "dialog_id": "ag1"}},
					"total":   1,
				}); err != nil {
					t.Error(err)
				}
				return
			}
			writeEnvelope(t, w, map[string]interface{}{"id": "s1", "dialog_id": "ag1", "name": "S1"})
		case "/api/v1/agents/ag1/sessions/s1":
			if r.Method == http.MethodGet {
				writeEnvelope(t, w, map[string]interface{}{"id": "s1", "dialog_id": "ag1", "name": "S1"})
				return
			}
			writeEnvelope(t, w, true)
		case "/api/v1/agents/chat/completions":
			// RAGFlow v0.27 returns a raw OpenAI-compatible body here.
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "cmpl-1",
				"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "hi"}}},
				"usage":   map[string]int64{"prompt_tokens": 2, "completion_tokens": 1},
			}); err != nil {
				t.Error(err)
			}
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	created, err := client.CreateAgent(ctx, CreateAgentRequest{Title: "a", Dsl: map[string]interface{}{"x": 1}, Release: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "ag1" || created.Title != "a" {
		t.Fatalf("unexpected created: %+v", created)
	}

	list, total, err := client.ListAgents(ctx, ListAgentsFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Title != "a" {
		t.Fatalf("unexpected list: %+v total=%d", list, total)
	}

	// v1.0.0+ surfaces compilation template groups through the same list; they
	// are not runnable agents and must not leak into the agent sync surface.
	mixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]interface{}{
			"total": 3,
			"canvas": []map[string]interface{}{
				{"id": "ag1", "title": "a", "type": "agent"},
				{"id": "grp1", "title": "g", "type": "compilation_template_group", "name": "g"},
				{"id": "legacy", "title": "old"},
			},
		})
	}))
	defer mixed.Close()
	mixedClient := NewHTTPClient(mixed.URL, "key", 5*time.Second, 1)
	filtered, filteredTotal, err := mixedClient.ListAgents(ctx, ListAgentsFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 || filteredTotal != 2 {
		t.Fatalf("non-agent canvas rows must be filtered: %+v total=%d", filtered, filteredTotal)
	}
	for _, agent := range filtered {
		if agent.ID == "grp1" {
			t.Fatalf("compilation template group leaked into the agent list: %+v", filtered)
		}
	}

	got, err := client.GetAgent(ctx, "ag1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ag1" || got.Release != true {
		t.Fatalf("unexpected get: %+v", got)
	}

	rl := false
	if err := client.UpdateAgent(ctx, "ag1", UpdateAgentRequest{Release: &rl}); err != nil {
		t.Fatal(err)
	}

	versions, err := client.ListAgentVersions(ctx, "ag1")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Title != "a" {
		t.Fatalf("unexpected versions: %+v", versions)
	}

	sessions, stotal, err := client.ListAgentSessions(ctx, "ag1", SessionListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stotal != 1 || len(sessions) != 1 {
		t.Fatalf("unexpected sessions: %+v total=%d", sessions, stotal)
	}
	if _, err := client.CreateAgentSession(ctx, "ag1", "S2"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetAgentSession(ctx, "ag1", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteAgentSession(ctx, "ag1", "s1"); err != nil {
		t.Fatal(err)
	}

	comp, err := client.AgentChatCompletion(ctx, CompletionRequest{ChatID: "ag1", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Choices) == 0 || comp.Choices[0].Message.Content != "hi" {
		t.Fatalf("unexpected completion: %+v", comp)
	}

	if err := client.DeleteAgent(ctx, "ag1"); err != nil {
		t.Fatal(err)
	}
}

func TestMock_AgentLifecycle(t *testing.T) {
	mock := NewMock()
	ctx := context.Background()
	a, err := mock.CreateAgent(ctx, CreateAgentRequest{Title: "agent", Dsl: map[string]interface{}{"x": 1}, Release: true})
	if err != nil {
		t.Fatal(err)
	}
	s, err := mock.CreateAgentSession(ctx, a.ID, "S1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := mock.GetAgentSession(ctx, a.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "S1" {
		t.Fatalf("unexpected session: %+v", got)
	}
	if err := mock.DeleteAgentSession(ctx, a.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	comp, err := mock.AgentChatCompletion(ctx, CompletionRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comp.Choices[0].Message.Content, "hello") {
		t.Fatalf("unexpected completion: %+v", comp)
	}
	var buf bytes.Buffer
	if err := mock.StreamAgentChatCompletion(ctx, CompletionRequest{}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "mock agent response") {
		t.Fatalf("unexpected stream: %s", buf.String())
	}
	if err := mock.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClient_AgentAttachmentsContract(t *testing.T) {
	var uploadedPath, completionBody string
	var uploadedSize int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/ag1/upload":
			uploadedPath = r.URL.Path
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing upload", http.StatusBadRequest)
				return
			}
			defer func() { _ = file.Close() }()
			data, err := io.ReadAll(file)
			if err != nil {
				http.Error(w, "read upload", http.StatusBadRequest)
				return
			}
			uploadedSize = int64(len(data))
			writeEnvelope(t, w, map[string]interface{}{
				"id": "file-1", "name": header.Filename, "size": len(data),
				"mime_type": header.Header.Get("Content-Type"), "created_by": "tenant-1",
			})
		case "/api/v1/agents/chat/completions":
			b, _ := io.ReadAll(r.Body)
			completionBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "hi"}}},
			}); err != nil {
				t.Error(err)
			}
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	uploaded, err := client.UploadAgentFile(context.Background(), "ag1", "notes.txt", []byte("attachment body"))
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.ID != "file-1" || uploaded.Name != "notes.txt" || uploadedSize != int64(len("attachment body")) {
		t.Fatalf("unexpected upload: %+v size=%d", uploaded, uploadedSize)
	}
	if uploadedPath != "/api/v1/agents/ag1/upload" {
		t.Fatalf("unexpected upload path: %s", uploadedPath)
	}

	files := []map[string]interface{}{{"id": uploaded.ID, "created_by": uploaded.CreatedBy}}
	comp, err := client.AgentChatCompletion(context.Background(), CompletionRequest{
		ChatID: "ag1", Messages: []Message{{Role: "user", Content: "hi"}}, Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.Choices) == 0 || comp.Choices[0].Message.Content != "hi" {
		t.Fatalf("unexpected completion: %+v", comp)
	}
	var request map[string]interface{}
	if err := json.Unmarshal([]byte(completionBody), &request); err != nil {
		t.Fatal(err)
	}
	requestFiles, ok := request["files"].([]interface{})
	if !ok || len(requestFiles) != 1 {
		t.Fatalf("completion body missing top-level files: %s", completionBody)
	}
}

// TestHTTPClient_AgentPublishVersionContract pins the one call that actually
// writes an immutable version row. Publishing through PUT /agents/{id} with
// release=true only flips a flag: the version page stays empty and rollback has
// no target. The provider must therefore POST /agents/{id}/publish and treat a
// response without a version id as a protocol error (not as success).
func TestHTTPClient_AgentPublishVersionContract(t *testing.T) {
	var method, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/ag1/publish" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		method, body = r.Method, string(b)
		writeEnvelope(t, w, map[string]interface{}{
			"id": "v9", "user_canvas_id": "ag1", "title": "a_2026-11-19 08:30:52",
			"release": true, "dsl": map[string]interface{}{"components": map[string]interface{}{}},
			"create_time": 1795048252290, "update_time": 1795048252290,
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)

	version, err := client.PublishAgentVersion(context.Background(), "ag1")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost {
		t.Fatalf("publish must POST, got %s", method)
	}
	if strings.TrimSpace(body) != "{}" {
		t.Fatalf("publish body must be an empty object, got %q", body)
	}
	if version.ID != "v9" || version.UserCanvasID != "ag1" || !version.Release {
		t.Fatalf("unexpected version: %+v", version)
	}
	// 引擎时间是毫秒；把它当秒再乘 1000 会让界面印出 58852 年。
	if version.UpdateTime != 1795048252290 {
		t.Fatalf("update_time must stay in milliseconds, got %d", version.UpdateTime)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]interface{}{"release": true})
	}))
	defer empty.Close()
	if _, err := NewHTTPClient(empty.URL, "key", 5*time.Second, 1).PublishAgentVersion(context.Background(), "ag1"); err == nil {
		t.Fatal("a publish response without a version id must be a protocol error")
	}
}

// TestMock_AgentPublishWritesVersion keeps the mock honest: the service tests
// assert "publish produces a version", which is only meaningful if the mock
// implements the same contract as the engine.
func TestMock_AgentPublishWritesVersion(t *testing.T) {
	ctx := context.Background()
	mock := NewMock()
	a, err := mock.CreateAgent(ctx, CreateAgentRequest{
		Title: "agent", Dsl: map[string]interface{}{"k": "v"}, Release: false})
	if err != nil {
		t.Fatal(err)
	}
	if versions, err := mock.ListAgentVersions(ctx, a.ID); err != nil || len(versions) != 0 {
		t.Fatalf("a fresh agent must have no versions: %+v err=%v", versions, err)
	}
	version, err := mock.PublishAgentVersion(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if version.ID == "" || version.UserCanvasID != a.ID || !version.Release {
		t.Fatalf("unexpected version: %+v", version)
	}
	if version.Dsl == nil {
		t.Fatal("version must carry a DSL snapshot")
	}
	if version.UpdateTime < int64(1)<<40 {
		t.Fatalf("version update_time must be in milliseconds, got %d", version.UpdateTime)
	}
	versions, err := mock.ListAgentVersions(ctx, a.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("publish must leave one version: %+v err=%v", versions, err)
	}
	if got, _ := mock.GetAgent(ctx, a.ID); !got.Release {
		t.Fatal("publish must mark the agent released")
	}
	if _, err := mock.PublishAgentVersion(ctx, "missing-agent"); err == nil {
		t.Fatal("publishing an unknown agent must fail")
	}
}
