package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// chatAppSvc returns a Service wired to a controllable mock engine so the
// gateway chat-app path can be exercised without a live RAGFlow.
func chatAppSvc(t *testing.T) (*Service, *ragflow.Mock) {
	t.Helper()
	svc := newAuthzSvc(t)
	m := ragflow.NewMock()
	svc.RAGFlow = m
	return svc, m
}

func TestChatAppCompletionMetersUsage(t *testing.T) {
	ctx := context.Background()
	svc, m := chatAppSvc(t)

	chat, err := m.CreateChat(ctx, ragflow.CreateChatRequest{Name: "kb-assistant", DatasetIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{ID: chat.ID, TenantID: "t1", Name: chat.Name, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	session, err := m.CreateChatSession(ctx, chat.ID, "session")
	if err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{ID: "key-1", TenantID: "t1", UserID: "u1"}
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}],"chat_id":"` + chat.ID + `"}`)
	res, err := svc.ChatAppCompletion(ctx, key, chat.ID, session.ID, raw, "req-chat-1")
	if err != nil {
		t.Fatalf("chat app completion: %v", err)
	}
	if res.Status != 200 || !strings.Contains(string(res.Body), "mock chat") {
		t.Fatalf("unexpected chat completion response: %d %s", res.Status, res.Body)
	}

	// Daily aggregate metered.
	usage, err := svc.Store.SummarizeUsage(ctx, "t1", "")
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage summary: %v %+v", err, usage)
	}
	if usage[0].TokensIn != 10 || usage[0].TokensOut != 5 || usage[0].Requests != 1 {
		t.Fatalf("unexpected aggregate usage: %+v", usage[0])
	}

	// Per-request cost detail metered with chat/session attributes.
	rows, total, err := svc.Store.ListCostMetrics(ctx, "t1", false, 1, 20, repository.CostMetricFilter{})
	if err != nil || total != 1 {
		t.Fatalf("cost metric list: %v total=%d", err, total)
	}
	if rows[0].Model != chat.ID || rows[0].Scenario != "chat" || rows[0].SessionID != session.ID {
		t.Fatalf("unexpected cost detail: %+v", rows[0])
	}
}

func TestChatAppCompletionMissingChatDegrades(t *testing.T) {
	ctx := context.Background()
	svc, _ := chatAppSvc(t)
	key := &model.APIKey{ID: "key-2", TenantID: "t1", UserID: "u1"}
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if _, err := svc.ChatAppCompletion(ctx, key, "does-not-exist", "", raw, "req-chat-missing"); err == nil {
		t.Fatal("expected error for unknown chat id")
	}
}

func TestStreamChatAppCompletionMeters(t *testing.T) {
	ctx := context.Background()
	svc, m := chatAppSvc(t)
	chat, err := m.CreateChat(ctx, ragflow.CreateChatRequest{Name: "kb"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{ID: chat.ID, TenantID: "t1", Name: chat.Name, Status: model.TenantStatusActive, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{ID: "key-3", TenantID: "t1", UserID: "u1"}
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	var buf bytes.Buffer
	status, ct, err := svc.StreamChatAppCompletion(ctx, key, chat.ID, "", raw, &buf, "req-chat-s")
	if err != nil {
		t.Fatalf("stream chat app: %v", err)
	}
	if status != 200 || ct != "text/event-stream" {
		t.Fatalf("unexpected stream metadata: %d %s", status, ct)
	}
	if !strings.Contains(buf.String(), "mock stream") || !strings.Contains(buf.String(), "[DONE]") {
		t.Fatalf("unexpected stream body: %q", buf.String())
	}
	// Even a zero-token stream must be metered exactly once.
	usage, err := svc.Store.SummarizeUsage(ctx, "t1", "")
	if err != nil || len(usage) != 1 || usage[0].Requests != 1 {
		t.Fatalf("stream usage: %v %+v", err, usage)
	}
}

func TestResolveChatTarget(t *testing.T) {
	svc := &Service{}
	chatID, sessionID := svc.ResolveChatTarget([]byte(`{"chat_id":"c1","session_id":"s1"}`))
	if chatID != "c1" || sessionID != "s1" {
		t.Fatalf("resolve chat target: chat=%q session=%q", chatID, sessionID)
	}
	chatID, sessionID = svc.ResolveChatTarget([]byte(`{"model":"alias"}`))
	if chatID != "" || sessionID != "" {
		t.Fatalf("no chat target expected: chat=%q session=%q", chatID, sessionID)
	}
}
