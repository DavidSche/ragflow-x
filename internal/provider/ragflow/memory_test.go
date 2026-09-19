package ragflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClient_MemoryContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		switch r.URL.Path {
		case "/api/v1/memories":
			if r.Method == http.MethodPost {
				if !strings.Contains(body, `"name":"mem"`) {
					http.Error(w, "bad create body "+body, http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"id": "m1", "name": "mem"})
				return
			}
			writeEnvelope(t, w, map[string]interface{}{
				"memory_list": []map[string]interface{}{{"id": "m1", "name": "mem"}},
				"total_count": 1,
			})
		case "/api/v1/memories/m1/config":
			writeEnvelope(t, w, map[string]interface{}{"id": "m1", "name": "mem", "storage_type": "table"})
		case "/api/v1/memories/m1":
			switch r.Method {
			case http.MethodPut:
				if !strings.Contains(body, `"temperature"`) {
					http.Error(w, "bad update body "+body, http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"id": "m1", "name": "mem"})
			case http.MethodGet:
				writeEnvelope(t, w, map[string]interface{}{
					"messages": map[string]interface{}{
						"message_list": []map[string]interface{}{{"message_id": 1, "memory_id": "m1", "user_input": "hi"}},
					},
					"storage_type": "table",
				})
			case http.MethodDelete:
				writeEnvelope(t, w, true)
			}
		case "/api/v1/messages":
			if r.Method == http.MethodPost {
				if !strings.Contains(body, `"memory_id":"m1"`) {
					http.Error(w, "bad add body "+body, http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, true)
				return
			}
			writeEnvelope(t, w, map[string]interface{}{"messages": []map[string]interface{}{{"message_id": 1}}})
		case "/api/v1/messages/m1:1":
			if r.Method == http.MethodDelete {
				writeEnvelope(t, w, true)
				return
			}
			writeEnvelope(t, w, true)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	id, err := client.CreateMemory(ctx, CreateMemoryRequest{Name: "mem", MemoryType: []string{"raw"}, EmbdID: "e", LLMID: "l"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "m1" {
		t.Fatalf("unexpected created id: %s", id)
	}

	list, total, err := client.ListMemories(ctx, ListMemoriesFilter{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Name != "mem" {
		t.Fatalf("unexpected list: %+v total=%d", list, total)
	}

	cfg, err := client.GetMemoryConfig(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "m1" || cfg.StorageType != "table" {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	temp := 0.3
	if _, err := client.UpdateMemory(ctx, "m1", UpdateMemoryRequest{Temperature: &temp}); err != nil {
		t.Fatal(err)
	}

	msgs, err := client.ListMemoryMessages(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].UserInput != "hi" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	if err := client.AddMemoryMessage(ctx, AddMessageRequest{MemoryID: "m1", AgentID: "a", SessionID: "s", UserInput: "q", AgentResponse: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteMemoryMessage(ctx, "m1", 1); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateMemoryMessageStatus(ctx, "m1", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteMemory(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
}

func TestMock_MemoryLifecycle(t *testing.T) {
	mock := NewMock()
	ctx := context.Background()
	id, err := mock.CreateMemory(ctx, CreateMemoryRequest{Name: "mem", MemoryType: []string{"raw"}, EmbdID: "e", LLMID: "l"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.AddMemoryMessage(ctx, AddMessageRequest{MemoryID: id, AgentID: "a", SessionID: "s", UserInput: "q", AgentResponse: "a"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := mock.ListMemoryMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].UserInput != "q" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if err := mock.UpdateMemoryMessageStatus(ctx, id, msgs[0].MessageID, false); err != nil {
		t.Fatal(err)
	}
	if err := mock.DeleteMemoryMessage(ctx, id, msgs[0].MessageID); err != nil {
		t.Fatal(err)
	}
	msgs, _ = mock.ListMemoryMessages(ctx, id)
	if len(msgs) != 0 {
		t.Fatalf("expected no messages after delete: %+v", msgs)
	}
	if err := mock.DeleteMemory(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClient_MemorySearchAndContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/messages/search":
			if r.URL.Query().Get("memory_id") != "m1" || r.URL.Query().Get("query") != "hello" {
				http.Error(w, "bad search params", http.StatusBadRequest)
				return
			}
			writeEnvelope(t, w, []map[string]interface{}{{"message_id": 1, "memory_id": "m1", "user_input": "hello"}})
		case "/api/v1/messages/m1:1/content":
			writeEnvelope(t, w, map[string]interface{}{"user_input": "hello", "agent_response": "world"})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	res, err := client.SearchMemoryMessages(ctx, "m1", MemorySearchParams{Query: "hello", TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].UserInput != "hello" {
		t.Fatalf("unexpected search: %+v", res)
	}

	content, err := client.GetMemoryMessageContent(ctx, "m1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "hello") {
		t.Fatalf("unexpected content: %s", string(content))
	}
}

func TestMock_MemorySearchAndContent(t *testing.T) {
	mock := NewMock()
	ctx := context.Background()
	id, err := mock.CreateMemory(ctx, CreateMemoryRequest{Name: "mem", MemoryType: []string{"raw"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.AddMemoryMessage(ctx, AddMessageRequest{MemoryID: id, UserInput: "how are you", AgentResponse: "fine"}); err != nil {
		t.Fatal(err)
	}
	res, err := mock.SearchMemoryMessages(ctx, id, MemorySearchParams{Query: "fine", TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(res))
	}
	content, err := mock.GetMemoryMessageContent(ctx, id, res[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "how are you") {
		t.Fatalf("unexpected content: %s", string(content))
	}
}

// TestHTTPClient_CreateMemoryBoolMessage verifies that a successful RAGFlow
// create-memory response (which uses message: true, a boolean) decodes without
// error. This is the exact regression behind "json: cannot unmarshal bool into
// Go struct field envelope.message of type string".
func TestHTTPClient_CreateMemoryBoolMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/memories" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"message":true,"data":{"id":"m-bool","name":"mem"}}`)
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	id, err := client.CreateMemory(ctx, CreateMemoryRequest{Name: "mem", MemoryType: []string{"raw"}, EmbdID: "e", LLMID: "l"})
	if err != nil {
		t.Fatalf("create memory with bool message must succeed: %v", err)
	}
	if id != "m-bool" {
		t.Fatalf("unexpected created id: %s", id)
	}
}
