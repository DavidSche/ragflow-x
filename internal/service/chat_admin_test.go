package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newChatTestService(t *testing.T) (*Service, repository.Store, *ragflow.Mock) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "chat.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	mock := ragflow.NewMock()
	svc := New(store, mock, jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc, store, mock
}

// TestChatTenantIsolation verifies that chat assistants are owned by a single
// platform tenant and that cross-tenant reads/writes are rejected unless the
// caller is a platform admin (scopeAll).
func TestChatTenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}

	chat, err := svc.CreateChat(ctx, ta.ID, "kb-assistant", []string{"d1"})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if chat.ID == "" || chat.TenantID != ta.ID {
		t.Fatalf("unexpected chat shadow: %+v", chat)
	}

	// Tenant B must not read/update/delete/list Tenant A's chat.
	if _, err := svc.GetChat(ctx, tb.ID, chat.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's chat")
	}
	if _, err := svc.UpdateChat(ctx, tb.ID, chat.ID, "x", nil, false); err == nil {
		t.Fatal("tenant B must not update tenant A's chat")
	}
	if err := svc.DeleteChat(ctx, tb.ID, chat.ID, false); err == nil {
		t.Fatal("tenant B must not delete tenant A's chat")
	}
	if _, _, err := svc.ListChats(ctx, tb.ID, false, repository.ChatFilter{}, 1, 20); err != nil {
		t.Fatalf("list own chats: %v", err)
	}
	items, total, err := svc.ListChats(ctx, tb.ID, false, repository.ChatFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("tenant B leaked tenant A chats: total=%d", total)
	}
	if _, err := svc.ListChatSessions(ctx, tb.ID, chat.ID, false, ragflow.SessionListOptions{}); err == nil {
		t.Fatal("tenant B must not list tenant A's sessions")
	}

	// Owner sees its own chat; platform admin (scopeAll) sees it too.
	if got, err := svc.GetChat(ctx, ta.ID, chat.ID, false); err != nil || got.ID != chat.ID {
		t.Fatalf("owner must read own chat: err=%v got=%+v", err, got)
	}
	_, total, err = svc.ListChats(ctx, "any", true, repository.ChatFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 {
		t.Fatalf("scopeAll should include the chat, total=%d", total)
	}
	if _, err := svc.GetChat(ctx, tb.ID, chat.ID, true); err != nil {
		t.Fatalf("scopeAll read should succeed: %v", err)
	}
}

func TestCreateChatWithConfigPassesLLMOnCreate(t *testing.T) {
	svc, _, mock := newChatTestService(t)
	tenant, err := svc.CreateTenant(t.Context(), "chat-model-tenant")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := tenant.ID
	dataset, err := mock.CreateDataset(t.Context(), ragflow.CreateDatasetRequest{Name: "Policy"})
	if err != nil {
		t.Fatal(err)
	}
	authoring := ChatAuthoring{LLMID: "glm@gpustack-local@OpenAI-API-Compatible"}
	if _, err := svc.CreateChatWithConfig(t.Context(), tenantID, "Model Assistant", []string{dataset.ID}, authoring); err != nil {
		t.Fatal(err)
	}
	requests := mock.ChatCreateRequests()
	if len(requests) != 1 || requests[0].LLMID != authoring.LLMID {
		t.Fatalf("expected create request to carry llm_id, got %+v", requests)
	}
}

func TestCreateChatWithConfigPassesLanguageOnCreate(t *testing.T) {
	svc, _, mock := newChatTestService(t)
	tenant, err := svc.CreateTenant(t.Context(), "chat-language-tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := mock.CreateDataset(t.Context(), ragflow.CreateDatasetRequest{Name: "Policy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChatWithConfig(t.Context(), tenant.ID, "Chinese Assistant", []string{dataset.ID}, ChatAuthoring{Language: "Chinese"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChatWithConfig(t.Context(), tenant.ID, "Default Assistant", []string{dataset.ID}, ChatAuthoring{}); err != nil {
		t.Fatal(err)
	}
	requests := mock.ChatCreateRequests()
	if len(requests) != 2 {
		t.Fatalf("expected two create requests, got %+v", requests)
	}
	if requests[0].Language != "Chinese" || requests[1].Language != "Chinese" {
		t.Fatalf("expected Chinese language fallback, got %+v", requests)
	}
}
